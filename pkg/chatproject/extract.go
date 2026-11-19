package chatproject

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

const maxExpandedOOXMLBytes = 32 << 20

var supportedExtensions = map[string]struct{}{
	".pdf": {}, ".docx": {}, ".xlsx": {}, ".pptx": {},
	".md": {}, ".markdown": {}, ".txt": {}, ".csv": {}, ".json": {},
	".go": {}, ".js": {}, ".jsx": {}, ".ts": {}, ".tsx": {}, ".py": {}, ".rs": {},
	".java": {}, ".kt": {}, ".kts": {}, ".c": {}, ".h": {}, ".cc": {}, ".cpp": {},
	".hpp": {}, ".cs": {}, ".rb": {}, ".php": {}, ".swift": {}, ".scala": {}, ".sh": {},
	".ps1": {}, ".sql": {}, ".html": {}, ".htm": {}, ".css": {}, ".scss": {}, ".yaml": {},
	".yml": {}, ".toml": {}, ".xml": {}, ".ini": {}, ".conf": {}, ".log": {},
}

type Section struct {
	Locator string
	Content string
}

func Supported(filename string) bool {
	_, ok := supportedExtensions[strings.ToLower(filepath.Ext(strings.TrimSpace(filename)))]
	return ok
}

func Extract(path, filename string) ([]Section, error) {
	if !Supported(filename) {
		return nil, fmt.Errorf("unsupported file type %q", filepath.Ext(filename))
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf":
		return extractPDF(path)
	case ".docx":
		return extractDOCX(path)
	case ".xlsx":
		return extractXLSX(path)
	case ".pptx":
		return extractPPTX(path)
	default:
		return extractText(path)
	}
}

func extractPDF(path string) ([]Section, error) {
	file, reader, err := pdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open PDF: %w", err)
	}
	defer file.Close()
	sections := make([]Section, 0, reader.NumPage())
	for pageNumber := 1; pageNumber <= reader.NumPage(); pageNumber++ {
		page := reader.Page(pageNumber)
		text, err := page.GetPlainText(nil)
		if err != nil {
			return nil, fmt.Errorf("extract PDF page %d: %w", pageNumber, err)
		}
		if text = normalizeExtractedText(text); text != "" {
			sections = append(sections, Section{Locator: fmt.Sprintf("Page %d", pageNumber), Content: text})
		}
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("PDF contains no extractable text")
	}
	return sections, nil
}

func extractDOCX(path string) ([]Section, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open DOCX: %w", err)
	}
	defer archive.Close()
	entry := findZipEntry(archive.File, "word/document.xml")
	if entry == nil {
		return nil, fmt.Errorf("DOCX document.xml is missing")
	}
	data, err := readZipEntryLimited(entry)
	if err != nil {
		return nil, err
	}
	decoder := xml.NewDecoder(bytes.NewReader(data))
	paragraphs := make([]string, 0)
	var paragraph strings.Builder
	var inText bool
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse DOCX XML: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "p":
				paragraph.Reset()
			case "t":
				inText = true
			case "tab":
				paragraph.WriteByte('\t')
			case "br", "cr":
				paragraph.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				paragraph.Write(value)
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "t":
				inText = false
			case "p":
				if text := normalizeExtractedText(paragraph.String()); text != "" {
					paragraphs = append(paragraphs, text)
				}
			}
		}
	}
	return numberedSections("Paragraph", paragraphs), nil
}

func extractPPTX(path string) ([]Section, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open PPTX: %w", err)
	}
	defer archive.Close()
	entries := make([]*zip.File, 0)
	for _, entry := range archive.File {
		name := strings.ToLower(entry.Name)
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return naturalFileLess(entries[i].Name, entries[j].Name) })
	sections := make([]Section, 0, len(entries))
	for index, entry := range entries {
		data, err := readZipEntryLimited(entry)
		if err != nil {
			return nil, err
		}
		texts := extractXMLTexts(data, "t")
		if text := normalizeExtractedText(strings.Join(texts, "\n")); text != "" {
			sections = append(sections, Section{Locator: fmt.Sprintf("Slide %d", index+1), Content: text})
		}
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("PPTX contains no extractable text")
	}
	return sections, nil
}

func extractXLSX(path string) ([]Section, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open XLSX: %w", err)
	}
	defer archive.Close()
	shared := make([]string, 0)
	if entry := findZipEntry(archive.File, "xl/sharedStrings.xml"); entry != nil {
		data, err := readZipEntryLimited(entry)
		if err != nil {
			return nil, err
		}
		shared = extractXMLTexts(data, "t")
	}
	entries := make([]*zip.File, 0)
	for _, entry := range archive.File {
		name := strings.ToLower(entry.Name)
		if strings.HasPrefix(name, "xl/worksheets/sheet") && strings.HasSuffix(name, ".xml") {
			entries = append(entries, entry)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return naturalFileLess(entries[i].Name, entries[j].Name) })
	sections := make([]Section, 0)
	for sheetIndex, entry := range entries {
		data, err := readZipEntryLimited(entry)
		if err != nil {
			return nil, err
		}
		rows, err := extractXLSXRows(data, shared)
		if err != nil {
			return nil, err
		}
		for rowIndex, cells := range rows {
			if text := normalizeExtractedText(strings.Join(cells, " | ")); text != "" {
				sections = append(sections, Section{
					Locator: fmt.Sprintf("Sheet %d, row %d", sheetIndex+1, rowIndex+1),
					Content: text,
				})
			}
		}
	}
	if len(sections) == 0 {
		return nil, fmt.Errorf("XLSX contains no extractable text")
	}
	return sections, nil
}

