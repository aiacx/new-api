package advancedcustom

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdaptorUsesExactRouteAndQueryAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "https://upstream.example/v1/chat/completions?existing=1",
				Converter:    relayconvert.ConverterClaudeMessagesToOpenAIChat,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeQuery,
					Name:  "api_key",
					Value: "{api_key}",
				},
			},
		},
	})
	info.RequestURLPath = "/v1/messages?client=1"

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)

	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "https", parsedURL.Scheme)
	assert.Equal(t, "upstream.example", parsedURL.Host)
	assert.Equal(t, "/v1/chat/completions", parsedURL.Path)
	assert.Equal(t, "1", parsedURL.Query().Get("existing"))
	assert.Equal(t, "sk-test", parsedURL.Query().Get("api_key"))
}

func TestAdaptorJoinsUpstreamPathWithChannelBaseURL(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/proxy/v1/chat/completions?existing=1",
				Converter:    relayconvert.ConverterNone,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeQuery,
					Name:  "api_key",
					Value: "{api_key}",
				},
			},
		},
	})
	info.ChannelBaseUrl = "https://gateway.example/base"

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)

	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "https", parsedURL.Scheme)
	assert.Equal(t, "gateway.example", parsedURL.Host)
	assert.Equal(t, "/base/proxy/v1/chat/completions", parsedURL.Path)
	assert.Equal(t, "1", parsedURL.Query().Get("existing"))
	assert.Equal(t, "sk-test", parsedURL.Query().Get("api_key"))
}

func TestAdaptorReturnsErrorWhenUpstreamPathNeedsMissingBaseURL(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterNone,
			},
		},
	})
	info.ChannelBaseUrl = ""

	_, err := adaptor.GetRequestURL(info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "base URL is required")
}

func TestAdaptorSetupRequestHeaderUsesDefaultBearerAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "https://upstream.example/v1/chat/completions",
				Converter:    relayconvert.ConverterNone,
			},
		},
	})
	c := advancedCustomGinContext("/v1/chat/completions")
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "Bearer sk-test", header.Get("Authorization"))
}

func TestAdaptorSetupRequestHeaderUsesConfiguredHeaderAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "https://upstream.example/v1/chat/completions",
				Converter:    relayconvert.ConverterNone,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeHeader,
					Name:  "x-api-key",
					Value: "{api_key}",
				},
			},
		},
	})
	c := advancedCustomGinContext("/v1/chat/completions")
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Empty(t, header.Get("Authorization"))
	assert.Equal(t, "sk-test", header.Get("x-api-key"))
}

func TestAdaptorSetupRequestHeaderAddsClaudeDefaultHeaders(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "https://api.anthropic.com/v1/messages",
				Converter:    relayconvert.ConverterNone,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeHeader,
					Name:  "x-api-key",
					Value: "{api_key}",
				},
			},
		},
	})
	info.RelayFormat = types.RelayFormatClaude
	c := advancedCustomGinContext("/v1/messages")
	header := http.Header{}

	require.NoError(t, adaptor.SetupRequestHeader(c, &header, info))
	assert.Equal(t, "sk-test", header.Get("x-api-key"))
	assert.Equal(t, "2023-06-01", header.Get("anthropic-version"))
}

func TestAdaptorReturnsErrorWhenNoRouteMatchesPath(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "https://upstream.example/v1/chat/completions",
				Converter:    relayconvert.ConverterClaudeMessagesToOpenAIChat,
			},
		},
	})
	info.RequestURLPath = "/v1/chat/completions"

	_, err := adaptor.GetRequestURL(info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not support request path")
}

func TestAdaptorReplacesModelPlaceholderInRouteURL(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent",
				Converter:    relayconvert.ConverterOpenAIChatToGeminiContent,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeQuery,
					Name:  "key",
					Value: "{api_key}",
				},
			},
		},
	})
	info.UpstreamModelName = "gemini-2.5-flash"

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)

	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "/v1beta/models/gemini-2.5-flash:generateContent", parsedURL.Path)
	assert.Equal(t, "sk-test", parsedURL.Query().Get("key"))
	assert.Empty(t, parsedURL.Query().Get("alt"))
}

func TestAdaptorSwitchesGeminiGenerateContentURLForStream(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?existing=1",
				Converter:    relayconvert.ConverterOpenAIChatToGeminiContent,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeQuery,
					Name:  "key",
					Value: "{api_key}",
				},
			},
		},
	})
	info.UpstreamModelName = "gemini-2.5-pro"
	info.IsStream = true

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)

	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "/v1beta/models/gemini-2.5-pro:streamGenerateContent", parsedURL.Path)
	assert.Equal(t, "sse", parsedURL.Query().Get("alt"))
	assert.Equal(t, "1", parsedURL.Query().Get("existing"))
	assert.Equal(t, "sk-test", parsedURL.Query().Get("key"))
}

func TestAdaptorMatchesGeminiIncomingPathTemplate(t *testing.T) {
	tests := []struct {
		name            string
		requestURLPath  string
		wantRequestPath string
	}{
		{
			name:            "generate content",
			requestURLPath:  "/v1beta/models/gemini-2.5-flash:generateContent",
			wantRequestPath: "/v1/chat/completions",
		},
		{
			name:            "stream generate content",
			requestURLPath:  "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
			wantRequestPath: "/v1/chat/completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := &Adaptor{}
			info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
				Routes: []dto.AdvancedCustomRoute{
					{
						IncomingPath: "/v1beta/models/{model}:generateContent",
						UpstreamPath: "https://upstream.example/v1/chat/completions",
						Converter:    relayconvert.ConverterGeminiContentToOpenAIChat,
					},
				},
			})
			info.RequestURLPath = tt.requestURLPath

			requestURL, err := adaptor.GetRequestURL(info)
			require.NoError(t, err)

			parsedURL, err := url.Parse(requestURL)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRequestPath, parsedURL.Path)
		})
	}
}

