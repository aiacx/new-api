package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestExternalBillingBearerMatchesOnlyExactDigest(t *testing.T) {
	secret := "sk-panstar-service-secret"
	digest := sha256.Sum256([]byte(secret))
	encoded := hex.EncodeToString(digest[:])
	if !externalBillingBearerMatches(secret, encoded) {
		t.Fatal("exact service credential digest did not match")
	}
	if externalBillingBearerMatches(secret+"x", encoded) {
		t.Fatal("different credential matched external billing digest")
	}
	for _, invalid := range []string{"", "xyz", encoded[:62]} {
		if externalBillingBearerMatches(secret, invalid) {
			t.Fatalf("invalid digest %q matched", invalid)
		}
	}
}
