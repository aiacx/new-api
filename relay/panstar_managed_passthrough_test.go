package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPanstarManagedAdvancedCustomKeepsResponsesAndCompactBodies(t *testing.T) {
	for _, test := range []struct {
		path   string
		format types.RelayFormat
	}{
		{path: "/v1/chat/completions", format: types.RelayFormatOpenAI},
		{path: "/v1/responses", format: types.RelayFormatOpenAIResponses},
		{path: "/v1/responses/compact", format: types.RelayFormatOpenAIResponsesCompaction},
	} {
		t.Run(test.path, func(t *testing.T) {
			raw := []byte(`{"model":"gpt-6-astra", "input":[{"type":"additional_tools","role":"developer","tools":[{"type":"custom","name":"exec"}]},{"type":"compaction","encrypted_content":"opaque-synthetic"}], "tools":[{"type":"custom","name":"shell"},{"type":"function","name":"status"}], "instructions":"synthetic", "prompt_cache_key":"a3f0186c36bb3035bfac40f69a6ab44e53f74e69568fb496e54cd55461778b2", "wire_extension":{"version":1}}`)
			if test.path == "/v1/responses" {
				raw = []byte(`{"model":"gpt-6-astra", "input":[{"type":"additional_tools","role":"developer","tools":[{"type":"custom","name":"exec"}]},{"type":"compaction","encrypted_content":"opaque-synthetic"}], "tools":[{"type":"custom","name":"shell"},{"type":"function","name":"status"}], "instructions":"synthetic", "prompt_cache_key":"a3f0186c36bb3035bfac40f69a6ab44e53f74e69568fb496e54cd55461778b2", "context_management":[{"type":"compaction","compact_threshold":1024}], "wire_extension":{"version":1}}`)
			}
			if test.path == "/v1/chat/completions" {
				raw = []byte(`{"model":"gpt-6-astra", "messages":[{"role":"user","content":"synthetic"}], "tools":[{"type":"function","function":{"name":"status","parameters":{"type":"object"}}}]}`)
			}
			captured := make(chan []byte, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, test.path, r.URL.Path)
				require.Equal(t, "Bearer synthetic-managed-key", r.Header.Get("Authorization"))
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				captured <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(raw))
			c.Request.Header.Set("Content-Type", "application/json")
			common.SetContextKey(c, constant.ContextKeyOriginalModel, "gpt-6-astra")
			common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeAdvancedCustom)
			common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, server.URL)
			common.SetContextKey(c, constant.ContextKeyChannelKey, "synthetic-managed-key")
			common.SetContextKey(c, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
				AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
					{IncomingPath: test.path, UpstreamPath: test.path,
						Models: []string{"gpt-6-astra"}, PassThroughBodyEnabled: true},
				}},
			})
			req, err := helper.GetAndValidateRequest(c, test.format)
			require.NoError(t, err)
			info, err := relaycommon.GenRelayInfo(c, test.format, req, nil)
			require.NoError(t, err)
			if test.path == "/v1/chat/completions" {
				info.InitChannelMeta(c)
				adaptor := GetAdaptor(info.ApiType)
				adaptor.Init(info)
				storage, err := common.GetBodyStorage(c)
				require.NoError(t, err)
				body := common.NewReplayableBodyReader(storage)
				response, err := adaptor.DoRequest(c, info, body)
				require.NoError(t, err)
				defer response.(*http.Response).Body.Close()
				require.Equal(t, raw, <-captured)
				return
			}
			var responses *dto.OpenAIResponsesRequest
			switch request := req.(type) {
			case *dto.OpenAIResponsesRequest:
				responses = request
			case *dto.OpenAIResponsesCompactionRequest:
				responses = &dto.OpenAIResponsesRequest{Model: request.Model, Input: request.Input}
			}
			adaptor, body, closer, apiErr := PrepareResponsesRequest(c, info, responses)
			require.Nil(t, apiErr)
			defer closer.Close()
			response, err := adaptor.DoRequest(c, info, body)
			require.NoError(t, err)
			defer response.(*http.Response).Body.Close()
			require.Equal(t, raw, <-captured)
		})
	}
}