func TestAdaptorBuildModelListRequestUsesConfiguredRouteAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/models",
				UpstreamPath: "/provider/models",
				Converter:    relayconvert.ConverterNone,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeHeader,
					Name:  "x-api-key",
					Value: "token {api_key}",
				},
			},
		},
	})
	info.RequestURLPath = "/v1/models"

	requestURL, header, err := adaptor.BuildModelListRequest(info)
	require.NoError(t, err)

	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "fallback.example", parsedURL.Host)
	assert.Equal(t, "/provider/models", parsedURL.Path)
	assert.Equal(t, "token sk-test", header.Get("x-api-key"))
	assert.Empty(t, header.Get("Authorization"))
}

func TestAdaptorBuildModelListRequestUsesConfiguredQueryAuth(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/models",
				UpstreamPath: "https://upstream.example/v1/models?existing=1",
				Converter:    relayconvert.ConverterNone,
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeQuery,
					Name:  "key",
					Value: "{api_key}",
				},
			},
		},
	})
	info.RequestURLPath = "/v1/models"

	requestURL, header, err := adaptor.BuildModelListRequest(info)
	require.NoError(t, err)

	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "upstream.example", parsedURL.Host)
	assert.Equal(t, "/v1/models", parsedURL.Path)
	assert.Equal(t, "1", parsedURL.Query().Get("existing"))
	assert.Equal(t, "sk-test", parsedURL.Query().Get("key"))
	assert.Empty(t, header.Get("Authorization"))
}

func TestAdaptorBuildModelListRequestDefaultAndNoAuth(t *testing.T) {
	tests := []struct {
		name              string
		auth              *dto.AdvancedCustomRouteAuth
		wantAuthorization string
	}{
		{
			name:              "default bearer",
			wantAuthorization: "Bearer sk-test",
		},
		{
			name: "no authentication",
			auth: &dto.AdvancedCustomRouteAuth{
				Type: dto.AdvancedCustomAuthTypeNone,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
				Routes: []dto.AdvancedCustomRoute{
					{
						IncomingPath: dto.AdvancedCustomModelListPath,
						UpstreamPath: "/provider/models",
						Auth:         tt.auth,
					},
				},
			})
			info.RequestURLPath = "/unrelated/path"

			requestURL, header, err := (&Adaptor{}).BuildModelListRequest(info)
			require.NoError(t, err)
			assert.Equal(t, "https://fallback.example/provider/models", requestURL)
			assert.Equal(t, tt.wantAuthorization, header.Get("Authorization"))
		})
	}
}

func TestAdaptorBuildModelListRequestDoesNotReuseRelayRoute(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/chat",
			},
			{
				IncomingPath: dto.AdvancedCustomModelListPath,
				UpstreamPath: "/provider/models",
			},
		},
	})

	chatURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	assert.Equal(t, "https://fallback.example/chat", chatURL)

	modelURL, header, err := adaptor.BuildModelListRequest(info)
	require.NoError(t, err)
	assert.Equal(t, "https://fallback.example/provider/models", modelURL)
	assert.Equal(t, "Bearer sk-test", header.Get("Authorization"))
}

func TestAdaptorBuildModelListRequestRequiresConfiguredRoute(t *testing.T) {
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/chat/completions",
			},
		},
	})

	_, _, err := (&Adaptor{}).BuildModelListRequest(info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not configure a /v1/models route")
}

func TestAdaptorBuildBalanceRequestUsesConfiguredRoute(t *testing.T) {
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: dto.AdvancedCustomModelListPath,
				UpstreamPath: "/provider/models",
			},
			{
				IncomingPath: dto.AdvancedCustomBalancePath,
				UpstreamPath: "/provider/balance?existing=1",
				Auth: &dto.AdvancedCustomRouteAuth{
					Type:  dto.AdvancedCustomAuthTypeQuery,
					Name:  "token",
					Value: "prefix-{api_key}",
				},
			},
		},
	})

	requestURL, header, err := (&Adaptor{}).BuildBalanceRequest(info)
	require.NoError(t, err)

	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "/provider/balance", parsedURL.Path)
	assert.Equal(t, "1", parsedURL.Query().Get("existing"))
	assert.Equal(t, "prefix-sk-test", parsedURL.Query().Get("token"))
	assert.Empty(t, header.Get("Authorization"))
}

func TestAdaptorBuildBalanceRequestRequiresConfiguredRoute(t *testing.T) {
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{{
			IncomingPath: dto.AdvancedCustomModelListPath,
			UpstreamPath: "/provider/models",
		}},
	})

	_, _, err := (&Adaptor{}).BuildBalanceRequest(info)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not configure a /v1/dashboard/billing/credit_grants route")
}

func TestAdaptorConvertsResponsesRequestToOpenAIChatUpstream(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterOpenAIResponsesToOpenAIChat,
			},
		},
	})
	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, dto.OpenAIResponsesRequest{
		Model:        "gpt-test",
		Instructions: mustAdvancedCustomRawMessage(t, "system rules"),
		Input:        mustAdvancedCustomRawMessage(t, "hello"),
	})
	require.NoError(t, err)

	chatReq, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Equal(t, "gpt-test", chatReq.Model)
	require.Len(t, chatReq.Messages, 2)
	assert.Equal(t, "system", chatReq.Messages[0].Role)
	assert.Equal(t, "system rules", chatReq.Messages[0].StringContent())
	assert.Equal(t, "user", chatReq.Messages[1].Role)
	assert.Equal(t, "hello", chatReq.Messages[1].StringContent())

	requestURL, err := adaptor.GetRequestURL(info)
	require.NoError(t, err)
	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "/v1/chat/completions", parsedURL.Path)
}

