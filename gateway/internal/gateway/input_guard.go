package gateway

import (
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// DefaultJevInputTokens is a conservative text estimate, not a provider limit.
// Jev documents 32k for state + the longest question, and 64k for the full request;
// the exact integer behind "32k" and its tokenizer are not published.
// Reserve 10% of 32,000 for question text and estimation differences.
// https://docs.typesafe.ai/models
const DefaultJevInputTokens = 28800

// Jev does not publish a tokenizer. cl100k_base is an explicitly approximate,
// local preflight count, not Jev usage or a billing measurement.
var inputEncoder = sync.OnceValues(func() (tokenizer.Codec, error) {
	return tokenizer.Get(tokenizer.Cl100kBase)
})

func estimateInputTokens(text string) (int, error) {
	enc, err := inputEncoder()
	if err != nil {
		return 0, err
	}
	return enc.Count(text)
}

func (c JevConfig) inputLimit() int {
	if c.MaxInputTokens > 0 {
		return c.MaxInputTokens
	}
	return DefaultJevInputTokens
}

// BPE merges bytes, so a text shorter than the token limit in bytes is safe.
// Only return a token estimate when the text could exceed the limit.
func inputTokensOverLimit(text string, limit int) (int, error) {
	if len(text) <= limit {
		return 0, nil
	}
	return estimateInputTokens(text)
}
