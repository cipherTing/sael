package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/cipherTing/sael/gateway/internal/policy"
)

func (s *Server) policyInput(r *http.Request) (PolicyUpdate, error) {
	var update PolicyUpdate
	if r.Method == http.MethodPut {
		var next policy.Policy
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		err := dec.Decode(&next)
		update.Replace = &next
		return update, err
	}
	var patch map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		return update, errors.New("策略配置格式错误")
	}
	if len(patch) == 0 {
		return update, errors.New("没有要保存的配置")
	}
	allowed := map[string]bool{"scenes": true, "enabled": true, "trusted_key_idle_days": true, "preview_chars": true, "retention_days": true, "block_message": true}
	for k := range patch {
		if !allowed[k] {
			return update, errors.New("不支持的策略配置字段")
		}
	}
	update.Fields = patch
	return update, nil
}
func (s *Server) adminFeatures(w http.ResponseWriter, r *http.Request) bool {
	switch {
	case r.URL.Path == "/admin/trusted-keys" && r.Method == http.MethodGet:
		lister, ok := s.Store.(CredentialLister)
		if !ok {
			http.Error(w, "可信密钥列表暂不可用", http.StatusServiceUnavailable)
			return true
		}
		p, err := s.Store.Policy(r.Context())
		if err != nil {
			http.Error(w, "配置暂不可用", http.StatusServiceUnavailable)
			return true
		}
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if offset < 0 {
			offset = 0
		}
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		if limit < 1 || limit > 100 {
			limit = 50
		}
		list, err := lister.TrustedCredentials(r.Context(), p.TrustedKeyIdle(), offset, limit)
		if err != nil {
			http.Error(w, "可信密钥列表读取失败", http.StatusServiceUnavailable)
			return true
		}
		writeJSON(w, list)
		return true
	case r.URL.Path == "/admin/risk-sources" && r.Method == http.MethodGet:
		filter, err := analyticsFilter(r.URL.Query())
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return true
		}
		reader, ok := s.Store.(RiskSourceReader)
		if !ok {
			http.Error(w, "风险来源统计暂不可用", http.StatusServiceUnavailable)
			return true
		}
		result, err := reader.RiskSources(r.Context(), filter)
		if err != nil {
			http.Error(w, "风险来源统计读取失败", http.StatusServiceUnavailable)
			return true
		}
		writeJSON(w, result)
		return true
	case r.URL.Path == "/admin/review-cache" && (r.Method == http.MethodGet || r.Method == http.MethodPut):
		manager, ok := s.ReviewCache.(ReviewCacheAdmin)
		if !ok {
			http.Error(w, "审核缓存未配置", http.StatusServiceUnavailable)
			return true
		}
		if r.Method == http.MethodPut {
			var cfg ReviewCacheConfig
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if dec.Decode(&cfg) != nil {
				http.Error(w, "缓存配置格式错误", http.StatusBadRequest)
				return true
			}
			if err := cfg.Validate(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return true
			}
			if err := manager.Configure(r.Context(), cfg); err != nil {
				http.Error(w, "审核缓存配置应用失败", http.StatusServiceUnavailable)
				return true
			}
		}
		status, err := manager.Status(r.Context())
		if err != nil {
			http.Error(w, "审核缓存状态读取失败", http.StatusServiceUnavailable)
			return true
		}
		writeJSON(w, status)
		return true
	}
	return false
}