func TestAdaptorSelectsDuplicateResponsesRoutesByModel(t *testing.T) {
	config := &dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterOpenAIResponsesToOpenAIChat,
				Models:       []string{"gpt-test"},
			},
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    relayconvert.ConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-test"},
			},
		},
	}

	chatAdaptor := &Adaptor{}
	chatInfo := advancedCustomRelayInfo(config)
	chatInfo.RelayFormat = types.RelayFormatOpenAIResponses
	chatInfo.RelayMode = relayconstant.RelayModeResponses
	chatInfo.RequestURLPath = "/v1/responses"
	chatInfo.OriginModelName = "gpt-test"
	chatInfo.UpstreamModelName = "gpt-test"
	chatConverted, err := chatAdaptor.ConvertOpenAIResponsesRequest(advancedCustomGinContext("/v1/responses"), chatInfo, dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustAdvancedCustomRawMessage(t, "hello"),
	})
	require.NoError(t, err)
	_, ok := chatConverted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)

	geminiAdaptor := &Adaptor{}
	geminiInfo := advancedCustomRelayInfo(config)
	geminiInfo.RelayFormat = types.RelayFormatOpenAIResponses
	geminiInfo.RelayMode = relayconstant.RelayModeResponses
	geminiInfo.RequestURLPath = "/v1/responses"
	geminiInfo.OriginModelName = "gemini-test"
	geminiInfo.UpstreamModelName = "gemini-test"
	geminiInfo.IsStream = true
	geminiConverted, err := geminiAdaptor.ConvertOpenAIResponsesRequest(advancedCustomGinContext("/v1/responses"), geminiInfo, dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustAdvancedCustomRawMessage(t, "hello"),
	})
	require.NoError(t, err)
	_, ok = geminiConverted.(*dto.GeminiChatRequest)
	require.True(t, ok)

	requestURL, err := geminiAdaptor.GetRequestURL(geminiInfo)
	require.NoError(t, err)
	parsedURL, err := url.Parse(requestURL)
	require.NoError(t, err)
	assert.Equal(t, "/v1beta/models/gemini-test:streamGenerateContent", parsedURL.Path)
	assert.Equal(t, "sse", parsedURL.Query().Get("alt"))
}

func TestAdaptorResponsesToGeminiUsesResponsesBridge(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    relayconvert.ConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-test"},
			},
		},
	})
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"
	info.OriginModelName = "gemini-test"
	info.UpstreamModelName = "gemini-test"
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("Content-Type", "application/json")

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "hello"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     2,
			CandidatesTokenCount: 3,
			TotalTokenCount:      5,
		},
	}
	body, err := common.Marshal(payload)
	require.NoError(t, err)

	usage, newAPIError := adaptor.DoResponse(c, &http.Response{
		Body: io.NopCloser(bytes.NewReader(body)),
	}, info)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)

	got := recorder.Body.String()
	assert.Contains(t, got, `"object":"response"`)
	assert.Contains(t, got, `"type":"output_text"`)
	assert.Contains(t, got, `"text":"hello"`)
	assert.NotContains(t, got, `"candidates"`)
}

func TestAdaptorResponsesToGeminiAddsThoughtSignatureForFunctionCallHistory(t *testing.T) {
	geminiSettings := model_setting.GetGeminiSettings()
	originalThoughtSignatureEnabled := geminiSettings.FunctionCallThoughtSignatureEnabled
	geminiSettings.FunctionCallThoughtSignatureEnabled = true
	t.Cleanup(func() {
		geminiSettings.FunctionCallThoughtSignatureEnabled = originalThoughtSignatureEnabled
	})

	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    relayconvert.ConverterOpenAIResponsesToGemini,
				Models:       []string{"gemini-test"},
			},
		},
	})
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayMode = relayconstant.RelayModeResponses
	info.RequestURLPath = "/v1/responses"
	info.OriginModelName = "gemini-test"
	info.UpstreamModelName = "gemini-test"

	converted, err := adaptor.ConvertOpenAIResponsesRequest(advancedCustomGinContext("/v1/responses"), info, dto.OpenAIResponsesRequest{
		Model: "gemini-test",
		Input: mustAdvancedCustomRawMessage(t, []map[string]any{
			{
				"role":    "user",
				"content": "hi",
			},
			{
				"type":      "function_call",
				"call_id":   "call_1",
				"name":      "glob",
				"arguments": map[string]any{"query": "*"},
			},
			{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  []map[string]any{{"path": "report.md"}},
			},
		}),
		Tools: mustAdvancedCustomRawMessage(t, []map[string]any{
			{"type": "function", "name": "glob", "parameters": map[string]any{"type": "object"}},
		}),
	})
	require.NoError(t, err)

	geminiReq, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Len(t, geminiReq.Contents, 3)
	require.Len(t, geminiReq.Contents[1].Parts, 1)
	require.NotNil(t, geminiReq.Contents[1].Parts[0].FunctionCall)
	assert.NotEmpty(t, geminiReq.Contents[1].Parts[0].ThoughtSignature)
	require.Len(t, geminiReq.Contents[2].Parts, 1)
	require.NotNil(t, geminiReq.Contents[2].Parts[0].FunctionResponse)
	assert.Empty(t, geminiReq.Contents[2].Parts[0].ThoughtSignature)
}

