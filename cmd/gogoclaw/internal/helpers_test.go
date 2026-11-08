package internal

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestGetConfigPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("HOME-based expectation is POSIX-specific; see TestGetConfigPath_Windows")
	}
	t.Setenv("HOME", "/tmp/home")

	got := GetConfigPath()
	want := filepath.Join("/tmp/home", ".gogoclaw", "config.json")

	assert.Equal(t, want, got)
}

func TestGetConfigPath_WithGOGOCLAW_HOME(t *testing.T) {
	t.Setenv(config.EnvHome, "/custom/gogoclaw")
	t.Setenv("HOME", "/tmp/home")

	got := GetConfigPath()
	want := filepath.Join("/custom/gogoclaw", "config.json")

	assert.Equal(t, want, got)
}

func TestGetConfigPath_WithGOGOCLAW_CONFIG(t *testing.T) {
	t.Setenv("GOGOCLAW_CONFIG", "/custom/config.json")
	t.Setenv(config.EnvHome, "/custom/gogoclaw")
	t.Setenv("HOME", "/tmp/home")

	got := GetConfigPath()
	want := "/custom/config.json"

	assert.Equal(t, want, got)
}

func TestGetConfigPath_Windows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows-specific HOME behavior varies; run on windows")
	}

	testUserProfilePath := `C:\Users\Test`
	t.Setenv("USERPROFILE", testUserProfilePath)

	got := GetConfigPath()
	want := filepath.Join(testUserProfilePath, ".gogoclaw", "config.json")

	require.True(t, strings.EqualFold(got, want), "GetConfigPath() = %q, want %q", got, want)
}
