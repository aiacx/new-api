package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPanstarManagedPreviousResponseBecomesStatelessSupplierInput(t *testing.T) {
	previousDB, previousType := model.DB, common.MainDatabaseType()
	database, err := gorm.Open(sqlite.Open("file:panstar_response_relay?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, database.AutoMigrate(&model.PanstarResponseState{}))
	model.DB = database
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() { model.DB = previousDB; common.SetMainDatabaseType(previousType) })
	keyring, err := common.Marshal(map[string]any{
		"active_version": 1,
		"keys":           map[string]string{"1": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x32}, 32))},
	})
	require.NoError(t, err)
	keyringPath := filepath.Join(t.TempDir(), "channel-key-master")
	require.NoError(t, os.WriteFile(keyringPath, keyring, 0o600))
	t.Setenv("PANSTAR_CHANNEL_KEY_MASTER_FILE", keyringPath)
	t.Setenv("PANSTAR_RESPONSES_STATE_ENABLED", "true")
	t.Setenv("PANSTAR_RESPONSES_STATE_MAX_MB", "16")

	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		require.NoError(t, readErr)
		var payload map[string]json.RawMessage
		require.NoError(t, common.Unmarshal(body, &payload))
		require.JSONEq(t, `false`, string(payload["store"]))
		require.NotContains(t, payload, "previous_response_id")
		require.Contains(t, payload, "future_control")
		call := calls.Add(1)
		switch call {
		case 1:
			require.JSONEq(t, `[{"role":"user","content":"first"}]`, string(payload["input"]))
		case 2:
			var input []json.RawMessage
			require.NoError(t, common.Unmarshal(payload["input"], &input))
			require.Len(t, input, 3)
			require.Contains(t, string(input[0]), "first")
			require.Contains(t, string(input[1]), "first answer")
			require.Contains(t, string(input[2]), "second")
		case 3:
			var input []json.RawMessage
			require.NoError(t, common.Unmarshal(payload["input"], &input))
			require.Len(t, input, 5)
			require.Contains(t, string(input[0]), "first")
			require.Contains(t, string(input[1]), "first answer")
			require.Contains(t, string(input[2]), "second")
			require.Contains(t, string(input[3]), "second answer")
			require.Contains(t, string(input[4]), "third")
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, map[int32]string{
			1: `{"id":"resp_first","status":"completed","model":"gpt-test","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"first answer"}]}],"usage":{"input_tokens":2,"output_tokens":2,"total_tokens":4}}`,
			2: `{"id":"resp_second","status":"completed","model":"gpt-test","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"second answer"}]}],"usage":{"input_tokens":6,"output_tokens":2,"total_tokens":8}}`,
			3: `{"id":"resp_third","status":"completed","model":"gpt-test","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"third answer"}]}],"usage":{"input_tokens":10,"output_tokens":2,"total_tokens":12}}`,
		}[call])
	}))
	t.Cleanup(upstream.Close)

	owner := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	first := []byte(`{"model":"gpt-test","input":"first","store":true,"future_control":{"keep":1}}`)
	runPanstarStateRelay(t, upstream.URL, owner, first)
	second := []byte(`{"model":"gpt-test","input":"second","previous_response_id":"resp_first","future_control":{"keep":2}}`)
	runPanstarStateRelay(t, upstream.URL, owner, second)
	third := []byte(`{"model":"gpt-test","input":"third","store":false,"previous_response_id":"resp_second","future_control":{"keep":3}}`)
	runPanstarStateRelay(t, upstream.URL, owner, third)
	require.EqualValues(t, 3, calls.Load())
}

func runPanstarStateRelay(t *testing.T, upstreamURL, owner string, body []byte) {
	t.Helper()
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	context.Request.Header.Set("Content-Type", "application/json")
	context.Request.Header.Set(service.PanstarStateOwnerHeader, owner)
	defer common.CleanupBodyStorage(context)
	context.Set(string(constant.ContextKeyExternalBilling), true)
	context.Set("external_billing_managed_channel_id", 5)
	common.SetContextKey(context, constant.ContextKeyOriginalModel, "gpt-test")
	common.SetContextKey(context, constant.ContextKeyChannelType, constant.ChannelTypeAdvancedCustom)
	common.SetContextKey(context, constant.ContextKeyChannelBaseUrl, upstreamURL)
	common.SetContextKey(context, constant.ContextKeyChannelKey, "synthetic-managed-key")
	common.SetContextKey(context, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: "/v1/responses", UpstreamPath: "/v1/responses",
			Models: []string{"gpt-test"}, PassThroughBodyEnabled: true,
		}}},
	})
	request, err := helper.GetAndValidateRequest(context, types.RelayFormatOpenAIResponses)
	require.NoError(t, err)
	require.Nil(t, service.PreparePanstarResponseState(context, request))
	responses := request.(*dto.OpenAIResponsesRequest)
	info, err := relaycommon.GenRelayInfo(context, types.RelayFormatOpenAIResponses, responses, nil)
	require.NoError(t, err)
	adaptor, requestBody, closer, apiErr := PrepareResponsesRequest(context, info, responses)
	require.Nil(t, apiErr)
	defer closer.Close()
	var response any
	response, err = adaptor.DoRequest(context, info, requestBody)
	require.NoError(t, err)
	_, apiErr = adaptor.DoResponse(context, response.(*http.Response), info)
	require.Nil(t, apiErr)
	require.NotEmpty(t, recorder.Body.String())
}
