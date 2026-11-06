package gateway

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

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
	allowed := map[string]bool{"scenes": true, "enabled": true, "review_api_enabled": true, "trusted_key_idle_days": true, "preview_chars": true, "retention_days": true, "block_message": true}
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
	case strings.HasPrefix(r.URL.Path, "/admin/trusted-keys/") && r.Method == http.MethodPatch:
		manager, ok := s.Store.(CredentialNoteAdmin)
		if !ok {
			http.Error(w, "可信密钥备注暂不可用", http.StatusServiceUnavailable)
			return true
		}
		id := strings.TrimPrefix(r.URL.Path, "/admin/trusted-keys/")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return true
		}
		var input struct {
			Note *string `json:"note"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil || input.Note == nil || dec.Decode(new(any)) != io.EOF || strings.ContainsAny(*input.Note, "\r\n\u0085\u2028\u2029") {
			http.Error(w, "备注须为最多 240 字的单行文本", http.StatusBadRequest)
			return true
		}
		note := strings.TrimSpace(strings.ReplaceAll(*input.Note, "\x00", " "))
		if utf8.RuneCountInString(note) > 240 {
			http.Error(w, "备注须为最多 240 字的单行文本", http.StatusBadRequest)
			return true
		}
		if err := manager.SetCredentialNote(r.Context(), id, note); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return true
		} else if err != nil {
			http.Error(w, "可信密钥备注保存失败", http.StatusServiceUnavailable)
			return true
		}
		writeJSON(w, map[string]string{"note": note})
		return true
	case strings.HasPrefix(r.URL.Path, "/admin/key-sampling/") && r.Method == http.MethodPatch:
		manager, ok := s.Store.(KeySamplingAdmin)
		if !ok {
			http.Error(w, "Key 取样率管理暂不可用", http.StatusServiceUnavailable)
			return true
		}
		id := strings.TrimPrefix(r.URL.Path, "/admin/key-sampling/")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return true
		}
		var input struct {
			SampleRate *int `json:"sample_rate"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil || input.SampleRate == nil || *input.SampleRate < 0 || *input.SampleRate > 100 {
			http.Error(w, "取样率必须是 0 到 100 的整数", http.StatusBadRequest)
			return true
		}
		if err := manager.SetKeySampleRate(r.Context(), id, *input.SampleRate); errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return true
		} else if err != nil {
			http.Error(w, "Key 取样率设置保存失败", http.StatusServiceUnavailable)
			return true
		}
		writeJSON(w, map[string]int{"sample_rate": *input.SampleRate})
		return true
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
		filter := TrustedCredentialFilter{KeyPrefix: strings.TrimSpace(r.URL.Query().Get("key_prefix")), Note: strings.TrimSpace(r.URL.Query().Get("note")), Offset: offset, Limit: limit}
		list, err := lister.TrustedCredentials(r.Context(), p.TrustedKeyIdle(), filter)
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
