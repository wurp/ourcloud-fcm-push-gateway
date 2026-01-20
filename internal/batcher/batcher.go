// Package batcher provides notification batching with persistence and timed flushing.
package batcher

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/wurp/ourcloud-fcm-push-gateway/internal/store"
)

// Sender sends batched notifications to FCM.
type Sender interface {
	Send(ctx context.Context, fcmToken string, dataIDs [][]byte) error
}

// Config holds batcher configuration.
type Config struct {
	BatchWindow     time.Duration
	MaxBatchSize    int
	LockTimeout     time.Duration
	StatusRetention time.Duration
}

// Batcher queues notifications per endpoint and flushes periodically.
type Batcher struct {
	store           store.Store
	sender          Sender
	cfg             Config

	mu      sync.Mutex
	batches map[string]*batchEntry
	timers  map[string]*time.Timer
	stopped bool
}

// batchEntry holds a batch and its per-endpoint lock.
type batchEntry struct {
	mu    sync.Mutex
	batch *store.Batch
}

// New creates a new Batcher.
func New(s store.Store, sender Sender, cfg Config) *Batcher {
	return &Batcher{
		store:   s,
		sender:  sender,
		cfg:     cfg,
		batches: make(map[string]*batchEntry),
		timers:  make(map[string]*time.Timer),
	}
}

// Queue adds a notification to the batch for the given FCM token.
// Returns the generated request ID for status tracking.
func (b *Batcher) Queue(ctx context.Context, fcmToken string, dataIDs [][]byte) (string, error) {
	requestID := uuid.New().String()

	entry := b.getOrCreateEntry(fcmToken)

	// Acquire per-endpoint lock with timeout
	locked := make(chan struct{})
	go func() {
		entry.mu.Lock()
		close(locked)
	}()

	select {
	case <-locked:
		// Got the lock
	case <-time.After(b.cfg.LockTimeout):
		log.Printf("ERROR: lock timeout for fcmToken %s, dropping notification", fcmToken)
		return "", context.DeadlineExceeded
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer entry.mu.Unlock()

	// Check if batcher is stopped
	b.mu.Lock()
	if b.stopped {
		b.mu.Unlock()
		return "", context.Canceled
	}
	b.mu.Unlock()

	// Add notification to batch
	now := time.Now()
	isNewBatch := entry.batch == nil || len(entry.batch.Notifications) == 0

	if entry.batch == nil {
		entry.batch = &store.Batch{
			CreatedAt: now,
			FlushAt:   now.Add(b.cfg.BatchWindow),
		}
	}

	entry.batch.Notifications = append(entry.batch.Notifications, store.QueuedNotification{
		DataIDs:   dataIDs,
		RequestID: requestID,
	})

	// Persist to DB
	if err := b.store.SaveBatch(ctx, fcmToken, entry.batch); err != nil {
		log.Printf("ERROR: failed to persist batch for %s: %v", fcmToken, err)
		// Continue anyway - we have it in memory
	}

	// Start timer if this is a new batch
	if isNewBatch {
		b.startTimer(fcmToken, entry.batch.FlushAt.Sub(now))
	}

	// Check if we need to flush immediately due to size
	if len(entry.batch.Notifications) >= b.cfg.MaxBatchSize {
		b.stopTimer(fcmToken)
		go b.flush(fcmToken)
	}

	return requestID, nil
}

// getOrCreateEntry returns the batch entry for an FCM token, creating if needed.
func (b *Batcher) getOrCreateEntry(fcmToken string) *batchEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	entry, ok := b.batches[fcmToken]
	if !ok {
		entry = &batchEntry{}
		b.batches[fcmToken] = entry
	}
	return entry
}

// startTimer starts the flush timer for an endpoint.
func (b *Batcher) startTimer(fcmToken string, duration time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.stopped {
		return
	}

	// Cancel existing timer if any
	if timer, ok := b.timers[fcmToken]; ok {
		timer.Stop()
	}

	b.timers[fcmToken] = time.AfterFunc(duration, func() {
		b.flush(fcmToken)
	})
}

// stopTimer stops the flush timer for an endpoint.
func (b *Batcher) stopTimer(fcmToken string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if timer, ok := b.timers[fcmToken]; ok {
		timer.Stop()
		delete(b.timers, fcmToken)
	}
}

// flush sends the batch for an FCM token and updates status (async, for timer callback).
func (b *Batcher) flush(fcmToken string) {
	b.flushSync(context.Background(), fcmToken)
}

// flushSync sends the batch for an FCM token and updates status.
func (b *Batcher) flushSync(ctx context.Context, fcmToken string) {
	b.mu.Lock()
	entry, ok := b.batches[fcmToken]
	if !ok {
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()

	entry.mu.Lock()
	defer entry.mu.Unlock()

	if entry.batch == nil || len(entry.batch.Notifications) == 0 {
		return
	}

	// Collect all data IDs
	var allDataIDs [][]byte
	for _, notif := range entry.batch.Notifications {
		allDataIDs = append(allDataIDs, notif.DataIDs...)
	}

	// Send to FCM
	now := time.Now()
	var status store.Status

	err := b.sender.Send(ctx, fcmToken, allDataIDs)
	if err != nil {
		log.Printf("ERROR: flush failed for %s: %v", fcmToken, err)
		status = store.Status{
			State:     store.StatusFailed,
			Error:     err.Error(),
			ExpiresAt: now.Add(b.cfg.StatusRetention),
		}
	} else {
		status = store.Status{
			State:     store.StatusSent,
			SentAt:    &now,
			ExpiresAt: now.Add(b.cfg.StatusRetention),
		}
	}

	// Delete batch from DB and set status
	if err := b.store.DeleteBatchAndSetStatus(ctx, fcmToken, status); err != nil {
		log.Printf("ERROR: failed to update status for %s: %v", fcmToken, err)
	}

	// Clear from memory
	entry.batch = nil

	b.mu.Lock()
	delete(b.timers, fcmToken)
	b.mu.Unlock()
}

// Recover loads persisted batches from the database and flushes them synchronously.
// Call this at startup before processing new requests.
func (b *Batcher) Recover(ctx context.Context) error {
	const pageSize = 100

	for {
		batches, err := b.store.LoadOldestBatches(ctx, pageSize)
		if err != nil {
			return err
		}

		if len(batches) == 0 {
			break
		}

		// Flush each batch synchronously
		for fcmToken, batch := range batches {
			entry := b.getOrCreateEntry(fcmToken)
			entry.batch = batch
			b.flushSync(ctx, fcmToken)
		}

		if len(batches) < pageSize {
			break
		}
		// Flushed batches are deleted from DB, so next query returns new oldest
	}

	return nil
}

// Stop gracefully shuts down the batcher.
// Pending batches remain in the database for recovery on restart.
// In-memory batches that haven't been persisted yet may be lost, but this window
// is tiny since Queue() persists to DB immediately. Push notifications are
// best-effort; the Android app syncs periodically regardless.
func (b *Batcher) Stop() {
	b.mu.Lock()
	b.stopped = true

	// Stop all timers
	for _, timer := range b.timers {
		timer.Stop()
	}
	b.timers = make(map[string]*time.Timer)
	b.mu.Unlock()
}

// GetStatus returns the delivery status for a request.
func (b *Batcher) GetStatus(ctx context.Context, requestID string) (store.Status, error) {
	return b.store.GetStatus(ctx, requestID)
}
