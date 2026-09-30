package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSensitivePromptIsRejectedBeforeBillingWithSafeBadRequest(t *testing.T) {
	previousWords := setting.SensitiveWords
	previousEnabled := setting.CheckSensitiveEnabled
	previousPromptEnabled := setting.CheckSensitiveOnPromptEnabled
	t.Cleanup(func() {
		setting.SensitiveWords = previousWords
		setting.CheckSensitiveEnabled = previousEnabled
		setting.CheckSensitiveOnPromptEnabled = previousPromptEnabled
	})
	setting.SensitiveWords = []string{"synthetic_block_word"}
	setting.CheckSensitiveEnabled = true
	setting.CheckSensitiveOnPromptEnabled = true

	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	info := &relaycommon.RelayInfo{Request: &dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "a synthetic_block_word in code"}},
	}}

	apiErr := PrepareRequestBilling(context, info)
	require.NotNil(t, apiErr)
	require.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
	require.Equal(t, types.ErrorCodeSensitiveWordsDetected, apiErr.GetErrorCode())
	require.NotContains(t, apiErr.ToOpenAIError().Message, "synthetic_block_word")
	require.Nil(t, info.Billing)
}