func TestAdaptorConvertsOpenAIChatRequestToResponsesUpstream(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/responses",
				Converter:    relayconvert.ConverterOpenAIChatToOpenAIResponses,
			},
		},
	})
	c := advancedCustomGinContext("/v1/chat/completions")

	converted, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
		Model: "gpt-test",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	})
	require.NoError(t, err)

	responsesReq, ok := converted.(*dto.OpenAIResponsesRequest)
	require.True(t, ok)
	assert.Equal(t, "gpt-test", responsesReq.Model)
	assert.NotEmpty(t, responsesReq.Input)
}

func TestAdaptorConvertsOpenAIChatRequestToClaudeUpstream(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1/messages",
				Converter:    relayconvert.ConverterOpenAIChatToClaudeMessages,
			},
		},
	})
	c := advancedCustomGinContext("/v1/chat/completions")

	converted, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
		Model: "claude-test",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	})
	require.NoError(t, err)

	claudeReq, ok := converted.(*dto.ClaudeRequest)
	require.True(t, ok)
	assert.Equal(t, "claude-test", claudeReq.Model)
	require.Len(t, claudeReq.Messages, 1)
	assert.Equal(t, "user", claudeReq.Messages[0].Role)
}

func TestAdaptorConvertsOpenAIChatRequestToGeminiUpstream(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/v1beta/models/{model}:generateContent",
				Converter:    relayconvert.ConverterOpenAIChatToGeminiContent,
			},
		},
	})
	info.UpstreamModelName = "gemini-2.5-flash"
	c := advancedCustomGinContext("/v1/chat/completions")

	converted, err := adaptor.ConvertOpenAIRequest(c, info, &dto.GeneralOpenAIRequest{
		Model: "gemini-2.5-flash",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
		},
	})
	require.NoError(t, err)

	geminiReq, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Len(t, geminiReq.Contents, 1)
	assert.Equal(t, "user", geminiReq.Contents[0].Role)
}

func TestAdaptorConvertsClaudeRequestToOpenAIChatUpstream(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterClaudeMessagesToOpenAIChat,
			},
		},
	})
	info.RelayFormat = types.RelayFormatClaude
	info.RequestURLPath = "/v1/messages"
	c := advancedCustomGinContext("/v1/messages")

	converted, err := adaptor.ConvertClaudeRequest(c, info, &dto.ClaudeRequest{
		Model: "gpt-test",
		Messages: []dto.ClaudeMessage{
			{Role: "user", Content: "hello"},
		},
	})
	require.NoError(t, err)

	chatReq, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Equal(t, "gpt-test", chatReq.Model)
	require.Len(t, chatReq.Messages, 1)
	assert.Equal(t, "user", chatReq.Messages[0].Role)
}

func TestAdaptorConvertsGeminiRequestToOpenAIChatUpstream(t *testing.T) {
	adaptor := &Adaptor{}
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{
		Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1beta/models/{model}:generateContent",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterGeminiContentToOpenAIChat,
			},
		},
	})
	info.RelayFormat = types.RelayFormatGemini
	info.RequestURLPath = "/v1beta/models/gemini-2.5-flash:generateContent"
	info.UpstreamModelName = "gpt-test"
	c := advancedCustomGinContext("/v1beta/models/gemini-2.5-flash:generateContent")

	converted, err := adaptor.ConvertGeminiRequest(c, info, &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{
			{
				Role: "user",
				Parts: []dto.GeminiPart{
					{Text: "hello"},
				},
			},
		},
	})
	require.NoError(t, err)

	chatReq, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	assert.Equal(t, "gpt-test", chatReq.Model)
	require.Len(t, chatReq.Messages, 1)
	assert.Equal(t, "user", chatReq.Messages[0].Role)
}

