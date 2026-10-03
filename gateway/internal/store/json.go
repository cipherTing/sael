package store

import (
	"bytes"
	"encoding/json"
	"strings"
)

// PostgreSQL text and JSONB cannot represent NUL. Replace it with a space
// so adjacent text stays separated; ordinary Unicode and literal escapes stay intact.
func postgresText(value string) string {
	return strings.ReplaceAll(strings.ToValidUTF8(value, "�"), "\x00", " ")
}

// Sanitize only serialized storage data, including batches written by older
// publishers. Skip escaped backslashes so the literal text "\u0000" is preserved.
func marshalPostgresJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil || !bytes.Contains(raw, []byte(`\u0000`)) {
		return raw, err
	}
	for i := 0; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if bytes.HasPrefix(raw[i:], []byte(`\u0000`)) {
			copy(raw[i+2:i+6], "0020")
			i += 5
		} else {
			i++
		}
	}
	return raw, nil
}
