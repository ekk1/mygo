package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func conversationStreamBody(body json.RawMessage, stream bool) json.RawMessage {
	if !stream {
		return body
	}
	var fields map[string]any
	_ = json.Unmarshal(body, &fields)
	fields["stream"] = true
	result, _ := json.Marshal(fields)
	return result
}

func writeConversationEvent(w http.ResponseWriter, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = w.Write(append(data, '\n')); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// reduceConversationStream preserves native content for the next turn, and
// retains raw events when the upstream terminates before a complete response.
func reduceConversationStream(kind, operation string, value any) any {
	raw, _ := json.Marshal(value)
	var envelope struct {
		Events  []resourceEvent `json:"events"`
		Partial bool            `json:"partial"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Events == nil {
		return value
	}
	nativeEventsRaw, _ := json.Marshal(envelope.Events)
	out := map[string]any{}
	blocks := map[int]map[string]any{}
	fragments := map[int]string{}
	choices := map[int]map[string]any{}
	chatToolCalls := map[int]map[int]map[string]any{}
	parts := []any{}
	var responseText strings.Builder
	index := func(v any) int {
		if n, ok := v.(float64); ok {
			return int(n)
		}
		return 0
	}
	for _, event := range envelope.Events {
		data, _ := event.Data.(map[string]any)
		if data == nil {
			continue
		}
		switch operation {
		case "responses.create":
			if text, ok := data["delta"].(string); ok && event.Type == "response.output_text.delta" {
				responseText.WriteString(text)
			}
			if response, ok := data["response"].(map[string]any); ok {
				out = response
			}
		case "messages.create":
			switch event.Type {
			case "message_start":
				if message, ok := data["message"].(map[string]any); ok {
					out = message
				}
			case "content_block_start":
				if block, ok := data["content_block"].(map[string]any); ok {
					blocks[index(data["index"])] = block
				}
			case "content_block_delta":
				i := index(data["index"])
				block := blocks[i]
				if block == nil {
					block = map[string]any{}
					blocks[i] = block
				}
				delta, _ := data["delta"].(map[string]any)
				for _, key := range []string{"text", "thinking", "signature"} {
					if text, ok := delta[key].(string); ok {
						before, _ := block[key].(string)
						block[key] = before + text
					}
				}
				if text, ok := delta["partial_json"].(string); ok {
					fragments[i] += text
				}
			case "message_delta":
				if delta, ok := data["delta"].(map[string]any); ok {
					for k, v := range delta {
						out[k] = v
					}
				}
				if usage, ok := data["usage"].(map[string]any); ok {
					old, _ := out["usage"].(map[string]any)
					if old == nil {
						old = map[string]any{}
					}
					for k, v := range usage {
						old[k] = v
					}
					out["usage"] = old
				}
			}
		case "content.generate":
			if list, ok := data["candidates"].([]any); ok && len(list) > 0 {
				first, _ := list[0].(map[string]any)
				content, _ := first["content"].(map[string]any)
				if p, ok := content["parts"].([]any); ok {
					parts = append(parts, p...)
				}
			}
			if usage := data["usageMetadata"]; usage != nil {
				out["usageMetadata"] = usage
			}
		case "chat.create":
			if list, ok := data["choices"].([]any); ok {
				for _, item := range list {
					choice, _ := item.(map[string]any)
					i := index(choice["index"])
					if choices[i] == nil {
						choices[i] = map[string]any{"index": i, "message": map[string]any{"role": "assistant", "content": ""}}
					}
					message := choices[i]["message"].(map[string]any)
					delta, _ := choice["delta"].(map[string]any)
					for _, key := range []string{"content", "reasoning_content", "refusal"} {
						if text, ok := delta[key].(string); ok {
							before, _ := message[key].(string)
							message[key] = before + text
						}
					}
					if list, ok := delta["tool_calls"].([]any); ok {
						if chatToolCalls[i] == nil {
							chatToolCalls[i] = map[int]map[string]any{}
						}
						for _, rawCall := range list {
							call, _ := rawCall.(map[string]any)
							callIndex := index(call["index"])
							if chatToolCalls[i][callIndex] == nil {
								chatToolCalls[i][callIndex] = map[string]any{}
							}
							mergeChatCallDelta(chatToolCalls[i][callIndex], call)
						}
					}
					if call, ok := delta["function_call"].(map[string]any); ok {
						current, _ := message["function_call"].(map[string]any)
						if current == nil {
							current = map[string]any{}
							message["function_call"] = current
						}
						mergeChatCallDelta(current, call)
					}
					if reason := choice["finish_reason"]; reason != nil {
						choices[i]["finish_reason"] = reason
					}
				}
			}
			if usage := data["usage"]; usage != nil {
				out["usage"] = usage
			}
		}
	}
	switch operation {
	case "responses.create":
		if nativeText(out) == "" && responseText.Len() > 0 {
			out["output_text"] = responseText.String()
		}
	case "messages.create":
		ids := make([]int, 0, len(blocks))
		for i := range blocks {
			ids = append(ids, i)
		}
		sort.Ints(ids)
		content := []any{}
		for _, i := range ids {
			if fragment := fragments[i]; fragment != "" {
				var input any
				if json.Unmarshal([]byte(fragment), &input) == nil {
					blocks[i]["input"] = input
				}
			}
			content = append(content, blocks[i])
		}
		out["content"] = content
		out["role"] = "assistant"
	case "content.generate":
		out["candidates"] = []any{map[string]any{"content": map[string]any{"role": "model", "parts": parts}}}
	case "chat.create":
		ids := make([]int, 0, len(choices))
		for i := range choices {
			ids = append(ids, i)
		}
		sort.Ints(ids)
		list := []any{}
		for _, i := range ids {
			if calls := chatToolCalls[i]; len(calls) > 0 {
				callIDs := make([]int, 0, len(calls))
				for callID := range calls {
					callIDs = append(callIDs, callID)
				}
				sort.Ints(callIDs)
				ordered := make([]any, 0, len(callIDs))
				for _, callID := range callIDs {
					ordered = append(ordered, calls[callID])
				}
				choices[i]["message"].(map[string]any)["tool_calls"] = ordered
			}
			list = append(list, choices[i])
		}
		out["choices"] = list
	}
	out["native_events"] = json.RawMessage(nativeEventsRaw)
	if envelope.Partial {
		out["partial"] = true
	}
	_ = kind
	return out
}

func mergeChatCallDelta(destination, delta map[string]any) {
	for key, value := range delta {
		if key == "index" {
			continue
		}
		if nested, ok := value.(map[string]any); ok {
			current, _ := destination[key].(map[string]any)
			if current == nil {
				current = map[string]any{}
				destination[key] = current
			}
			mergeChatCallDelta(current, nested)
			continue
		}
		if fragment, ok := value.(string); ok && key != "id" && key != "type" {
			before, _ := destination[key].(string)
			destination[key] = before + fragment
			continue
		}
		destination[key] = value
	}
}

func validateConversationOperation(kind, operation string) error {
	valid := false
	switch kind {
	case "openai", "compatible", "xai":
		valid = operation == "responses.create" || operation == "chat.create"
	case "anthropic":
		valid = operation == "messages.create"
	case "gemini":
		valid = operation == "content.generate"
	}
	if !valid {
		return fmt.Errorf("此服务商不支持文字协议 %q", operation)
	}
	return nil
}
