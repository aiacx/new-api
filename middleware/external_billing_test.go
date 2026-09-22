package middleware

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestManagedBillingPinsConfiguredChannelBeforeDistribution(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	pinManagedExternalBillingChannel(c, 91)

	pin, found, overridden := service.GetChannelConstraints(c).ResolvedPin()
	require.True(t, found)
	assert.Equal(t, 91, pin.ChannelId)
	assert.Equal(t, dto.PinSourceToken, pin.Source)
	assert.Equal(t, dto.PinRetrySingleAttempt, pin.RetryMode)
	assert.Empty(t, overridden)
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

func TestManagedBillingRequiresDedicatedGroup(t *testing.T) {
	if !validManagedBillingGroup("panstar_pipio_managed") {
		t.Fatal("dedicated managed group rejected")
	}
	for _, group := range []string{"", "default", "a", "comma,group", "unicode组"} {
		if validManagedBillingGroup(group) {
			t.Fatalf("unsafe managed group accepted: %q", group)
		}
	}
}
