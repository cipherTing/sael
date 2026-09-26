package gateway

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// JevConfig is the saved connection used for production and operator tests.
type JevConfig struct {
	BaseURL        string    `json:"base_url"`
	Model          string    `json:"model"`
	APIKey         string    `json:"api_key"`
	TimeoutMS      int       `json:"timeout_ms"`
	MaxInputTokens int       `json:"max_input_tokens"`
	UpdatedAt      time.Time `json:"updated_at"`
}

func (c JevConfig) timeout() time.Duration {
	if c.TimeoutMS <= 0 {
		return 5 * time.Second
	}
	return time.Duration(c.TimeoutMS) * time.Millisecond
}

func (c JevConfig) validate() error {
	if strings.TrimSpace(c.APIKey) == "" || strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.BaseURL) == "" {
		return errors.New("Jev 接口地址、模型和 API Key 均不能为空")
	}
	if c.TimeoutMS < 0 || c.TimeoutMS > 120000 {
		return errors.New("分类器超时须在 1–120000 毫秒之间")
	}
	if c.MaxInputTokens < 0 {
		return errors.New("送审上限必须是正整数")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("Jev 接口地址必须是有效的 HTTP(S) URL，不能包含凭证、查询参数或片段")
	}
	return nil
}

func (c JevConfig) public() any {
	return struct {
		BaseURL        string    `json:"base_url"`
		Model          string    `json:"model"`
		APIKeySet      bool      `json:"api_key_set"`
		TimeoutMS      int       `json:"timeout_ms"`
		MaxInputTokens int       `json:"max_input_tokens"`
		UpdatedAt      time.Time `json:"updated_at"`
	}{c.BaseURL, c.Model, c.APIKey != "", int(c.timeout().Milliseconds()), c.inputLimit(), c.UpdatedAt}
}
