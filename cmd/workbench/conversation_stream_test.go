package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConversationStreamRetainsNativeToolsAndPartialText(t *testing.T) {
	cases := []struct{ kind, op, events, want string }{
		{"openai", "responses.create", `{"events":[{"type":"response.created","data":{"response":{"output":[]}}},{"type":"response.output_text.delta","data":{"delta":"partial"}}],"partial":true}`, "partial"},
		{"openai", "responses.create", `{"events":[{"type":"response.completed","data":{"response":{"output":[{"type":"reasoning","id":"rs_1","encrypted_content":"encrypted-reasoning"},{"type":"image_generation_call","id":"ig_1","result":"image-result"},{"type":"code_interpreter_call","id":"tool-1"}]}}}]}`, "encrypted-reasoning"},
		{"anthropic", "messages.create", `{"events":[{"type":"content_block_start","data":{"index":0,"content_block":{"type":"thinking","thinking":""}}},{"type":"content_block_delta","data":{"index":0,"delta":{"type":"thinking_delta","thinking":"reason"}}},{"type":"content_block_delta","data":{"index":0,"delta":{"type":"signature_delta","signature":"sig"}}}]}`, "sig"},
		{"anthropic", "messages.create", `{"events":[{"type":"content_block_start","data":{"index":0,"content_block":{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"q"}}}},{"type":"content_block_start","data":{"index":1,"content_block":{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","encrypted_content":"encrypted-result"}]}}}]}`, "encrypted-result"},
		{"gemini", "content.generate", `{"events":[{"data":{"candidates":[{"content":{"role":"model","parts":[{"text":"one"}]}}]}} ,{"data":{"candidates":[{"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"image-data"},"thoughtSignature":"image-signature"}]}}]}}]}`, "image-signature"},
		{"xai", "chat.create", `{"events":[{"data":{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"reason","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}},{"data":{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]},"finish_reason":"tool_calls"}]}}]}`, `"arguments":"{\"q\":1}"`},
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
