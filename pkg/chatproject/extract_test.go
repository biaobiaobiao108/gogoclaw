package chatproject

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractDOCX(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.docx")
	writeZipFixture(t, path, map[string]string{
		"word/document.xml": `<?xml version="1.0"?><w:document xmlns:w="w"><w:body>` +
			`<w:p><w:r><w:t>Project brief</w:t></w:r></w:p>` +
			`<w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p></w:body></w:document>`,
	})
	sections, err := Extract(path, "sample.docx")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 2 || sections[0].Content != "Project brief" || sections[1].Locator != "Paragraph 2" {
		t.Fatalf("sections = %#v", sections)
	}
}

func TestExtractXLSXSharedStrings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.xlsx")
	writeZipFixture(t, path, map[string]string{
		"xl/sharedStrings.xml": `<sst xmlns="x"><si><t>Name</t></si><si><t>Alice</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet xmlns="x"><sheetData>` +
			`<row><c t="s"><v>0</v></c><c t="s"><v>1</v></c></row>` +
			`</sheetData></worksheet>`,
	})
	sections, err := Extract(path, "sample.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 || sections[0].Content != "Name | Alice" || sections[0].Locator != "Sheet 1, row 1" {
		t.Fatalf("sections = %#v", sections)
	}
}

func TestChunkSectionsOverlapAndLimit(t *testing.T) {
	sections := []Section{{Locator: "Lines 1-20", Content: strings.Repeat("alpha beta gamma delta\n", 80)}}
	chunks := ChunkSections(sections, 120, 20)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %#v", chunks)
	}
	for _, chunk := range chunks {
		if ApproxTokens(chunk.Content) > 140 {
			t.Fatalf("chunk token count too large: %d", ApproxTokens(chunk.Content))
		}
	}
}

func writeZipFixture(t *testing.T, path string, files map[string]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	archive := zip.NewWriter(file)
	for name, content := range files {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
}
