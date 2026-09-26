// Package protocol extracts current user text from supported AI request formats.
package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"strconv"
	"strings"
)

// Request contains only the current user text and key request parameters.
type Request struct {
	SessionID  string     `json:"session_id,omitempty"`
	Parameters Parameters `json:"parameters"`
	Text       string     `json:"text"`
	Protocol   string     `json:"protocol"`
	Model      string     `json:"model"`
	Stream     bool       `json:"stream"`
	HasNonText bool       `json:"has_non_text_input"`
}

// ErrUnsupported indicates a path outside the supported AI endpoints.
var ErrUnsupported = errors.New("unsupported AI endpoint")

// Endpoint identifies a monitored request format independently of its upstream host.
type Endpoint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
}

// Endpoints is the catalog of monitored API request formats.
var Endpoints = []Endpoint{
	{"openai_chat", "Chat Completions", "/v1/chat/completions"},
	{"openai_responses", "Responses", "/v1/responses"},
	{"anthropic", "Messages", "/v1/messages"},
	{"openai_images_generations", "Images Generations", "/v1/images/generations"},
	{"openai_images_edits", "Images Edits", "/v1/images/edits"},
	{"openai_images_variations", "Images Variations", "/v1/images/variations"},
}

// Monitored distinguishes audited formats from transparent passthrough traffic.
func Monitored(name string) bool {
	for _, endpoint := range Endpoints {
		if endpoint.ID == name {
			return true
		}
	}
	return false
}

// Supported reports whether the route has a known request extractor.
func Supported(path string) bool {
	return Name(path) != ""
}

// Name returns the protocol label for a supported route.
func Name(path string) string {
	switch path {
	case "/v1/chat/completions":
		return "openai_chat"
	case "/v1/responses":
		return "openai_responses"
	case "/v1/messages":
		return "anthropic"
	case "/v1/images/generations":
		return "openai_images_generations"
	case "/v1/images/edits":
		return "openai_images_edits"
	case "/v1/images/variations":
		return "openai_images_variations"
	default:
		if geminiModel(path) != "" {
			return "gemini"
		}
		return ""
	}
}

// Extract reads the final current user input without scanning history.
func Extract(path string, body []byte) (Request, error) {
	return ExtractWithContentType(path, "application/json", body)
}

// ExtractWithContentType extracts only text needed by the classifier. Multipart
// image bytes are deliberately skipped; only the prompt field is retained.
func ExtractWithContentType(path, contentType string, body []byte) (Request, error) {
	if !Supported(path) {
		return Request{}, ErrUnsupported
	}
	var root struct {
		Model    string          `json:"model"`
		Prompt   string          `json:"prompt"`
		Metadata json.RawMessage `json:"metadata"`
		Stream   bool            `json:"stream"`
		Messages json.RawMessage `json:"messages"`
		Input    json.RawMessage `json:"input"`
		Contents json.RawMessage `json:"contents"`
	}
	if strings.HasPrefix(strings.ToLower(contentType), "multipart/") && (path == "/v1/images/edits" || path == "/v1/images/variations") {
		// Multipart image requests are parsed below; image bytes never enter the
		// JSON decoder or classifier payload.
	} else if err := json.Unmarshal(body, &root); err != nil {
		return Request{}, err
	}
	out := Request{Model: root.Model, Stream: root.Stream, Protocol: Name(path), Parameters: extractParameters(path, body)}
	switch path {
	case "/v1/chat/completions":
		out.Text, out.HasNonText = lastMessage(root.Messages, "text")
	case "/v1/responses":
		if len(root.Input) > 0 && root.Input[0] == '"' {
			_ = json.Unmarshal(root.Input, &out.Text)
			out.Text = strings.TrimSpace(out.Text)
		} else {
			out.Text, out.HasNonText = lastMessage(root.Input, "input_text")
		}
	case "/v1/messages":
		out.SessionID = anthropicSession(root.Metadata)
		out.Text, out.HasNonText = lastMessage(root.Messages, "text")
	case "/v1/images/generations":
		out.Text = strings.TrimSpace(root.Prompt)
		out.HasNonText = false
	case "/v1/images/edits", "/v1/images/variations":
		out.HasNonText = true
		out.Text = strings.TrimSpace(root.Prompt)
		if strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
			model, prompt, stream := multipartImageFields(contentType, body)
			if model != "" {
				out.Model = model
			}
			out.Text = prompt
			out.Stream = stream
		}
		if path == "/v1/images/variations" {
			out.Text = ""
		}
	default:
		out.Model = geminiModel(path)
		out.Stream = strings.HasSuffix(path, ":streamGenerateContent")
		out.Text, out.HasNonText = lastGeminiContent(root.Contents)
	}
	return out, nil
}

func multipartImageFields(contentType string, body []byte) (model, prompt string, stream bool) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" || params["boundary"] == "" {
		return "", "", false
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	for {
		part, err := reader.NextPart()
		if err != nil {
			break
		}
		name := part.FormName()
		if (name != "model" && name != "prompt" && name != "stream") || part.FileName() != "" {
			continue
		}
		data, readErr := io.ReadAll(part)
		value := string(data)
		if readErr != nil {
			continue
		}
		switch name {
		case "model":
			model = strings.TrimSpace(value)
		case "prompt":
			prompt = strings.TrimSpace(value)
		case "stream":
			stream, _ = strconv.ParseBool(strings.TrimSpace(value))
		}
	}
	return model, prompt, stream
}

func geminiModel(path string) string {
	const prefix = "/v1beta/models/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	name := strings.TrimPrefix(path, prefix)
	for _, suffix := range []string{":generateContent", ":streamGenerateContent"} {
		if strings.HasSuffix(name, suffix) && len(name) > len(suffix) {
			return strings.TrimSuffix(name, suffix)
		}
	}
	return ""
}

func lastMessage(raw json.RawMessage, textType string) (string, bool) {
	var messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &messages) != nil || len(messages) == 0 {
		return "", false
	}
	last := messages[len(messages)-1]
	if last.Role != "user" {
		return "", false
	}
	if len(last.Content) > 0 && last.Content[0] == '"' {
		var text string
		_ = json.Unmarshal(last.Content, &text)
		return strings.TrimSpace(text), false
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(last.Content, &parts) != nil {
		return "", false
	}
	texts := make([]string, 0, len(parts))
	nonText := false
	for _, part := range parts {
		if part.Type == textType {
			if text := strings.TrimSpace(part.Text); text != "" {
				texts = append(texts, text)
			}
		} else {
			nonText = true
		}
	}
	return strings.Join(texts, "\n"), nonText
}

func lastGeminiContent(raw json.RawMessage) (string, bool) {
	var contents []struct {
		Role  string            `json:"role"`
		Parts []json.RawMessage `json:"parts"`
	}
	if json.Unmarshal(raw, &contents) != nil || len(contents) == 0 {
		return "", false
	}
	last := contents[len(contents)-1]
	if last.Role != "user" {
		return "", false
	}
	texts := make([]string, 0, len(last.Parts))
	nonText := false
	for _, rawPart := range last.Parts {
		var part map[string]json.RawMessage
		if json.Unmarshal(rawPart, &part) != nil {
			nonText = true
			continue
		}
		if rawText, ok := part["text"]; ok {
			var value string
			if json.Unmarshal(rawText, &value) == nil && strings.TrimSpace(value) != "" {
				texts = append(texts, strings.TrimSpace(value))
			}
			if len(part) > 1 {
				nonText = true
			}
		} else if len(bytes.TrimSpace(rawPart)) > 0 {
			nonText = true
		}
	}
	return strings.Join(texts, "\n"), nonText
}
