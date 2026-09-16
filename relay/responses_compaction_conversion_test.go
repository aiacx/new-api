package relay

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
)

func TestResponsesRequestFromCompactionPreservesCodexFields(t *testing.T) {
	tools := json.RawMessage(`[{"type":"function","name":"read_file","parameters":{"type":"object"}}]`)
	reasoning := &dto.Reasoning{Effort: "low"}
	text := json.RawMessage(`{"verbosity":"low"}`)
	req := responsesRequestFromCompaction(&dto.OpenAIResponsesCompactionRequest{
		Model: "gpt-6-astra", Input: json.RawMessage(`[{"type":"compaction","id":"cmp_1"}]`),
		Tools: tools, Reasoning: reasoning, Text: text, ParallelToolCalls: json.RawMessage(`true`),
	})

	encoded, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"tools", "reasoning", "text", "parallel_tool_calls"} {
		if len(got[field]) == 0 {
			t.Fatalf("compact field %q was dropped", field)
		}
	}
}
