package protocol

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"testing"
)

func TestImageRequestRecordsExplicitOutputOptionsOnly(t *testing.T) {
	for _, path := range []string{"/v1/images/generations", "/v1/images/edits"} {
		t.Run(path, func(t *testing.T) {
			request, err := Extract(path, []byte(`{"prompt":"draw a tree","size":"1536x1024","quality":"high","n":2,"output_format":"webp","images":[{"image_url":"data:image/png;base64,private-image"}],"metadata":{"private":"not-for-records"}}`))
			if err != nil {
				t.Fatal(err)
			}
			assertImageOptions(t, request.Parameters)
		})
	}
}

func TestMultipartImageOptionsIgnoreImageAndFileNamedLikeOption(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for name, value := range map[string]string{"model": "image-test", "prompt": "draw a tree", "size": "1536x1024", "quality": "high", "n": "2", "output_format": "webp"} {
		if err := w.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"image", "size"} {
		part, err := w.CreateFormFile(name, "private-file.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte("private-image-bytes")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := ExtractWithContentType("/v1/images/edits", w.FormDataContentType(), body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if request.Text != "draw a tree" || !request.HasNonText {
		t.Fatal("image option logging changed the audited input")
	}
	assertImageOptions(t, request.Parameters)
}

func assertImageOptions(t *testing.T, parameters Parameters) {
	t.Helper()
	data, err := json.Marshal(parameters)
	if err != nil {
		t.Fatal(err)
	}
	var fields struct {
		Size    string `json:"image_size"`
		Quality string `json:"image_quality"`
		Count   int64  `json:"image_count"`
		Format  string `json:"image_output_format"`
	}
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields.Size != "1536x1024" || fields.Quality != "high" || fields.Count != 2 || fields.Format != "webp" {
		t.Fatalf("missing explicit image options: %s", data)
	}
	if bytes.Contains(data, []byte("private")) {
		t.Fatal("stored image/file contents or unrelated metadata")
	}
}

func TestImageOptionsNeverFillDefaultsOrRejectForwardableValues(t *testing.T) {
	for _, body := range []string{`{"prompt":"tree"}`, `{"prompt":"tree","n":"invalid","quality":{},"size":[],"output_format":false}`} {
		request, err := Extract("/v1/images/generations", []byte(body))
		if err != nil || request.Text != "tree" {
			t.Fatalf("metadata options rejected a forwardable prompt: %v", err)
		}
		data, err := json.Marshal(request.Parameters)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("image_")) {
			t.Fatalf("invented output defaults: %s", data)
		}
	}
}
