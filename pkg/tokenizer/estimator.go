package tokenizer

import (
	"encoding/json"
	"unicode"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

// EstimateTextTokens returns a locale-aware token estimate. ASCII-heavy
// prompts average roughly 3.5 characters per token, CJK text is close to one
// token per rune, and other non-ASCII text is counted conservatively.
func EstimateTextTokens(text string) int {
	asciiRunes := 0
	cjkRunes := 0
	otherRunes := 0
	wideRunes := 0

	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r),
			unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r),
			unicode.Is(unicode.Hangul, r):
			cjkRunes++
		case r <= unicode.MaxASCII:
			asciiRunes++
		case r > 0xFFFF:
			wideRunes++
		default:
			otherRunes++
		}
	}

	asciiTokens := (asciiRunes*2 + 6) / 7
	otherTokens := (otherRunes + 1) / 2
	return asciiTokens + cjkRunes + otherTokens + wideRunes
}

// EstimateMessageTokens estimates the token count for a single message,
// including Content, ReasoningContent, ToolCalls arguments, ToolCallID
// metadata, and Media items.
func EstimateMessageTokens(msg providers.Message) int {
	contentTokens := EstimateTextTokens(msg.Content)

	// SystemParts are structured system blocks used for cache-aware adapters.
	// They carry the same content as Content, but in multiple blocks.
	// We estimate them as an alternative representation, not additive.
	systemPartsTokens := 0
	if len(msg.SystemParts) > 0 {
		for _, part := range msg.SystemParts {
			systemPartsTokens += EstimateTextTokens(part.Text)
		}
		// Per-part overhead for JSON structure (type, text, cache_control).
		systemPartsTokens += len(msg.SystemParts) * 5
	}

	// Use the larger of the two representations to stay conservative.
	tokens := contentTokens
	if systemPartsTokens > tokens {
		tokens = systemPartsTokens
	}

	tokens += EstimateTextTokens(msg.ReasoningContent)

	for _, tc := range msg.ToolCalls {
		tokens += EstimateTextTokens(tc.ID) + EstimateTextTokens(tc.Type)
		if tc.Function != nil {
			// Count function name + arguments (the wire format for most providers).
			// tc.Name mirrors tc.Function.Name — count only once to avoid double-counting.
			tokens += EstimateTextTokens(tc.Function.Name)
			tokens += EstimateTextTokens(tc.Function.Arguments)
		} else {
			// Fallback: some provider formats use top-level Name without Function.
			tokens += EstimateTextTokens(tc.Name)
		}
	}

	if msg.ToolCallID != "" {
		tokens += EstimateTextTokens(msg.ToolCallID)
	}

	// Per-message overhead for role label, JSON structure, separators.
	tokens += 12

	// Media items (images, files) are serialized by provider adapters into
	// multipart or image_url payloads. Add a fixed per-item token estimate
	// directly (not through the chars heuristic) since actual cost depends
	// on resolution and provider-specific image tokenization.
	const mediaTokensPerItem = 256
	tokens += len(msg.Media) * mediaTokensPerItem

	return tokens
}

// EstimateToolDefsTokens estimates the total token cost of tool definitions
// as they appear in the LLM request.
func EstimateToolDefsTokens(defs []providers.ToolDefinition) int {
	if len(defs) == 0 {
		return 0
	}

	totalTokens := 0
	for _, d := range defs {
		totalTokens += EstimateTextTokens(d.Function.Name)
		totalTokens += EstimateTextTokens(d.Function.Description)

		if d.Function.Parameters != nil {
			if paramJSON, err := json.Marshal(d.Function.Parameters); err == nil {
				totalTokens += EstimateTextTokens(string(paramJSON))
			}
		}

		// Per-tool overhead: type field, JSON structure, separators.
		totalTokens += 5
	}

	return totalTokens
}
