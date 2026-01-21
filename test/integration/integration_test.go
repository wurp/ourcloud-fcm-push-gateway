//go:build integration

// Package integration contains integration tests for the push gateway.
// These tests run against a real push gateway binary with stub external services.
//
// Run with: go test -v ./test/integration/... -tags=integration
// Or use: test/integration/run.sh (which starts all services first)
package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"github.com/wurp/ourcloud-fcm-push-gateway/test/integration/testutil"
	"google.golang.org/protobuf/proto"
)

const (
	gatewayURL = "http://localhost:8085"
	fcmStubURL = "http://localhost:9099"
)

// TestFullPushFlow tests the complete flow: request → validation → queue → flush → FCM delivery
func TestFullPushFlow(t *testing.T) {
	// Clear any previous FCM captures
	clearFCMCaptures(t)

	// Send push from bob@oc to alice@oc
	// Consent: fixtures.json defines alice@oc.consents = ["bob@oc", "carol@oc"]
	// Endpoints: fixtures.json defines alice@oc.endpoints with 2 devices
	resp := sendPush(t, "bob@oc", "alice@oc", [][]byte{{0x01, 0x02, 0x03}})

	if !resp.Accepted {
		t.Fatalf("expected accepted=true, got false (error_code=%d, message=%s)", resp.ErrorCode, resp.Message)
	}
	if resp.RequestId == "" {
		t.Error("expected non-empty request_id")
	}

	// Wait for batch window (100ms) + processing time
	time.Sleep(300 * time.Millisecond)

	// Verify FCM received the notification
	captures := getFCMCaptures(t)
	if captures.Count == 0 {
		t.Fatal("expected FCM to receive at least one message")
	}

	// Alice has 2 endpoints, so we should see 2 FCM calls
	if captures.Count != 2 {
		t.Errorf("expected 2 FCM calls (alice has 2 devices), got %d", captures.Count)
	}

	// Verify the tokens match alice's devices
	tokens := make(map[string]bool)
	for _, msg := range captures.Messages {
		tokens[msg.Token] = true
	}
	if !tokens["fcm-token-alice-phone"] {
		t.Error("expected FCM call to fcm-token-alice-phone")
	}
	if !tokens["fcm-token-alice-tablet"] {
		t.Error("expected FCM call to fcm-token-alice-tablet")
	}
}

// TestBatchAccumulation tests that multiple requests within the batch window are accumulated
func TestBatchAccumulation(t *testing.T) {
	clearFCMCaptures(t)

	// Send multiple pushes quickly (within batch window)
	// Uses same sender/recipient as TestFullPushFlow (bob→alice)
	// config.yaml sets batch.window = 100ms, so these accumulate
	for i := 0; i < 5; i++ {
		resp := sendPush(t, "bob@oc", "alice@oc", [][]byte{{byte(i)}})
		if !resp.Accepted {
			t.Fatalf("request %d not accepted: %s", i, resp.Message)
		}
	}

	// Wait for batch to flush
	time.Sleep(300 * time.Millisecond)

	// Should have 2 FCM calls (one per device), each with accumulated data
	captures := getFCMCaptures(t)
	if captures.Count != 2 {
		t.Errorf("expected 2 FCM calls (batched), got %d", captures.Count)
	}
}

// TestNoConsent tests that requests without consent are rejected
func TestNoConsent(t *testing.T) {
	// Consent: fixtures.json defines carol@oc.consents = [] (empty list)
	// alice@oc is NOT in carol's consent list, so this request is rejected
	resp := sendPush(t, "alice@oc", "carol@oc", [][]byte{{0x01}})

	if resp.Accepted {
		t.Error("expected request to be rejected (no consent)")
	}
	if resp.ErrorCode != 2 { // ErrorCodeNoConsent
		t.Errorf("expected error_code=2 (no consent), got %d", resp.ErrorCode)
	}
}

// TestNoEndpoints tests that requests to users with no endpoints are rejected
func TestNoEndpoints(t *testing.T) {
	// Consent: fixtures.json defines nodevice@oc.consents = ["alice@oc"]
	// Endpoints: fixtures.json defines nodevice@oc.endpoints = [] (no devices)
	// Consent passes, but rejected because there's nowhere to deliver
	resp := sendPush(t, "alice@oc", "nodevice@oc", [][]byte{{0x01}})

	if resp.Accepted {
		t.Error("expected request to be rejected (no endpoints)")
	}
	if resp.ErrorCode != 1 { // ErrorCodeNoEndpoints
		t.Errorf("expected error_code=1 (no endpoints), got %d", resp.ErrorCode)
	}
}

