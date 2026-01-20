package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

func TestHandleGetStatus_BeforeFlush_NotFound(t *testing.T) {
	// Status is only stored after flush, so a queued (but not flushed)
	// request will not be found in the status table.
	b, cleanup := createTestBatcher(t)
	defer cleanup()
	h := NewStatusHandler(b)

	// Queue a notification to get a request ID
	requestID, err := b.Queue(context.Background(), "test-token", [][]byte{{1, 2, 3}})
	if err != nil {
		t.Fatalf("failed to queue: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/status/"+requestID, nil)
	rr := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", requestID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetStatus(rr, req)

	// Before flush, status is not in the DB yet
	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d (status not stored until flush)", rr.Code, http.StatusNotFound)
	}
}

func TestHandleGetStatus_NotFound(t *testing.T) {
	b, cleanup := createTestBatcher(t)
	defer cleanup()
	h := NewStatusHandler(b)

	req := httptest.NewRequest(http.MethodGet, "/status/nonexistent-id", nil)
	rr := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent-id")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetStatus(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}
}

func TestHandleGetStatus_MissingID(t *testing.T) {
	b, cleanup := createTestBatcher(t)
	defer cleanup()
	h := NewStatusHandler(b)

	req := httptest.NewRequest(http.MethodGet, "/status/", nil)
	rr := httptest.NewRecorder()

	// Empty URL param
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetStatus(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestHandleGetStatus_AfterFlush_Sent(t *testing.T) {
	b, cleanup := createTestBatcher(t)
	defer cleanup()
	h := NewStatusHandler(b)

	// Queue a notification
	requestID, err := b.Queue(context.Background(), "test-token", [][]byte{{1, 2, 3}})
	if err != nil {
		t.Fatalf("failed to queue: %v", err)
	}

	// Queue enough to trigger immediate flush (MaxBatchSize is 100, so queue 100)
	for i := 0; i < 99; i++ {
		_, err := b.Queue(context.Background(), "test-token", [][]byte{{byte(i)}})
		if err != nil {
			t.Fatalf("failed to queue: %v", err)
		}
	}

	// Wait for async flush to complete
	time.Sleep(100 * time.Millisecond)

	// Now check status
	req := httptest.NewRequest(http.MethodGet, "/status/"+requestID, nil)
	rr := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", requestID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	var resp StatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.State != "sent" {
		t.Errorf("state = %q, want %q", resp.State, "sent")
	}
	if resp.SentAt == 0 {
		t.Error("expected non-zero sent_at")
	}
	if resp.ExpiresAt == 0 {
		t.Error("expected non-zero expires_at")
	}
}

func TestHandleGetStatus_ContentType(t *testing.T) {
	b, cleanup := createTestBatcher(t)
	defer cleanup()
	h := NewStatusHandler(b)

	// Queue and flush to get a valid status
	requestID, _ := b.Queue(context.Background(), "test-token", [][]byte{{1}})
	for i := 0; i < 99; i++ {
		b.Queue(context.Background(), "test-token", [][]byte{{byte(i)}})
	}
	time.Sleep(100 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/status/"+requestID, nil)
	rr := httptest.NewRecorder()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", requestID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	h.HandleGetStatus(rr, req)

	contentType := rr.Header().Get("Content-Type")
	if contentType != "application/json" {
		t.Errorf("Content-Type = %q, want %q", contentType, "application/json")
	}
}
