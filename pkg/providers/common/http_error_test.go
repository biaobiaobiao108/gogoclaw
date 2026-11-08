package common

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandleErrorResponse_ReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	err = HandleErrorResponse(resp, server.URL)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("HandleErrorResponse() error = %T, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want %d", httpErr.StatusCode, http.StatusUnauthorized)
	}
	if httpErr.BodyPreview != `{"error":"unauthorized"}` {
		t.Fatalf("BodyPreview = %q", httpErr.BodyPreview)
	}
	if httpErr.RetryAfter != 7*time.Second {
		t.Fatalf("RetryAfter = %s, want 7s", httpErr.RetryAfter)
	}
	if !strings.Contains(err.Error(), "Status: 401") {
		t.Fatalf("Error() should preserve status text, got %q", err.Error())
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name string
		raw  string
		want time.Duration
		min  time.Duration
		max  time.Duration
	}{
		{name: "integer seconds", raw: "5", want: 5 * time.Second},
		{name: "zero seconds", raw: "0", want: 0},
		{name: "negative seconds", raw: "-5", want: 0},
		{name: "future HTTP date", raw: now.Add(90 * time.Second).UTC().Format(http.TimeFormat), min: 80 * time.Second, max: 90 * time.Second},
		{name: "expired HTTP date", raw: now.Add(-time.Minute).UTC().Format(http.TimeFormat), want: 0},
		{name: "invalid value", raw: "later", want: 0},
		{name: "empty value", raw: "", want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseRetryAfter(test.raw)
			if test.min != 0 || test.max != 0 {
				if got < test.min || got > test.max {
					t.Fatalf("parseRetryAfter(%q) = %s, want between %s and %s", test.raw, got, test.min, test.max)
				}
				return
			}
			if got != test.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", test.raw, got, test.want)
			}
		})
	}
}

func TestWrapHTMLResponseError_ReturnsHTTPError(t *testing.T) {
	err := WrapHTMLResponseError(
		http.StatusBadGateway,
		[]byte("<html>bad</html>"),
		"text/html",
		"https://api.example.com",
	)

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("WrapHTMLResponseError() error = %T, want *HTTPError", err)
	}
	if !httpErr.IsHTML {
		t.Fatal("expected IsHTML")
	}
	if !strings.Contains(err.Error(), "HTML instead of JSON") {
		t.Fatalf("Error() should preserve HTML message, got %q", err.Error())
	}
}