func TestAdaptorCrossProtocolChatUpstreamRequestsStreamUsage(t *testing.T) {
	claudeReq := &dto.ClaudeRequest{
		Model:    "gpt-test",
		Messages: []dto.ClaudeMessage{{Role: "user", Content: "hello"}},
	}
	geminiReq := &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: "hello"}}}},
	}
	responsesReq := dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: mustAdvancedCustomRawMessage(t, "hello"),
	}

	tests := []struct {
		name         string
		route        dto.AdvancedCustomRoute
		relayFormat  types.RelayFormat
		relayMode    int
		requestPath  string
		convert      func(*Adaptor, *gin.Context, *relaycommon.RelayInfo) (any, error)
		isStream     bool
		supportUsage bool
		wantUsage    bool
	}{
		{
			name: "claude stream requests usage",
			route: dto.AdvancedCustomRoute{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterClaudeMessagesToOpenAIChat,
			},
			relayFormat: types.RelayFormatClaude,
			relayMode:   relayconstant.RelayModeChatCompletions,
			requestPath: "/v1/messages",
			convert: func(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (any, error) {
				return a.ConvertClaudeRequest(c, info, claudeReq)
			},
			isStream:     true,
			supportUsage: true,
			wantUsage:    true,
		},
		{
			name: "gemini stream requests usage",
			route: dto.AdvancedCustomRoute{
				IncomingPath: "/v1beta/models/{model}:generateContent",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterGeminiContentToOpenAIChat,
			},
			relayFormat: types.RelayFormatGemini,
			relayMode:   relayconstant.RelayModeGemini,
			requestPath: "/v1beta/models/gpt-test:generateContent",
			convert: func(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (any, error) {
				return a.ConvertGeminiRequest(c, info, geminiReq)
			},
			isStream:     true,
			supportUsage: true,
			wantUsage:    true,
		},
		{
			name: "responses stream requests usage",
			route: dto.AdvancedCustomRoute{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterOpenAIResponsesToOpenAIChat,
			},
			relayFormat: types.RelayFormatOpenAIResponses,
			relayMode:   relayconstant.RelayModeResponses,
			requestPath: "/v1/responses",
			convert: func(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (any, error) {
				return a.ConvertOpenAIResponsesRequest(c, info, responsesReq)
			},
			isStream:     true,
			supportUsage: true,
			wantUsage:    true,
		},
		{
			name: "claude non-stream leaves stream options unset",
			route: dto.AdvancedCustomRoute{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterClaudeMessagesToOpenAIChat,
			},
			relayFormat: types.RelayFormatClaude,
			relayMode:   relayconstant.RelayModeChatCompletions,
			requestPath: "/v1/messages",
			convert: func(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (any, error) {
				return a.ConvertClaudeRequest(c, info, claudeReq)
			},
			isStream:     false,
			supportUsage: true,
			wantUsage:    false,
		},
		{
			name: "claude stream without stream options support",
			route: dto.AdvancedCustomRoute{
				IncomingPath: "/v1/messages",
				UpstreamPath: "/v1/chat/completions",
				Converter:    relayconvert.ConverterClaudeMessagesToOpenAIChat,
			},
			relayFormat: types.RelayFormatClaude,
			relayMode:   relayconstant.RelayModeChatCompletions,
			requestPath: "/v1/messages",
			convert: func(a *Adaptor, c *gin.Context, info *relaycommon.RelayInfo) (any, error) {
				return a.ConvertClaudeRequest(c, info, claudeReq)
			},
			isStream:     true,
			supportUsage: false,
			wantUsage:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := &Adaptor{}
			info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{tt.route}})
			info.RelayFormat = tt.relayFormat
			info.RelayMode = tt.relayMode
			info.RequestURLPath = tt.requestPath
			info.IsStream = tt.isStream
			info.SupportStreamOptions = tt.supportUsage
			c := advancedCustomGinContext(tt.requestPath)

			converted, err := tt.convert(adaptor, c, info)
			require.NoError(t, err)
			chatReq, ok := converted.(*dto.GeneralOpenAIRequest)
			require.True(t, ok)

			if !tt.wantUsage {
				assert.Nil(t, chatReq.StreamOptions)
				return
			}
			require.NotNil(t, chatReq.StreamOptions)
			assert.True(t, chatReq.StreamOptions.IncludeUsage)
		})
	}
}

func TestPipioClaudeResponsesOutputLimitOutbound(t *testing.T) {
	originalTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 30
	t.Cleanup(func() { constant.StreamingTimeout = originalTimeout })
	service.InitHttpClient()
	client := service.GetHttpClient()
	require.NotNil(t, client)
	originalTransport := client.Transport
	t.Cleanup(func() { client.Transport = originalTransport })
	models := []string{
		"claude-opus-5", "claude-sonnet-5", "claude-haiku-4-5-20251001", "claude-opus-4-7",
		"claude-fable-5", "claude-opus-4-6", "claude-opus-4-8", "claude-opus-5-5",
		"claude-opus-4-5-20251101", "claude-sonnet-4-5-20250929", "claude-sonnet-4-6",
	}
	for _, model := range models {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", model, stream), func(t *testing.T) {
				adaptor, c, info, request := pipioClaudeResponsesFixture(t, model, stream)
				recorder := httptest.NewRecorder()
				originalContext := c
				c, _ = gin.CreateTestContext(recorder)
				c.Request, c.Keys = originalContext.Request, originalContext.Keys
				original := mustAdvancedCustomRawMessage(t, request)
				converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
				require.NoError(t, err)
				chat, ok := converted.(*dto.GeneralOpenAIRequest)
				require.True(t, ok)
				require.NotNil(t, chat.MaxTokens)
				assert.EqualValues(t, 32, *chat.MaxTokens)
				assert.Nil(t, chat.MaxCompletionTokens)
				assert.Equal(t, original, mustAdvancedCustomRawMessage(t, request))
				require.NotNil(t, chat.Temperature)
				assert.Zero(t, *chat.Temperature)
				if stream {
					require.NotNil(t, chat.StreamOptions)
					assert.True(t, chat.StreamOptions.IncludeUsage)
				} else {
					assert.Nil(t, chat.StreamOptions)
				}
				data := mustAdvancedCustomRawMessage(t, chat)
				data, err = relaycommon.RemoveDisabledFields(data, info.ChannelOtherSettings, false)
				require.NoError(t, err)
				body, closer, err := relaycommon.NewOutboundJSONBody(data)
				require.NoError(t, err)
				defer closer.Close()
				var outbound []byte
				client.Transport = advancedCustomRoundTripFunc(func(r *http.Request) (*http.Response, error) {
					assert.Equal(t, "http://ai-upstream-pipio:8080/v1/chat/completions", r.URL.String())
					outbound, err = io.ReadAll(r.Body)
					require.NoError(t, err)
					response := fmt.Sprintf(`{"id":"chat-fixture","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`, model)
					contentType := "application/json"
					if stream {
						contentType = "text/event-stream"
						response = fmt.Sprintf("data: {\"id\":\"chat-fixture\",\"object\":\"chat.completion.chunk\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"OK\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat-fixture\",\"model\":%q,\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: {\"id\":\"chat-fixture\",\"model\":%q,\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15}}\n\ndata: [DONE]\n\n", model, model, model)
					}
					return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
				})
				response, err := adaptor.DoRequest(c, info, body)
				require.NoError(t, err)
				assert.Equal(t, data, outbound, "metadata capture must not consume or rewrite the actual outbound reader")
				var emitted map[string]common.RawMessage
				require.NoError(t, common.Unmarshal(outbound, &emitted))
				assert.JSONEq(t, "32", string(emitted["max_tokens"]))
				assert.NotContains(t, emitted, "max_completion_tokens")
				diagnostics := info.ConversionDiagnostics()
				require.Len(t, diagnostics, 1)
				assert.Equal(t, pipioClaudeResponsesOutputLimitProfile, diagnostics[0].Code)
				assert.Equal(t, fmt.Sprintf("max_output_tokens=32 max_tokens=32 providerBodyBytes=%d providerBodySha256=%x", len(outbound), sha256.Sum256(outbound)), diagnostics[0].Message)
				usageValue, apiErr := adaptor.DoResponse(c, response.(*http.Response), info)
				require.Nil(t, apiErr)
				usage, ok := usageValue.(*dto.Usage)
				require.True(t, ok)
				assert.EqualValues(t, 10, usage.PromptTokens)
				assert.EqualValues(t, 5, usage.CompletionTokens)
				assert.EqualValues(t, 15, usage.TotalTokens)
				if stream {
					assert.Contains(t, recorder.Body.String(), "response.completed")
					assert.NotContains(t, recorder.Body.String(), "response.incomplete")
				} else {
					var reply dto.OpenAIResponsesResponse
					require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &reply))
					assert.JSONEq(t, `"completed"`, string(reply.Status))
				}
				assert.Equal(t, original, mustAdvancedCustomRawMessage(t, request))
			})
		}
	}
}

