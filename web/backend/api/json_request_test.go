package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONRequestRejectsOversizedBody(t *testing.T) {
	body := fmt.Sprintf(`{"value":%q}`, strings.Repeat("x", int(maxJSONRequestBodyBytes)))
	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body))
	rec := httptest.NewRecorder()

	var payload struct {
		Value string `json:"value"`
	}
	err := decodeJSONRequest(rec, req, &payload)
	var maxBytesErr *http.MaxBytesError
	if !errors.As(err, &maxBytesErr) {
		t.Fatalf("decodeJSONRequest() error = %v, want MaxBytesError", err)
	}

	writeJSONDecodeError(rec, err)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestReadJSONRequestBodyRejectsOversizedBody(t *testing.T) {
	body := fmt.Sprintf(`{"value":%q}`, strings.Repeat("x", int(maxJSONRequestBodyBytes)))
	req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(body))
	rec := httptest.NewRecorder()

	_, err := readJSONRequestBody(rec, req, maxJSONRequestBodyBytes)
	var maxBytesErr *http.MaxBytesError
	if !errors.As(err, &maxBytesErr) {
		t.Fatalf("readJSONRequestBody() error = %v, want MaxBytesError", err)
	}

	writeJSONReadError(rec, err)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}
