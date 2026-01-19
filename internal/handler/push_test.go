package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/protobuf/proto"
)

// mockOurCloudClient is a mock implementation for testing.
// It implements the OurCloudClient interface with configurable behavior.
type mockOurCloudClient struct {
	verifyResult     bool
	verifyErr        error
	hasConsentResult bool
	hasConsentErr    error
	endpointsResult  *pb.PushEndpointList
	endpointsErr     error
}

func (m *mockOurCloudClient) VerifyPushRequest(ctx context.Context, req *pb.PushRequest) (bool, error) {
	return m.verifyResult, m.verifyErr
}

func (m *mockOurCloudClient) HasConsent(ctx context.Context, recipientUsername, senderUsername string) (bool, error) {
	return m.hasConsentResult, m.hasConsentErr
}

func (m *mockOurCloudClient) GetEndpoints(ctx context.Context, username string) (*pb.PushEndpointList, error) {
	return m.endpointsResult, m.endpointsErr
}

func TestHandlePush_MalformedRequest_EmptyBody(t *testing.T) {
	h := NewPushHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/push", nil)
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for empty body")
	}
	if resp.ErrorCode != ErrorCodeInvalidRequest {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeInvalidRequest, resp.ErrorCode)
	}
}

func TestHandlePush_MalformedRequest_InvalidContentType(t *testing.T) {
	h := NewPushHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader([]byte("invalid")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for invalid content type")
	}
	if resp.ErrorCode != ErrorCodeInvalidRequest {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeInvalidRequest, resp.ErrorCode)
	}
}

func TestHandlePush_MalformedRequest_InvalidProtobuf(t *testing.T) {
	h := NewPushHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader([]byte("not-valid-protobuf")))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for invalid protobuf")
	}
	if resp.ErrorCode != ErrorCodeInvalidRequest {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeInvalidRequest, resp.ErrorCode)
	}
}

func TestHandlePush_MalformedRequest_MissingSenderUsername(t *testing.T) {
	h := NewPushHandler(nil)

	pushReq := &pb.PushRequest{
		TargetUsername: "bob@oc",
		Signature:      []byte("sig"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for missing sender_username")
	}
	if resp.ErrorCode != ErrorCodeInvalidRequest {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeInvalidRequest, resp.ErrorCode)
	}
}

func TestHandlePush_MalformedRequest_MissingTarget(t *testing.T) {
	h := NewPushHandler(nil)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		Signature:      []byte("sig"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for missing target")
	}
	if resp.ErrorCode != ErrorCodeInvalidRequest {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeInvalidRequest, resp.ErrorCode)
	}
}

func TestHandlePush_MalformedRequest_MissingSignature(t *testing.T) {
	h := NewPushHandler(nil)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for missing signature")
	}
	if resp.ErrorCode != ErrorCodeInvalidRequest {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeInvalidRequest, resp.ErrorCode)
	}
}

func TestParseRequest_ValidProtobuf(t *testing.T) {
	h := NewPushHandler(nil)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("sig"),
		Timestamp:      1234567890,
	}
	body := marshalPushRequest(t, pushReq)

	httpReq := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/x-protobuf")

	parsed, err := h.parseRequest(httpReq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.SenderUsername != "alice@oc" {
		t.Errorf("sender_username = %q, want %q", parsed.SenderUsername, "alice@oc")
	}
	if parsed.TargetUsername != "bob@oc" {
		t.Errorf("target_username = %q, want %q", parsed.TargetUsername, "bob@oc")
	}
	if parsed.Timestamp != 1234567890 {
		t.Errorf("timestamp = %d, want %d", parsed.Timestamp, 1234567890)
	}
}

func TestParseRequest_AcceptsProtobufContentType(t *testing.T) {
	h := NewPushHandler(nil)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("sig"),
	}
	body := marshalPushRequest(t, pushReq)

	// Test "application/protobuf" (alternative content type)
	httpReq := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/protobuf")

	_, err := h.parseRequest(httpReq)
	if err != nil {
		t.Errorf("should accept application/protobuf: %v", err)
	}
}

