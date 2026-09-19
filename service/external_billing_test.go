package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestExternalBillingSkipsNewAPIPreConsumeAndSettlement(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(constant.ContextKeyExternalBilling), true)
	info := &relaycommon.RelayInfo{RequestId: "req_internal_1", OriginModelName: "gpt-6-astra"}

	if apiErr := PreConsumeBilling(c, 12345, info); apiErr != nil {
		t.Fatalf("external billing pre-consume failed: %v", apiErr)
	}
	if info.Billing != nil {
		t.Fatal("external billing created a New API billing session")
	}
	if info.Billing != nil || info.FinalPreConsumedQuota != 0 {
		t.Fatal("external billing mutated New API reservation state")
	}
}

func TestExternalBillingProviderErrorDoesNotEmbedBody(t *testing.T) {
	ctx := context.WithValue(context.Background(), string(constant.ContextKeyExternalBilling), true)
	if !ExternalBillingFromContext(ctx) {
		t.Fatal("external billing context identity was lost")
	}
	resp := &http.Response{StatusCode: http.StatusBadGateway,
		Body: io.NopCloser(strings.NewReader("synthetic-private-opaque"))}
	err := RelayErrorHandler(ctx, resp, true)
	if err == nil || strings.Contains(err.Error(), "synthetic-private-opaque") {
		t.Fatal("external billing error retained upstream response body")
	}
}

func TestManagedExternalBillingRefusesDifferentChannelBeforeDispatch(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set(string(constant.ContextKeyExternalBilling), true)
	c.Set("external_billing_managed_channel_id", 91)
	if !ManagedExternalBillingChannelMatches(c, 91) {
		t.Fatal("pinned managed channel rejected")
	}
	if ManagedExternalBillingChannelMatches(c, 92) {
		t.Fatal("unexpected channel would have been dispatched")
	}
}
