package session

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestNormalizeTitle(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain", raw: "Bun 前端工具链迁移", want: "Bun 前端工具链迁移"},
		{name: "first line only", raw: "标题：Bun 构建迁移\n这里是解释", want: "Bun 构建迁移"},
		{name: "markdown and quotes", raw: "### \"Bun HMR 调研\"。", want: "Bun HMR 调研"},
		{name: "english label", raw: "Title: Bun build migration", want: "Bun build migration"},
		{name: "collapses whitespace", raw: "  Bun   前端\t迁移  ", want: "Bun 前端 迁移"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeTitle(tt.raw); got != tt.want {
				t.Fatalf("NormalizeTitle(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestNormalizeTitleTruncatesToSidebarLimit(t *testing.T) {
	got := NormalizeTitle(strings.Repeat("很长的会话标题", 8))
	if utf8.RuneCountInString(got) > MaxTitleRunes {
		t.Fatalf("title has %d runes, want at most %d: %q", utf8.RuneCountInString(got), MaxTitleRunes, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("truncated title = %q, want ellipsis", got)
	}
}

func TestNormalizeTitleDoesNotSplitEnglishWord(t *testing.T) {
	got := NormalizeTitle("Investigate inconsistent frontend production build behavior")
	if got != "Investigate inconsistent frontend…" {
		t.Fatalf("truncated title = %q, want a word-boundary truncation", got)
	}
}

func TestNormalizeTitleKeepsMixedCJKTopic(t *testing.T) {
	got := NormalizeTitle("AnySearch 搜索今天的 AI 新闻")
	if got != "AnySearch 搜索今天的 AI 新闻" {
		t.Fatalf("truncated title = %q", got)
	}
}

func TestFallbackTitleRemovesRequestFraming(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{raw: "请帮我看看 Bun 构建后的样式问题", want: "Bun 构建后的样式问题"},
		{raw: "Please help me investigate the Bun build issue", want: "investigate the Bun build issue"},
		{raw: "请帮我使用 AnySearch 搜索今天的 AI 新闻", want: "AnySearch 搜索今天的 AI 新闻"},
	}
	for _, tt := range tests {
		if got := FallbackTitle(tt.raw); got != tt.want {
			t.Fatalf("FallbackTitle(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}
