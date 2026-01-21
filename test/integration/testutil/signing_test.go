package testutil

import (
	"encoding/hex"
	"testing"
)

func TestPrintFixtureKeys(t *testing.T) {
	t.Log("Public keys for fixtures.json:")
	for username, user := range TestUsers {
		t.Logf("  %s: %s", username, hex.EncodeToString(user.PublicKey))
	}
}
