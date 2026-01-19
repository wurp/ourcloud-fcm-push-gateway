package ourcloud

import (
	"fmt"
	"testing"

	pb "github.com/wurp/friendly-backup-reboot/src/go/ourcloud-proto"
	"google.golang.org/protobuf/proto"
)

func TestLabelPaths(t *testing.T) {
	tests := []struct {
		username      string
		wantConsents  string
		wantEndpoints string
	}{
		{
			username:      "alice@oc",
			wantConsents:  "/users/alice@oc/platform/push/consents",
			wantEndpoints: "/users/alice@oc/platform/push/endpoints",
		},
		{
			username:      "bob@oc",
			wantConsents:  "/users/bob@oc/platform/push/consents",
			wantEndpoints: "/users/bob@oc/platform/push/endpoints",
		},
	}

	for _, tt := range tests {
		t.Run(tt.username, func(t *testing.T) {
			gotConsents := labelPathPushConsents(tt.username)
			if gotConsents != tt.wantConsents {
				t.Errorf("labelPathPushConsents(%q) = %q, want %q", tt.username, gotConsents, tt.wantConsents)
			}

			gotEndpoints := labelPathPushEndpoints(tt.username)
			if gotEndpoints != tt.wantEndpoints {
				t.Errorf("labelPathPushEndpoints(%q) = %q, want %q", tt.username, gotEndpoints, tt.wantEndpoints)
			}
		})
	}
}

func TestNewClient(t *testing.T) {
	c := NewClient("localhost:50051")
	if c == nil {
		t.Fatal("NewClient returned nil")
	}
	if c.address != "localhost:50051" {
		t.Errorf("client address = %q, want %q", c.address, "localhost:50051")
	}
	if c.client != nil {
		t.Error("client should not be connected initially")
	}
}

func TestIsConnected(t *testing.T) {
	c := NewClient("localhost:50051")
	if c.IsConnected() {
		t.Error("IsConnected() should return false before Connect()")
	}
}

func TestComputeContentAddress(t *testing.T) {
	// Create a test UserAuth
	userAuth := &pb.UserAuth{
		FormatVersion:  &pb.FormatVersion{Value: 1},
		UserName:       "alice@oc",
		PublicCryptKey: []byte("test-crypt-key-32-bytes-padding!"),
		PublicSignKey:  []byte("test-sign-key-32-bytes-padding!!"),
	}

	// Compute the content address
	addr1 := computeContentAddress(userAuth)

	// Verify it's 32 bytes (SHA-256)
	if len(addr1) != 32 {
		t.Errorf("content address length = %d, want 32", len(addr1))
	}

	// Verify expected content address for alice@oc
	expectedAlice := "6ba56fa912fff194d1d0ebc1044ad307046a4f930d070aead616600dc8f5c96c"
	gotAlice := fmt.Sprintf("%x", addr1)
	if gotAlice != expectedAlice {
		t.Errorf("alice@oc content address = %s, want %s", gotAlice, expectedAlice)
	}

	// Verify determinism - same input should produce same output
	addr2 := computeContentAddress(userAuth)
	if !equal(addr1, addr2) {
		t.Error("computeContentAddress is not deterministic")
	}

	// Different input should produce different output
	userAuth2 := &pb.UserAuth{
		FormatVersion:  &pb.FormatVersion{Value: 1},
		UserName:       "bob@oc",
		PublicCryptKey: []byte("test-crypt-key-32-bytes-padding!"),
		PublicSignKey:  []byte("test-sign-key-32-bytes-padding!!"),
	}
	addr3 := computeContentAddress(userAuth2)
	if equal(addr1, addr3) {
		t.Error("different UserAuth should produce different content address")
	}

	// Verify expected content address for bob@oc
	expectedBob := "b2328b68951a9c60c87f8e5ee2df3b71acdf70a6be49e249ece05e841d5eb0cd"
	gotBob := fmt.Sprintf("%x", addr3)
	if gotBob != expectedBob {
		t.Errorf("bob@oc content address = %s, want %s", gotBob, expectedBob)
	}
}

func TestComputeContentAddressConsistency(t *testing.T) {
	// Test that we get consistent content addresses for known inputs
	// This helps verify the implementation matches what the DHT expects

	userAuth := &pb.UserAuth{
		FormatVersion:  &pb.FormatVersion{Value: 1},
		UserName:       "test@oc",
		PublicCryptKey: make([]byte, 32),
		PublicSignKey:  make([]byte, 32),
	}

	addr := computeContentAddress(userAuth)

	// Verify expected content address
	expectedHex := "b78fbcc43c921fbb8559cd4a74aabf477930bd58323f5cf1bf2e2fc3117783be"
	gotHex := fmt.Sprintf("%x", addr)
	if gotHex != expectedHex {
		t.Errorf("test@oc content address = %s, want %s", gotHex, expectedHex)
	}

	// Verify the protobuf marshaling is deterministic
	data1, _ := proto.MarshalOptions{Deterministic: true}.Marshal(userAuth)
	data2, _ := proto.MarshalOptions{Deterministic: true}.Marshal(userAuth)
	if !equal(data1, data2) {
		t.Error("protobuf marshaling is not deterministic")
	}
}

func equal(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
