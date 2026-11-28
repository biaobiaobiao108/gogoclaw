// GoGoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package channels

import (
	"regexp"
	"strings"
)

// MessageSplitMarker is the delimiter used to split a message into multiple outbound messages.
// When SplitOnMarker is enabled in config, the Manager will split messages on this marker
// and send each part as a separate message.
const MessageSplitMarker = "<|[SPLIT]|>"

// splitMarkerRegex matches the canonical <|[SPLIT]|> marker as well as common
// model-hallucinated variants (e.g. <|SPLIT|>, <[SPLIT]>, <split>, </split>).
var splitMarkerRegex = regexp.MustCompile(`(?i)</?\|?\[?\s*SPLIT\s*\]?\|?>`)

var splitMarkerPrefixes = []string{
	"<|[split]|>",
	"<|split|>",
	"<[split]>",
	"<split>",
	"</split>",
}

// SplitByMarker splits a message by the MessageSplitMarker (and supported variants
// outside Markdown code blocks/inline code) and returns the non-empty trimmed parts.
// If no marker is found, returns a single-element slice containing the original content.
func SplitByMarker(content string) []string {
	if content == "" {
		return nil
	}
	ranges := findSplitMarkerRanges(content)
	if len(ranges) == 0 {
		return []string{content}
	}
	parts := splitContentByRanges(content, ranges)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return []string{content}
	}
	return result
}

func splitContentByRanges(content string, ranges [][2]int) []string {
	parts := make([]string, 0, len(ranges)+1)
	cursor := 0
	for _, r := range ranges {
		parts = append(parts, content[cursor:r[0]])
		cursor = r[1]
	}
	parts = append(parts, content[cursor:])
	return parts
}

func findSplitMarkerRanges(content string) [][2]int {
	if !strings.Contains(content, "<") {
		return nil
	}
	matches := splitMarkerRegex.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return nil
	}
	code := findMarkdownCodeRanges(content)
	filtered := make([][2]int, 0, len(matches))
	for _, m := range matches {
		if len(code) == 0 || !rangeOverlapsAny(m[0], m[1], code) {
			filtered = append(filtered, [2]int{m[0], m[1]})
		}
	}
	return filtered
}

func trimTrailingMarkerPrefix(content string) string {
	lastLT := strings.LastIndexByte(content, '<')
	if lastLT < 0 || len(content)-lastLT > 16 {
		return content
	}
	tail := strings.ToLower(content[lastLT:])
	isPrefix := false
	for _, p := range splitMarkerPrefixes {
		if len(tail) < len(p) && strings.HasPrefix(p, tail) {
			isPrefix = true
			break
		}
	}
	if !isPrefix {
		return content
	}
	if code := findMarkdownCodeRanges(content); rangeOverlapsAny(lastLT, len(content), code) {
		return content
	}
	return content[:lastLT]
}

func rangeOverlapsAny(start, end int, ranges [][2]int) bool {
	for _, r := range ranges {
		if start < r[1] && end > r[0] {
			return true
		}
	}
	return false
}

// findMarkdownCodeRanges returns byte ranges [start, end] for fenced code blocks
// and inline code spans in Markdown content.
func findMarkdownCodeRanges(content string) [][2]int {
	var ranges [][2]int
	n := len(content)
	i := 0
	lineStart := true

	for i < n {
		if lineStart {
			indent := i
			for indent < n && indent-i < 3 && content[indent] == ' ' {
				indent++
			}
			if indent < n && (content[indent] == '`' || content[indent] == '~') {
				ch := content[indent]
				fenceLen := 0
				for indent+fenceLen < n && content[indent+fenceLen] == ch {
					fenceLen++
				}
				if fenceLen >= 3 {
					// Scan until closing fence or EOF.
					j := indent + fenceLen
					for j < n && content[j] != '\n' {
						j++
					}
					if j < n {
						j++
					}
					for j < n {
						ls := j
						ind := ls
						for ind < n && ind-ls < 3 && content[ind] == ' ' {
							ind++
						}
						closeLen := 0
						for ind+closeLen < n && content[ind+closeLen] == ch {
							closeLen++
						}
						lineEnd := ind + closeLen
						if closeLen >= fenceLen {
							onlySpace := true
							for lineEnd < n && content[lineEnd] != '\n' {
								if content[lineEnd] != ' ' && content[lineEnd] != '\t' && content[lineEnd] != '\r' {
									onlySpace = false
									break
								}
								lineEnd++
							}
							if onlySpace {
								if lineEnd < n && content[lineEnd] == '\n' {
									lineEnd++
								}
								j = lineEnd
								break
							}
						}
						for j < n && content[j] != '\n' {
							j++
						}
						if j < n {
							j++
						}
					}
					ranges = append(ranges, [2]int{i, j})
					i = j
					lineStart = true
					continue
				}
			}
			lineStart = false
		}

		if content[i] == '`' {
			tickLen := 0
			for i+tickLen < n && content[i+tickLen] == '`' {
				tickLen++
			}
			j := i + tickLen
			found := false
			for j < n {
				if content[j] == '`' {
					closeLen := 0
					for j+closeLen < n && content[j+closeLen] == '`' {
						closeLen++
					}
					if closeLen == tickLen {
						ranges = append(ranges, [2]int{i, j + closeLen})
						i = j + closeLen
						found = true
						break
					}
					j += closeLen
					continue
				}
				j++
			}
			if found {
				continue
			}
			i += tickLen
			continue
		}

		if content[i] == '\n' {
			lineStart = true
		}
		i++
	}

	return ranges
}
