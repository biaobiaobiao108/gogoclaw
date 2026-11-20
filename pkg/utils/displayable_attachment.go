package utils

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

func DurableAttachmentID(attachment providers.Attachment) string {
	path := strings.TrimSpace(attachment.Ref)
	if path == "" || (!strings.HasPrefix(path, "/") && !strings.Contains(path, `:\`)) {
		return ""
	}
	return filepath.Base(path)
}

func DisplayableAttachmentURL(attachment providers.Attachment) string {
	rawURL := strings.TrimSpace(attachment.URL)
	parsed, err := url.Parse(rawURL)
	if strings.HasPrefix(rawURL, "data:image/") ||
		(err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http")) ||
		(err == nil && parsed.Scheme == "" && strings.HasPrefix(rawURL, "/") && !strings.HasPrefix(rawURL, "//")) {
		return rawURL
	}
	return ""
}
