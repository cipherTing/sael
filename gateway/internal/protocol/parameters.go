package protocol

import (
	"encoding/json"
	"regexp"
	"strings"
)

// Parameters is a fixed allowlist of inbound options; it never includes histories or arbitrary metadata.
type Parameters struct {
	ReasoningEffort     string   `json:"reasoning_effort,omitempty"`
	ServiceTier         string   `json:"service_tier,omitempty"`
	MaxTokens           *int64   `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int64   `json:"max_completion_tokens,omitempty"`
	MaxOutputTokens     *int64   `json:"max_output_tokens,omitempty"`
	Temperature         *float64 `json:"temperature,omitempty"`
	TopP                *float64 `json:"top_p,omitempty"`
	Seed                *int64   `json:"seed,omitempty"`
	ToolCount           int      `json:"tool_count,omitempty"`
	ToolChoice          string   `json:"tool_choice,omitempty"`
	ResponseFormat      string   `json:"response_format,omitempty"`
	ThinkingType        string   `json:"thinking_type,omitempty"`
	ThinkingBudget      *int64   `json:"thinking_budget,omitempty"`
	PreviousResponseID  string   `json:"previous_response_id,omitempty"`
	ConversationID      string   `json:"conversation_id,omitempty"`
}

func extractParameters(path string, body []byte) Parameters {
	var root map[string]json.RawMessage
	_ = json.Unmarshal(body, &root)
	var p Parameters
	// Decode each optional field separately: logging must not reject an otherwise forwardable body.
	_ = json.Unmarshal(root["service_tier"], &p.ServiceTier)
	p.Temperature = optionalNumber[float64](root["temperature"])
	p.TopP = optionalNumber[float64](root["top_p"])
	var tools []json.RawMessage
	_ = json.Unmarshal(root["tools"], &tools)
	p.ToolCount = len(tools)
	_ = json.Unmarshal(root["tool_choice"], &p.ToolChoice)
	if p.ToolChoice == "" {
		var choice struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(root["tool_choice"], &choice)
		p.ToolChoice = choice.Type
	}
	switch path {
	case "/v1/chat/completions":
		_ = json.Unmarshal(root["reasoning_effort"], &p.ReasoningEffort)
		p.MaxTokens = optionalNumber[int64](root["max_tokens"])
		p.MaxCompletionTokens = optionalNumber[int64](root["max_completion_tokens"])
		p.Seed = optionalNumber[int64](root["seed"])
		var format struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(root["response_format"], &format)
		p.ResponseFormat = format.Type
	case "/v1/responses":
		var reasoning struct {
			Effort string `json:"effort"`
		}
		_ = json.Unmarshal(root["reasoning"], &reasoning)
		p.ReasoningEffort = reasoning.Effort
		p.MaxOutputTokens = optionalNumber[int64](root["max_output_tokens"])
		_ = json.Unmarshal(root["previous_response_id"], &p.PreviousResponseID)
		_ = json.Unmarshal(root["conversation"], &p.ConversationID)
		if p.ConversationID == "" {
			var conv struct {
				ID string `json:"id"`
			}
			_ = json.Unmarshal(root["conversation"], &conv)
			p.ConversationID = conv.ID
		}
		var text struct {
			Format struct {
				Type string `json:"type"`
			} `json:"format"`
		}
		_ = json.Unmarshal(root["text"], &text)
		p.ResponseFormat = text.Format.Type
	case "/v1/messages":
		p.MaxTokens = optionalNumber[int64](root["max_tokens"])
		var thinking struct {
			Type   string          `json:"type"`
			Budget json.RawMessage `json:"budget_tokens"`
		}
		_ = json.Unmarshal(root["thinking"], &thinking)
		p.ThinkingType = thinking.Type
		p.ThinkingBudget = optionalNumber[int64](thinking.Budget)
		var output struct {
			Effort string `json:"effort"`
		}
		_ = json.Unmarshal(root["output_config"], &output)
		p.ReasoningEffort = output.Effort
	}
	return p
}

var claudeSession = regexp.MustCompile(`_session_([a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

func anthropicSession(raw json.RawMessage) string {
	var metadata struct {
		UserID string `json:"user_id"`
	}
	if json.Unmarshal(raw, &metadata) != nil {
		return ""
	}
	var identity struct {
		SessionID string `json:"session_id"`
	}
	if json.Unmarshal([]byte(metadata.UserID), &identity) == nil {
		return strings.TrimSpace(identity.SessionID)
	}
	if match := claudeSession.FindStringSubmatch(metadata.UserID); len(match) == 2 {
		return match[1]
	}
	return ""
}

func optionalNumber[T int64 | float64](raw json.RawMessage) *T {
	var value *T
	if json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}
