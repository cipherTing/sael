package protocol

import "testing"

func TestExtractCurrentUserText(t *testing.T) {
	tests := []struct {
		name, path, body, text, protocol, model string
		stream, nonText                         bool
	}{
		{"chat string", "/v1/chat/completions", `{"model":"gpt-4.1","stream":true,"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"reply"},{"role":"user","content":"new"}]}`, "new", "openai_chat", "gpt-4.1", true, false},
		{"chat mixed", "/v1/chat/completions", `{"model":"gpt-4.1","messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"https://example.test/a"}}]}]}`, "look", "openai_chat", "gpt-4.1", false, true},
		{"chat history only", "/v1/chat/completions", `{"messages":[{"role":"user","content":"old"},{"role":"assistant","content":"reply"}]}`, "", "openai_chat", "", false, false},
		{"responses string", "/v1/responses", `{"model":"gpt-4.1","input":"now"}`, "now", "openai_responses", "gpt-4.1", false, false},
		{"responses array", "/v1/responses", `{"input":[{"role":"user","content":[{"type":"input_text","text":"old"}]},{"role":"user","content":[{"type":"input_text","text":"new"},{"type":"input_image","image_url":"https://example.test/a"}]}]}`, "new", "openai_responses", "", false, true},
		{"responses tool only", "/v1/responses", `{"input":[{"role":"user","content":"old"},{"type":"function_call_output","output":"result"}]}`, "", "openai_responses", "", false, false},
		{"anthropic", "/v1/messages", `{"model":"claude-test","messages":[{"role":"user","content":"old"},{"role":"assistant","content":"reply"},{"role":"user","content":[{"type":"text","text":"now"},{"type":"tool_result","content":"ignore"}]}]}`, "now", "anthropic", "claude-test", false, true},
		{"gemini", "/v1beta/models/gemini-test:streamGenerateContent", `{"contents":[{"role":"user","parts":[{"text":"old"}]},{"role":"model","parts":[{"text":"reply"}]},{"role":"user","parts":[{"text":"now"},{"inlineData":{"mimeType":"image/png","data":"abc"}}]}]}`, "now", "gemini", "gemini-test", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Extract(tt.path, []byte(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if got.Text != tt.text || got.Protocol != tt.protocol || got.Model != tt.model || got.Stream != tt.stream || got.HasNonText != tt.nonText {
				t.Fatalf("got %+v, want text=%q protocol=%q model=%q stream=%v nonText=%v", got, tt.text, tt.protocol, tt.model, tt.stream, tt.nonText)
			}
		})
	}
}

func TestExtractRejectsInvalidJSON(t *testing.T) {
	if _, err := Extract("/v1/chat/completions", []byte(`{"messages":`)); err == nil {
		t.Fatal("expected malformed JSON error")
	}
}

func TestExtractRejectsUnsupportedPath(t *testing.T) {
	if _, err := Extract("/other", []byte(`{}`)); err == nil {
		t.Fatal("expected unsupported path error")
	}
}
