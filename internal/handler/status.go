// Package handler provides HTTP request handlers for the push gateway.
package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/wurp/ourcloud-fcm-push-gateway/internal/batcher"
)

// StatusHandler handles status query requests.
type StatusHandler struct {
	batcher *batcher.Batcher
}

// NewStatusHandler creates a new StatusHandler.
func NewStatusHandler(b *batcher.Batcher) *StatusHandler {
	return &StatusHandler{
		batcher: b,
	}
}

// StatusResponse is the JSON response for GET /status/{id}.
type StatusResponse struct {
	State     string `json:"state"`                // "queued", "sent", "failed"
	SentAt    int64  `json:"sent_at,omitempty"`    // Unix timestamp (seconds), omitted if not sent
	Error     string `json:"error,omitempty"`      // Error message if failed
	ExpiresAt int64  `json:"expires_at,omitempty"` // Unix timestamp (seconds) when record expires
}

// HandleGetStatus handles GET /status/{id} requests.
// Returns JSON with delivery status for the given request ID.
//
// HTTP Status Codes:
//   - 200 OK: Status found
//   - 400 Bad Request: Missing request ID
//   - 404 Not Found: Request ID not found or expired
//   - 500 Internal Server Error: Database error
func (h *StatusHandler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	requestID := chi.URLParam(r, "id")
	if requestID == "" {
		http.Error(w, "missing request ID", http.StatusBadRequest)
		return
	}

	status, err := h.batcher.GetStatus(r.Context(), requestID)
	if err != nil {
		if strings.Contains(err.Error(), "request not found") {
			http.Error(w, "request not found", http.StatusNotFound)
			return
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	resp := &StatusResponse{
		State:     status.State,
		Error:     status.Error,
		ExpiresAt: status.ExpiresAt.Unix(),
	}
	if status.SentAt != nil {
		resp.SentAt = status.SentAt.Unix()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
