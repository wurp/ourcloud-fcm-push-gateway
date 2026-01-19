// Package handler provides HTTP request handlers for the push gateway.
package handler

import (
	"context"
	"io"
	"net/http"

	"github.com/google/uuid"
	"github.com/wurp/ourcloud-fcm-push-gateway/internal/ourcloud"
	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/protobuf/proto"
)

// Error codes for PushResponse.
const (
	ErrorCodeSuccess         = 0 // Success
	ErrorCodeNoEndpoints     = 1 // No endpoints registered
	ErrorCodeNoConsent       = 2 // Sender not in consent list
	ErrorCodeSignatureFailed = 3 // Signature verification failed
	ErrorCodeInvalidRequest  = 4 // Invalid request / internal error
)

// OurCloudClient defines the interface for OurCloud operations needed by the push handler.
// This interface allows for easy testing with mock implementations.
type OurCloudClient interface {
	VerifyPushRequest(ctx context.Context, req *pb.PushRequest) (bool, error)
	HasConsent(ctx context.Context, recipientUsername, senderUsername string) (bool, error)
	GetEndpoints(ctx context.Context, username string) (*pb.PushEndpointList, error)
}

// PushHandler handles incoming push notification requests.
type PushHandler struct {
	ocClient OurCloudClient
}

// NewPushHandler creates a new PushHandler.
func NewPushHandler(ocClient *ourcloud.Client) *PushHandler {
	return &PushHandler{
		ocClient: ocClient,
	}
}

// NewPushHandlerWithClient creates a new PushHandler with any OurCloudClient implementation.
// This is useful for testing with mock clients.
func NewPushHandlerWithClient(client OurCloudClient) *PushHandler {
	return &PushHandler{
		ocClient: client,
	}
}

// PushResponse represents the response to a push request.
// This is serialized as protobuf in the HTTP response.
type PushResponse struct {
	Accepted  bool   `json:"accepted"`
	RequestID string `json:"request_id,omitempty"`
	ErrorCode int32  `json:"error_code"`
	Message   string `json:"message,omitempty"`
}

// HandlePush handles POST /push requests.
// It implements the validation pipeline:
// 1. Parse request          -> error_code=4 on failure
// 2. Verify sender sig      -> error_code=3 on failure
// 3. Check consent list     -> error_code=2 if not consented
// 4. Get endpoints          -> error_code=1 if none
// 5. Queue for delivery     -> return request_id
func (h *PushHandler) HandlePush(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Step 1: Parse the protobuf request
	req, err := h.parseRequest(r)
	if err != nil {
		h.writeResponse(w, &PushResponse{
			Accepted:  false,
			ErrorCode: ErrorCodeInvalidRequest,
			Message:   "failed to parse request",
		})
		return
	}

	// Validate required fields
	if err := h.validateRequest(req); err != nil {
		h.writeResponse(w, &PushResponse{
			Accepted:  false,
			ErrorCode: ErrorCodeInvalidRequest,
			Message:   err.Error(),
		})
		return
	}

	// Step 2: Verify sender signature
	valid, err := h.ocClient.VerifyPushRequest(ctx, req)
	if err != nil || !valid {
		h.writeResponse(w, &PushResponse{
			Accepted:  false,
			ErrorCode: ErrorCodeSignatureFailed,
			Message:   "signature verification failed",
		})
		return
	}

	// Step 3: Check consent list
	hasConsent, err := h.isConsented(ctx, req.TargetUsername, req.SenderUsername)
	if err != nil || !hasConsent {
		h.writeResponse(w, &PushResponse{
			Accepted:  false,
			ErrorCode: ErrorCodeNoConsent,
			Message:   "sender not in consent list",
		})
		return
	}

	// Step 4: Get endpoints for target user
	endpoints, err := h.ocClient.GetEndpoints(ctx, req.TargetUsername)
	if err != nil || len(endpoints.Endpoints) == 0 {
		h.writeResponse(w, &PushResponse{
			Accepted:  false,
			ErrorCode: ErrorCodeNoEndpoints,
			Message:   "no endpoints registered",
		})
		return
	}

	// Step 5: Queue for delivery (TODO: implement batching system in phase 4)
	// For now, generate a request ID and return success
	requestID := uuid.New().String()

	h.writeResponse(w, &PushResponse{
		Accepted:  true,
		RequestID: requestID,
		ErrorCode: ErrorCodeSuccess,
	})
}

// parseRequest reads and parses the protobuf PushRequest from the HTTP request body.
func (h *PushHandler) parseRequest(r *http.Request) (*pb.PushRequest, error) {
	// Check content type
	contentType := r.Header.Get("Content-Type")
	if contentType != "application/x-protobuf" && contentType != "application/protobuf" {
		return nil, &requestError{message: "invalid content type, expected application/x-protobuf"}
	}

	// Read body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, &requestError{message: "failed to read request body"}
	}
	defer r.Body.Close()

	if len(body) == 0 {
		return nil, &requestError{message: "empty request body"}
	}

	// Parse protobuf
	var req pb.PushRequest
	if err := proto.Unmarshal(body, &req); err != nil {
		return nil, &requestError{message: "failed to unmarshal protobuf"}
	}

	return &req, nil
}

// validateRequest performs basic validation on the parsed PushRequest.
func (h *PushHandler) validateRequest(req *pb.PushRequest) error {
	if req.SenderUsername == "" {
		return &requestError{message: "sender_username is required"}
	}
	if req.TargetUsername == "" && len(req.TargetNodeIds) == 0 {
		return &requestError{message: "target_username or target_node_ids is required"}
	}
	if len(req.Signature) == 0 {
		return &requestError{message: "signature is required"}
	}
	return nil
}

// isConsented checks if the sender has consent to send push notifications to the target.
func (h *PushHandler) isConsented(ctx context.Context, targetUsername, senderUsername string) (bool, error) {
	return h.ocClient.HasConsent(ctx, targetUsername, senderUsername)
}

// writeResponse writes a PushResponse as protobuf to the HTTP response.
func (h *PushHandler) writeResponse(w http.ResponseWriter, resp *PushResponse) {
	// Create protobuf response
	pbResp := &pb.PushResponse{
		Accepted:  resp.Accepted,
		RequestId: resp.RequestID,
		ErrorCode: resp.ErrorCode,
		Message:   resp.Message,
	}

	data, err := proto.Marshal(pbResp)
	if err != nil {
		// Fallback to a simple error response
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-protobuf")

	// Set appropriate status code based on error
	switch resp.ErrorCode {
	case ErrorCodeSuccess:
		w.WriteHeader(http.StatusOK)
	case ErrorCodeInvalidRequest:
		w.WriteHeader(http.StatusBadRequest)
	case ErrorCodeSignatureFailed:
		w.WriteHeader(http.StatusUnauthorized)
	case ErrorCodeNoConsent:
		w.WriteHeader(http.StatusForbidden)
	case ErrorCodeNoEndpoints:
		w.WriteHeader(http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}

	w.Write(data)
}

// requestError represents a validation error in the request.
type requestError struct {
	message string
}

func (e *requestError) Error() string {
	return e.message
}
