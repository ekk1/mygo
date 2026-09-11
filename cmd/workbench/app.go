package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/ekk1/mygo/utils/httpserver"
	"github.com/ekk1/mygo/utils/webui"
)

type app struct {
	store    *store
	activeMu sync.Mutex
	closing  bool
	active   sync.WaitGroup
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	b, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintln(os.Stderr, "response encode:", err)
		status = 500
		b = []byte(`{"error":"响应无法编码"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if _, err = w.Write(append(b, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "response write:", err)
	}
}
func apiError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
func errorStatus(err error) int {
	switch {
	case errors.Is(err, errConflict), errors.Is(err, errBusy):
		return 409
	case errors.Is(err, errNotFound):
		return 404
	default:
		return 400
	}
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	return decodeJSONLimit(w, r, dst, 4<<20)
}
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("JSON 请求无效: %w", err)
	}
	var more any
	if err := d.Decode(&more); err != io.EOF {
		return fmt.Errorf("只允许一个 JSON 对象")
	}
	return nil
}
func protection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; media-src 'self' data: blob:; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if r.Method != "GET" && r.Method != "HEAD" {
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				scheme := "http"
				if r.TLS != nil {
					scheme = "https"
				}
				if err != nil || u.Host != r.Host || u.Scheme != scheme || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
					apiError(w, 403, fmt.Errorf("拒绝跨站请求"))
					return
				}
			} else if r.Header.Get("X-Workbench-Request") != "1" {
				apiError(w, 403, fmt.Errorf("缺少同源请求标识"))
				return
			}
		}
		r.Body = http.MaxBytesReader(w, r.Body, 300<<20)
		next.ServeHTTP(w, r)
	})
}
func newApp(dir, password string) (*app, *httpserver.Server, error) {
	st, err := openStore(dir)
	if err != nil {
		return nil, nil, err
	}
	a := &app{store: st}
	middleware := []httpserver.Middleware{a.track, protection}
	if password == "" {
		middleware = append(middleware, localHostOnly)
	}
	if password != "" {
		middleware = append(middleware, httpserver.BasicAuth("workbench", password))
	}
	s := httpserver.New(middleware...)
	routes := map[string]http.HandlerFunc{"POST /api/sessions/{id}/native": a.sendConversation, "GET /api/config": a.configAPI, "PUT /api/config": a.configAPI, "GET /api/sessions": a.sessionsAPI, "POST /api/sessions": a.sessionsAPI, "GET /api/sessions/{id}": a.sessionAPI, "PATCH /api/sessions/{id}": a.sessionAPI, "DELETE /api/sessions/{id}": a.sessionAPI, "POST /api/sessions/{id}/fork": a.forkAPI, "GET /api/logs": a.listLogs, "GET /api/logs/{id}": a.getLog, "GET /api/logs/{id}/{request}/{file}": a.downloadLog}
	for path, fn := range routes {
		if err = s.HandleFunc(path, fn); err != nil {
			s.Close()
			return nil, nil, err
		}
	}
	if err = s.Handle("/webui/", http.StripPrefix("/webui/", webui.Assets())); err != nil {
		s.Close()
		return nil, nil, err
	}
	if err = registerPages(s); err != nil {
		s.Close()
		return nil, nil, err
	}
	if err = a.registerNative(s); err != nil {
		s.Close()
		return nil, nil, err
	}
	return a, s, nil
}
func (a *app) sessionsAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		sessions := a.store.listSessions()
		if profileID := r.URL.Query().Get("profile_id"); profileID != "" {
			filtered := []session{}
			for _, item := range sessions {
				if item.ProfileID == profileID {
					filtered = append(filtered, item)
				}
			}
			sessions = filtered
		}
		writeJSON(w, 200, sessions)
		return
	}
	var in struct {
		Title     string `json:"title"`
		ProfileID string `json:"profile_id"`
		Operation string `json:"operation"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apiError(w, 400, err)
		return
	}
	p, err := a.store.getProvider(in.ProfileID)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	if err := validateConversationOperation(p.Kind, in.Operation); err != nil {
		apiError(w, 400, err)
		return
	}
	v, err := a.store.createSession(in.Title, in.ProfileID, in.Operation)
	if err != nil {
		apiError(w, 500, err)
		return
	}
	writeJSON(w, 201, v)
}
func (a *app) sessionAPI(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if r.Method == "DELETE" {
		if err := a.store.deleteSession(id); err != nil {
			apiError(w, errorStatus(err), err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if r.Method == "PATCH" {
		var in struct {
			Title string `json:"title"`
		}
		if err := decodeJSON(w, r, &in); err != nil {
			apiError(w, 400, err)
			return
		}
		if strings.TrimSpace(in.Title) == "" || len(in.Title) > 500 {
			apiError(w, 400, fmt.Errorf("标题不能为空或超过 500 字节"))
			return
		}
		e, err := a.store.entry(id)
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
		v.Title = strings.TrimSpace(in.Title)
		err = a.store.persistSession(e, v)
		e.mu.Unlock()
		if err != nil {
			apiError(w, 500, err)
			return
		}
	}
	v, err := a.store.getSession(id)
	if err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, 200, v)
}
func (a *app) forkAPI(w http.ResponseWriter, r *http.Request) {
	var in struct {
		NodeID string `json:"node_id"`
		Title  string `json:"title"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		apiError(w, 400, err)
		return
	}
	v, err := a.store.forkSession(r.PathValue("id"), in.NodeID, in.Title)
	if err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, 201, v)
}

func (a *app) track(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.activeMu.Lock()
		if a.closing {
			a.activeMu.Unlock()
			apiError(w, 503, fmt.Errorf("服务正在关闭"))
			return
		}
		a.active.Add(1)
		a.activeMu.Unlock()
		defer a.active.Done()
		next.ServeHTTP(w, r)
	})
}
func (a *app) stopAccepting() { a.activeMu.Lock(); a.closing = true; a.activeMu.Unlock() }

func localHostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			apiError(w, 403, fmt.Errorf("本地模式仅接受 localhost 或回环 IP，请通过本地地址访问"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
