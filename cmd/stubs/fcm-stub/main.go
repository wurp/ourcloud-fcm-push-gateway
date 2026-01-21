// FCM HTTP stub server for integration testing.
// This stub captures FCM send requests and returns configurable responses.
//
// # Authentication Flow
//
// The Firebase Admin SDK authenticates using OAuth 2.0 with service account credentials:
//  1. SDK reads the service account JSON file (fake-credentials.json in tests)
//  2. SDK creates a JWT signed with the private key from that file
//  3. SDK POSTs the JWT to the token_uri specified in the credentials
//  4. Token endpoint returns an access token (this stub returns a fake one)
//  5. SDK includes "Authorization: Bearer <token>" in FCM API calls
//
// For this to work, fake-credentials.json must have a valid RSA private key
// (so the SDK can sign JWTs), and token_uri must point to this stub.
//
// # Usage
//
//	fcm-stub -port 9099 -project test-project
//
// The stub exposes:
//   - POST /v1/projects/{project}/messages:send - captures FCM messages
//   - POST /projects/{project}/messages:send - same, without /v1/ prefix
//   - POST /oauth2/v4/token - returns fake OAuth tokens
//   - GET /captured - returns all captured messages as JSON
//   - DELETE /captured - clears captured messages
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
)

// CapturedMessage represents a captured FCM send request.
type CapturedMessage struct {
	Token     string            `json:"token"`
	Data      map[string]string `json:"data"`
	Timestamp time.Time         `json:"timestamp"`
	RawBody   json.RawMessage   `json:"raw_body"`
}

// FCMStub captures and responds to FCM requests.
type FCMStub struct {
	mu       sync.Mutex
	messages []CapturedMessage

	// Configurable behavior
	failNext     bool
	failNextErr  string
	projectID    string
}

func NewFCMStub(projectID string) *FCMStub {
	return &FCMStub{
		messages:  make([]CapturedMessage, 0),
		projectID: projectID,
	}
}

// HandleSend handles POST /v1/projects/{project}/messages:send
func (s *FCMStub) HandleSend(w http.ResponseWriter, r *http.Request) {
	project := chi.URLParam(r, "project")
	if project != s.projectID {
		http.Error(w, fmt.Sprintf("project mismatch: expected %s, got %s", s.projectID, project), http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Parse the FCM request
	var fcmReq struct {
		Message struct {
			Token   string            `json:"token"`
			Data    map[string]string `json:"data"`
			Android struct {
				Priority string `json:"priority"`
			} `json:"android"`
		} `json:"message"`
	}

	if err := json.Unmarshal(body, &fcmReq); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Check if we should fail
	if s.failNext {
		s.failNext = false
		errMsg := s.failNextErr
		if errMsg == "" {
			errMsg = "INTERNAL: simulated failure"
		}
		log.Printf("FCM stub: failing request to %s", truncateToken(fcmReq.Message.Token))
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"code":    500,
				"message": errMsg,
				"status":  "INTERNAL",
			},
		})
		return
	}

	// Capture the message
	captured := CapturedMessage{
		Token:     fcmReq.Message.Token,
		Data:      fcmReq.Message.Data,
		Timestamp: time.Now(),
		RawBody:   body,
	}
	s.messages = append(s.messages, captured)

	log.Printf("FCM stub: captured message to %s", truncateToken(fcmReq.Message.Token))

	// Return success response
	msgID := fmt.Sprintf("projects/%s/messages/%d", s.projectID, len(s.messages))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"name": msgID,
	})
}

// HandleGetCaptured returns all captured messages.
func (s *FCMStub) HandleGetCaptured(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"count":    len(s.messages),
		"messages": s.messages,
	})
}

// HandleClearCaptured clears all captured messages.
func (s *FCMStub) HandleClearCaptured(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := len(s.messages)
	s.messages = make([]CapturedMessage, 0)

	log.Printf("FCM stub: cleared %d captured messages", count)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]int{"cleared": count})
}

// HandleSetFailNext configures the next send to fail.
func (s *FCMStub) HandleSetFailNext(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var req struct {
		Error string `json:"error"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	s.failNext = true
	s.failNextErr = req.Error

	log.Printf("FCM stub: configured to fail next request")
	w.WriteHeader(http.StatusOK)
}

func truncateToken(token string) string {
	if len(token) <= 12 {
		return token
	}
	return token[:6] + "..." + token[len(token)-6:]
}

func main() {
	port := flag.Int("port", 9099, "HTTP server port")
	projectID := flag.String("project", "test-project", "Firebase project ID")
	flag.Parse()

	stub := NewFCMStub(*projectID)

	r := chi.NewRouter()

	// FCM API endpoint - handle both with and without /v1/ prefix
	r.Post("/v1/projects/{project}/messages:send", stub.HandleSend)
	r.Post("/projects/{project}/messages:send", stub.HandleSend)

	// Test control endpoints
	r.Get("/captured", stub.HandleGetCaptured)
	r.Delete("/captured", stub.HandleClearCaptured)
	r.Post("/fail-next", stub.HandleSetFailNext)

	// Health check
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	// Debug: catch-all to log unmatched requests
	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("FCM stub: unmatched request: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	// OAuth2 token endpoint (FCM SDK may call this)
	r.Post("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	// Handle token endpoint variations
	r.Post("/oauth2/v4/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"access_token": "fake-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: r,
	}

	// Graceful shutdown
	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Shutting down...")
		srv.Close()
	}()

	// Print available endpoints
	log.Printf("FCM stub listening on :%d", *port)
	log.Printf("  POST /v1/projects/%s/messages:send - FCM send endpoint", *projectID)
	log.Printf("  GET  /captured - get captured messages")
	log.Printf("  DELETE /captured - clear captured messages")
	log.Printf("  POST /fail-next - configure next send to fail")

	if err := srv.ListenAndServe(); err != nil && !strings.Contains(err.Error(), "Server closed") {
		log.Fatalf("Failed to serve: %v", err)
	}
}
