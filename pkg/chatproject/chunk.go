package chatproject

import (
	"strings"
	"unicode"
)

const (
	DefaultChunkTokens   = 600
	DefaultOverlapTokens = 80
)

type Chunk struct {
	Ordinal    int
	Locator    string
	Content    string
	TokenCount int
}

func ChunkSections(sections []Section, maxTokens, overlapTokens int) []Chunk {
	if maxTokens <= 0 {
		maxTokens = DefaultChunkTokens
	}
	if overlapTokens < 0 {
		overlapTokens = DefaultOverlapTokens
	}
	overlapTokens = min(overlapTokens, maxTokens/2)
	chunks := make([]Chunk, 0)
	var current strings.Builder
	currentTokens := 0
	currentLocator := ""
	flush := func() {
		content := strings.TrimSpace(current.String())
		if content == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Ordinal: len(chunks) + 1, Locator: currentLocator, Content: content, TokenCount: ApproxTokens(content),
		})
	}
	for _, section := range sections {
		content := strings.TrimSpace(section.Content)
		if content == "" {
			continue
		}
		parts := splitByTokenBudget(content, maxTokens-overlapTokens)
		for i, part := range parts {
			partTokens := ApproxTokens(part)
			locator := section.Locator
			if len(parts) > 1 {
				locator = strings.TrimSpace(locator + " part " + itoa(i+1))
			}
			if currentTokens > 0 && currentLocator != "" && currentLocator != locator {
				flush()
				current.Reset()
				currentTokens = 0
				currentLocator = ""
			}
			if currentTokens > 0 && currentTokens+partTokens > maxTokens {
				flush()
				tail := tokenTail(current.String(), overlapTokens)
				current.Reset()
				if tail != "" {
					current.WriteString(tail)
					current.WriteString("\n\n")
				}
				currentTokens = ApproxTokens(current.String())
				currentLocator = locator
			}
			if currentTokens == 0 {
				currentLocator = locator
			} else if currentLocator == "" {
				currentLocator = locator
			}
			if currentTokens > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(part)
			currentTokens += partTokens
		}
	}
	flush()
	return chunks
}

func ApproxTokens(value string) int {
	cjk := 0
	other := 0
	for _, r := range value {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			cjk++
		} else if !unicode.IsSpace(r) {
			other++
		}
	}
	return cjk + (other+3)/4
}

func SplitByTokenBudget(value string, tokenBudget int) []string {
	return splitByTokenBudget(value, tokenBudget)
}

func splitByTokenBudget(value string, tokenBudget int) []string {
	if tokenBudget <= 0 {
		tokenBudget = DefaultChunkTokens
	}
	if ApproxTokens(value) <= tokenBudget {
		return []string{value}
	}
	runes := []rune(value)
	parts := make([]string, 0)
	for len(runes) > 0 {
		best := len(runes)
		if approxRuneTokens(runes) > tokenBudget {
			low, high := 1, len(runes)
			for low < high {
				mid := (low + high + 1) / 2
				if approxRuneTokens(runes[:mid]) <= tokenBudget {
					low = mid
				} else {
					high = mid - 1
				}
			}
			best = max(1, low)
		}
		cut := best
		for cut > best/2 && cut < len(runes) {
			r := runes[cut]
			if unicode.IsSpace(r) || unicode.IsPunct(r) {
				cut++
				break
			}
			cut--
		}
		if cut <= 0 {
			cut = best
		}
		part := strings.TrimSpace(string(runes[:cut]))
		if part != "" {
			parts = append(parts, part)
		}
		runes = runes[cut:]
		for len(runes) > 0 && unicode.IsSpace(runes[0]) {
			runes = runes[1:]
		}
	}
	return parts
}

func tokenTail(value string, tokenBudget int) string {
	if tokenBudget <= 0 || value == "" {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	for len(runes) > 0 && approxRuneTokens(runes) > tokenBudget {
		runes = runes[1:]
	}
	return strings.TrimSpace(string(runes))
}

func approxRuneTokens(value []rune) int {
	cjk := 0
	other := 0
	for _, r := range value {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			cjk++
		} else if !unicode.IsSpace(r) {
			other++
		}
	}
	return cjk + (other+3)/4
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[i:])
}
