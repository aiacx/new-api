package service

import (
	"net/http/httptest"
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