// TestStatusAfterFlush tests the status endpoint after delivery
func TestStatusAfterFlush(t *testing.T) {
	clearFCMCaptures(t)

	resp := sendPush(t, "bob@oc", "alice@oc", [][]byte{{0xAA}})
	if !resp.Accepted {
		t.Fatalf("request not accepted: %s", resp.Message)
	}

	requestID := resp.RequestId

	// Wait for flush
	time.Sleep(300 * time.Millisecond)

	// Check status
	status := getStatus(t, requestID)
	if status.State != "sent" {
		t.Errorf("expected state=sent, got %s", status.State)
	}
	if status.SentAt == 0 {
		t.Error("expected non-zero sent_at")
	}
}

// TestStatusNotFound tests status endpoint for unknown request
func TestStatusNotFound(t *testing.T) {
	httpResp, err := http.Get(gatewayURL + "/status/nonexistent-request-id")
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", httpResp.StatusCode)
	}
}

// TestHealthEndpoint tests the health check endpoint
func TestHealthEndpoint(t *testing.T) {
	httpResp, err := http.Get(gatewayURL + "/health")
	if err != nil {
		t.Fatalf("health request failed: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", httpResp.StatusCode)
	}

	var health struct {
		Status   string `json:"status"`
		OurCloud string `json:"ourcloud"`
	}
	if err := json.NewDecoder(httpResp.Body).Decode(&health); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}

	if health.Status != "ok" {
		t.Errorf("expected status=ok, got %s", health.Status)
	}
}

// Helper functions

func sendPush(t *testing.T, sender, target string, dataIDs [][]byte) *pb.PushResponse {
	t.Helper()

	pushReq := &pb.PushRequest{
		SenderUsername: sender,
		TargetUsername: target,
		Timestamp:      time.Now().Unix(),
		DataIds:        dataIDs,
	}

	// Sign the request with the sender's private key
	if err := testutil.SignPushRequest(pushReq); err != nil {
		t.Fatalf("failed to sign PushRequest: %v", err)
	}

	body, err := proto.Marshal(pushReq)
	if err != nil {
		t.Fatalf("failed to marshal PushRequest: %v", err)
	}

	httpResp, err := http.Post(gatewayURL+"/push", "application/x-protobuf", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("push request failed: %v", err)
	}
	defer httpResp.Body.Close()

	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	var resp pb.PushResponse
	if err := proto.Unmarshal(respBody, &resp); err != nil {
		t.Fatalf("failed to unmarshal PushResponse: %v", err)
	}

	return &resp
}

type statusResponse struct {
	State     string `json:"state"`
	SentAt    int64  `json:"sent_at,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

func getStatus(t *testing.T, requestID string) *statusResponse {
	t.Helper()

	httpResp, err := http.Get(gatewayURL + "/status/" + requestID)
	if err != nil {
		t.Fatalf("status request failed: %v", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode != http.StatusOK {
		t.Fatalf("status request returned %d", httpResp.StatusCode)
	}

	var resp statusResponse
	if err := json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}

	return &resp
}

type fcmCaptures struct {
	Count    int          `json:"count"`
	Messages []fcmMessage `json:"messages"`
}

type fcmMessage struct {
	Token string            `json:"token"`
	Data  map[string]string `json:"data"`
}

func getFCMCaptures(t *testing.T) *fcmCaptures {
	t.Helper()

	httpResp, err := http.Get(fcmStubURL + "/captured")
	if err != nil {
		t.Fatalf("failed to get FCM captures: %v", err)
	}
	defer httpResp.Body.Close()

	var captures fcmCaptures
	if err := json.NewDecoder(httpResp.Body).Decode(&captures); err != nil {
		t.Fatalf("failed to decode FCM captures: %v", err)
	}

	return &captures
}

func clearFCMCaptures(t *testing.T) {
	t.Helper()

	req, _ := http.NewRequest(http.MethodDelete, fcmStubURL+"/captured", nil)
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to clear FCM captures: %v", err)
	}
	httpResp.Body.Close()
}

func init() {
	// Give services a moment to be ready when tests start
	fmt.Println("Integration tests starting - services should be running")
}
