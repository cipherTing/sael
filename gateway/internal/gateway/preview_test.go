package gateway

import (
	"strings"
	"testing"
)

func TestPreviewRedactsCommonSecretsBeforeTruncating(t *testing.T) {
	got := preview("mail alice@example.com key sk-abcdefghijklmnopqrstuvxyz", 100)
	if strings.Contains(got, "alice@example.com") || strings.Contains(got, "sk-abcdefghijklmnopqrstuvxyz") || !strings.Contains(got, "[已隐藏]") {
		t.Fatalf("sensitive preview: %q", got)
	}
	if got := preview("你好世界", 2); got != "你好" {
		t.Fatalf("rune truncation: %q", got)
	}
}
