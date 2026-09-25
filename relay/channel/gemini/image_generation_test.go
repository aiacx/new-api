package gemini

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
)

func TestConvertGeminiNativeImageRequest(t *testing.T) {
	n := uint(1)
	request := dto.ImageRequest{
		Model:   "gemini-3.1-flash-image-preview",
		Prompt:  "a blue circle",
		N:       &n,
		Size:    "1024x1536",
		Quality: "high",
	}

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.1-flash-image-preview"},
	}, request)

	require.NoError(t, err)
	geminiRequest, ok := converted.(*dto.GeminiChatRequest)
	require.True(t, ok)
	require.Equal(t, []string{"IMAGE"}, geminiRequest.GenerationConfig.ResponseModalities)
	require.Equal(t, "a blue circle", geminiRequest.Contents[0].Parts[0].Text)
	var imageConfig map[string]string
	require.NoError(t, json.Unmarshal(geminiRequest.GenerationConfig.ImageConfig, &imageConfig))
	require.Equal(t, "2:3", imageConfig["aspectRatio"])
	require.Equal(t, "2K", imageConfig["imageSize"])
}

func TestConvertGeminiNativeImageRequestRejectsMultipleImages(t *testing.T) {
	n := uint(2)
	_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-3.1-flash-image-preview"},
	}, dto.ImageRequest{Prompt: "two images", N: &n})

	require.ErrorContains(t, err, "requires n=1")
}

func TestConvertGeminiNativeImageResponse(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("image-bytes"))
	response := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		Content: dto.GeminiChatContent{Parts: []dto.GeminiPart{{
			InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: encoded},
		}}},
	}}}

	converted, err := convertGeminiNativeImageResponse(response)

	require.NoError(t, err)
	require.Len(t, converted.Data, 1)
	require.Equal(t, encoded, converted.Data[0].B64Json)
}

func TestConvertGeminiNativeImageResponseRejectsMissingImage(t *testing.T) {
	_, err := convertGeminiNativeImageResponse(&dto.GeminiChatResponse{})

	require.ErrorContains(t, err, "did not contain an image")
}
