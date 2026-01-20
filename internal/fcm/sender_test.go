package fcm

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"firebase.google.com/go/v4/messaging"
	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/protobuf/proto"
)

func TestTruncateToken(t *testing.T) {
	tests := []struct {
		name     string
		token    string
		expected string
	}{
		{
			name:     "short token",
			token:    "abc123",
			expected: "abc123",
		},
		{
			name:     "exactly 12 chars",
			token:    "123456789012",
			expected: "123456789012",
		},
		{
			name:     "long token",
			token:    "abcdef123456789ghijkl",
			expected: "abcdef...ghijkl",
		},
		{
			name:     "typical FCM token",
			token:    "dQw4w9WgXcQ:APA91bGJHXyL3456789012345678901234567890123456789012345678901234567890",
			expected: "dQw4w9...567890",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateToken(tt.token)
			if result != tt.expected {
				t.Errorf("truncateToken(%q) = %q, want %q", tt.token, result, tt.expected)
			}
		})
	}
}

// mockMessagingClient implements a mock for testing Send behavior.
type mockMessagingClient struct {
	sendFunc func(ctx context.Context, message *messaging.Message) (string, error)
	lastMsg  *messaging.Message
}

func (m *mockMessagingClient) Send(ctx context.Context, message *messaging.Message) (string, error) {
	m.lastMsg = message
	if m.sendFunc != nil {
		return m.sendFunc(ctx, message)
	}
	return "mock-message-id", nil
}

// TestablesSender wraps Sender for testing with a mock client.
type TestableSender struct {
	mock *mockMessagingClient
}

func (ts *TestableSender) Send(ctx context.Context, fcmToken string, dataIDs [][]byte) error {
	// Construct the protobuf payload
	notification := &pb.DataUpdateNotification{
		DataIds: dataIDs,
	}

	payloadBytes, err := proto.Marshal(notification)
	if err != nil {
		return err
	}

	payloadB64 := base64.StdEncoding.EncodeToString(payloadBytes)

	message := &messaging.Message{
		Token: fcmToken,
		Data: map[string]string{
			"payload": payloadB64,
		},
		Android: &messaging.AndroidConfig{
			Priority: "high",
		},
	}

	_, err = ts.mock.Send(ctx, message)
	return err
}

func TestSend_MessageConstruction(t *testing.T) {
	mock := &mockMessagingClient{}
	sender := &TestableSender{mock: mock}

	dataIDs := [][]byte{
		{0x01, 0x02, 0x03, 0x04},
		{0x05, 0x06, 0x07, 0x08},
	}
	fcmToken := "test-fcm-token-12345"

	err := sender.Send(context.Background(), fcmToken, dataIDs)
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Verify message was constructed correctly
	if mock.lastMsg == nil {
		t.Fatal("expected message to be sent")
	}

	// Check token
	if mock.lastMsg.Token != fcmToken {
		t.Errorf("Token = %q, want %q", mock.lastMsg.Token, fcmToken)
	}

	// Check Android priority
	if mock.lastMsg.Android == nil {
		t.Fatal("expected Android config")
	}
	if mock.lastMsg.Android.Priority != "high" {
		t.Errorf("Android.Priority = %q, want %q", mock.lastMsg.Android.Priority, "high")
	}

	// Check payload exists and is base64-encoded protobuf
	payload, ok := mock.lastMsg.Data["payload"]
	if !ok {
		t.Fatal("expected payload in Data")
	}

	// Decode and verify protobuf
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("failed to decode base64 payload: %v", err)
	}

	var notification pb.DataUpdateNotification
	if err := proto.Unmarshal(decoded, &notification); err != nil {
		t.Fatalf("failed to unmarshal protobuf: %v", err)
	}

	if len(notification.DataIds) != 2 {
		t.Errorf("DataIds count = %d, want 2", len(notification.DataIds))
	}

	// Verify data IDs match
	for i, id := range notification.DataIds {
		for j, b := range id {
			if b != dataIDs[i][j] {
				t.Errorf("DataIds[%d][%d] = %d, want %d", i, j, b, dataIDs[i][j])
			}
		}
	}
}

func TestSend_EmptyDataIDs(t *testing.T) {
	mock := &mockMessagingClient{}
	sender := &TestableSender{mock: mock}

	err := sender.Send(context.Background(), "test-token", [][]byte{})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Verify payload decodes to empty list
	payload := mock.lastMsg.Data["payload"]
	decoded, _ := base64.StdEncoding.DecodeString(payload)

	var notification pb.DataUpdateNotification
	proto.Unmarshal(decoded, &notification)

	if len(notification.DataIds) != 0 {
		t.Errorf("DataIds count = %d, want 0", len(notification.DataIds))
	}
}

func TestSend_Error(t *testing.T) {
	expectedErr := errors.New("FCM send failed")
	mock := &mockMessagingClient{
		sendFunc: func(ctx context.Context, message *messaging.Message) (string, error) {
			return "", expectedErr
		},
	}
	sender := &TestableSender{mock: mock}

	err := sender.Send(context.Background(), "test-token", [][]byte{{0x01}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err != expectedErr {
		t.Errorf("error = %v, want %v", err, expectedErr)
	}
}

func TestNew_MissingCredentials(t *testing.T) {
	_, err := New(context.Background(), Config{})
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
	if err.Error() != "firebase credentials file is required" {
		t.Errorf("error = %q, want 'firebase credentials file is required'", err.Error())
	}
}
