package logger

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

var (
	bearerSecret   = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`)
	namedSecret    = regexp.MustCompile(`(?i)(["']?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|bot[_-]?token|token|client[_-]?secret|password|authorization)["']?\s*[:=]\s*["']?)([^\s,"'&}]+)`)
	providerSecret = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|ghp_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,})\b`)
)

// RedactText removes recognizable credential values from diagnostic text.
func RedactText(s string) string {
	s = maskSecrets(s)
	s = bearerSecret.ReplaceAllString(s, `${1}[REDACTED]`)
	s = namedSecret.ReplaceAllString(s, `${1}[REDACTED]`)
	return providerSecret.ReplaceAllString(s, `[REDACTED]`)
}

func secretField(key string) bool {
	key = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "_", ""), "-", ""))
	switch key {
	case "password", "passwd", "apikey", "accesstoken", "refreshtoken", "bottoken", "token", "clientsecret", "authorization", "cookie", "setcookie", "privatekey":
		return true
	}
	return false
}

func redactValue(value any) any {
	switch v := value.(type) {
	case string:
		return RedactText(v)
	case map[string]any:
		for key, item := range v {
			if secretField(key) {
				v[key] = "[REDACTED]"
			} else {
				v[key] = redactValue(item)
			}
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = redactValue(item)
		}
		return v
	default:
		return value
	}
}

// Encode complex values before redaction so nested structs and maps receive the same treatment.
func redactStructured(value any) any {
	data, err := json.Marshal(value)
	if err != nil {
		return "[unserializable field]"
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return "[unserializable field]"
	}
	return redactValue(decoded)
}
