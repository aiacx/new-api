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

func TestPanstarRequestIDRequiresSafeBoundedIdentifier(t *testing.T) {
	for _, id := range []string{"req_ps_12345678", "7d2a41b8-1024:one"} {
		if !panstarRequestIDPattern.MatchString(id) {
			t.Fatalf("valid request ID rejected: %q", id)
		}
	}
	for _, id := range []string{"", "short", "req_ps_\nAuthorization: bearer", "req_ps_汉字"} {
		if panstarRequestIDPattern.MatchString(id) {
			t.Fatalf("unsafe request ID accepted: %q", id)
		}
	}
}
