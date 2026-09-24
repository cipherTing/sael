// Package config validates deployment settings supplied as environment variables.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// Config contains the deployment settings needed to start the gateway.
type Config struct {
	DatabaseURL    string
	Upstream       *url.URL
	UpstreamAPIKey string
	AdminPassword  string
	CLIPath        string
	Listen         string
	WebDir         string
	SpoolPath      string
	Timeout        time.Duration
	Concurrency    int
}

// Load reads and validates a gateway configuration.
func Load(getenv func(string) string) (Config, error) {
	c := Config{DatabaseURL: getenv("DATABASE_URL"), UpstreamAPIKey: getenv("UPSTREAM_API_KEY"), AdminPassword: getenv("ADMIN_PASSWORD"), CLIPath: getenv("SAEL_CLI_PATH"), Listen: getenv("LISTEN_ADDR"), WebDir: getenv("WEB_DIR"), SpoolPath: getenv("SPOOL_PATH"), Timeout: 5 * time.Second, Concurrency: 32}
	if c.DatabaseURL == "" || c.AdminPassword == "" || getenv("UPSTREAM_URL") == "" {
		return c, errors.New("DATABASE_URL, UPSTREAM_URL and ADMIN_PASSWORD are required")
	}
	u, err := url.Parse(getenv("UPSTREAM_URL"))
	if err != nil {
		return c, err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return c, errors.New("UPSTREAM_URL must be an HTTP(S) origin without path, query or credentials")
	}
	c.Upstream = u
	if c.Listen == "" {
		c.Listen = ":8080"
	}
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
	if getenv("CLASSIFIER_CONCURRENCY") != "" {
		c.Concurrency, err = strconv.Atoi(getenv("CLASSIFIER_CONCURRENCY"))
		if err != nil || c.Concurrency < 1 {
			return c, errors.New("CLASSIFIER_CONCURRENCY must be positive")
		}
	}
	return c, nil
}
