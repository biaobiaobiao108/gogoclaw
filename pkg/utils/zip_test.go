package utils

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestExtractZipFileRejectsTooManyEntries(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "many.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writer := zip.NewWriter(file)
	for i := range maxZipEntries + 1 {
		entry, err := writer.Create(filepath.Join("files", "entry-"+strconv.Itoa(i)))
		if err != nil {
			t.Fatalf("Create entry error = %v", err)
		}
		_ = entry
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close zip writer error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close file error = %v", err)
	}

	err = ExtractZipFile(archivePath, filepath.Join(dir, "out"))
	if err == nil || !strings.Contains(err.Error(), "too many entries") {
		t.Fatalf("ExtractZipFile() error = %v, want entry count error", err)
	}
}

func TestExtractZipFileRejectsTotalUncompressedLimit(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "large.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writer := zip.NewWriter(file)
	chunk := make([]byte, maxZipFileSize)
	for i := range maxZipUncompressedSize/maxZipFileSize + 1 {
		entry, err := writer.Create(filepath.Join("files", "chunk-"+strconv.Itoa(i)))
		if err != nil {
			t.Fatalf("Create entry error = %v", err)
		}
		if _, err := entry.Write(chunk); err != nil {
			t.Fatalf("Write entry error = %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close zip writer error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close file error = %v", err)
	}

	err = ExtractZipFile(archivePath, filepath.Join(dir, "out"))
	if err == nil || !strings.Contains(err.Error(), "uncompressed size exceeds limit") {
		t.Fatalf("ExtractZipFile() error = %v, want total size error", err)
	}
}
