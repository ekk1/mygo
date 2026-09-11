package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ekk1/mygo/utils/openai"
)

type toolSelection struct {
	Web         bool   `json:"web"`
	Image       bool   `json:"image"`
	Exec        bool   `json:"exec"`
	WebType     string `json:"web_type"`
	ImageType   string `json:"image_type"`
	ImageModel  string `json:"image_model"`
	ContainerID string `json:"container_id"`
}
type chatInput struct {
	ParentID     string         `json:"parent_id"`
	ModelID      string         `json:"model_id"`
	Text         string         `json:"text"`
	System       string         `json:"system"`
	Files        []string       `json:"files"`
	Tools        toolSelection  `json:"tools"`
	Options      map[string]any `json:"options"`
	SaveResponse *bool          `json:"save_response"`
}

func buildResponseRequest(in chatInput, path []message, rt route) (openai.ResponseRequest, error) {
	input := []any{}
	for _, m := range path {
		if m.Role == "assistant" && m.Status == "complete" && m.Protocol == "responses" && m.ProviderID == rt.ProviderID && m.ActualModel == rt.Model && len(m.Output) > 0 {
			var items []json.RawMessage
			if err := json.Unmarshal(m.Output, &items); err != nil {
				return openai.ResponseRequest{}, err
			}
			if len(items) > 0 {
				for _, item := range items {
					input = append(input, item)
				}
				continue
			}
		}
		content := []any{map[string]any{"type": "input_text", "text": m.Text}}
		if m.Role == "user" && m.ProviderID == rt.ProviderID {
			for _, id := range m.Files {
				content = append(content, map[string]any{"type": "input_file", "file_id": id})
			}
		}
		if m.Role == "assistant" {
			input = append(input, map[string]any{"role": "assistant", "content": m.Text})
		} else {
			input = append(input, map[string]any{"role": "user", "content": content})
		}
	}
	content := []any{map[string]any{"type": "input_text", "text": in.Text}}
	for _, id := range in.Files {
		content = append(content, map[string]any{"type": "input_file", "file_id": id})
	}
	input = append(input, map[string]any{"role": "user", "content": content})
	req := openai.ResponseRequest{Model: rt.Model, Input: input, Extra: in.Options}
	if in.System != "" {
		req.Instructions = in.System
	}
	if in.Tools.Web {
		req.Tools = append(req.Tools, openai.WebSearch(openai.WebSearchOptions{Type: in.Tools.WebType}))
	}
	if in.Tools.Image {
		req.Tools = append(req.Tools, openai.ImageGeneration(openai.ImageGenerationOptions{Type: in.Tools.ImageType, Model: in.Tools.ImageModel}))
	}
	if in.Tools.Exec {
		var container any
		if in.Tools.ContainerID != "" {
			container = in.Tools.ContainerID
		} else {
			container = openai.AutoContainer{Type: "auto", FileIDs: in.Files}
		}
		req.Tools = append(req.Tools, openai.CodeInterpreter(openai.CodeInterpreterOptions{Container: container}))
	}
	_, err := json.Marshal(req)
	return req, err
}
func buildChatRequest(in chatInput, path []message, rt route) (openai.ChatCompletionRequest, error) {
	if in.Tools.Web || in.Tools.Image || in.Tools.Exec {
		return openai.ChatCompletionRequest{}, fmt.Errorf("这些工具开关用于 Responses；Chat 模型请使用原生参数")
	}
	if len(in.Files) > 0 {
		return openai.ChatCompletionRequest{}, fmt.Errorf("文件 ID 对话请使用 Responses 映射")
	}
	messages := []openai.ChatMessage{}
	if in.System != "" {
		messages = append(messages, openai.ChatMessage{Role: "system", Content: in.System})
	}
	for _, m := range path {
		messages = append(messages, openai.ChatMessage{Role: m.Role, Content: m.Text})
	}
	messages = append(messages, openai.ChatMessage{Role: "user", Content: in.Text})
	req := openai.ChatCompletionRequest{Model: rt.Model, Messages: messages, Extra: in.Options}
	_, err := json.Marshal(req)
	return req, err
}
func (a *app) sendMessage(w http.ResponseWriter, r *http.Request) {
	var in chatInput
	if err := decodeJSON(w, r, &in); err != nil {
		apiError(w, 400, err)
		return
	}
	if strings.TrimSpace(in.Text) == "" {
		apiError(w, 400, fmt.Errorf("消息不能为空"))
		return
	}
	for _, key := range []string{"model", "input", "messages", "stream", "previous_response_id", "conversation"} {
		if _, ok := in.Options[key]; ok {
			apiError(w, 400, fmt.Errorf("参数 %s 由工作台维护", key))
			return
		}
	}
	_, rt, err := a.store.resolveModel(in.ModelID)
	if err != nil {
		apiError(w, 400, err)
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
	path, err := ancestors(v, in.ParentID)
	if err != nil {
		e.mu.Unlock()
		apiError(w, 400, err)
		return
	}
	var responseReq openai.ResponseRequest
	var chatReq openai.ChatCompletionRequest
	if rt.Protocol == "responses" {
		responseReq, err = buildResponseRequest(in, path, rt)
	} else {
		chatReq, err = buildChatRequest(in, path, rt)
	}
	if err != nil {
		e.mu.Unlock()
		apiError(w, 400, err)
		return
	}
	// Reserve session before releasing its lock; configuration and other sessions stay independent.
	e.busy = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.busy = false; e.mu.Unlock() }()
	operation := "responses.stream"
	if rt.Protocol == "chat" {
		operation = "chat.stream"
	}
	client, finish, err := a.providerClient(rt.ProviderID, operation, in.SaveResponse)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	defer client.CloseIdleConnections()
	user := message{ID: newID(), ParentID: in.ParentID, Role: "user", Text: in.Text, Status: "complete", ModelID: in.ModelID, ProviderID: rt.ProviderID, ActualModel: rt.Model, Protocol: rt.Protocol, Files: in.Files, CreatedAt: now()}
	assistant := message{ID: newID(), ParentID: user.ID, Role: "assistant", Status: "pending", ModelID: in.ModelID, ProviderID: rt.ProviderID, ActualModel: rt.Model, Protocol: rt.Protocol, CreatedAt: now()}
	v.Messages = append(v.Messages, user, assistant)
	v.HeadID = assistant.ID
	e.mu.Lock()
	err = a.store.persistSession(e, v)
	v = clone(e.data)
	e.mu.Unlock()
	if err != nil {
		finish(err)
		apiError(w, 500, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(200)
	encoder := json.NewEncoder(w)
	send := func(event any) error {
		if err := encoder.Encode(event); err != nil {
			return err
		}
		return http.NewResponseController(w).Flush()
	}
	sendErr := send(map[string]any{"type": "start", "session": v})
	text := strings.Builder{}
	var output json.RawMessage
	status := "complete"
	var nativeErr error
	callback := func(event openai.Event) error {
		var raw map[string]json.RawMessage
		if string(event.Data) == "[DONE]" {
			return nil
		}
		if err := json.Unmarshal(event.Data, &raw); err != nil {
			return err
		}
		var delta string
		if rt.Protocol == "responses" {
			var typ string
			json.Unmarshal(raw["type"], &typ)
			if typ == "response.output_text.delta" || typ == "response.refusal.delta" {
				json.Unmarshal(raw["delta"], &delta)
			}
			if len(raw["response"]) > 0 {
				var res struct {
					Output            json.RawMessage `json:"output"`
					Status            string          `json:"status"`
					Error             json.RawMessage `json:"error"`
					IncompleteDetails json.RawMessage `json:"incomplete_details"`
				}
				if err := json.Unmarshal(raw["response"], &res); err != nil {
					return err
				}
				output = res.Output
				if typ == "response.incomplete" {
					status = "error"
					nativeErr = fmt.Errorf("生成未完成: %s", res.IncompleteDetails)
				}
			}
		} else {
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
						Refusal string `json:"refusal"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
			}
			if err := json.Unmarshal(event.Data, &chunk); err != nil {
				return err
			}
			if len(chunk.Choices) > 0 {
				delta = chunk.Choices[0].Delta.Content + chunk.Choices[0].Delta.Refusal
				if chunk.Choices[0].FinishReason == "length" || chunk.Choices[0].FinishReason == "content_filter" {
					status = "error"
					nativeErr = fmt.Errorf("生成终止: %s", chunk.Choices[0].FinishReason)
				}
			}
		}
		if delta != "" {
			text.WriteString(delta)
			return send(map[string]any{"type": "delta", "text": delta})
		}
		return nil
	}
	if sendErr != nil {
		err = sendErr
	} else if rt.Protocol == "responses" {
		_, err = client.StreamResponse(r.Context(), responseReq, callback)
	} else {
		_, err = client.StreamChatCompletion(r.Context(), chatReq, callback)
	}
	err = errors.Join(err, nativeErr)
	err = finish(err)
	assistant.Text = text.String()
	assistant.Output = output
	assistant.Status = status
	if assistant.Text == "" && len(output) > 0 {
		var items []struct {
			Content []struct {
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		}
		if json.Unmarshal(output, &items) == nil {
			for _, item := range items {
				for _, part := range item.Content {
					assistant.Text += part.Text + part.Refusal
				}
			}
		}
	}
	if err != nil {
		assistant.Status = "error"
		assistant.Error = err.Error()
		if errors.Is(err, context.Canceled) || r.Context().Err() != nil {
			assistant.Status = "cancelled"
		}
	}
	v.Messages[len(v.Messages)-1] = assistant
	e.mu.Lock()
	saveErr := a.store.persistSession(e, v)
	saved := clone(e.data)
	e.mu.Unlock()
	err = errors.Join(err, saveErr)
	if err != nil {
		send(map[string]any{"type": "error", "error": err.Error(), "session": saved})
		return
	}
	send(map[string]any{"type": "done", "session": saved})
}