func TestPipioClaudeResponsesOutputLimitScope(t *testing.T) {
	service.InitHttpClient()
	client := service.GetHttpClient()
	originalTransport := client.Transport
	t.Cleanup(func() { client.Transport = originalTransport })
	var emitted []byte
	client.Transport = advancedCustomRoundTripFunc(func(r *http.Request) (*http.Response, error) {
		var err error
		emitted, err = io.ReadAll(r.Body)
		require.NoError(t, err)
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})
	tests := []struct {
		name   string
		change func(*gin.Context, *relaycommon.RelayInfo)
	}{
		{"other channel name", func(c *gin.Context, _ *relaycommon.RelayInfo) {
			common.SetContextKey(c, constant.ContextKeyChannelName, "other")
		}},
		{"other selected group", func(c *gin.Context, _ *relaycommon.RelayInfo) {
			common.SetContextKey(c, constant.ContextKeyChannelGroup, "other")
		}},
		{"no selected group despite matching caller group", func(c *gin.Context, _ *relaycommon.RelayInfo) {
			c.Keys[string(constant.ContextKeyChannelGroup)] = nil
			common.SetContextKey(c, constant.ContextKeyUsingGroup, "panstar_pipio_claude")
		}},
		{"other base", func(_ *gin.Context, info *relaycommon.RelayInfo) { info.ChannelBaseUrl = "https://other.example" }},
		{"other channel type", func(_ *gin.Context, info *relaycommon.RelayInfo) { info.ChannelType = constant.ChannelTypeOpenAI }},
		{"other origin model", func(_ *gin.Context, info *relaycommon.RelayInfo) {
			info.OriginModelName = "gpt-test"
			info.ChannelOtherSettings.AdvancedCustom.Routes[0].Models = nil
		}},
		{"other upstream model", func(_ *gin.Context, info *relaycommon.RelayInfo) { info.UpstreamModelName = "gpt-test" }},
		{"different Claude model binding", func(_ *gin.Context, info *relaycommon.RelayInfo) { info.UpstreamModelName = "claude-sonnet-5" }},
		{"native Responses converter", func(_ *gin.Context, info *relaycommon.RelayInfo) {
			info.ChannelOtherSettings.AdvancedCustom.Routes[0].Converter = relayconvert.ConverterNone
			info.ChannelOtherSettings.AdvancedCustom.Routes[0].UpstreamPath = "/v1/responses"
		}},
		{"none chat destination", func(_ *gin.Context, info *relaycommon.RelayInfo) {
			info.ChannelOtherSettings.AdvancedCustom.Routes[0].Converter = relayconvert.ConverterNone
		}},
		{"passthrough route", func(_ *gin.Context, info *relaycommon.RelayInfo) {
			info.ChannelOtherSettings.AdvancedCustom.Routes[0].Converter = relayconvert.ConverterNone
			info.ChannelOtherSettings.AdvancedCustom.Routes[0].PassThroughBodyEnabled = true
		}},
		{"channel passthrough", func(_ *gin.Context, info *relaycommon.RelayInfo) { info.ChannelSetting.PassThroughBodyEnabled = true }},
		{"other upstream path", func(_ *gin.Context, info *relaycommon.RelayInfo) {
			info.ChannelOtherSettings.AdvancedCustom.Routes[0].UpstreamPath = "https://other.example/v1/chat/completions"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", false)
			tt.change(c, info)
			converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
			require.NoError(t, err)
			switch converted := converted.(type) {
			case *dto.GeneralOpenAIRequest:
				assert.Nil(t, converted.MaxTokens)
				require.NotNil(t, converted.MaxCompletionTokens)
				assert.EqualValues(t, 32, *converted.MaxCompletionTokens)
			case dto.OpenAIResponsesRequest:
				assert.Equal(t, request.MaxOutputTokens, converted.MaxOutputTokens)
			default:
				t.Fatalf("unexpected converted type %T", converted)
			}
			assert.Empty(t, info.ConversionDiagnostics())
			data := mustAdvancedCustomRawMessage(t, converted)
			response, err := adaptor.DoRequest(c, info, bytes.NewBuffer(data))
			require.NoError(t, err)
			require.NoError(t, response.(*http.Response).Body.Close())
			assert.Equal(t, data, emitted)
			assert.Empty(t, info.ConversionDiagnostics())
		})
	}
	t.Run("global passthrough", func(t *testing.T) {
		settings := model_setting.GetGlobalSettings()
		before := settings.PassThroughRequestEnabled
		settings.PassThroughRequestEnabled = true
		t.Cleanup(func() { settings.PassThroughRequestEnabled = before })
		adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", false)
		converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
		require.NoError(t, err)
		chat := converted.(*dto.GeneralOpenAIRequest)
		assert.Nil(t, chat.MaxTokens)
		assert.Equal(t, request.MaxOutputTokens, chat.MaxCompletionTokens)
	})
	t.Run("Chat none passthrough retains MC", func(t *testing.T) {
		adaptor, c, info, _ := pipioClaudeResponsesFixture(t, "claude-opus-5", true)
		info.RelayFormat = types.RelayFormatOpenAI
		info.RelayMode = relayconstant.RelayModeChatCompletions
		info.RequestURLPath = "/v1/chat/completions"
		c.Request.URL.Path = info.RequestURLPath
		info.ChannelOtherSettings.AdvancedCustom.Routes[0].IncomingPath = info.RequestURLPath
		info.ChannelOtherSettings.AdvancedCustom.Routes[0].Converter = relayconvert.ConverterNone
		info.ChannelOtherSettings.AdvancedCustom.Routes[0].PassThroughBodyEnabled = true
		request := &dto.GeneralOpenAIRequest{Model: info.UpstreamModelName, MaxCompletionTokens: lo.ToPtr(uint(32)), Messages: []dto.Message{{Role: "user", Content: "Reply exactly OK"}}}
		converted, err := adaptor.ConvertOpenAIRequest(c, info, request)
		require.NoError(t, err)
		chat := converted.(*dto.GeneralOpenAIRequest)
		assert.Nil(t, chat.MaxTokens)
		require.NotNil(t, chat.MaxCompletionTokens)
		assert.EqualValues(t, 32, *chat.MaxCompletionTokens)
		assert.Empty(t, info.ConversionDiagnostics())
		data := mustAdvancedCustomRawMessage(t, request)
		adaptor = &Adaptor{}
		response, err := adaptor.DoRequest(c, info, bytes.NewBuffer(data))
		require.NoError(t, err)
		require.NoError(t, response.(*http.Response).Body.Close())
		assert.Equal(t, data, emitted, "raw none/passthrough chat body remains byte-identical")
		assert.Empty(t, info.ConversionDiagnostics())
	})
}