func TestValidateRequest(t *testing.T) {
	h := NewPushHandler(nil)

	tests := []struct {
		name    string
		req     *pb.PushRequest
		wantErr bool
	}{
		{
			name: "valid with target_username",
			req: &pb.PushRequest{
				SenderUsername: "alice@oc",
				TargetUsername: "bob@oc",
				Signature:      []byte("sig"),
			},
			wantErr: false,
		},
		{
			name: "valid with target_node_ids",
			req: &pb.PushRequest{
				SenderUsername: "alice@oc",
				TargetNodeIds:  []string{"node1"},
				Signature:      []byte("sig"),
			},
			wantErr: false,
		},
		{
			name: "missing sender",
			req: &pb.PushRequest{
				TargetUsername: "bob@oc",
				Signature:      []byte("sig"),
			},
			wantErr: true,
		},
		{
			name: "missing target",
			req: &pb.PushRequest{
				SenderUsername: "alice@oc",
				Signature:      []byte("sig"),
			},
			wantErr: true,
		},
		{
			name: "missing signature",
			req: &pb.PushRequest{
				SenderUsername: "alice@oc",
				TargetUsername: "bob@oc",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := h.validateRequest(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateRequest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWriteResponse_StatusCodes(t *testing.T) {
	h := NewPushHandler(nil)

	tests := []struct {
		name       string
		errorCode  int32
		wantStatus int
	}{
		{"success", ErrorCodeSuccess, http.StatusOK},
		{"invalid_request", ErrorCodeInvalidRequest, http.StatusBadRequest},
		{"signature_failed", ErrorCodeSignatureFailed, http.StatusUnauthorized},
		{"no_consent", ErrorCodeNoConsent, http.StatusForbidden},
		{"no_endpoints", ErrorCodeNoEndpoints, http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			h.writeResponse(rr, &PushResponse{
				Accepted:  tt.errorCode == ErrorCodeSuccess,
				ErrorCode: tt.errorCode,
			})

			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}

			// Verify content type
			if ct := rr.Header().Get("Content-Type"); ct != "application/x-protobuf" {
				t.Errorf("Content-Type = %q, want %q", ct, "application/x-protobuf")
			}

			// Verify response can be parsed
			resp := parsePushResponse(t, rr)
			if resp.ErrorCode != tt.errorCode {
				t.Errorf("ErrorCode = %d, want %d", resp.ErrorCode, tt.errorCode)
			}
		})
	}
}

func TestWriteResponse_IncludesRequestID(t *testing.T) {
	h := NewPushHandler(nil)
	rr := httptest.NewRecorder()

	h.writeResponse(rr, &PushResponse{
		Accepted:  true,
		RequestID: "test-request-id-123",
		ErrorCode: ErrorCodeSuccess,
	})

	resp := parsePushResponse(t, rr)
	if resp.RequestId != "test-request-id-123" {
		t.Errorf("RequestId = %q, want %q", resp.RequestId, "test-request-id-123")
	}
}

// Helper functions

func marshalPushRequest(t *testing.T, req *pb.PushRequest) []byte {
	t.Helper()
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("failed to marshal PushRequest: %v", err)
	}
	return data
}

func parsePushResponse(t *testing.T, rr *httptest.ResponseRecorder) *pb.PushResponse {
	t.Helper()
	body, err := io.ReadAll(rr.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	var resp pb.PushResponse
	if err := proto.Unmarshal(body, &resp); err != nil {
		t.Fatalf("failed to unmarshal PushResponse: %v", err)
	}
	return &resp
}

// Integration tests for the full validation pipeline

func TestHandlePush_Success(t *testing.T) {
	// Test acceptance criteria: Valid push request returns accepted=true with request_id
	mock := &mockOurCloudClient{
		verifyResult:     true,
		hasConsentResult: true,
		endpointsResult: &pb.PushEndpointList{
			Endpoints: []*pb.PushEndpoint{
				{DeviceId: "device1", FcmToken: "token1"},
			},
		},
	}
	h := NewPushHandlerWithClient(mock)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("valid-signature"),
		Timestamp:      1234567890,
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusOK)
	}

	resp := parsePushResponse(t, rr)
	if !resp.Accepted {
		t.Error("expected accepted=true for valid request")
	}
	if resp.ErrorCode != ErrorCodeSuccess {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeSuccess, resp.ErrorCode)
	}
	if resp.RequestId == "" {
		t.Error("expected non-empty request_id")
	}
}

