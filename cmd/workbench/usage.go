package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Absent fields stay absent: unreported usage is not a zero-token request.
type usageInfo struct {
	Tokens  map[string]int64 `json:"tokens"`
	Raw     json.RawMessage  `json:"raw,omitempty"`
	Partial bool             `json:"partial"`
}
type usageSummary struct {
	Tokens   map[string]int64 `json:"tokens"`
	Requests int              `json:"requests"`
	Reported int              `json:"reported"`
	Partial  int              `json:"partial"`
}
type usageOrigin struct{ SessionID, MessageID, TaskID string }
type usageContextKey struct{}
type usageRecord struct {
	ID             string     `json:"id"`
	ProfileID      string     `json:"profile_id"`
	ProfileName    string     `json:"profile_name"`
	Vendor         string     `json:"vendor"`
	BaseURL        string     `json:"base_url"`
	Operation      string     `json:"operation"`
	Model          string     `json:"model"`
	RequestedModel string     `json:"requested_model"`
	ServiceTier    string     `json:"service_tier,omitempty"`
	ResponseID     string     `json:"response_id,omitempty"`
	SessionID      string     `json:"session_id,omitempty"`
	MessageID      string     `json:"message_id,omitempty"`
	TaskID         string     `json:"task_id,omitempty"`
	CreatedAt      string     `json:"created_at"`
	Status         string     `json:"status"`
	Historical     bool       `json:"historical"`
	Usage          *usageInfo `json:"usage"`
}

