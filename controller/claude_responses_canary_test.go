package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/advancedcustom"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudeResponsesCanaryChannel(t *testing.T, baseURL string) (*model.Channel, int) {
	t.Helper()
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	user := model.User{Username: "claude-canary-replay", Group: "default", Quota: 1_000_000, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	ratio_setting.InitRatioSettings()
	before := ratio_setting.ModelRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"claude-opus-5":1}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(before)) })
	service.InitHttpClient()
	oldTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = oldTimeout })
	channel := &model.Channel{Id: 10, Name: "synthetic-claude-canary", Type: constant.ChannelTypeAdvancedCustom,
		BaseURL: &baseURL, Key: "synthetic-test-key", Models: "claude-opus-5", Group: "default", Status: common.ChannelStatusEnabled}
	channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions",
			Converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat, Models: []string{"claude-opus-5"}}},
	}})
	return channel, user.Id
}

func TestPipioClaudeRecordedSSECanaryRemainsIncomplete(t *testing.T) {
	data, err := os.ReadFile("testdata/claude-responses-canary-replay.json")
	require.NoError(t, err)
	var replay struct {
		Chunks  []map[string]any `json:"chunks"`
		HasDone bool             `json:"hasDone"`
	}
	require.NoError(t, common.Unmarshal(data, &replay))
	require.True(t, replay.HasDone)
	var stream strings.Builder
	for _, chunk := range replay.Chunks {
		encoded, err := common.Marshal(chunk)
		require.NoError(t, err)
		fmt.Fprintf(&stream, "data: %s\n\n", encoded)
	}
	stream.WriteString("data: [DONE]\n\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/chat/completions", r.URL.Path)
		var request dto.GeneralOpenAIRequest
		require.NoError(t, common.DecodeJson(r.Body, &request))
		require.NotNil(t, request.StreamOptions)
		assert.True(t, request.StreamOptions.IncludeUsage)
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := io.WriteString(w, stream.String())
		require.NoError(t, err)
	}))
	t.Cleanup(upstream.Close)
	channel, userID := claudeResponsesCanaryChannel(t, upstream.URL)
	result := testChannel(context.Background(), channel, userID, "claude-opus-5", string(constant.EndpointTypeOpenAIResponse), true)
	require.Error(t, result.localErr)
	assert.Contains(t, result.localErr.Error(), "responses stream did not reach response.completed")
	require.NotNil(t, result.newAPIError)
	assert.Equal(t, "bad_response_body", string(result.newAPIError.GetErrorCode()))
	var successfulLogs int64
	require.NoError(t, model.LOG_DB.Model(&model.Log{}).Count(&successfulLogs).Error)
	assert.Zero(t, successfulLogs)
	t.Logf("exact testChannel failure: %s, code=%s", result.localErr.Error(), result.newAPIError.GetErrorCode())
}

func TestClaudeResponsesCanaryRequestOnlyChangesTheSelectedChatBridge(t *testing.T) {
	for _, tc := range []struct {
		name             string
		channelType      int
		model, converter string
		models           []string
		wantShort        bool
		passThrough      bool
	}{
		{"Claude Chat bridge", constant.ChannelTypeAdvancedCustom, "claude-opus-5", relayconvert.ConverterOpenAIResponsesToOpenAIChat, []string{"claude-opus-5"}, true, false},
		{"native Advanced Custom Responses", constant.ChannelTypeAdvancedCustom, "claude-opus-5", relayconvert.ConverterNone, nil, false, false},
		{"native Claude channel", constant.ChannelTypeAnthropic, "claude-opus-5", relayconvert.ConverterOpenAIResponsesToOpenAIChat, nil, false, false},
		{"OpenAI-compatible channel", constant.ChannelTypeOpenAI, "claude-opus-5", relayconvert.ConverterOpenAIResponsesToOpenAIChat, nil, false, false},
		{"other model Chat bridge", constant.ChannelTypeAdvancedCustom, "gpt-6.1-sol", relayconvert.ConverterOpenAIResponsesToOpenAIChat, nil, false, false},
		{"different model route", constant.ChannelTypeAdvancedCustom, "claude-opus-5", relayconvert.ConverterOpenAIResponsesToOpenAIChat, []string{"claude-sonnet-5"}, false, false},
		{"pass-through request body", constant.ChannelTypeAdvancedCustom, "claude-opus-5", relayconvert.ConverterOpenAIResponsesToOpenAIChat, nil, false, true},
		{"missing Advanced Custom config", constant.ChannelTypeAdvancedCustom, "claude-opus-5", "", nil, false, false},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				channel := &model.Channel{Type: tc.channelType}
				if tc.converter != "" {
					channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
						Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: tc.converter, Models: tc.models, PassThroughBodyEnabled: tc.passThrough}},
					}})
				}
				request, ok := buildTestRequest(tc.model, string(constant.EndpointTypeOpenAIResponse), channel, stream).(*dto.OpenAIResponsesRequest)
				require.True(t, ok)
				require.NotNil(t, request.Stream)
				assert.Equal(t, stream, *request.Stream)
				if tc.wantShort {
					assert.JSONEq(t, `[{"role":"user","content":"Reply exactly OK"}]`, string(request.Input))
					require.NotNil(t, request.MaxOutputTokens)
					assert.EqualValues(t, 32, *request.MaxOutputTokens)
					require.NotNil(t, request.Temperature)
					assert.Zero(t, *request.Temperature)
				} else {
					assert.JSONEq(t, `[{"role":"user","content":"hi"}]`, string(request.Input))
					assert.Nil(t, request.MaxOutputTokens)
					assert.Nil(t, request.Temperature)
				}
			})
		}
	}
}