func TestPipioClaudeResponsesOutputLimitValidation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, limit := range []*uint{nil, lo.ToPtr(uint(0)), lo.ToPtr(uint(math.MaxInt32/2 + 1)), lo.ToPtr(^uint(0))} {
			name := "omitted"
			if limit != nil {
				name = fmt.Sprint(*limit)
			}
			t.Run(fmt.Sprintf("stream=%t/limit=%s", stream, name), func(t *testing.T) {
				adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", stream)
				request.MaxOutputTokens = limit
				converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
				if limit == nil {
					require.NoError(t, err)
					chat := converted.(*dto.GeneralOpenAIRequest)
					assert.Nil(t, chat.MaxTokens)
					assert.Nil(t, chat.MaxCompletionTokens)
					assert.Empty(t, info.ConversionDiagnostics())
				} else {
					require.Error(t, err)
					var conversionLoss *types.ConversionLossError
					require.ErrorAs(t, err, &conversionLoss)
					assert.Nil(t, converted)
				}
			})
		}
	}
	for _, tt := range []struct {
		name              string
		mc, mt, requested *uint
		wantError         bool
	}{
		{"equal converted aliases", lo.ToPtr(uint(32)), lo.ToPtr(uint(32)), lo.ToPtr(uint(32)), false},
		{"different converted aliases", lo.ToPtr(uint(32)), lo.ToPtr(uint(33)), lo.ToPtr(uint(32)), true},
		{"zero second alias", lo.ToPtr(uint(32)), lo.ToPtr(uint(0)), lo.ToPtr(uint(32)), true},
		{"converted budget larger", lo.ToPtr(uint(33)), nil, lo.ToPtr(uint(32)), true},
		{"converted budget smaller", lo.ToPtr(uint(31)), nil, lo.ToPtr(uint(32)), true},
		{"converted limit absent", nil, nil, lo.ToPtr(uint(32)), true},
		{"invented default budget", lo.ToPtr(uint(32)), nil, nil, true},
		{"existing MT equal", nil, lo.ToPtr(uint(32)), lo.ToPtr(uint(32)), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := &Adaptor{}
			request := &dto.GeneralOpenAIRequest{MaxCompletionTokens: tt.mc, MaxTokens: tt.mt}
			err := adaptor.normalizePipioClaudeResponsesOutputLimit(request, tt.requested)
			if tt.wantError {
				require.Error(t, err)
				assert.Equal(t, tt.mc, request.MaxCompletionTokens)
				assert.Equal(t, tt.mt, request.MaxTokens)
			} else {
				require.NoError(t, err)
				assert.Nil(t, request.MaxCompletionTokens)
				require.NotNil(t, request.MaxTokens)
				assert.EqualValues(t, 32, *request.MaxTokens)
			}
		})
	}
}

