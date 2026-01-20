// Package fcm provides Firebase Cloud Messaging integration for sending push notifications.
package fcm

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/proto"
)

// Config holds FCM sender configuration.
type Config struct {
	CredentialsFile string
	ProjectID       string
}

// Sender sends notifications to devices via Firebase Cloud Messaging.
type Sender struct {
	client *messaging.Client
}

// New creates a new FCM Sender.
// The credentials file should be a Firebase service account JSON file.
func New(ctx context.Context, cfg Config) (*Sender, error) {
	if cfg.CredentialsFile == "" {
		return nil, errors.New("firebase credentials file is required")
	}

	var opts []option.ClientOption
	opts = append(opts, option.WithCredentialsFile(cfg.CredentialsFile))

	firebaseConfig := &firebase.Config{}
	if cfg.ProjectID != "" {
		firebaseConfig.ProjectID = cfg.ProjectID
	}

	app, err := firebase.NewApp(ctx, firebaseConfig, opts...)
	if err != nil {
		return nil, fmt.Errorf("initializing firebase app: %w", err)
	}

	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting messaging client: %w", err)
	}

	return &Sender{client: client}, nil
}

// Send sends a data-only push notification to the specified FCM token.
// The dataIDs are encoded as a protobuf DataUpdateNotification, then base64-encoded
// and placed in the data payload.
//
// This implements the batcher.Sender interface.
func (s *Sender) Send(ctx context.Context, fcmToken string, dataIDs [][]byte) error {
	// Construct the protobuf payload
	notification := &pb.DataUpdateNotification{
		DataIds: dataIDs,
	}

	payloadBytes, err := proto.Marshal(notification)
	if err != nil {
		return fmt.Errorf("marshaling notification: %w", err)
	}

	// Base64-encode the protobuf
	payloadB64 := base64.StdEncoding.EncodeToString(payloadBytes)

	// Construct the FCM message
	message := &messaging.Message{
		Token: fcmToken,
		Data: map[string]string{
			"payload": payloadB64,
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",
		},
	}

	// Send the message
	messageID, err := s.client.Send(ctx, message)
	if err != nil {
		s.handleError(fcmToken, err)
		return err
	}

	log.Printf("INFO: sent FCM message %s to token %s (%d data IDs)", messageID, truncateToken(fcmToken), len(dataIDs))
	return nil
}

// handleError logs FCM errors with appropriate context.
// Push is best-effort, so errors are logged but don't propagate beyond the return.
func (s *Sender) handleError(fcmToken string, err error) {
	tokenSnippet := truncateToken(fcmToken)

	// Check for specific FCM error types
	if messaging.IsUnregistered(err) {
		log.Printf("WARNING: FCM token %s is no longer valid (NotRegistered)", tokenSnippet)
		return
	}

	if messaging.IsInvalidArgument(err) {
		log.Printf("WARNING: FCM token %s has invalid registration", tokenSnippet)
		return
	}

	// Network or other errors
	log.Printf("ERROR: FCM send failed for token %s: %v", tokenSnippet, err)
}

// truncateToken returns a truncated version of the FCM token for logging.
// FCM tokens are sensitive and should not be fully logged.
func truncateToken(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:6] + "..." + token[len(token)-6:]
}
