package gateway

import (
	"errors"
	"net/url"
	"strings"
	"time"
)

// UpstreamConfig is the saved destination used by the reverse proxy.
type UpstreamConfig struct {
	BaseURL   string    `json:"base_url"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (c UpstreamConfig) target() (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(c.BaseURL))
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("上游地址必须是 HTTP(S) 根地址，不能包含路径、参数或凭证")
	}
	u.Path = ""
	return u, nil
}

func (c UpstreamConfig) public() any {
	return struct {
		BaseURL   string    `json:"base_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}{c.BaseURL, c.UpdatedAt}
}
