package middleware

import (
	"crypto/hmac"
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

func TestManagedBillingAcceptsOnlySignedRequestScopedChannelOverride(t *testing.T) {
	t.Setenv("EXTERNAL_BILLING_MANAGED_CHANNEL_ID", "5")
	const bearer = "managed-service-secret"
	const requestID = "req_ps_signed_channel_123"
	const path = "/v1/responses"
	mac := hmac.New(sha256.New, []byte(bearer))
	_, _ = mac.Write([]byte(managedExternalBillingChannelCanonical(path, requestID, 8)))
	signature := hex.EncodeToString(mac.Sum(nil))

	channel, err := managedExternalBillingChannel(path, requestID, bearer, "8", signature)
	require.NoError(t, err)
	assert.Equal(t, 8, channel)

	for _, attempt := range []struct {
		path, requestID, channel, signature string
	}{
		{path, requestID, "9", signature},
		{path, requestID + "x", "8", signature},
		{"/v1/chat/completions", requestID, "8", signature},
		{path, requestID, "8", "00" + signature[2:]},
		{path, requestID, "8", ""},
	} {
		_, err := managedExternalBillingChannel(attempt.path, attempt.requestID,
			bearer, attempt.channel, attempt.signature)
		require.Error(t, err)
	}
}

func TestManagedBillingKeepsConfiguredDefaultWithoutOverride(t *testing.T) {
	t.Setenv("EXTERNAL_BILLING_MANAGED_CHANNEL_ID", "5")
	channel, err := managedExternalBillingChannel("/v1/responses",
		"req_ps_default_channel_123", "managed-service-secret", "", "")
	require.NoError(t, err)
	assert.Equal(t, 5, channel)
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
