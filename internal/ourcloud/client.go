// Package ourcloud provides a wrapper around the ourcloud-client library
// for accessing DHT data needed by the push gateway.
package ourcloud

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/wurp/friendly-backup-reboot/src/go/ourcloud-client/service"
	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/protobuf/proto"
)

// labelPathPushConsents returns the label path for a user's push consent list.
func labelPathPushConsents(username string) string {
	return fmt.Sprintf("/users/%s/platform/push/consents", username)
}

// labelPathPushEndpoints returns the label path for a user's push endpoints.
func labelPathPushEndpoints(username string) string {
	return fmt.Sprintf("/users/%s/platform/push/endpoints", username)
}

// Client wraps the ourcloud-client service.Client to provide
// high-level access to push notification related data.
type Client struct {
	address string
	client  *service.Client
	mu      sync.RWMutex
}

// NewClient creates a new OurCloud client wrapper.
// The address should be in the form "host:port" (e.g., "localhost:50051").
func NewClient(address string) *Client {
	return &Client{
		address: address,
	}
}

// Connect establishes a connection to the OurCloud node.
func (c *Client) Connect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client != nil {
		return nil
	}

	client, err := service.NewClient(c.address)
	if err != nil {
		return fmt.Errorf("connecting to OurCloud node: %w", err)
	}

	c.client = client
	return nil
}

// Close closes the connection to the OurCloud node.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.client == nil {
		return nil
	}

	err := c.client.Close()
	c.client = nil
	return err
}

// IsConnected returns true if the client is connected to the OurCloud node.
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.client != nil
}

// HealthCheck verifies the connection to the OurCloud node is working.
// It attempts to look up a well-known user (root@oc) to verify connectivity.
func (c *Client) HealthCheck(ctx context.Context) error {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return fmt.Errorf("not connected to OurCloud node")
	}

	// Try to look up root@oc as a connectivity check
	_, err := client.GetUserAuth(ctx, "root@oc")
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	return nil
}

// GetUserAuth retrieves a user's public authentication info by username.
// The username should be in the form "alice@oc".
func (c *Client) GetUserAuth(ctx context.Context, username string) (*pb.UserAuth, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected to OurCloud node")
	}

	return client.GetUserAuth(ctx, username)
}

// GetConsentList retrieves the push notification consent list for a user.
// The username should be in the form "alice@oc".
func (c *Client) GetConsentList(ctx context.Context, username string) (*pb.PushConsentList, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected to OurCloud node")
	}

	// First get the user's UserAuth to compute their owner ID
	userAuth, err := client.GetUserAuth(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("getting user auth for %q: %w", username, err)
	}

	ownerID := computeContentAddress(userAuth)

	// Read the consent list label
	label, err := client.ReadLabel(ctx, ownerID, labelPathPushConsents(username))
	if err != nil {
		return nil, fmt.Errorf("reading consent list label: %w", err)
	}

	if label.DataId == nil {
		return nil, fmt.Errorf("consent list label has no data ID")
	}

	// Fetch the actual data
	data, err := client.Lookup(ctx, label.DataId.Value)
	if err != nil {
		return nil, fmt.Errorf("looking up consent list data: %w", err)
	}

	var consentList pb.PushConsentList
	if err := proto.Unmarshal(data, &consentList); err != nil {
		return nil, fmt.Errorf("unmarshaling consent list: %w", err)
	}

	return &consentList, nil
}

// GetEndpoints retrieves the push notification endpoints for a user.
// The username should be in the form "alice@oc".
func (c *Client) GetEndpoints(ctx context.Context, username string) (*pb.PushEndpointList, error) {
	c.mu.RLock()
	client := c.client
	c.mu.RUnlock()

	if client == nil {
		return nil, fmt.Errorf("not connected to OurCloud node")
	}

	// First get the user's UserAuth to compute their owner ID
	userAuth, err := client.GetUserAuth(ctx, username)
	if err != nil {
		return nil, fmt.Errorf("getting user auth for %q: %w", username, err)
	}

	ownerID := computeContentAddress(userAuth)

	// Read the endpoints label
	label, err := client.ReadLabel(ctx, ownerID, labelPathPushEndpoints(username))
	if err != nil {
		return nil, fmt.Errorf("reading endpoints label: %w", err)
	}

	if label.DataId == nil {
		return nil, fmt.Errorf("endpoints label has no data ID")
	}

	// Fetch the actual data
	data, err := client.Lookup(ctx, label.DataId.Value)
	if err != nil {
		return nil, fmt.Errorf("looking up endpoints data: %w", err)
	}

	var endpointList pb.PushEndpointList
	if err := proto.Unmarshal(data, &endpointList); err != nil {
		return nil, fmt.Errorf("unmarshaling endpoint list: %w", err)
	}

	return &endpointList, nil
}

// HasConsent checks if the sender has consent to send push notifications to the recipient.
func (c *Client) HasConsent(ctx context.Context, recipientUsername, senderUsername string) (bool, error) {
	consentList, err := c.GetConsentList(ctx, recipientUsername)
	if err != nil {
		return false, err
	}

	for _, consent := range consentList.Consents {
		if consent.Username == senderUsername {
			return true, nil
		}
	}

	return false, nil
}

// computeContentAddress computes the content-based address (SHA-256 hash)
// of a protobuf message. This is used to derive the owner ID from a UserAuth.
func computeContentAddress(msg proto.Message) []byte {
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
	if err != nil {
		// This should not happen for valid protobuf messages
		panic(fmt.Sprintf("failed to marshal message: %v", err))
	}
	hash := sha256.Sum256(data)
	return hash[:]
}
