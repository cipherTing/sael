package protocol

import (
	"bytes"
	"mime/multipart"
	"net/textproto"
	"testing"
)

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

func TestImageGenerationPromptIsMonitored(t *testing.T) {
	got, err := ExtractWithContentType("/v1/images/generations", "application/json", []byte(`{"model":"gpt-image-1","prompt":"draw a quiet lake"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != "openai_images_generations" || got.Model != "gpt-image-1" || got.Text != "draw a quiet lake" || got.HasNonText {
		t.Fatalf("unexpected image generation request: %+v", got)
	}
}

func TestJSONImageEditReviewsPromptOnly(t *testing.T) {
	got, err := ExtractWithContentType("/v1/images/edits", "application/json", []byte(`{"model":"image-test","prompt":"add a tree","images":[{"image_url":"data:image/png;base64,private"}],"mask":"private"}`))
	if err != nil || got.Text != "add a tree" || !got.HasNonText || got.Model != "image-test" {
		t.Fatalf("missed edit prompt: %+v %v", got, err)
	}
}

func TestImageEditExtractsPromptAndIgnoresFiles(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "gpt-image-1")
	_ = writer.WriteField("prompt", "add a sailboat")
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="image"; filename="secret.png"`)
	header.Set("Content-Type", "image/png")
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("image-bytes-must-not-be-sent-to-jev"))
	_ = writer.Close()

	got, err := ExtractWithContentType("/v1/images/edits", writer.FormDataContentType(), body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != "openai_images_edits" || got.Model != "gpt-image-1" || got.Text != "add a sailboat" || !got.HasNonText {
		t.Fatalf("unexpected image edit request: %+v", got)
	}
}

func TestImageVariationIsMonitoredButHasNoPrompt(t *testing.T) {
	got, err := ExtractWithContentType("/v1/images/variations", "multipart/form-data; boundary=missing", []byte{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Protocol != "openai_images_variations" || got.Text != "" || !got.HasNonText {
		t.Fatalf("unexpected image variation request: %+v", got)
	}
}

func TestNonMonitoredEndpointIsNotInCatalog(t *testing.T) {
	if Monitored("openai_embeddings") || Supported("/v1/embeddings") {
		t.Fatal("unmonitored endpoint must remain transparent")
	}
}

func TestEndpointParametersKeepOnlySelectedRequestOptions(t *testing.T) {
	for _, tc := range []struct {
		path, body string
		check      func(Parameters) bool
	}{
		{"/v1/chat/completions", `{"messages":[{"role":"user","content":"hi"}],"reasoning_effort":"high","max_completion_tokens":200,"temperature":0,"tools":[{},{}],"response_format":{"type":"json_schema","schema":{"secret":"ignored"}}}`, func(p Parameters) bool {
			return p.ReasoningEffort == "high" && p.MaxCompletionTokens != nil && *p.MaxCompletionTokens == 200 && p.Temperature != nil && *p.Temperature == 0 && p.ToolCount == 2 && p.ResponseFormat == "json_schema"
		}},
		{"/v1/messages", `{"messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":4096},"output_config":{"effort":"max"},"max_tokens":8192}`, func(p Parameters) bool {
			return p.ThinkingType == "enabled" && p.ThinkingBudget != nil && *p.ThinkingBudget == 4096 && p.ReasoningEffort == "max"
		}},
		{"/v1/responses", `{"input":"hi","conversation":{"id":"conv_123"},"max_output_tokens":"invalid"}`, func(p Parameters) bool { return p.ConversationID == "conv_123" && p.MaxOutputTokens == nil }},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got, err := Extract(tc.path, []byte(tc.body))
			if err != nil || !tc.check(got.Parameters) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}
