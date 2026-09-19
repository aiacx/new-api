package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPanstarRawResponsesStreamPreservesEveryFrame(t *testing.T) {
	raw := "event: response.created\ndata: {\"type\":\"response.created\",\"unknown\":{\"opaque\":\"value\"}}\n\n" +
		": provider comment\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":3}}}\n\n" +
		"data: [DONE]\n\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{}
	resp := &http.Response{Body: io.NopCloser(strings.NewReader(raw)), Header: http.Header{}}
	usage, apiErr := OaiResponsesRawStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, raw, recorder.Body.String())
	require.NotNil(t, info.StreamStatus)
}

type interruptedSSEReader struct{ sent bool }

func (r *interruptedSSEReader) Read(buffer []byte) (int, error) {
	if r.sent {
		return 0, errors.New("synthetic upstream disconnect")
	}
	r.sent = true
	return copy(buffer, "event: response.created\ndata: {}\n\n"), nil
}

func TestPanstarRawResponsesStreamNeverForgesTerminalOnDisconnect(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	info := &relaycommon.RelayInfo{}
	resp := &http.Response{Body: io.NopCloser(&interruptedSSEReader{}), Header: http.Header{}}
	usage, apiErr := OaiResponsesRawStreamHandler(c, info, resp)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.Equal(t, "event: response.created\ndata: {}\n\n", recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "response.completed")
	require.NotContains(t, recorder.Body.String(), "response.failed")
}
