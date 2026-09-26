package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cipherTing/sael/gateway/internal/policy"
	"github.com/cipherTing/sael/gateway/internal/privacy"
	"github.com/cipherTing/sael/gateway/internal/protocol"
)

type performanceStore struct{ *testStore }

func (s *performanceStore) Increment(context.Context, Count) error  { return nil }
func (s *performanceStore) WriteEvent(context.Context, Event) error { return nil }

type performanceClassifier struct{ answers []policy.Answer }

func (c performanceClassifier) Check(context.Context, string) ([]policy.Answer, error) {
	return c.answers, nil
}

// These isolate gateway CPU/allocation cost: persistence and model inference are excluded.
func BenchmarkGatewayRequest(b *testing.B) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()
	for _, size := range []int{1024, 16384} {
		for _, mode := range []string{"disabled", "clean", "hit"} {
			b.Run(fmt.Sprintf("%s/%dB", mode, size), func(b *testing.B) {
				p := activePolicy()
				p.Enabled = mode != "disabled"
				preview := 200
				p.PreviewChars = &preview
				values := map[string]float64{}
				if mode == "hit" {
					values["cyber_abuse"] = .9
				}
				store := &performanceStore{&testStore{policy: p, upstream: UpstreamConfig{BaseURL: upstream.URL}}}
				server := New(store, performanceClassifier{fullAnswers(values)}, "test")
				text := strings.Repeat("The current user asks a routine question. ", size/40+1)[:size]
				raw, _ := json.Marshal(map[string]any{"model": "benchmark", "input": text})
				body := string(raw)
				_, _ = estimateInputTokens(text)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					w := httptest.NewRecorder()
					server.ServeHTTP(w, authorizedRequest("POST", "/v1/responses", strings.NewReader(body)))
					if w.Code != 200 && w.Code != 403 {
						b.Fatal(w.Code)
					}
				}
			})
		}
	}
}
func BenchmarkInputPreflight(b *testing.B) {
	for _, size := range []int{1024, 16384, 131072} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			part := "用户当前输入一段普通文本进行分类。"
			text := strings.Repeat(part, size/len(part)+1)
			_, _ = estimateInputTokens(text)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := estimateInputTokens(text); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
func BenchmarkRequestExtract(b *testing.B) {
	for _, history := range []int{0, 65536, 1048576} {
		b.Run(fmt.Sprintf("history_%dB", history), func(b *testing.B) {
			raw, _ := json.Marshal(map[string]any{"model": "benchmark", "messages": []any{map[string]string{"role": "user", "content": strings.Repeat("x", history)}, map[string]string{"role": "assistant", "content": "reply"}, map[string]string{"role": "user", "content": "current user text"}}})
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			b.ResetTimer()
			for b.Loop() {
				m, err := protocol.Extract("/v1/chat/completions", raw)
				if err != nil || m.Text != "current user text" {
					b.Fatal(m, err)
				}
			}
		})
	}
}
func BenchmarkPromptRedaction(b *testing.B) {
	for _, size := range []int{1024, 16384} {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			text := strings.Repeat("用户当前输入普通文字，凭据 api_key=example-secret-value 和邮件 person@example.com。", size/100+1)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				_ = privacy.RedactText(text)
			}
		})
	}
}
