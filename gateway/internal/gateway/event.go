package gateway

import (
	"unicode/utf8"

	"github.com/cipherTing/sael/gateway/internal/privacy"
)

// previewEvent prepares the default operator response without changing the
// stored event or loading text through the list API.
func previewEvent(e Event) Event {
	if e.Text != "" {
		e.TextAvailable = true
		e.TextChars = utf8.RuneCountInString(e.Text)
		e.TextPreview = textPreview(e.Text)
	} else {
		e.TextPreview = textPreview(e.TextPreview)
	}
	e.Text = ""
	return e
}

func textPreview(text string) string {
	chars := 0
	for index := range text {
		if chars == 500 {
			return text[:index]
		}
		chars++
	}
	return text
}

// Redact prepares a record for persistence, including database outage spools.
func (e *Event) Redact() {
	e.Text = privacy.RedactText(e.Text)
	e.TextPreview = privacy.RedactText(e.TextPreview)
	for _, value := range []*string{&e.UserAgent, &e.SessionID, &e.ClientRequestID, &e.Parameters.ReasoningEffort, &e.Parameters.ServiceTier, &e.Parameters.ToolChoice, &e.Parameters.ResponseFormat, &e.Parameters.ThinkingType, &e.Parameters.PreviousResponseID, &e.Parameters.ConversationID} {
		*value = privacy.RedactText(*value)
	}
}
