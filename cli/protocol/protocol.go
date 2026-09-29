// Package protocol defines the private gateway-to-CLI wire format, without SDK clients.
package protocol

import "time"

// Version identifies compatible startup handshakes and request formats.
const Version = 2

// Ready is written once to the owning process, never to application logs.
type Ready struct {
	Version int    `json:"version"`
	Address string `json:"address"`
	Token   string `json:"token"`
}

// Request carries an immutable configuration snapshot and an absolute deadline.
type Request struct {
	Text      string    `json:"text"`
	Questions []string  `json:"questions"`
	BaseURL   string    `json:"base_url"`
	APIKey    string    `json:"api_key"`
	Model     string    `json:"model"`
	Deadline  time.Time `json:"deadline"`
}

// Answer preserves the original probability or graded score.
type Answer struct {
	Question string  `json:"question"`
	Type     string  `json:"type"`
	Value    float64 `json:"value"`
}

// Response contains measurements or a safe error category, never provider bodies.
type Response struct {
	Answers []Answer `json:"answers,omitempty"`
	Error   string   `json:"error,omitempty"`
}
