// Package store provides SQLite-based persistence for batch queues and request status.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// Status states for delivery tracking.
const (
	StatusQueued = "queued"
	StatusSent   = "sent"
	StatusFailed = "failed"
)

// QueuedNotification represents a single push notification queued for delivery.
// This mirrors the proto definition until it's generated.
type QueuedNotification struct {
	DataIDs   [][]byte // Content IDs to cache (32 bytes each)
	RequestID string   // Gateway-generated ID for status tracking
}

// Batch represents queued notifications for a single endpoint.
type Batch struct {
	Notifications []QueuedNotification
	CreatedAt     time.Time
	FlushAt       time.Time
}

// Status represents the delivery status of a request.
type Status struct {
	State     string
	SentAt    *time.Time
	Error     string
	ExpiresAt time.Time
}

// Store defines the interface for persistence operations.
type Store interface {
	SaveBatch(ctx context.Context, fcmToken string, batch *Batch) error
	LoadOldestBatches(ctx context.Context, limit int) (map[string]*Batch, error)
	DeleteBatchAndSetStatus(ctx context.Context, fcmToken string, status Status) error

	GetStatus(ctx context.Context, requestID string) (Status, error)
	CleanupExpiredStatus(ctx context.Context) (int64, error)

	Close() error
}

// SQLiteStore implements Store using SQLite.
type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex // serializes writes
}

// Config holds SQLite store configuration.
type Config struct {
	Path string
}

// New creates a new SQLiteStore.
func New(cfg Config) (*SQLiteStore, error) {
	dir := filepath.Dir(cfg.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("creating storage directory: %w", err)
	}

	db, err := sql.Open("sqlite3", cfg.Path+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &SQLiteStore{db: db}

	if err := store.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return store, nil
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	var version int
	err := s.db.QueryRowContext(ctx, `
		SELECT version FROM schema_version ORDER BY version DESC LIMIT 1
	`).Scan(&version)
	if err != nil && err != sql.ErrNoRows {
		version = 0
	}

	if version < 1 {
		if err := s.migrateV1(ctx); err != nil {
			return err
		}
	}

	return nil
}

func (s *SQLiteStore) migrateV1(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY
		)`,
		`CREATE TABLE IF NOT EXISTS batches (
			fcm_token TEXT PRIMARY KEY,
			notifications BLOB NOT NULL,
			created_at INTEGER NOT NULL,
			flush_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_batches_flush_at ON batches(flush_at)`,
		`CREATE TABLE IF NOT EXISTS status (
			request_id TEXT PRIMARY KEY,
			state TEXT NOT NULL,
			sent_at INTEGER,
			error TEXT,
			expires_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_status_expires ON status(expires_at)`,
		`INSERT OR REPLACE INTO schema_version (version) VALUES (1)`,
	}

	for _, stmt := range statements {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("executing %q: %w", stmt, err)
		}
	}

	return tx.Commit()
}

// SaveBatch persists a batch for the given FCM token.
func (s *SQLiteStore) SaveBatch(ctx context.Context, fcmToken string, batch *Batch) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	notifData, err := serializeNotifications(batch.Notifications)
	if err != nil {
		return fmt.Errorf("serializing notifications: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO batches (fcm_token, notifications, created_at, flush_at)
		VALUES (?, ?, ?, ?)
	`, fcmToken, notifData, batch.CreatedAt.Unix(), batch.FlushAt.Unix())

	return err
}

// LoadOldestBatches loads the oldest batches ordered by flush_at.
// Returns fewer than limit entries when no more batches exist.
func (s *SQLiteStore) LoadOldestBatches(ctx context.Context, limit int) (map[string]*Batch, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT fcm_token, notifications, created_at, flush_at
		FROM batches
		ORDER BY flush_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	batches := make(map[string]*Batch)
	for rows.Next() {
		var (
			fcmToken  string
			notifData []byte
			createdAt int64
			flushAt   int64
		)

		if err := rows.Scan(&fcmToken, &notifData, &createdAt, &flushAt); err != nil {
			return nil, err
		}

		notifications, err := deserializeNotifications(notifData)
		if err != nil {
			return nil, fmt.Errorf("deserializing notifications for token %s: %w", fcmToken, err)
		}

		batches[fcmToken] = &Batch{
			Notifications: notifications,
			CreatedAt:     time.Unix(createdAt, 0),
			FlushAt:       time.Unix(flushAt, 0),
		}
	}

	return batches, rows.Err()
}

// DeleteBatchAndSetStatus atomically deletes a batch and sets status for all its request IDs.
func (s *SQLiteStore) DeleteBatchAndSetStatus(ctx context.Context, fcmToken string, status Status) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Get notifications from the batch to extract request IDs
	var notifData []byte
	err = tx.QueryRowContext(ctx, `
		SELECT notifications FROM batches WHERE fcm_token = ?
	`, fcmToken).Scan(&notifData)
	if err == sql.ErrNoRows {
		return nil // No batch exists, nothing to do
	}
	if err != nil {
		return err
	}

	notifications, err := deserializeNotifications(notifData)
	if err != nil {
		return fmt.Errorf("deserializing notifications: %w", err)
	}

	// Delete the batch
	_, err = tx.ExecContext(ctx, `DELETE FROM batches WHERE fcm_token = ?`, fcmToken)
	if err != nil {
		return err
	}

	// Set status for all request IDs
	var sentAt *int64
	if status.SentAt != nil {
		t := status.SentAt.Unix()
		sentAt = &t
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT OR REPLACE INTO status (request_id, state, sent_at, error, expires_at)
		VALUES (?, ?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, notif := range notifications {
		_, err = stmt.ExecContext(ctx, notif.RequestID, status.State, sentAt, status.Error, status.ExpiresAt.Unix())
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetStatus retrieves the delivery status for a request.
func (s *SQLiteStore) GetStatus(ctx context.Context, requestID string) (Status, error) {
	var (
		state     string
		sentAt    *int64
		errMsg    sql.NullString
		expiresAt int64
	)

	err := s.db.QueryRowContext(ctx, `
		SELECT state, sent_at, error, expires_at FROM status WHERE request_id = ?
	`, requestID).Scan(&state, &sentAt, &errMsg, &expiresAt)
	if err == sql.ErrNoRows {
		return Status{}, fmt.Errorf("request not found: %s", requestID)
	}
	if err != nil {
		return Status{}, err
	}

	status := Status{
		State:     state,
		ExpiresAt: time.Unix(expiresAt, 0),
	}
	if sentAt != nil {
		t := time.Unix(*sentAt, 0)
		status.SentAt = &t
	}
	if errMsg.Valid {
		status.Error = errMsg.String
	}

	return status, nil
}

// CleanupExpiredStatus removes expired status records.
func (s *SQLiteStore) CleanupExpiredStatus(ctx context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	result, err := s.db.ExecContext(ctx, `
		DELETE FROM status WHERE expires_at < ?
	`, time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Close closes the database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Serialization helpers using JSON for simplicity.
// Can be replaced with protobuf once the proto is generated.

func serializeNotifications(notifications []QueuedNotification) ([]byte, error) {
	return json.Marshal(notifications)
}

func deserializeNotifications(data []byte) ([]QueuedNotification, error) {
	var notifications []QueuedNotification
	if err := json.Unmarshal(data, &notifications); err != nil {
		return nil, err
	}
	return notifications, nil
}