func TestClaudeResponsesCanaryKeepsRealUsageForJSONAndCompletedSSE(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request dto.GeneralOpenAIRequest
				require.NoError(t, common.DecodeJson(r.Body, &request))
				require.Len(t, request.Messages, 1)
				assert.Equal(t, "Reply exactly OK", request.Messages[0].Content)
				require.NotNil(t, request.MaxCompletionTokens)
				assert.EqualValues(t, 32, *request.MaxCompletionTokens)
				require.NotNil(t, request.Temperature)
				assert.Zero(t, *request.Temperature)
				assert.Equal(t, stream, lo.FromPtr(request.Stream))
				usage := `{"prompt_tokens":8,"completion_tokens":20,"total_tokens":28,"usage_semantic":"openai","usage_source":"anthropic","billing_usage":{"source":"claude_messages","semantic":"anthropic","claude_usage":{"input_tokens":8,"output_tokens":20}},"input_tokens":8,"output_tokens":0,"input_tokens_details":null}`
				if stream {
					require.NotNil(t, request.StreamOptions)
					assert.True(t, request.StreamOptions.IncludeUsage)
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprintf(w, "data: {\"id\":\"chat_synthetic\",\"model\":\"claude-opus-5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":null}\n\ndata: {\"choices\":[],\"usage\":%s}\n\ndata: [DONE]\n\n", usage)
				} else {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"id":"chat_synthetic","model":"claude-opus-5","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":%s}`, usage)
				}
			}))
			t.Cleanup(upstream.Close)
			channel, userID := claudeResponsesCanaryChannel(t, upstream.URL)
			result := testChannel(context.Background(), channel, userID, "claude-opus-5", string(constant.EndpointTypeOpenAIResponse), stream)
			require.NoError(t, result.localErr)
			require.Nil(t, result.newAPIError)
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Equal(t, 8, logs[0].PromptTokens)
			assert.Equal(t, 20, logs[0].CompletionTokens)
			assert.Equal(t, stream, logs[0].IsStream)
		})
	}
}

func TestClaudeResponsesCustomerRelayPreservesPromptBudgetAndTemperature(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	channel := &model.Channel{}
	channel.SetOtherSettings(dto.ChannelOtherSettings{AdvancedCustom: &dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat}},
	}})
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAIResponses, RequestURLPath: "/v1/responses", OriginModelName: "claude-opus-5", IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAdvancedCustom, UpstreamModelName: "claude-opus-5", SupportStreamOptions: true, ChannelOtherSettings: channel.GetOtherSettings()}}
	adaptor := &advancedcustom.Adaptor{}
	adaptor.Init(info)
	request := dto.OpenAIResponsesRequest{Model: "claude-opus-5", Input: []byte(`[{"role":"user","content":"customer prompt"}]`), Stream: lo.ToPtr(true), MaxOutputTokens: lo.ToPtr(uint(128)), Temperature: lo.ToPtr(0.7)}
	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
	require.NoError(t, err)
	chat, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.Len(t, chat.Messages, 1)
	assert.Equal(t, "customer prompt", chat.Messages[0].Content)
	require.NotNil(t, chat.MaxCompletionTokens)
	assert.EqualValues(t, 128, *chat.MaxCompletionTokens)
	require.NotNil(t, chat.Temperature)
	assert.Equal(t, 0.7, *chat.Temperature)
	assert.False(t, info.IsChannelTest)
}
