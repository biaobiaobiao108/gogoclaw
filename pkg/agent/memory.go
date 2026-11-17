// GoGoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package agent

import (
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/tokenizer"
)

// MemoryStore manages the agent's long-term memory file.
type MemoryStore struct {
	memoryFile string
}

// NewMemoryStore creates a new MemoryStore with the given workspace path.
// It ensures the memory directory exists.
func NewMemoryStore(workspace string) *MemoryStore {
	memoryDir := filepath.Join(workspace, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	os.MkdirAll(memoryDir, 0o755)

	return &MemoryStore{
		memoryFile: memoryFile,
	}
}

// Read bounded excerpts for prompt injection. Original files remain available
// through read_file; large memories must not crowd out the user's current task.
func (ms *MemoryStore) GetMemoryContextWithBudget(budget int) string {
	if budget <= 0 {
		return ""
	}
	readExcerpt := func(path string, tokens int) string {
		f, err := os.Open(path)
		if err != nil {
			return ""
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, int64(tokens)*4+1))
		if err != nil {
			return ""
		}
		return trimMemoryTokens(strings.ToValidUTF8(string(data), ""), tokens)
	}
	longTerm := readExcerpt(ms.memoryFile, budget)
	if longTerm == "" {
		return ""
	}
	return trimMemoryTokens("Memory excerpt; use read_file on memory/MEMORY.md for omitted details.\n\n## Long-term Memory (memory/MEMORY.md)\n\n"+longTerm, budget)
}

func trimMemoryTokens(text string, budget int) string {
	if tokenizer.EstimateTextTokens(text) <= budget {
		return text
	}
	runes := []rune(text)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if tokenizer.EstimateTextTokens(string(runes[:mid])) <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return string(runes[:low])
}
