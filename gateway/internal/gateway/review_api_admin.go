package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"
)

func (s *Server) adminReviewAPI(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/admin/review-api/") {
		return false
	}
	store, ok := s.Store.(ReviewAPIStore)
	if !ok {
		writeReviewAPIError(w, 503, "store_unavailable", "审核 API 暂不可用")
		return true
	}
	switch {
	case r.URL.Path == "/admin/review-api/keys" && r.Method == "GET":
		keys, err := store.ListReviewAPIKeys(r.Context())
		if err != nil {
			writeReviewAPIError(w, 503, "keys_unavailable", "密钥列表读取失败")
			break
		}
		for i := range keys {
			keys[i].Secret = ""
		}
		writeJSON(w, keys)
	case r.URL.Path == "/admin/review-api/keys" && r.Method == "POST":
		var input struct {
			Name string `json:"name"`
			Note string `json:"note"`
		}
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		if dec.Decode(&input) != nil || strings.TrimSpace(input.Name) == "" || utf8.RuneCountInString(input.Name) > 80 || utf8.RuneCountInString(input.Note) > 240 {
			writeReviewAPIError(w, 400, "invalid_key", "名称须为 1–80 字，备注最多 240 字")
			break
		}
		key, err := store.CreateReviewAPIKey(r.Context(), strings.TrimSpace(input.Name), strings.TrimSpace(input.Note))
		if err != nil {
			writeReviewAPIError(w, 503, "key_creation_failed", "密钥创建失败")
			break
		}
		writeJSON(w, key)
	case strings.HasPrefix(r.URL.Path, "/admin/review-api/keys/") && r.Method == "DELETE":
		id := strings.TrimPrefix(r.URL.Path, "/admin/review-api/keys/")
		err := store.RevokeReviewAPIKey(r.Context(), id)
		if errors.Is(err, ErrNotFound) {
			writeReviewAPIError(w, 404, "key_not_found", "密钥不存在")
			break
		}
		if err != nil {
			writeReviewAPIError(w, 503, "key_revocation_failed", "密钥撤销失败")
			break
		}
		writeJSON(w, map[string]bool{"revoked": true})
	case (r.URL.Path == "/admin/review-api/overview" || r.URL.Path == "/admin/review-api/analytics") && r.Method == "GET":
		p, err := s.Store.Policy(r.Context())
		if err != nil {
			writeReviewAPIError(w, 503, "policy_unavailable", "配置暂不可用")
			break
		}
		if !p.ReviewAPIEnabled {
			writeJSON(w, map[string]bool{"enabled": false})
			break
		}
		f, err := analyticsFilter(r.URL.Query())
		if err != nil {
			writeReviewAPIError(w, 400, "invalid_range", err.Error())
			break
		}
		result, err := store.ReviewAPIOverview(r.Context(), f.Since, f.Until)
		if err != nil {
			writeReviewAPIError(w, 503, "statistics_unavailable", "审核 API 统计读取失败")
			break
		}
		writeJSON(w, struct {
			ReviewAPIOverview
			Enabled bool `json:"enabled"`
		}{result, true})
	default:
		writeReviewAPIError(w, 404, "not_found", "接口不存在")
	}
	return true
}
