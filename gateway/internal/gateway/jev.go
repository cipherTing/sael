package gateway

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// JevConfig is the saved connection used for production and operator tests.
type JevConfig struct {
	BaseURL   string    `json:"base_url"`
	Model     string    `json:"model"`
	APIKey    string    `json:"api_key"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c JevConfig) validate() error {
	if strings.TrimSpace(c.APIKey) == "" || strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("Jev 接口地址、模型和 API Key 均不能为空")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Jev 接口地址必须是有效的 HTTP(S) URL，不能包含凭证、查询参数或片段")
	}
	return nil
}

func (c JevConfig) public() any {
	return struct {
		BaseURL   string    `json:"base_url"`
		Model     string    `json:"model"`
		APIKeySet bool      `json:"api_key_set"`
		UpdatedAt time.Time `json:"updated_at"`
	}{c.BaseURL, c.Model, c.APIKey != "", c.UpdatedAt}
}
