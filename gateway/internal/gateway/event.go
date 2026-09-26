package gateway

import "github.com/cipherTing/sael/gateway/internal/privacy"

// Redact prepares a record for persistence, including database outage spools.
func (e *Event) Redact() {
	e.Text = privacy.RedactText(e.Text)
	e.TextPreview = privacy.RedactText(e.TextPreview)
	for _, value := range []*string{&e.UserAgent, &e.SessionID, &e.ClientRequestID, &e.Parameters.ReasoningEffort, &e.Parameters.ServiceTier, &e.Parameters.ToolChoice, &e.Parameters.ResponseFormat, &e.Parameters.ThinkingType, &e.Parameters.PreviousResponseID, &e.Parameters.ConversationID} {
		*value = privacy.RedactText(*value)
	}
}
