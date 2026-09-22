package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesStreamCanaryRequiresTerminalCompletedEvent(t *testing.T) {
	deltaOnly := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n")
	require.ErrorContains(t, validateTestResponseBody(deltaOnly, true, true),
		"response.completed")
	require.NoError(t, validateTestResponseBody(deltaOnly, true, false))

	completed := []byte("data: {\"type\":\"response.created\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"total_tokens\":2}}}\n\n")
	require.NoError(t, validateTestResponseBody(completed, true, true))
}
