package gateway

import (
	"sync"

	"github.com/tiktoken-go/tokenizer"
)

// DefaultJevInputTokens reserves 10% of Jev's documented 32k state-plus-question budget.
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