func extractXLSXRows(data []byte, shared []string) ([][]string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	rows := make([][]string, 0)
	var row []string
	var current strings.Builder
	var cellType string
	var inValue, inInlineText bool
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse XLSX sheet: %w", err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			switch value.Name.Local {
			case "row":
				row = nil
			case "c":
				current.Reset()
				cellType = xmlAttr(value.Attr, "t")
			case "v":
				inValue = true
			case "t":
				if cellType == "inlineStr" {
					inInlineText = true
				}
			}
		case xml.CharData:
			if inValue || inInlineText {
				current.Write(value)
			}
		case xml.EndElement:
			switch value.Name.Local {
			case "v":
				inValue = false
			case "t":
				inInlineText = false
			case "c":
				cell := normalizeExtractedText(current.String())
				if cellType == "s" {
					if index, err := strconv.Atoi(strings.TrimSpace(cell)); err == nil && index >= 0 && index < len(shared) {
						cell = shared[index]
					}
				}
				row = append(row, cell)
			case "row":
				rows = append(rows, append([]string(nil), row...))
			}
		}
	}
	return rows, nil
}

func extractText(path string) ([]Section, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read text file: %w", err)
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, fmt.Errorf("file contains binary data")
	}
	text := normalizeExtractedText(string(data))
	if text == "" {
		return nil, fmt.Errorf("file contains no extractable text")
	}
	lines := strings.Split(text, "\n")
	sections := make([]Section, 0)
	const linesPerSection = 80
	for start := 0; start < len(lines); start += linesPerSection {
		end := min(len(lines), start+linesPerSection)
		content := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if content == "" {
			continue
		}
		locator := fmt.Sprintf("Lines %d-%d", start+1, end)
		sections = append(sections, Section{Locator: locator, Content: content})
	}
	return sections, nil
}

func numberedSections(label string, values []string) []Section {
	sections := make([]Section, 0, len(values))
	for index, value := range values {
		value = normalizeExtractedText(value)
		if value == "" {
			continue
		}
		sections = append(sections, Section{Locator: fmt.Sprintf("%s %d", label, index+1), Content: value})
	}
	if len(sections) == 0 {
		return nil
	}
	return sections
}

func extractXMLTexts(data []byte, localName string) []string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	values := make([]string, 0)
	var current strings.Builder
	var inText bool
	for {
		token, err := decoder.Token()
		if err != nil {
			break
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == localName {
				current.Reset()
				inText = true
			}
		case xml.CharData:
			if inText {
				current.Write(value)
			}
		case xml.EndElement:
			if value.Name.Local == localName && inText {
				values = append(values, current.String())
				inText = false
			}
		}
	}
	return values
}

func findZipEntry(entries []*zip.File, name string) *zip.File {
	for _, entry := range entries {
		if strings.EqualFold(entry.Name, name) {
			return entry
		}
	}
	return nil
}

func readZipEntryLimited(entry *zip.File) ([]byte, error) {
	if entry.UncompressedSize64 > maxExpandedOOXMLBytes {
		return nil, fmt.Errorf("document XML expands beyond %d bytes", maxExpandedOOXMLBytes)
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("open document XML: %w", err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, maxExpandedOOXMLBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read document XML: %w", err)
	}
	if len(data) > maxExpandedOOXMLBytes {
		return nil, fmt.Errorf("document XML expands beyond %d bytes", maxExpandedOOXMLBytes)
	}
	return data, nil
}

func xmlAttr(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if attr.Name.Local == name {
			return attr.Value
		}
	}
	return ""
}

func naturalFileLess(a, b string) bool {
	na, nb := trailingNumber(a), trailingNumber(b)
	if na >= 0 && nb >= 0 && na != nb {
		return na < nb
	}
	return a < b
}

func trailingNumber(value string) int {
	value = strings.TrimSuffix(value, filepath.Ext(value))
	start := len(value)
	for start > 0 && unicode.IsDigit(rune(value[start-1])) {
		start--
	}
	if start == len(value) {
		return -1
	}
	n, _ := strconv.Atoi(value[start:])
	return n
}

func normalizeExtractedText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\x00", "")
	lines := strings.Split(value, "\n")
	cleaned := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			if !blank && len(cleaned) > 0 {
				cleaned = append(cleaned, "")
			}
			blank = true
			continue
		}
		blank = false
		cleaned = append(cleaned, line)
	}
	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
