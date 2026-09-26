package gateway

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelayFlushesSSEBeforeUpstreamCompletes(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: second\n\n")
	}))
	defer upstream.Close()
	s, store, _ := makeServer(t, activePolicy(), &testClassifier{answers: fullAnswers(nil)})
	store.upstream.BaseURL = upstream.URL
	relay := httptest.NewServer(s)
	defer relay.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Post(relay.URL+"/v1/responses", "application/json", strings.NewReader(`{"input":"hello","stream":true}`))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	close(release)
	defer response.Body.Close()
	if err != nil || line != "data: first\n" {
		t.Fatal("stream buffered until completion", line, err)
	}
}
