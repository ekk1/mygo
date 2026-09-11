package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConversationStreamRetainsNativeToolsAndPartialText(t *testing.T) {
	cases := []struct{ kind, op, events, want string }{
		{"openai", "responses.create", `{"events":[{"type":"response.created","data":{"response":{"output":[]}}},{"type":"response.output_text.delta","data":{"delta":"partial"}}],"partial":true}`, "partial"},
		{"openai", "responses.create", `{"events":[{"type":"response.completed","data":{"response":{"output":[{"type":"code_interpreter_call","id":"tool-1"}]}}}]}`, "tool-1"},
		{"anthropic", "messages.create", `{"events":[{"type":"content_block_start","data":{"index":0,"content_block":{"type":"thinking","thinking":""}}},{"type":"content_block_delta","data":{"index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}},{"type":"content_block_delta","data":{"index":0,"delta":{"type":"signature_delta","signature":"sig"}}}]}`, "sig"},
		{"gemini", "content.generate", `{"events":[{"data":{"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]}}]}} ,{"data":{"candidates":[{"content":{"role":"model","parts":[{"text":"two","thoughtSignature":"signed"}]}}]}}]}`, "signed"},
	}
	for _, tc := range cases {
		var value any
		json.Unmarshal([]byte(tc.events), &value)
		got := reduceConversationStream(tc.kind, tc.op, value)
		raw, _ := json.Marshal(got)
		if !strings.Contains(string(raw), tc.want) {
			t.Fatalf("%s lost native data: %s", tc.op, raw)
		}
	}
}