func TestPipioClaudeResponsesOutputLimitFinalOverridesFailClosed(t *testing.T) {
	for _, override := range []map[string]any{
		{"max_tokens": 33}, {"max_tokens": 31}, {"max_tokens": 0}, {"max_tokens": nil},
		{"max_completion_tokens": 32}, {"max_completion_tokens": nil}, {"model": "claude-sonnet-5"},
	} {
		t.Run(fmt.Sprint(override), func(t *testing.T) {
			adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", false)
			converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
			require.NoError(t, err)
			info.ParamOverride = override
			data, err := relaycommon.ApplyParamOverrideWithRelayInfo(mustAdvancedCustomRawMessage(t, converted), info)
			require.NoError(t, err)
			body := bytes.NewBuffer(data)
			response, err := adaptor.DoRequest(c, info, body)
			require.Error(t, err)
			assert.Nil(t, response)
			var apiErr *types.NewAPIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, http.StatusBadRequest, apiErr.StatusCode)
			assert.True(t, types.IsSkipRetryError(apiErr))
			assert.Equal(t, data, body.Bytes(), "failed validation must not consume the outbound body")
			assert.Empty(t, info.ConversionDiagnostics())
		})
	}
	t.Run("closed replay source", func(t *testing.T) {
		adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", false)
		converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
		require.NoError(t, err)
		body, closer, err := relaycommon.NewOutboundJSONBody(mustAdvancedCustomRawMessage(t, converted))
		require.NoError(t, err)
		require.NoError(t, closer.Close())
		_, err = adaptor.DoRequest(c, info, body)
		require.Error(t, err)
		assert.Empty(t, info.ConversionDiagnostics())
	})
	t.Run("non-replayable body", func(t *testing.T) {
		adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", false)
		converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
		require.NoError(t, err)
		body := io.LimitReader(bytes.NewBuffer(mustAdvancedCustomRawMessage(t, converted)), 1024)
		_, err = adaptor.DoRequest(c, info, body)
		require.ErrorContains(t, err, "not replayable")
		assert.Empty(t, info.ConversionDiagnostics())
	})
	t.Run("bounded metadata exhausted", func(t *testing.T) {
		adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", false)
		converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
		require.NoError(t, err)
		for i := range 32 {
			info.RecordConversionDiagnostics(c, []types.ConversionDiagnostic{{Code: fmt.Sprintf("fixture-%d", i), Severity: types.ConversionDiagnosticWarning}})
		}
		body := bytes.NewBuffer(mustAdvancedCustomRawMessage(t, converted))
		before := bytes.Clone(body.Bytes())
		_, err = adaptor.DoRequest(c, info, body)
		require.ErrorContains(t, err, "metadata was not retained")
		assert.Equal(t, before, body.Bytes())
		assert.True(t, info.ConversionDiagnosticsTruncated())
	})
	t.Run("selected binding changed before dispatch", func(t *testing.T) {
		adaptor, c, info, request := pipioClaudeResponsesFixture(t, "claude-opus-5", false)
		converted, err := adaptor.ConvertOpenAIResponsesRequest(c, info, request)
		require.NoError(t, err)
		common.SetContextKey(c, constant.ContextKeyChannelGroup, "other-group")
		body := bytes.NewBuffer(mustAdvancedCustomRawMessage(t, converted))
		before := bytes.Clone(body.Bytes())
		_, err = adaptor.DoRequest(c, info, body)
		require.ErrorContains(t, err, "binding changed")
		assert.Equal(t, before, body.Bytes())
		assert.Empty(t, info.ConversionDiagnostics())
	})
}

func pipioClaudeResponsesFixture(t *testing.T, model string, stream bool) (*Adaptor, *gin.Context, *relaycommon.RelayInfo, dto.OpenAIResponsesRequest) {
	t.Helper()
	info := advancedCustomRelayInfo(&dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{{IncomingPath: "/v1/responses", UpstreamPath: "/v1/chat/completions", Converter: relayconvert.ConverterOpenAIResponsesToOpenAIChat, Models: []string{model}}}})
	info.RelayFormat = types.RelayFormatOpenAIResponses
	info.RelayMode = relayconstant.RelayModeResponses
	info.OriginModelName, info.UpstreamModelName = model, model
	info.RequestURLPath = "/v1/responses"
	info.ChannelBaseUrl = "http://ai-upstream-pipio:8080"
	info.SupportStreamOptions, info.IsStream, info.DisablePing = true, stream, true
	c := advancedCustomGinContext("/v1/responses")
	common.SetContextKey(c, constant.ContextKeyChannelName, "panstar-pipio-claude")
	common.SetContextKey(c, constant.ContextKeyChannelGroup, "panstar_pipio_claude")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	request := dto.OpenAIResponsesRequest{Model: model, Input: mustAdvancedCustomRawMessage(t, "Reply exactly OK"), MaxOutputTokens: lo.ToPtr(uint(32)), Stream: lo.ToPtr(stream), Temperature: lo.ToPtr(float64(0))}
	c.Request.Body = io.NopCloser(bytes.NewReader(mustAdvancedCustomRawMessage(t, request)))
	return &Adaptor{}, c, info, request
}

type advancedCustomRoundTripFunc func(*http.Request) (*http.Response, error)

func (f advancedCustomRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func advancedCustomRelayInfo(config *dto.AdvancedCustomConfig) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatOpenAI,
		RelayMode:       relayconstant.RelayModeChatCompletions,
		RequestURLPath:  "/v1/chat/completions",
		OriginModelName: "gpt-test",
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiKey:            "sk-test",
			ChannelBaseUrl:    "https://fallback.example",
			ChannelType:       constant.ChannelTypeAdvancedCustom,
			UpstreamModelName: "gpt-test",
			ChannelOtherSettings: dto.ChannelOtherSettings{
				AdvancedCustom: config,
			},
		},
	}
}

func advancedCustomGinContext(path string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func mustAdvancedCustomRawMessage(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := common.Marshal(value)
	require.NoError(t, err)
	return raw
}