func TestHandlePush_SignatureVerificationFailed(t *testing.T) {
	// Test acceptance criteria: Invalid signature returns error_code=3
	mock := &mockOurCloudClient{
		verifyResult: false,
	}
	h := NewPushHandlerWithClient(mock)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("invalid-signature"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for invalid signature")
	}
	if resp.ErrorCode != ErrorCodeSignatureFailed {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeSignatureFailed, resp.ErrorCode)
	}
}

func TestHandlePush_SignatureVerificationError(t *testing.T) {
	// Test that signature verification error returns error_code=3
	mock := &mockOurCloudClient{
		verifyResult: false,
		verifyErr:    errors.New("failed to get sender's public key"),
	}
	h := NewPushHandlerWithClient(mock)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("signature"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for signature error")
	}
	if resp.ErrorCode != ErrorCodeSignatureFailed {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeSignatureFailed, resp.ErrorCode)
	}
}

func TestHandlePush_NoConsent(t *testing.T) {
	// Test acceptance criteria: Missing consent returns error_code=2
	mock := &mockOurCloudClient{
		verifyResult:     true,
		hasConsentResult: false,
	}
	h := NewPushHandlerWithClient(mock)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("valid-signature"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusForbidden)
	}

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for missing consent")
	}
	if resp.ErrorCode != ErrorCodeNoConsent {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeNoConsent, resp.ErrorCode)
	}
}

func TestHandlePush_ConsentError(t *testing.T) {
	// Test that consent check error returns error_code=2
	mock := &mockOurCloudClient{
		verifyResult:     true,
		hasConsentResult: false,
		hasConsentErr:    errors.New("failed to get consent list"),
	}
	h := NewPushHandlerWithClient(mock)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("valid-signature"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for consent error")
	}
	if resp.ErrorCode != ErrorCodeNoConsent {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeNoConsent, resp.ErrorCode)
	}
}

func TestHandlePush_NoEndpoints(t *testing.T) {
	// Test acceptance criteria: No endpoints returns error_code=1
	mock := &mockOurCloudClient{
		verifyResult:     true,
		hasConsentResult: true,
		endpointsResult:  &pb.PushEndpointList{Endpoints: nil}, // empty list
	}
	h := NewPushHandlerWithClient(mock)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("valid-signature"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rr.Code, http.StatusNotFound)
	}

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for no endpoints")
	}
	if resp.ErrorCode != ErrorCodeNoEndpoints {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeNoEndpoints, resp.ErrorCode)
	}
}

func TestHandlePush_EndpointsError(t *testing.T) {
	// Test that endpoints error returns error_code=1
	mock := &mockOurCloudClient{
		verifyResult:     true,
		hasConsentResult: true,
		endpointsResult:  nil,
		endpointsErr:     errors.New("failed to get endpoints"),
	}
	h := NewPushHandlerWithClient(mock)

	pushReq := &pb.PushRequest{
		SenderUsername: "alice@oc",
		TargetUsername: "bob@oc",
		Signature:      []byte("valid-signature"),
	}
	body := marshalPushRequest(t, pushReq)

	req := httptest.NewRequest(http.MethodPost, "/push", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-protobuf")
	rr := httptest.NewRecorder()

	h.HandlePush(rr, req)

	resp := parsePushResponse(t, rr)
	if resp.Accepted {
		t.Error("expected accepted=false for endpoints error")
	}
	if resp.ErrorCode != ErrorCodeNoEndpoints {
		t.Errorf("expected error_code=%d, got %d", ErrorCodeNoEndpoints, resp.ErrorCode)
	}
}
