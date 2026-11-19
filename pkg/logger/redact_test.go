package logger

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactLogTextAndNestedFields(t *testing.T) {
	const apiKey = "sk-abcdefghijklmnopqrstuvwx"
	const bearer = "very-secret-bearer-value"
	input := "request failed Authorization: Bearer " + bearer + " api_key=" + apiKey + " status=401"
	output := RedactText(input)
	if strings.Contains(output, apiKey) || strings.Contains(output, bearer) || !strings.Contains(output, "status=401") {
		t.Fatalf("unexpected text redaction: %s", output)
	}
	value := struct {
		Config map[string]any `json:"config"`
		Status int            `json:"status"`
	}{
		Config: map[string]any{"api_key": "short-secret", "nested": []any{map[string]any{"password": "pwd", "detail": "token=" + bearer}}}, Status: 401,
	}
	encoded, err := json.Marshal(redactStructured(value))
	if err != nil {
		t.Fatal(err)
	}
	result := string(encoded)
	for _, secret := range []string{"short-secret", "pwd", bearer} {
		if strings.Contains(result, secret) {
			t.Fatalf("secret leaked in %s", result)
		}
	}
	if !strings.Contains(result, `"status":401`) {
		t.Fatalf("status lost in %s", result)
	}
}
