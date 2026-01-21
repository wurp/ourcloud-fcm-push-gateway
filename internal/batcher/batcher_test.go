package batcher

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wurp/ourcloud-fcm-push-gateway/internal/store"
)

// mockSender is a test sender that records calls and can be configured to fail.
type mockSender struct {
	mu        sync.Mutex
	calls     []sendCall
	failCount int // number of calls to fail before succeeding
	failErr   error
}

type sendCall struct {
	FcmToken string
	DataIDs  [][]byte
}

func (m *mockSender) Send(ctx context.Context, fcmToken string, dataIDs [][]byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, sendCall{FcmToken: fcmToken, DataIDs: dataIDs})

	if m.failCount > 0 {
		m.failCount--
		if m.failErr != nil {
			return m.failErr
		}
		return errors.New("mock send error")
	}

	return nil
}

func (m *mockSender) getCalls() []sendCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]sendCall{}, m.calls...)
}

func (m *mockSender) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

// createTestStore creates a temporary SQLite store for testing.
func createTestStore(t *testing.T) (store.Store, func()) {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "batcher-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	st, err := store.New(store.Config{Path: tmpFile.Name()})
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to create store: %v", err)
	}

	cleanup := func() {
		st.Close()
		os.Remove(tmpFile.Name())
	}

	return st, cleanup
}

func TestQueue_FirstItemStartsTimer(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     50 * time.Millisecond, // Short window for testing
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Queue first item
	requestID, err := b.Queue(context.Background(), "token1", [][]byte{{1, 2, 3}})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}
	if requestID == "" {
		t.Error("expected non-empty request ID")
	}

	// Verify timer was started by checking the timers map
	b.mu.Lock()
	_, hasTimer := b.timers["token1"]
	b.mu.Unlock()

	if !hasTimer {
		t.Error("expected timer to be started for first item")
	}

	// Verify no immediate send (batch window not reached)
	if sender.callCount() != 0 {
		t.Errorf("expected no immediate send, got %d calls", sender.callCount())
	}
}

func TestQueue_MaxSizeTriggersImmediateFlush(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     time.Minute, // Long window - won't trigger
		MaxBatchSize:    5,           // Small batch size for testing
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Queue items up to max size
	for i := 0; i < 5; i++ {
		_, err := b.Queue(context.Background(), "token1", [][]byte{{byte(i)}})
		if err != nil {
			t.Fatalf("Queue() error = %v", err)
		}
	}

	// Wait for async flush
	time.Sleep(50 * time.Millisecond)

	// Verify immediate flush occurred
	calls := sender.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 send call, got %d", len(calls))
	}

	// Verify all data IDs were sent
	if len(calls[0].DataIDs) != 5 {
		t.Errorf("expected 5 data IDs, got %d", len(calls[0].DataIDs))
	}
}

func TestQueue_TimerExpiryFlushes(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     30 * time.Millisecond, // Short window
		MaxBatchSize:    100,                   // Won't trigger by size
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Queue single item
	_, err := b.Queue(context.Background(), "token1", [][]byte{{1, 2, 3}})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}

	// Verify no immediate send
	if sender.callCount() != 0 {
		t.Error("expected no immediate send")
	}

	// Wait for timer to expire
	time.Sleep(60 * time.Millisecond)

	// Verify flush occurred
	calls := sender.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 send call after timer expiry, got %d", len(calls))
	}

	if calls[0].FcmToken != "token1" {
		t.Errorf("expected token1, got %s", calls[0].FcmToken)
	}
}

func TestRecover_RestoresAndFlushesPendingBatches(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "batcher-recover-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()
	dbPath := tmpFile.Name()
	defer os.Remove(dbPath)

	// Phase 1: Create batcher, queue items, stop without flushing
	st1, err := store.New(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}

	sender1 := &mockSender{}
	b1 := New(st1, sender1, Config{
		BatchWindow:     time.Minute, // Long window - won't auto-flush
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})

	// Queue items to two different endpoints
	_, err = b1.Queue(context.Background(), "token-a", [][]byte{{1, 2, 3}})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}
	_, err = b1.Queue(context.Background(), "token-b", [][]byte{{4, 5, 6}})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}

	// Verify nothing sent yet
	if sender1.callCount() != 0 {
		t.Errorf("expected no sends before stop, got %d", sender1.callCount())
	}

	// Stop batcher (simulates shutdown)
	b1.Stop()
	st1.Close()

	// Phase 2: Create new batcher, recover persisted batches
	st2, err := store.New(store.Config{Path: dbPath})
	if err != nil {
		t.Fatalf("failed to reopen store: %v", err)
	}
	defer st2.Close()

	sender2 := &mockSender{}
	b2 := New(st2, sender2, Config{
		BatchWindow:     time.Minute,
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b2.Stop()

	// Recover should flush persisted batches
	err = b2.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}

	// Verify both batches were flushed
	calls := sender2.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 send calls after recovery, got %d", len(calls))
	}

	// Verify tokens (order may vary)
	tokens := make(map[string]bool)
	for _, call := range calls {
		tokens[call.FcmToken] = true
	}
	if !tokens["token-a"] || !tokens["token-b"] {
		t.Errorf("expected both token-a and token-b to be flushed, got %v", tokens)
	}
}

