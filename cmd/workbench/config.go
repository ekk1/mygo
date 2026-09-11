package main

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

func (s *store) saveConfig(next configuration) (configuration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if next.Revision != s.config.Revision {
		return configuration{}, errConflict
	}
	old := map[string]provider{}
	for _, p := range s.config.Providers {
		old[p.ID] = p
	}
	seen := map[string]bool{}
	for i := range next.Providers {
		p := &next.Providers[i]
		p.Name = strings.TrimSpace(p.Name)
		if !validID.MatchString(p.ID) || p.Name == "" || seen[p.ID] {
			return configuration{}, fmt.Errorf("provider ID 必须唯一且只含字母、数字、横线；名称不能为空")
		}
		seen[p.ID] = true
		if p.Kind == "" {
			p.Kind = "openai"
		}
		defaults := map[string]string{"openai": "https://api.openai.com/v1", "compatible": "", "anthropic": "https://api.anthropic.com/v1", "gemini": "https://generativelanguage.googleapis.com", "xai": "https://api.x.ai/v1"}
		base, ok := defaults[p.Kind]
		if !ok {
			return configuration{}, fmt.Errorf("未知服务商 %q", p.Kind)
		}
		if oldProfile, exists := old[p.ID]; exists && oldProfile.Kind != "" && oldProfile.Kind != p.Kind {
			return configuration{}, fmt.Errorf("profile 所属服务商不能修改，请新建 profile")
		}
		if p.Protocol == "" {
			p.Protocol = "responses"
		}
		if p.Protocol != "responses" && p.Protocol != "chat" {
			return configuration{}, fmt.Errorf("未知协议 %q", p.Protocol)
		}
		for _, capability := range p.Resources {
			if capability != "files" && capability != "containers" && capability != "batches" && capability != "images" && capability != "audio" {
				return configuration{}, fmt.Errorf("未知兼容能力 %q", capability)
			}
		}
		if p.BaseURL == "" {
			p.BaseURL = base
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return configuration{}, fmt.Errorf("provider %s 的 API 地址无效", p.Name)
		}
		if p.ProxyURL != "" && p.ProxyURL != "-" {
			proxy, err := url.Parse(p.ProxyURL)
			if err != nil || proxy.Host == "" {
				return configuration{}, fmt.Errorf("代理地址无效")
			}
			switch proxy.Scheme {
			case "http", "https", "socks5", "socks5h":
			default:
				return configuration{}, fmt.Errorf("代理协议无效")
			}
		}
		if p.ClearKey {
			p.APIKey = ""
		} else if p.APIKey == "" {
			p.APIKey = old[p.ID].APIKey
		}
		p.HasKey = p.APIKey != ""
		p.ClearKey = false
		if p.Models == nil {
			p.Models = []string{}
		}
	}
	next.Revision++
	if err := saveKV(filepath.Join(s.dir, "config.json"), next); err != nil {
		return configuration{}, err
	}
	s.config = clone(next)
	for i := range next.Providers {
		next.Providers[i].APIKey = ""
	}
	return next, nil
}
func (s *store) getProvider(id string) (provider, error) {
	c := s.configSnapshot(false)
	for _, p := range c.Providers {
		if p.ID == id {
			return p, nil
		}
	}
	return provider{}, errNotFound
}
func (a *app) configAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeJSON(w, 200, a.store.configSnapshot(true))
		return
	}
	var cfg configuration
	if err := decodeJSON(w, r, &cfg); err != nil {
		apiError(w, 400, err)
		return
	}
	v, err := a.store.saveConfig(cfg)
	if err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, 200, v)
}
