package main

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
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
		if p.BaseURL == "" {
			p.BaseURL = "https://api.openai.com/v1"
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
	models := map[string]bool{}
	for i := range next.Models {
		m := &next.Models[i]
		if !validID.MatchString(m.ID) || strings.TrimSpace(m.Name) == "" || models[m.ID] {
			return configuration{}, fmt.Errorf("模型 ID 必须唯一，名称不能为空")
		}
		models[m.ID] = true
		for j := range m.Routes {
			r := &m.Routes[j]
			if !seen[r.ProviderID] || strings.TrimSpace(r.Model) == "" {
				return configuration{}, fmt.Errorf("模型 %s 的线路缺少 provider 或实际模型名", m.Name)
			}
			if r.Protocol == "" {
				r.Protocol = "responses"
			}
			if r.Protocol != "responses" && r.Protocol != "chat" {
				return configuration{}, fmt.Errorf("不支持的生成协议")
			}
		}
		if m.Featured && len(m.Routes) == 0 {
			return configuration{}, fmt.Errorf("精选模型必须先建立映射")
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
func (s *store) resolveModel(id string) (model, route, error) {
	c := s.configSnapshot(false)
	for _, m := range c.Models {
		if m.ID == id && m.Featured && len(m.Routes) > 0 {
			return m, m.Routes[0], nil
		}
	}
	return model{}, route{}, fmt.Errorf("请选择已映射的精选模型")
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
func (a *app) discoverModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, finish, err := a.providerClient(id, "models.list", nil)
	if err != nil {
		apiError(w, 400, err)
		return
	}
	defer c.CloseIdleConnections()
	res, callErr := c.ListModels(r.Context())
	if err = finish(callErr); err != nil {
		apiError(w, 502, err)
		return
	}
	models := []string{}
	for _, m := range res.Data {
		models = append(models, m.ID)
	}
	sort.Strings(models)
	cfg := a.store.configSnapshot(false)
	for i := range cfg.Providers {
		if cfg.Providers[i].ID == id {
			cfg.Providers[i].Models = models
		}
	}
	if _, err = a.store.saveConfig(cfg); err != nil {
		apiError(w, errorStatus(err), err)
		return
	}
	writeJSON(w, 200, map[string]any{"models": models})
}
