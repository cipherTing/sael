package gateway

import "unicode/utf8"

// DefaultJevInputChars is the default user input limit. It counts Unicode
// characters and deliberately avoids predicting Jev's tokenizer or usage.
const DefaultJevInputChars = 5000

func (c JevConfig) inputLimit() int {
	if c.MaxInputChars > 0 {
		return c.MaxInputChars
	}
	return DefaultJevInputChars
}

func inputCharsOverLimit(text string, limit int) (int, bool) {
	chars := utf8.RuneCountInString(text)
	return chars, chars > limit
}
