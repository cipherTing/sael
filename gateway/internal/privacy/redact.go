// Package privacy removes credentials and common personal identifiers from stored text.
package privacy

import (
	"encoding/json"
	"regexp"
	"strings"
)

const hidden = "[已隐藏]"
const credentialKeys = `authorization|proxy[-_]authorization|x[-_]api[-_]key|api[-_]?key|access[-_]?token|refresh[-_]?token|id[-_]?token|session[-_]?token|token|password|passwd|passphrase|secret|client[-_]?secret|private[-_]?key|authorization[-_]?code|code[-_]?verifier|jwt|cookie|set-cookie|secretaccesskey|accesskeyid`

var (
	sensitiveKey = regexp.MustCompile(`(?i)^(?:` + credentialKeys + `|.*[_-](?:token|secret|password|passwd|key))$`)
	credentials  = regexp.MustCompile(`(?i)(["']?\b(?:` + credentialKeys + `)\b["']?\s*[:=]\s*)(?:\[已隐藏\]|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s,;&}\]"']+)`)
	secrets      = []*regexp.Regexp{
		regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z ]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z ]+ )?PRIVATE KEY-----`),
		regexp.MustCompile(`(?i)\b(?:Bearer|Basic)\s+[^\s,"';}]+`),
		regexp.MustCompile(`\b(?:sk-[a-zA-Z0-9_-]{12,}|AIza[0-9A-Za-z_-]{35}|GOCSPX-[0-9A-Za-z_-]{24,}|gh[pousr]_[A-Za-z0-9]{20,})\b`),
		regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
		regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`),
		regexp.MustCompile(`\b(?:\+?86[- ]?)?1[3-9]\d{9}\b`),
		regexp.MustCompile(`\b[1-9][0-9]{5}(?:19|20)[0-9]{2}[01][0-9][0-3][0-9][0-9]{3}[0-9Xx]\b`),
	}
)

// RedactText sanitizes the whole text before preview truncation or persistence.
func RedactText(text string) string {
	if json.Valid([]byte(text)) {
		var value any
		decoder := json.NewDecoder(strings.NewReader(text))
		decoder.UseNumber()
		if decoder.Decode(&value) == nil {
			raw, err := json.Marshal(redactValue(value))
			if err == nil {
				return string(raw)
			}
		}
	}
	return redactPlain(text)
}

func redactPlain(text string) string {
	// Go's Unicode case-folding adds long-s and Kelvin sign to the ASCII folds.
	// ToLower handles Kelvin; normalize long-s so the precheck cannot miss a regex match.
	folded := strings.ReplaceAll(strings.ToLower(text), "ſ", "s")
	hasDigits := strings.ContainsAny(text, "0123456789")
	for i, pattern := range secrets {
		// Required literal markers avoid scanning every ordinary prompt with every pattern.
		switch i {
		case 0:
			if !strings.Contains(text, "PRIVATE KEY") {
				continue
			}
		case 1:
			if !strings.Contains(folded, "bearer") && !strings.Contains(folded, "basic") {
				continue
			}
		case 2:
			if !strings.Contains(text, "sk-") && !strings.Contains(text, "AIza") && !strings.Contains(text, "GOCSPX-") && !strings.Contains(text, "gh") {
				continue
			}
		case 3:
			if !strings.Contains(text, "eyJ") {
				continue
			}
		case 4:
			if !strings.Contains(text, "@") {
				continue
			}
		case 5, 6:
			if !hasDigits {
				continue
			}
		}
		if pattern.MatchString(text) {
			text = pattern.ReplaceAllString(text, hidden)
		}
	}
	for _, marker := range [...]string{"authoriz", "key", "token", "pass", "secret", "code", "verifier", "jwt", "cookie"} {
		if strings.Contains(folded, marker) {
			return credentials.ReplaceAllString(text, `${1}`+hidden)
		}
	}
	return text
}

func redactValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if sensitiveKey.MatchString(key) {
				v[key] = hidden
			} else {
				v[key] = redactValue(item)
			}
		}
	case []any:
		for i, item := range v {
			v[i] = redactValue(item)
		}
	case string:
		return redactPlain(v)
	}
	return value
}
