package protocol

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
)

// ExtractModel reads the routing field without extracting prompts or request metadata.
func ExtractModel(path, contentType string, body []byte) (string, error) {
	if (path == "/v1/images/edits" || path == "/v1/images/variations") && strings.HasPrefix(strings.ToLower(contentType), "multipart/") {
		_, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			return "", err
		}
		reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			if part.FormName() == "model" && part.FileName() == "" {
				value, err := io.ReadAll(part)
				return string(value), err
			}
		}
	}
	var value struct {
		Model string `json:"model"`
	}
	err := json.Unmarshal(body, &value)
	return value.Model, err
}
