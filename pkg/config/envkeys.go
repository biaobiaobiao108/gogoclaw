// GoGoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package config

import (
	"os"
	"path/filepath"

	"github.com/biaobiaobiao108/gogoclaw/pkg"
)

// Runtime environment variable keys for the gogoclaw process.
// These control the location of files and binaries at runtime and are read
// directly via os.Getenv / os.LookupEnv. All gogoclaw-specific keys use the
// GOGOCLAW_ prefix. Reference these constants instead of inline string
// literals to keep all supported knobs visible in one place and to prevent
// typos.
const (
	// EnvHome overrides the base directory for all gogoclaw data
	// (config, workspace, skills, auth store, …).
	// Default: ~/.gogoclaw
	EnvHome = "GOGOCLAW_HOME"

	// EnvConfig overrides the full path to the JSON config file.
	// Default: $GOGOCLAW_HOME/config.json
	EnvConfig = "GOGOCLAW_CONFIG"

	// EnvBuiltinSkills overrides the directory from which built-in
	// skills are loaded.
	// Default: <cwd>/skills
	EnvBuiltinSkills = "GOGOCLAW_BUILTIN_SKILLS"

	// EnvBinary overrides the path to the gogoclaw executable.
	// Used by the web launcher when spawning the gateway subprocess.
	// Default: resolved from the same directory as the current executable.
	EnvBinary = "GOGOCLAW_BINARY"

	// EnvGatewayHost overrides the host address for the gateway server.
	// Default: "localhost"
	EnvGatewayHost = "GOGOCLAW_GATEWAY_HOST"
)

func GetHome() string {
	homePath, _ := os.UserHomeDir()
	if gogoclawHome := os.Getenv(EnvHome); gogoclawHome != "" {
		homePath = gogoclawHome
	} else if homePath != "" {
		homePath = filepath.Join(homePath, pkg.DefaultGoGoClawHome)
	}
	if homePath == "" {
		homePath = "."
	}
	return homePath
}
