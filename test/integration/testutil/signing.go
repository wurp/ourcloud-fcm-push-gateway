// Package testutil provides utilities for integration testing.
package testutil

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"

	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/protobuf/proto"
)

// TestUser represents a test user with keypair for signing.
type TestUser struct {
	Username   string
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

// TestUsers holds pre-generated test users with known keypairs.
var TestUsers = map[string]*TestUser{}

func init() {
	// Generate deterministic keypairs for test users.
	// Using fixed seeds so the keys are reproducible.
	users := []string{"alice@oc", "bob@oc", "carol@oc", "nodevice@oc", "root@oc"}

	for _, username := range users {
		// Create deterministic seed from username
		seed := make([]byte, ed25519.SeedSize)
		copy(seed, []byte(username))

		privateKey := ed25519.NewKeyFromSeed(seed)
		publicKey := privateKey.Public().(ed25519.PublicKey)

		TestUsers[username] = &TestUser{
			Username:   username,
			PublicKey:  publicKey,
			PrivateKey: privateKey,
		}
	}
}

// SignPushRequest signs a PushRequest with the sender's private key.
func SignPushRequest(req *pb.PushRequest) error {
	user, ok := TestUsers[req.SenderUsername]
	if !ok {
		return fmt.Errorf("unknown test user: %s", req.SenderUsername)
	}

	// Clear signature before marshaling
	req.Signature = nil

	// Marshal without signature
	reqBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshaling request: %w", err)
	}

	// Sign
	req.Signature = ed25519.Sign(user.PrivateKey, reqBytes)
	return nil
}

// GetPublicKeyHex returns the hex-encoded public key for a test user.
func GetPublicKeyHex(username string) string {
	user, ok := TestUsers[username]
	if !ok {
		return ""
	}
	return hex.EncodeToString(user.PublicKey)
}

// PrintFixtureKeys prints the public keys for all test users in fixture format.
func PrintFixtureKeys() {
	fmt.Println("Test user public keys for fixtures.json:")
	for username, user := range TestUsers {
		fmt.Printf("  %s: %s\n", username, hex.EncodeToString(user.PublicKey))
	}
}
