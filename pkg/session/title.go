package session

import (
	"strings"
	"unicode"
)

const (
	// TitleVersion is bumped when the generated title contract changes so a
	// stale generated title can be refreshed on the next Web turn.
	TitleVersion = 3
	// MaxTitleRunes keeps titles aligned with the single-line history menu.
	MaxTitleRunes = 40
)

var titleLabels = []string{
	"conversation title:",
	"chat title:",
	"title:",
	"会话标题：",
	"对话标题：",
	"标题：",
}

var fallbackRequestPrefixes = []string{
	"请你使用",
	"请你用",
	"请使用",
	"请用",
	"请帮我看看",
	"请帮我检查",
	"请帮我处理",
	"麻烦帮我看看",
	"能否帮我看看",
	"可以帮我看看",
	"please help me",
	"could you please",
	"can you please",
	"help me",
	"please",
	"can you",
	"could you",
	"would you",
	"帮我看看",
	"帮我检查",
	"帮我处理",
	"帮我",
	"帮忙",
	"请帮我",
	"使用",
	"我想",
	"我需要",
	"i would like to",
	"i want to",
	"i need to",
	"i want",
	"i need",
	"use ",
}

// NormalizeTitle cleans model output and applies the shared sidebar length limit.
func NormalizeTitle(raw string) string {
	return truncateTitle(normalizeTitleText(raw))
}

// FallbackTitle derives a compact title from the first user message when model
// title generation is unavailable.
func FallbackTitle(raw string) string {
	title := normalizeTitleText(raw)
	for {
		matched := false
		lower := strings.ToLower(title)
		for _, prefix := range fallbackRequestPrefixes {
			if !strings.HasPrefix(lower, prefix) {
				continue
			}
			candidate := strings.TrimSpace(title[len(prefix):])
			candidate = strings.TrimLeft(candidate, " ：:，,、-–—")
			if candidate != "" {
				title = candidate
				matched = true
			}
			break
		}
		if !matched {
			break
		}
	}
	return truncateTitle(normalizeTitleText(title))
}

func normalizeTitleText(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var title string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" && line != "```" {
			title = line
			break
		}
	}
	if title == "" {
		return ""
	}
	title = strings.TrimSpace(strings.TrimLeft(title, "#>-*• \t"))
	lower := strings.ToLower(title)
	for _, label := range titleLabels {
		if strings.HasPrefix(lower, label) {
			title = strings.TrimSpace(title[len(label):])
			break
		}
	}
	title = strings.Join(strings.Fields(title), " ")
	for {
		next := strings.Trim(title, " \t\"'“”‘’`#")
		next = strings.TrimRightFunc(next, isTitlePunctuation)
		next = strings.Trim(next, " \t\"'“”‘’`#")
		if next == title {
			break
		}
		title = next
	}
	return strings.TrimSpace(title)
}

func truncateTitle(title string) string {
	runes := []rune(strings.TrimSpace(title))
	if len(runes) <= MaxTitleRunes {
		return string(runes)
	}

	limit := MaxTitleRunes - 1
	if isASCIIWordRune(runes[limit-1]) && isASCIIWordRune(runes[limit]) {
		for i := limit; i >= MaxTitleRunes/2; i-- {
			if unicode.IsSpace(runes[i-1]) {
				limit = i - 1
				break
			}
		}
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func isASCIIWordRune(r rune) bool {
	return r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))
}

func isTitlePunctuation(r rune) bool {
	if unicode.IsSpace(r) {
		return true
	}
	return strings.ContainsRune(`.,;:!?，。；：！？、…`, r)
}
