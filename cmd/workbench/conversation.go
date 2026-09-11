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
	appendArray := func(value any) {
		switch v := value.(type) {
		case []any:
			items = append(items, v...)
		case string:
			items = append(items, map[string]any{"role": "user", "content": v})
		}
	}
	for _, m := range path {
		if m.Role == "assistant" && m.Status != "complete" && m.Text == "" {
			continue
		}
		var req, out map[string]any
		_ = json.Unmarshal(m.Request, &req)
		_ = json.Unmarshal(m.Output, &out)
		same := m.Role == "user" || (m.ActualModel == model && m.Status == "complete")
		switch operation {
		case "responses.create":
			if m.Role == "user" && same && req["input"] != nil {
				appendArray(req["input"])
			} else if m.Role == "assistant" && same && out["output"] != nil {
				appendArray(out["output"])
			} else {
				items = append(items, map[string]any{"role": m.Role, "content": m.Text})
			}
		case "chat.create", "messages.create":
			if m.Role == "user" && same && req["messages"] != nil {
				if msgs, ok := req["messages"].([]any); ok {
					for _, msg := range msgs {
						if entry, ok := msg.(map[string]any); ok && entry["role"] == "user" {
							items = append(items, msg)
						}
					}
				}
			} else if operation == "messages.create" && m.Role == "assistant" && same && out["content"] != nil {
				items = append(items, map[string]any{"role": "assistant", "content": out["content"]})
			} else {
				items = append(items, map[string]any{"role": m.Role, "content": m.Text})
			}
		case "content.generate":
			if m.Role == "user" && same && req["contents"] != nil {
				appendArray(req["contents"])
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
			if text, ok := x["text"].(string); ok {
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
		ProviderID   string          `json:"provider_id"`
		Operation    string          `json:"operation"`
		Params       json.RawMessage `json:"params"`
		ParentID     string          `json:"parent_id"`
		ExpectedHead string          `json:"expected_head"`
		Revision     int64           `json:"revision"`
		Text         string          `json:"text"`
		SaveResponse *bool           `json:"save_response,omitempty"`
		Stream       bool            `json:"stream"`
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
	if r.PathValue("id") == "new" && r.URL.Query().Get("preview") == "1" {
		params, err := conversationRequest(in.Operation, in.Params, nil)
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
	params, err := conversationRequest(in.Operation, in.Params, path)
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
	user := message{ID: newID(), ParentID: in.ParentID, Role: "user", Text: in.Text, Status: "complete", ProviderID: p.ID, ActualModel: model, Protocol: in.Operation, Request: in.Params, CreatedAt: now()}
	assistant := message{ID: newID(), ParentID: user.ID, Role: "assistant", Status: "pending", ProviderID: p.ID, ActualModel: model, Protocol: in.Operation, CreatedAt: now()}
	v.Messages = append(v.Messages, user, assistant)
	v.HeadID = assistant.ID
	if err = a.store.persistSession(e, v); err != nil {
		e.mu.Unlock()
		apiError(w, 500, err)
		return
	}
	e.busy = true
	e.mu.Unlock()
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
	e.mu.Lock()
	defer e.mu.Unlock()
	defer func() { e.busy = false }()
	v = clone(e.data)
	last := &v.Messages[len(v.Messages)-1]
	last.Status = "complete"
	encoded, encodeErr := json.Marshal(result)
	if encodeErr != nil {
		callErr = errors.Join(callErr, fmt.Errorf("保存原生结果失败: %w", encodeErr))
	} else {
		last.Output = encoded
	}
	last.Text = nativeText(result)
	if callErr != nil {
		last.Status = "error"
		if errors.Is(callErr, context.Canceled) {
			last.Status = "cancelled"
		}
		last.Error = callErr.Error()
	}
	if err = a.store.persistSession(e, v); err != nil {
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