func usageOperation(op string) bool {
	return op == "responses.create" || op == "chat.create" || op == "messages.create" || op == "content.generate"
}
func usageID(o usageOrigin) string {
	if o.MessageID != "" {
		return "message-" + o.MessageID
	}
	if o.TaskID != "" {
		return "task-" + o.TaskID
	}
	return "request-" + newID()
}
func usageNumber(m map[string]any, key string) (int64, bool) {
	v, ok := m[key].(float64)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > float64(1<<53-1) || v != math.Trunc(v) {
		return 0, false
	}
	return int64(v), true
}
func usageMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func usageObject(operation string, value any) map[string]any {
	// Extract only accounting metadata, never retain prompts or generated media.
	var data map[string]any
	if m, ok := value.(map[string]any); ok {
		data = m
	} else {
		raw, _ := json.Marshal(value)
		_ = json.Unmarshal(raw, &data)
	}
	if data == nil {
		return nil
	}
	events, ok := data["events"]
	if !ok {
		events, ok = data["native_events"]
	}
	if ok {
		raw, _ := json.Marshal(events)
		var list []resourceEvent
		_ = json.Unmarshal(raw, &list)
		out := map[string]any{}
		for _, event := range list {
			d := usageMap(event.Data)
			if response := usageMap(d["response"]); response != nil {
				d = response
			}
			if start := usageMap(d["message"]); start != nil {
				d = start
			}
			for _, key := range []string{"id", "responseId", "model", "modelVersion", "service_tier"} {
				if d[key] != nil {
					out[key] = d[key]
				}
			}
			for _, key := range []string{"usage", "usageMetadata"} {
				if u := usageMap(d[key]); u != nil {
					previous := usageMap(out[key])
					if previous == nil {
						previous = map[string]any{}
					}
					for name, v := range u {
						previous[name] = v
					}
					out[key] = previous
				}
			}
		}
		for _, key := range []string{"id", "responseId", "model", "modelVersion", "service_tier", "usage", "usageMetadata"} {
			if data[key] != nil {
				out[key] = data[key]
			}
		}
		out["partial"] = data["partial"]
		return out
	}
	return data
}
func parseUsage(operation string, value any, status string) *usageInfo {
	data := usageObject(operation, value)
	key := "usage"
	if operation == "content.generate" {
		key = "usageMetadata"
	}
	raw := usageMap(data[key])
	if len(raw) == 0 {
		return nil
	}
	u := &usageInfo{Tokens: map[string]int64{}, Partial: status != "complete" || data["partial"] == true}
	u.Raw, _ = json.Marshal(raw)
	set := func(name string, m map[string]any, key string) {
		if n, ok := usageNumber(m, key); ok {
			u.Tokens[name] = n
		}
	}
	switch operation {
	case "content.generate":
		set("input", raw, "promptTokenCount")
		set("output", raw, "candidatesTokenCount")
		set("reasoning", raw, "thoughtsTokenCount")
		if n, ok := u.Tokens["output"]; ok {
			u.Tokens["output"] = n + u.Tokens["reasoning"]
		}
		set("cache_read", raw, "cachedContentTokenCount")
		set("tool_input", raw, "toolUsePromptTokenCount")
		set("total", raw, "totalTokenCount")
	case "messages.create":
		set("input", raw, "input_tokens")
		set("output", raw, "output_tokens")
		set("cache_read", raw, "cache_read_input_tokens")
		set("cache_write", raw, "cache_creation_input_tokens")
		set("cache_write_5m", usageMap(raw["cache_creation"]), "ephemeral_5m_input_tokens")
		set("cache_write_1h", usageMap(raw["cache_creation"]), "ephemeral_1h_input_tokens")
		if n, ok := u.Tokens["input"]; ok {
			u.Tokens["input"] = n + u.Tokens["cache_read"] + u.Tokens["cache_write"]
		}
	default:
		input, output, inputDetails, outputDetails := "input_tokens", "output_tokens", "input_tokens_details", "output_tokens_details"
		if operation == "chat.create" {
			input, output, inputDetails, outputDetails = "prompt_tokens", "completion_tokens", "prompt_tokens_details", "completion_tokens_details"
		}
		set("input", raw, input)
		set("output", raw, output)
		set("total", raw, "total_tokens")
		set("cache_read", usageMap(raw[inputDetails]), "cached_tokens")
		set("reasoning", usageMap(raw[outputDetails]), "reasoning_tokens")
	}
	if _, ok := u.Tokens["total"]; !ok {
		i, iok := u.Tokens["input"]
		o, ook := u.Tokens["output"]
		if iok && ook {
			u.Tokens["total"] = i + o
		}
	}
	if len(u.Tokens) == 0 {
		return nil
	}
	if _, ok := u.Tokens["input"]; !ok {
		u.Partial = true
	}
	if _, ok := u.Tokens["output"]; !ok {
		u.Partial = true
	}
	return u
}
func addUsage(s *usageSummary, u *usageInfo) {
	s.Requests++
	if u == nil {
		return
	}
	s.Reported++
	if u.Partial {
		s.Partial++
	}
	if s.Tokens == nil {
		s.Tokens = map[string]int64{}
	}
	for k, v := range u.Tokens {
		if s.Tokens[k] > math.MaxInt64-v {
			s.Tokens[k] = math.MaxInt64
		} else {
			s.Tokens[k] += v
		}
	}
}
func sessionUsage(v *session) {
	v.Usage = usageSummary{Tokens: map[string]int64{}}
	seen := map[string]bool{}
	for i := range v.Messages {
		m := &v.Messages[i]
		if m.Role != "assistant" || seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		op := m.Protocol
		if op == "" {
			op = v.Operation
		}
		if m.Usage == nil {
			m.Usage = parseUsage(op, m.Output, m.Status)
		}
		addUsage(&v.Usage, m.Usage)
	}
}
func (a *app) saveUsage(v usageRecord) error {
	a.usageMu.Lock()
	defer a.usageMu.Unlock()
	if a.usage == nil {
		a.usage = map[string]usageRecord{}
	}
	dir := filepath.Join(a.store.dir, "usage")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := saveKV(filepath.Join(dir, v.ID+".json"), v); err != nil {
		return err
	}
	a.usage[v.ID] = clone(v)
	return nil
}
func (a *app) loadUsage() error {
	a.usage = map[string]usageRecord{}
	dir := filepath.Join(a.store.dir, "usage")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		var v usageRecord
		if err := loadKV(filepath.Join(dir, entry.Name()), &v); err != nil {
			return err
		}
		if !validID.MatchString(v.ID) || entry.Name() != v.ID+".json" {
			return fmt.Errorf("invalid usage record %s", entry.Name())
		}
		if v.Status == "pending" {
			v.Status = "interrupted"
			if err := a.saveUsage(v); err != nil {
				return err
			}
		} else {
			a.usage[v.ID] = v
		}
	}
	return a.backfillUsage()
}
func (a *app) backfillUsage() error {
	taskMessages := map[string]bool{}
	importRecord := func(v usageRecord) error {
		if old, exists := a.usage[v.ID]; exists {
			if v.Usage == nil || old.Usage != nil && (!old.Usage.Partial || v.Usage.Partial) {
				return nil
			}
			old.Usage = v.Usage
			old.Status = v.Status
			if v.Model != "" {
				old.Model = v.Model
			}
			if v.ResponseID != "" {
				old.ResponseID = v.ResponseID
			}
			if v.ServiceTier != "" {
				old.ServiceTier = v.ServiceTier
			}
			return a.saveUsage(old)
		}
		v.Historical = true
		if p, err := a.store.getProvider(v.ProfileID); err == nil {
			v.ProfileName = p.Name
			v.Vendor = p.Kind
		}
		return a.saveUsage(v)
	}
	for _, summary := range a.store.listSessions() {
		s, err := a.store.getSession(summary.ID)
		if err != nil {
			return err
		}
		for _, m := range s.Messages {
			if m.Role != "assistant" {
				continue
			}
			if m.TaskID != "" {
				taskMessages[m.TaskID] = true
			}
			op := m.Protocol
			if op == "" {
				op = s.Operation
			}
			if !usageOperation(op) {
				continue
			}
			v := usageRecord{ID: "message-" + m.ID, ProfileID: s.ProfileID, Operation: op, Model: m.ActualModel, RequestedModel: m.ActualModel, SessionID: s.ID, MessageID: m.ID, TaskID: m.TaskID, CreatedAt: m.CreatedAt, Status: m.Status, Usage: parseUsage(op, m.Output, m.Status)}
			usageMetadata(&v, m.Output)
			if m.TaskID != "" && v.Usage == nil {
				if task := a.tasks[m.TaskID]; task != nil {
					t := task.snapshot(false)
					v.Usage = parseUsage(op, t.Result, t.Status)
					if v.Usage != nil {
						v.Status = t.Status
						usageMetadata(&v, t.Result)
					}
				}
			}
			if err := importRecord(v); err != nil {
				return err
			}
		}
	}
	for _, task := range a.tasks {
		t := task.snapshot(false)
		if taskMessages[t.ID] || !usageOperation(t.Operation) {
			continue
		}
		// New conversation records use message IDs even if their session was deleted.
		linked := false
		for _, record := range a.usage {
			if record.TaskID == t.ID {
				recovered := record
				recovered.Status = t.Status
				recovered.Usage = parseUsage(t.Operation, t.Result, t.Status)
				usageMetadata(&recovered, t.Result)
				if err := importRecord(recovered); err != nil {
					return err
				}
				linked = true
				break
			}
		}
		if linked {
			continue
		}
		data := usageObject(t.Operation, t.Result)
		model, _ := data["model"].(string)
		if model == "" {
			model, _ = data["modelVersion"].(string)
		}
		v := usageRecord{ID: "task-" + t.ID, ProfileID: t.ProviderID, Vendor: t.Vendor, Operation: t.Operation, Model: model, SessionID: t.SessionID, TaskID: t.ID, CreatedAt: t.CreatedAt, Status: t.Status, Usage: parseUsage(t.Operation, t.Result, t.Status)}
		usageMetadata(&v, t.Result)
		if err := importRecord(v); err != nil {
			return err
		}
	}
	return nil
}
func (a *app) beginUsage(ctx context.Context, p provider, operation string, params json.RawMessage) (*usageRecord, error) {
	if !usageOperation(operation) {
		return nil, nil
	}
	o, _ := ctx.Value(usageContextKey{}).(usageOrigin)
	var fields map[string]any
	_ = json.Unmarshal(params, &fields)
	model, _ := fields["model"].(string)
	tier, _ := fields["service_tier"].(string)
	v := usageRecord{ID: usageID(o), ProfileID: p.ID, ProfileName: p.Name, Vendor: p.Kind, BaseURL: p.BaseURL, Operation: operation, RequestedModel: model, Model: model, ServiceTier: tier, SessionID: o.SessionID, MessageID: o.MessageID, TaskID: o.TaskID, CreatedAt: now(), Status: "pending"}
	if err := a.saveUsage(v); err != nil {
		return nil, fmt.Errorf("保存用量记录失败，请求尚未发送: %w", err)
	}
	return &v, nil
}
func (a *app) finishUsage(v *usageRecord, result any, callErr error) error {
	if v == nil {
		return nil
	}
	v.Status = "complete"
	if callErr != nil {
		v.Status = "error"
	}
	v.Usage = parseUsage(v.Operation, result, v.Status)
	usageMetadata(v, result)
	if err := a.saveUsage(*v); err != nil {
		return fmt.Errorf("上游请求已执行，但保存用量失败（请勿直接重试）: %w", err)
	}
	return nil
}
func usageMetadata(v *usageRecord, result any) {
	data := usageObject(v.Operation, result)
	for _, key := range []string{"model", "modelVersion"} {
		if model, ok := data[key].(string); ok && model != "" {
			v.Model = model
		}
	}
	for _, key := range []string{"id", "responseId"} {
		if id, ok := data[key].(string); ok {
			v.ResponseID = id
		}
	}
	if tier, ok := data["service_tier"].(string); ok {
		v.ServiceTier = tier
	}
}
func (a *app) usageAPI(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	for _, date := range []string{from, to} {
		if date != "" {
			if _, err := time.Parse("2006-01-02", date); err != nil {
				apiError(w, 400, fmt.Errorf("日期应为 YYYY-MM-DD（UTC）"))
				return
			}
		}
	}
	if from != "" && to != "" && from > to {
		apiError(w, 400, fmt.Errorf("起始日期不能晚于结束日期"))
		return
	}
	a.usageMu.RLock()
	records := make([]usageRecord, 0, len(a.usage))
	for _, v := range a.usage {
		records = append(records, clone(v))
	}
	a.usageMu.RUnlock()
	rows := []usageRecord{}
	total := usageSummary{Tokens: map[string]int64{}}
	type group struct {
		ProfileID   string       `json:"profile_id"`
		ProfileName string       `json:"profile_name"`
		Model       string       `json:"model"`
		Usage       usageSummary `json:"usage"`
	}
	groups := map[string]*group{}
	for _, v := range records {
		date := v.CreatedAt
		if len(date) > 10 {
			date = date[:10]
		}
		if from != "" && date < from || to != "" && date > to {
			continue
		}
		if p := r.URL.Query().Get("profile_id"); p != "" && v.ProfileID != p {
			continue
		}
		if m := r.URL.Query().Get("model"); m != "" && v.Model != m {
			continue
		}
		rows = append(rows, v)
		addUsage(&total, v.Usage)
		key := v.ProfileID + "\x00" + v.Model
		g := groups[key]
		if g == nil {
			g = &group{ProfileID: v.ProfileID, ProfileName: v.ProfileName, Model: v.Model, Usage: usageSummary{Tokens: map[string]int64{}}}
			groups[key] = g
		}
		addUsage(&g.Usage, v.Usage)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt > rows[j].CreatedAt })
	list := []*group{}
	for _, g := range groups {
		list = append(list, g)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ProfileID+list[i].Model < list[j].ProfileID+list[j].Model })
	writeJSON(w, 200, map[string]any{"records": rows, "groups": list, "total": total})
}

// Chat streaming requires opting into the final usage-only chunk. Preserve any
// explicit override for compatible endpoints that reject this optional field.
func usageStreamParams(operation string, raw json.RawMessage) json.RawMessage {
	if operation != "chat.create" {
		return raw
	}
	var fields map[string]any
	if json.Unmarshal(raw, &fields) != nil || fields["stream"] != true {
		return raw
	}
	options, ok := fields["stream_options"].(map[string]any)
	if !ok {
		if fields["stream_options"] != nil {
			return raw
		}
		options = map[string]any{}
	}
	if _, set := options["include_usage"]; !set {
		options["include_usage"] = true
	}
	fields["stream_options"] = options
	output, err := json.Marshal(fields)
	if err != nil {
		return raw
	}
	return output
}
