// Package config validates deployment settings supplied as environment variables.
package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config contains the deployment settings needed to start the gateway.
type Config struct {
	MaxRequestBodySize     int64
	MaxHeaderBytes         int
	ReadHeaderTimeout      time.Duration
	IdleTimeout            time.Duration
	AsyncReviewConcurrency int
	TrustedProxies         []netip.Prefix
	DatabaseURL            string
	RedisURL               string
	Upstream               *url.URL
	AdminPassword          string
	CLIPath                string
	Listen                 string
	AdminListen            string
	PublicIngressURL       string
	WebDir                 string
	SpoolPath              string
	Timeout                time.Duration
	JevMaxInputTokens      int
}

// Load reads and validates a gateway configuration.
func Load(getenv func(string) string) (Config, error) {
	c := Config{DatabaseURL: getenv("DATABASE_URL"), AdminPassword: getenv("ADMIN_PASSWORD"), CLIPath: getenv("SAEL_CLI_PATH"), Listen: getenv("LISTEN_ADDR"), WebDir: getenv("WEB_DIR"), SpoolPath: getenv("SPOOL_PATH"), Timeout: 5 * time.Second, JevMaxInputTokens: 28800}
	if c.DatabaseURL == "" && getenv("POSTGRES_PASSWORD") != "" {
		value := func(key, fallback string) string {
			if v := getenv(key); v != "" {
				return v
			}
			return fallback
		}
		u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(value("DB_HOST", "localhost"), value("DB_PORT", "5432")), Path: "/" + value("POSTGRES_DB", "sael"), User: url.UserPassword(value("POSTGRES_USER", "sael"), getenv("POSTGRES_PASSWORD"))}
		q := url.Values{"sslmode": {value("DB_SSLMODE", "disable")}}
		u.RawQuery = q.Encode()
		c.DatabaseURL = u.String()
	}
	if c.DatabaseURL == "" || c.AdminPassword == "" {
		return c, errors.New("DATABASE_URL and ADMIN_PASSWORD are required")
	}
	c.RedisURL = getenv("REDIS_URL")
	if c.RedisURL == "" && getenv("REDIS_PASSWORD") != "" {
		host, port := getenv("REDIS_HOST"), getenv("REDIS_PORT")
		if host == "" {
			host = "localhost"
		}
		if port == "" {
			port = "6379"
		}
		u := url.URL{Scheme: "redis", Host: net.JoinHostPort(host, port), User: url.UserPassword("", getenv("REDIS_PASSWORD")), Path: "/0"}
		c.RedisURL = u.String()
	}
	if c.RedisURL == "" {
		return c, errors.New("REDIS_URL or REDIS_PASSWORD is required")
	}

	for _, raw := range strings.Split(getenv("TRUSTED_PROXY_CIDRS"), ",") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil {
			return c, errors.New("TRUSTED_PROXY_CIDRS 必须是以逗号分隔的 IP 网段")
		}
		c.TrustedProxies = append(c.TrustedProxies, prefix)
	}
	var err error
	if raw := getenv("UPSTREAM_URL"); raw != "" {
		u, parseErr := url.Parse(raw)
		if parseErr != nil {
			return c, parseErr
		}
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return c, errors.New("UPSTREAM_URL must be an HTTP(S) origin without path, query or credentials")
		}
		c.Upstream = u
	}
	if c.Listen == "" {
		c.Listen = ":8081"
	}
	if value := getenv("INGRESS_LISTEN_ADDR"); value != "" {
		c.Listen = value
	}
	c.AdminListen = getenv("ADMIN_LISTEN_ADDR")
	if c.AdminListen == "" {
		c.AdminListen = ":8080"
	}
	if c.AdminListen == c.Listen {
		return c, errors.New("管理端口与进网端口必须不同")
	}
	c.PublicIngressURL = getenv("INGRESS_PUBLIC_URL")
	if c.WebDir == "" {
		c.WebDir = "web/dist"
	}
	if c.SpoolPath == "" {
		c.SpoolPath = "var/spool.jsonl"
	}
	if getenv("CLASSIFIER_TIMEOUT") != "" {
		c.Timeout, err = time.ParseDuration(getenv("CLASSIFIER_TIMEOUT"))
		if err != nil {
			return c, fmt.Errorf("invalid CLASSIFIER_TIMEOUT: %w", err)
		}
		if c.Timeout <= 0 {
			return c, errors.New("CLASSIFIER_TIMEOUT must be positive")
		}
	}
	if raw := strings.TrimSpace(getenv("JEV_MAX_INPUT_TOKENS")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return c, errors.New("JEV_MAX_INPUT_TOKENS must be a positive integer")
		}
		c.JevMaxInputTokens = value
	}
	c.MaxRequestBodySize = 256 << 20
	c.MaxHeaderBytes = 64 << 10
	c.ReadHeaderTimeout = 10 * time.Second
	c.IdleTimeout = 120 * time.Second
	c.AsyncReviewConcurrency = 256
	for _, item := range []struct {
		key    string
		target *time.Duration
	}{{"READ_HEADER_TIMEOUT", &c.ReadHeaderTimeout}, {"IDLE_TIMEOUT", &c.IdleTimeout}} {
		if raw := getenv(item.key); raw != "" {
			value, err := time.ParseDuration(raw)
			if err != nil || value <= 0 {
				return c, fmt.Errorf("%s must be a positive duration", item.key)
			}
			*item.target = value
		}
	}
	for _, item := range []struct {
		key    string
		target *int
	}{{"MAX_HEADER_BYTES", &c.MaxHeaderBytes}, {"ASYNC_REVIEW_CONCURRENCY", &c.AsyncReviewConcurrency}} {
		if raw := getenv(item.key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value <= 0 {
				return c, fmt.Errorf("%s must be a positive integer", item.key)
			}
			*item.target = value
		}
	}
	if raw := getenv("MAX_REQUEST_BODY_SIZE"); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value <= 0 {
			return c, errors.New("MAX_REQUEST_BODY_SIZE must be a positive byte count")
		}
		c.MaxRequestBodySize = value
	}
	return c, nil
}
