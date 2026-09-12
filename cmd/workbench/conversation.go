package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// conversationRequest retains each protocol's native history within one profile.
func conversationRequest(operation string, raw json.RawMessage, path []message) (json.RawMessage, error) {
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil || body == nil {
		return nil, fmt.Errorf("请求体必须为 JSON 对象")
	}
	if body["stream"] == true {
		return nil, fmt.Errorf("此对话入口接收完整结果，请勿在扩展参数中开启 stream")
	}
	model, _ := body["model"].(string)
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("模型不能为空")
	}
	var items []any
	if operation == "responses.create" && (strings.TrimSpace(stringValue(body["previous_response_id"])) != "" || body["conversation"] != nil) {
		path = nil
	}
	appendArray := func(value any) {
		switch v := value.(type) {
		case []any:
			items = append(items, v...)
		case string:
			items = append(items, map[string]any{"role": "user", "content": v})
		}
	}
	for _, m := range path {
		if m.Role == "assistant" && m.Text == "" && (m.Status != "complete" || m.ActualModel != model) {
			continue
		}
		var req, out map[string]any
		_ = json.Unmarshal(m.Request, &req)
		_ = json.Unmarshal(m.Output, &out)
		sameModel := m.ActualModel == model
		same := m.Role == "user" || (sameModel && m.Status == "complete")
		switch operation {
		case "responses.create":
			if m.Role == "user" && same && req["input"] != nil {
				appendResponseHistory(&items, req["input"], sameModel)
			} else if m.Role == "assistant" && same && out["output"] != nil {
				appendArray(out["output"])
			} else {
				items = append(items, map[string]any{"role": m.Role, "content": m.Text})
			}
		case "chat.create":
			if m.Role == "user" && same && req["messages"] != nil {
				if msgs, ok := req["messages"].([]any); ok {
					for _, msg := range msgs {
						if entry, ok := msg.(map[string]any); ok {
							role, _ := entry["role"].(string)
							if role == "user" || ((role == "tool" || role == "function") && sameModel) {
								items = append(items, msg)
							}
						}
					}
				}
			} else if m.Role == "assistant" && same {
				if assistant := firstChatAssistantMessage(out); assistant != nil {
					items = append(items, assistant)
				} else {
					items = append(items, map[string]any{"role": m.Role, "content": m.Text})
				}
			} else {
				items = append(items, map[string]any{"role": m.Role, "content": m.Text})
			}
		case "messages.create":
			if m.Role == "user" && same && req["messages"] != nil {
				if msgs, ok := req["messages"].([]any); ok {
					for _, msg := range msgs {
						if entry, ok := msg.(map[string]any); ok && entry["role"] == "user" {
							if filtered := anthropicUserHistory(entry, sameModel); filtered != nil {
								items = append(items, filtered)
							}
						}
					}
				}
			} else if m.Role == "assistant" && same && out["content"] != nil {
				items = append(items, map[string]any{"role": "assistant", "content": out["content"]})
			} else {
				items = append(items, map[string]any{"role": m.Role, "content": m.Text})
			}
		case "content.generate":
			if m.Role == "user" && same && req["contents"] != nil {
				appendGeminiHistory(&items, req["contents"], sameModel)
			} else if m.Role == "assistant" && same && out["candidates"] != nil {
				if candidates, ok := out["candidates"].([]any); ok && len(candidates) > 0 {
					if c, ok := candidates[0].(map[string]any); ok && c["content"] != nil {
						items = append(items, c["content"])
						continue
					}
				}
				items = append(items, map[string]any{"role": "model", "parts": []any{map[string]any{"text": m.Text}}})
			} else {
				role := m.Role
				if role == "assistant" {
					role = "model"
				}
				items = append(items, map[string]any{"role": role, "parts": []any{map[string]any{"text": m.Text}}})
			}
		default:
			return nil, fmt.Errorf("不支持的文字协议")
		}
	}
	switch operation {
	case "responses.create":
		appendArray(body["input"])
		body["input"] = items
	case "chat.create", "messages.create":
		var system []any
		if current, ok := body["messages"].([]any); ok {
			for _, item := range current {
				entry, _ := item.(map[string]any)
				if entry["role"] == "system" || entry["role"] == "developer" {
					system = append(system, item)
				} else {
					items = append(items, item)
				}
			}
		} else {
			return nil, fmt.Errorf("messages 必须为数组")
		}
		body["messages"] = append(system, items...)
	case "content.generate":
		if _, ok := body["contents"].([]any); !ok {
			return nil, fmt.Errorf("contents 必须为数组")
		}
		appendArray(body["contents"])
		body["contents"] = items
	default:
		return nil, fmt.Errorf("不支持的文字协议")
	}
	return json.Marshal(body)
}