func TestQueue_MultipleEndpoints(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     30 * time.Millisecond,
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Queue to different endpoints
	_, _ = b.Queue(context.Background(), "token1", [][]byte{{1}})
	_, _ = b.Queue(context.Background(), "token2", [][]byte{{2}})
	_, _ = b.Queue(context.Background(), "token1", [][]byte{{3}}) // Add to first endpoint

	// Wait for timers to expire
	time.Sleep(60 * time.Millisecond)

	// Verify separate batches for each endpoint
	calls := sender.getCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 send calls (one per endpoint), got %d", len(calls))
	}

	// Verify data IDs were batched correctly
	dataByToken := make(map[string]int)
	for _, call := range calls {
		dataByToken[call.FcmToken] = len(call.DataIDs)
	}

	if dataByToken["token1"] != 2 {
		t.Errorf("expected 2 data IDs for token1, got %d", dataByToken["token1"])
	}
	if dataByToken["token2"] != 1 {
		t.Errorf("expected 1 data ID for token2, got %d", dataByToken["token2"])
	}
}

func TestQueue_StatusAfterFlush(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     20 * time.Millisecond,
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Queue item
	requestID, err := b.Queue(context.Background(), "token1", [][]byte{{1}})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}

	// Status should not be found before flush
	_, err = b.GetStatus(context.Background(), requestID)
	if err == nil {
		t.Error("expected error for status before flush")
	}

	// Wait for flush
	time.Sleep(50 * time.Millisecond)

	// Status should now be "sent"
	status, err := b.GetStatus(context.Background(), requestID)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}

	if status.State != store.StatusSent {
		t.Errorf("expected state=%q, got %q", store.StatusSent, status.State)
	}
	if status.SentAt == nil {
		t.Error("expected non-nil SentAt")
	}
}

func TestQueue_StatusAfterFailedFlush(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{
		failCount: 1,
		failErr:   errors.New("FCM unavailable"),
	}
	b := New(st, sender, Config{
		BatchWindow:     20 * time.Millisecond,
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Queue item
	requestID, err := b.Queue(context.Background(), "token1", [][]byte{{1}})
	if err != nil {
		t.Fatalf("Queue() error = %v", err)
	}

	// Wait for flush
	time.Sleep(50 * time.Millisecond)

	// Status should be "failed"
	status, err := b.GetStatus(context.Background(), requestID)
	if err != nil {
		t.Fatalf("GetStatus() error = %v", err)
	}

	if status.State != store.StatusFailed {
		t.Errorf("expected state=%q, got %q", store.StatusFailed, status.State)
	}
	if status.Error != "FCM unavailable" {
		t.Errorf("expected error=%q, got %q", "FCM unavailable", status.Error)
	}
}

func TestQueue_StoppedBatcherRejects(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     time.Minute,
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})

	// Stop the batcher
	b.Stop()

	// Queue should fail
	_, err := b.Queue(context.Background(), "token1", [][]byte{{1}})
	if err == nil {
		t.Error("expected error when queuing to stopped batcher")
	}
}

func TestQueue_ConcurrentAccess(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     100 * time.Millisecond,
		MaxBatchSize:    1000,
		LockTimeout:     5 * time.Second,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Concurrent queuing from multiple goroutines
	var wg sync.WaitGroup
	var successCount int32
	numGoroutines := 10
	itemsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < itemsPerGoroutine; j++ {
				token := "token" // All go to same endpoint
				_, err := b.Queue(context.Background(), token, [][]byte{{byte(goroutineID), byte(j)}})
				if err == nil {
					atomic.AddInt32(&successCount, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	expectedTotal := numGoroutines * itemsPerGoroutine
	if int(successCount) != expectedTotal {
		t.Errorf("expected %d successful queues, got %d", expectedTotal, successCount)
	}

	// Wait for flush
	time.Sleep(150 * time.Millisecond)

	// Verify all items were sent in single batch
	calls := sender.getCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 batch, got %d", len(calls))
	}

	if len(calls[0].DataIDs) != expectedTotal {
		t.Errorf("expected %d data IDs in batch, got %d", expectedTotal, len(calls[0].DataIDs))
	}
}

func TestRecover_EmptyDatabase(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     time.Minute,
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})
	defer b.Stop()

	// Recover on empty database should succeed
	err := b.Recover(context.Background())
	if err != nil {
		t.Fatalf("Recover() error = %v", err)
	}

	// No sends should occur
	if sender.callCount() != 0 {
		t.Errorf("expected no sends for empty database, got %d", sender.callCount())
	}
}

func TestStop_CancelsTimers(t *testing.T) {
	st, cleanup := createTestStore(t)
	defer cleanup()

	sender := &mockSender{}
	b := New(st, sender, Config{
		BatchWindow:     100 * time.Millisecond,
		MaxBatchSize:    100,
		LockTimeout:     100 * time.Millisecond,
		StatusRetention: time.Hour,
	})

	// Queue item to start timer
	_, _ = b.Queue(context.Background(), "token1", [][]byte{{1}})

	// Verify timer exists
	b.mu.Lock()
	_, hasTimer := b.timers["token1"]
	b.mu.Unlock()
	if !hasTimer {
		t.Error("expected timer to exist")
	}

	// Stop should cancel timers
	b.Stop()

	// Verify timers cleared
	b.mu.Lock()
	timerCount := len(b.timers)
	b.mu.Unlock()

	if timerCount != 0 {
		t.Errorf("expected no timers after stop, got %d", timerCount)
	}

	// Wait past the batch window
	time.Sleep(150 * time.Millisecond)

	// Verify no flush occurred (timer was cancelled)
	if sender.callCount() != 0 {
		t.Errorf("expected no sends after stop, got %d", sender.callCount())
	}
}
