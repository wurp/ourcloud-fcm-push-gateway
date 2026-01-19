package ourcloud

import (
	"context"
	"fmt"

	"github.com/wurp/friendly-backup-reboot/src/go/ourcloud-client/crypto"
	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
)

// VerifyPushRequest verifies that a PushRequest was signed by the sender.
// It looks up the sender's UserAuth from the DHT and verifies the signature
// using their public signing key.
//
// Returns true if the signature is valid, false otherwise.
// Returns an error if the sender's UserAuth cannot be retrieved or verification fails.
func (c *Client) VerifyPushRequest(ctx context.Context, req *pb.PushRequest) (bool, error) {
	if req == nil {
		return false, fmt.Errorf("push request is nil")
	}

	if req.SenderUsername == "" {
		return false, fmt.Errorf("push request has no sender username")
	}

	// Get the sender's UserAuth to retrieve their public signing key
	senderAuth, err := c.GetUserAuth(ctx, req.SenderUsername)
	if err != nil {
		return false, fmt.Errorf("getting sender user auth: %w", err)
	}

	if len(senderAuth.PublicSignKey) == 0 {
		return false, fmt.Errorf("sender has no public signing key")
	}

	// Verify the signature using the ourcloud-client crypto package
	valid, err := crypto.VerifyPushRequestSignature(req, senderAuth.PublicSignKey)
	if err != nil {
		return false, fmt.Errorf("verifying signature: %w", err)
	}

	return valid, nil
}

// VerifyPushRequestWithKey verifies a PushRequest signature using a provided public key.
// This is useful when the caller has already retrieved the sender's public key.
func VerifyPushRequestWithKey(req *pb.PushRequest, publicKey []byte) (bool, error) {
	return crypto.VerifyPushRequestSignature(req, publicKey)
}