func conversationProviderRequest(kind, operation string, raw json.RawMessage, path []message) (json.RawMessage, error) {
	result, err := conversationRequest(operation, raw, path)
	if err != nil || kind != "xai" || operation != "responses.create" {
		return result, err
	}
	var body map[string]any
	if err := json.Unmarshal(result, &body); err != nil {
		return nil, err
	}
	include, ok := body["include"].([]any)
	if body["include"] != nil && !ok {
		return nil, fmt.Errorf("include 必须为数组")
	}
	for _, item := range include {
		if item == "reasoning.encrypted_content" {
			return result, nil
		}
	}
	body["include"] = append(include, "reasoning.encrypted_content")
	return json.Marshal(body)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func appendResponseHistory(items *[]any, value any, sameModel bool) {
	if text, ok := value.(string); ok {
		*items = append(*items, map[string]any{"role": "user", "content": text})
		return
	}
	list, _ := value.([]any)
	for _, item := range list {
		entry, _ := item.(map[string]any)
		typeName, _ := entry["type"].(string)
		if !sameModel && (strings.HasSuffix(typeName, "_call_output") || typeName == "mcp_approval_response") {
			continue
		}
		*items = append(*items, item)
	}
}

func anthropicUserHistory(message map[string]any, sameModel bool) map[string]any {
	if sameModel {
		return message
	}
	content, ok := message["content"].([]any)
	if !ok {
		return message
	}
	filtered := make([]any, 0, len(content))
	for _, item := range content {
		block, _ := item.(map[string]any)
		if block["type"] == "tool_result" {
			continue
		}
		filtered = append(filtered, item)
	}
	if len(filtered) == 0 {
		return nil
	}
	copy := make(map[string]any, len(message))
	for key, value := range message {
		copy[key] = value
	}
	copy["content"] = filtered
	return copy
}

func appendGeminiHistory(items *[]any, value any, sameModel bool) {
	list, _ := value.([]any)
	for _, item := range list {
		content, _ := item.(map[string]any)
		parts, ok := content["parts"].([]any)
		if sameModel || !ok {
			*items = append(*items, item)
			continue
		}
		filtered := make([]any, 0, len(parts))
		for _, part := range parts {
			block, _ := part.(map[string]any)
			if block["functionResponse"] != nil || block["toolResponse"] != nil {
				continue
			}
			filtered = append(filtered, part)
		}
		if len(filtered) == 0 {
			continue
		}
		copy := make(map[string]any, len(content))
		for key, field := range content {
			copy[key] = field
		}
		copy["parts"] = filtered
		*items = append(*items, copy)
	}
}

func firstChatAssistantMessage(output map[string]any) map[string]any {
	choices, _ := output["choices"].([]any)
	if len(choices) == 0 {
		return nil
	}
	choice, _ := choices[0].(map[string]any)
	message, _ := choice["message"].(map[string]any)
	if message == nil {
		return nil
	}
	assistant := map[string]any{"role": "assistant"}
	for _, key := range []string{"content", "name", "refusal", "tool_calls", "function_call", "reasoning_content"} {
		if value, ok := message[key]; ok {
			assistant[key] = value
		}
	}
	if audio, ok := message["audio"].(map[string]any); ok {
		if id, _ := audio["id"].(string); id != "" {
			assistant["audio"] = map[string]any{"id": id}
		}
	}
	return assistant
}

func nativeText(value any) string {
	body, _ := value.(map[string]any)
	var texts []string
	if text, ok := body["output_text"].(string); ok {
		texts = append(texts, text)
	}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				visit(item)
			}
		case map[string]any:
			if text, ok := x["text"].(string); ok && x["thought"] != true {
				texts = append(texts, text)
			}
			if role, ok := x["role"].(string); ok && role == "assistant" {
				if text, ok := x["content"].(string); ok {
					texts = append(texts, text)
				}
			}
			for _, key := range []string{"output", "content", "choices", "message", "candidates", "parts"} {
				if child, ok := x[key]; ok {
					visit(child)
				}
			}
		}
	}
	visit(body)
	return strings.Join(texts, "\n")
}
func (a *app) sendConversation(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProviderID   string              `json:"provider_id"`
		Operation    string              `json:"operation"`
		Params       json.RawMessage     `json:"params"`
		ParentID     string              `json:"parent_id"`
		ExpectedHead string              `json:"expected_head"`
		Revision     int64               `json:"revision"`
		Text         string              `json:"text"`
		SaveResponse *bool               `json:"save_response,omitempty"`
		Stream       bool                `json:"stream"`
		Background   bool                `json:"background"`
		Feature      string              `json:"feature,omitempty"`
		AssetIDs     map[string][]string `json:"asset_ids,omitempty"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apiError(w, 400, err)
		return
	}
	cfg := a.store.configSnapshot(false)
	if in.Revision != cfg.Revision {
		apiError(w, 409, errConflict)
		return
	}
	var p provider
	for _, item := range cfg.Providers {
		if item.ID == in.ProviderID {
			p = item
			break
		}
	}
	if p.ID == "" {
		apiError(w, 404, errNotFound)
		return
	}
	if err := validateConversationOperation(p.Kind, in.Operation); err != nil {
		apiError(w, 400, err)
		return
	}
	resolved, _, assetCleanup, err := a.resolveAssets(p.Kind, in.Operation, in.Params, nil, in.AssetIDs)
	defer assetCleanup()
	if err != nil {
		apiError(w, 400, err)
		return
	}
	in.Params = resolved
	if in.SaveResponse == nil {
		in.SaveResponse = &cfg.SaveResponses
	}
	if r.PathValue("id") == "new" && r.URL.Query().Get("preview") == "1" {
		params, err := conversationProviderRequest(p.Kind, in.Operation, in.Params, nil)
		if err != nil {
			apiError(w, 400, err)
			return
		}
		result, err := nativePreview(p, in.Operation, conversationStreamBody(params, in.Stream), nil)
		if err != nil {
			apiError(w, 400, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	e, err := a.store.entry(r.PathValue("id"))
	if err != nil {
		apiError(w, 404, err)
		return
	}
	e.mu.Lock()
	if e.busy {
		e.mu.Unlock()
		apiError(w, 409, errBusy)
		return
	}
	v := clone(e.data)
	if v.ProfileID != p.ID || v.Operation != in.Operation {
		e.mu.Unlock()
		apiError(w, 400, fmt.Errorf("会话与 profile 或协议不匹配，请新建会话"))
		return
	}
	if v.HeadID != in.ExpectedHead {
		e.mu.Unlock()
		apiError(w, 409, errConflict)
		return
	}
	path, err := ancestors(v, in.ParentID)
	if err != nil {
		e.mu.Unlock()
		apiError(w, 400, err)
		return
	}
	params, err := conversationProviderRequest(p.Kind, in.Operation, in.Params, path)
	if err != nil {
		e.mu.Unlock()
		apiError(w, 400, err)
		return
	}
	params = conversationStreamBody(params, in.Stream)
	if r.URL.Query().Get("preview") == "1" {
		e.mu.Unlock()
		preview, err := nativePreview(p, in.Operation, params, nil)
		if err != nil {
			apiError(w, 400, err)
			return
		}
		writeJSON(w, 200, preview)
		return
	}
	var raw map[string]any
	_ = json.Unmarshal(in.Params, &raw)
	model, _ := raw["model"].(string)
	user := message{ID: newID(), ParentID: in.ParentID, Role: "user", Text: in.Text, Status: "complete", ProviderID: p.ID, ActualModel: model, Protocol: in.Operation, Request: in.Params, CreatedAt: now(), AssetIDs: flattenAssetIDs(in.AssetIDs)}
	assistant := message{ID: newID(), ParentID: user.ID, Role: "assistant", Status: "pending", ProviderID: p.ID, ActualModel: model, Protocol: in.Operation, CreatedAt: now()}
	var task *taskEntry
	var taskCtx context.Context
	if in.Background {
		task, taskCtx, err = a.reserveTask(p, in.Operation, "chat", in.Text, v.ID, in.Stream)
		if err != nil {
			e.mu.Unlock()
			apiError(w, 500, err)
			return
		}
		assistant.TaskID = task.data.ID
	}
	v.Messages = append(v.Messages, user, assistant)
	v.HeadID = assistant.ID
	if err = a.store.persistSession(e, v); err != nil {
		e.mu.Unlock()
		if task != nil {
			go a.runGeneration(task, taskCtx, nativeGeneration{startupErr: err})
		}
		apiError(w, 500, err)
		return
	}
	e.busy = true
	e.mu.Unlock()
	if task != nil {
		go a.runGeneration(task, taskCtx, nativeGeneration{provider: p, operation: in.Operation, params: params, saveResponse: in.SaveResponse, stream: in.Stream, session: e, assistantID: assistant.ID, inputAssetIDs: user.AssetIDs})
		a.respondTask(w, r, task, &v)
		return
	}
	ctx := r.Context()
	if in.Stream {
		w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
		ctx = withNativeEventSink(ctx, func(event resourceEvent) error {
			return writeConversationEvent(w, map[string]any{"type": "event", "event": event})
		})
	}
	result, callErr := a.executeNative(ctx, p, in.Operation, params, nil, in.SaveResponse)
	if in.Stream {
		result = reduceConversationStream(p.Kind, in.Operation, result)
	}
	assetIDs, assetErr := a.captureAssets(ctx, p, in.Operation, "", v.ID, result)
	callErr = errors.Join(callErr, assetErr)
	status := "complete"
	if callErr != nil {
		status = "error"
		if errors.Is(callErr, context.Canceled) {
			status = "cancelled"
		}
	}
	if v, err = a.finishConversation(e, assistant.ID, result, assetIDs, status, callErr); err != nil {
		if in.Stream {
			_ = writeConversationEvent(w, map[string]any{"type": "error", "error": err.Error()})
		} else {
			apiError(w, 500, err)
		}
		return
	}
	if in.Stream {
		if callErr != nil {
			_ = writeConversationEvent(w, map[string]any{"type": "error", "error": callErr.Error()})
			return
		}
		_ = writeConversationEvent(w, map[string]any{"type": "done", "session": v})
		return
	}
	if callErr != nil {
		apiError(w, 502, callErr)
		return
	}
	writeJSON(w, 200, v)
}

// Keep the busy flag until the final save finishes. Failed saves are surfaced to
// the task or request and never expose an unpersisted session in memory.
func (a *app) finishConversation(e *sessionEntry, assistantID string, result any, assetIDs []string, status string, callErr error) (session, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() { e.busy = false }()
	v := clone(e.data)
	var last *message
	for i := range v.Messages {
		if v.Messages[i].ID == assistantID {
			last = &v.Messages[i]
			break
		}
	}
	if last == nil {
		return session{}, errNotFound
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return session{}, fmt.Errorf("保存原生结果失败: %w", err)
	}
	last.Output, last.Text, last.Status, last.AssetIDs = encoded, nativeText(result), status, assetIDs
	if callErr != nil {
		last.Error = callErr.Error()
	}
	if err := a.store.persistSession(e, v); err != nil {
		return session{}, err
	}
	return clone(e.data), nil
}
