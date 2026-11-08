// GoGoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 GoGoClaw contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestGetModelConfig_Found(t *testing.T) {
	cfg := &Config{
		Version: CurrentVersion,
		ModelList: []*ModelConfig{
			{ModelName: "test-model", Model: "openai/gpt-4o", APIKeys: SimpleSecureStrings("key1")},
			{ModelName: "other-model", Model: "anthropic/claude", APIKeys: SimpleSecureStrings("key2")},
		},
	}

	result, err := cfg.GetModelConfig("test-model")
	if err != nil {
		t.Fatalf("GetModelConfig() error = %v", err)
	}
	if result.Model != "openai/gpt-4o" {
		t.Errorf("Model = %q, want %q", result.Model, "openai/gpt-4o")
	}
}

func TestGetModelConfig_NotFound(t *testing.T) {
	cfg := &Config{
		ModelList: []*ModelConfig{
			{ModelName: "test-model", Model: "openai/gpt-4o", APIKeys: SimpleSecureStrings("key1")},
		},
	}

	_, err := cfg.GetModelConfig("nonexistent")
	if err == nil {
		t.Fatal("GetModelConfig() expected error for nonexistent model")
	}
}

func TestLoadConfig_ModelEnabledDefaultsTrueForCurrentVersion(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	data := `{
		"version": 3,
		"model_list": [
			{"model_name": "implicit", "model": "openai/gpt-4o"}
		]
	}`
	if err := os.WriteFile(configPath, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.ModelList) != 1 || !cfg.ModelList[0].IsEnabled() {
		t.Fatalf("model should be enabled when enabled is omitted: %#v", cfg.ModelList)
	}
}

func TestLoadConfig_ExplicitDisabledModelRemainsDisabled(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	data := `{
		"version": 3,
		"model_list": [
			{"model_name": "disabled", "model": "openai/gpt-4o", "enabled": false}
		]
	}`
	if err := os.WriteFile(configPath, []byte(data), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.ModelList[0].IsEnabled() {
		t.Fatal("model with explicit enabled=false should be disabled")
	}
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	reloaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() after save error = %v", err)
	}
	if reloaded.ModelList[0].IsEnabled() {
		t.Fatal("explicit enabled=false should survive save and reload")
	}
}

func TestGetModelConfig_EmptyList(t *testing.T) {
	cfg := &Config{
		ModelList: []*ModelConfig{},
	}

	_, err := cfg.GetModelConfig("any-model")
	if err == nil {
		t.Fatal("GetModelConfig() expected error for empty model list")
	}
}

func TestGetModelConfig_RoundRobin(t *testing.T) {
	cfg := &Config{
		ModelList: []*ModelConfig{
			{ModelName: "lb-model", Model: "openai/gpt-4o-1", APIKeys: SimpleSecureStrings("key1")},
			{ModelName: "lb-model", Model: "openai/gpt-4o-2", APIKeys: SimpleSecureStrings("key2")},
			{ModelName: "lb-model", Model: "openai/gpt-4o-3", APIKeys: SimpleSecureStrings("key3")},
		},
	}

	// Test round-robin distribution
	results := make(map[string]int)
	for range 30 {
		result, err := cfg.GetModelConfig("lb-model")
		if err != nil {
			t.Fatalf("GetModelConfig() error = %v", err)
		}
		results[result.Model]++
	}

	// Each model should appear roughly 10 times (30 calls / 3 models)
	for model, count := range results {
		if count < 5 || count > 15 {
			t.Errorf("Model %s appeared %d times, expected ~10", model, count)
		}
	}
}

func TestGetModelConfig_RoundRobinStartsFromFirstMatch(t *testing.T) {
	rrCounter.Store(0)

	cfg := &Config{
		ModelList: []*ModelConfig{
			{ModelName: "lb-model", Model: "openai/gpt-4o-1", APIKeys: SimpleSecureStrings("key1")},
			{ModelName: "lb-model", Model: "openai/gpt-4o-2", APIKeys: SimpleSecureStrings("key2")},
			{ModelName: "lb-model", Model: "openai/gpt-4o-3", APIKeys: SimpleSecureStrings("key3")},
		},
	}

	wantOrder := []string{
		"openai/gpt-4o-1",
		"openai/gpt-4o-2",
		"openai/gpt-4o-3",
		"openai/gpt-4o-1",
		"openai/gpt-4o-2",
	}

	for i, want := range wantOrder {
		result, err := cfg.GetModelConfig("lb-model")
		if err != nil {
			t.Fatalf("GetModelConfig() call %d error = %v", i, err)
		}
		if result.Model != want {
			t.Fatalf("GetModelConfig() call %d model = %q, want %q", i, result.Model, want)
		}
	}
}

func TestGetModelConfig_Concurrent(t *testing.T) {
	cfg := &Config{
		ModelList: []*ModelConfig{
			{ModelName: "concurrent-model", Model: "openai/gpt-4o-1", APIKeys: SimpleSecureStrings("key1")},
			{ModelName: "concurrent-model", Model: "openai/gpt-4o-2", APIKeys: SimpleSecureStrings("key2")},
		},
	}

	const goroutines = 100
	const iterations = 10

	var wg sync.WaitGroup
	errors := make(chan error, goroutines*iterations)

	for range goroutines {
		wg.Go(func() {
			for range iterations {
				_, err := cfg.GetModelConfig("concurrent-model")
				if err != nil {
					errors <- err
				}
			}
		})
	}

	wg.Wait()
	close(errors)

	for err := range errors {
		t.Errorf("Concurrent GetModelConfig() error: %v", err)
	}
}

func TestModelConfig_StreamingConfig(t *testing.T) {
	t.Run("loads streaming enabled", func(t *testing.T) {
		var cfg ModelConfig
		err := json.Unmarshal([]byte(`{
			"model_name": "stream-model",
			"model": "openai/gpt-5.4",
			"streaming": {"enabled": true}
		}`), &cfg)
		if err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if !cfg.Streaming.Enabled {
			t.Fatal("Streaming.Enabled = false, want true")
		}
	})

	t.Run("preserves explicit disable", func(t *testing.T) {
		var cfg ModelConfig
		err := json.Unmarshal([]byte(`{
			"model_name": "plain-model",
			"model": "openai/gpt-5.4",
			"streaming": {"enabled": false}
		}`), &cfg)
		if err != nil {
			t.Fatalf("Unmarshal() error = %v", err)
		}
		if cfg.Streaming.Enabled {
			t.Fatal("Streaming.Enabled = true, want explicit false preserved")
		}
	})

	t.Run("model streaming only has enabled", func(t *testing.T) {
		typ := reflect.TypeOf(ModelStreamingConfig{})
		if typ.NumField() != 1 {
			t.Fatalf("ModelStreamingConfig field count = %d, want 1", typ.NumField())
		}
		if _, ok := typ.FieldByName("Enabled"); !ok {
			t.Fatal("ModelStreamingConfig missing Enabled field")
		}
	})
}

func TestLoadConfig_ModelStreamingDefaultsEnabled(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
		"version": 3,
		"model_list": [
			{"model_name": "legacy", "provider": "openai", "model": "gpt-5.4"},
			{"model_name": "empty", "provider": "openai", "model": "gpt-5.4", "streaming": {}},
			{"model_name": "disabled", "provider": "openai", "model": "gpt-5.4", "streaming": {"enabled": false}}
		]
	}`)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if len(cfg.ModelList) != 3 {
		t.Fatalf("len(ModelList) = %d, want 3", len(cfg.ModelList))
	}
	if !cfg.ModelList[0].Streaming.Enabled {
		t.Fatal("legacy model streaming = false, want true by default")
	}
	if !cfg.ModelList[1].Streaming.Enabled {
		t.Fatal("empty streaming object = false, want true by default")
	}
	if cfg.ModelList[2].Streaming.Enabled {
		t.Fatal("explicit streaming disable = true, want false")
	}

	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	reloaded, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("reload LoadConfig() error = %v", err)
	}
	if !reloaded.ModelList[0].Streaming.Enabled {
		t.Fatal("saved default streaming = false, want true")
	}
	if reloaded.ModelList[2].Streaming.Enabled {
		securityData, _ := os.ReadFile(filepath.Join(filepath.Dir(configPath), SecurityConfigFile))
		t.Fatalf("saved explicit streaming disable = true, want false\nconfig:\n%s\nsecurity:\n%s", saved, securityData)
	}
}

func TestModelConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  ModelConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ModelConfig{
				ModelName: "test",
				Model:     "openai/gpt-4o",
			},
			wantErr: false,
		},
		{
			name: "valid tool schema transform",
			config: ModelConfig{
				ModelName:           "test",
				Model:               "openai/gpt-4o",
				ToolSchemaTransform: "simple",
			},
			wantErr: false,
		},
		{
			name: "missing model_name",
			config: ModelConfig{
				Model: "openai/gpt-4o",
			},
			wantErr: true,
		},
		{
			name: "missing model",
			config: ModelConfig{
				ModelName: "test",
			},
			wantErr: true,
		},
		{
			name:    "empty config",
			config:  ModelConfig{},
			wantErr: true,
		},
		{
			name: "invalid tool schema transform",
			config: ModelConfig{
				ModelName:           "test",
				Model:               "openai/gpt-4o",
				ToolSchemaTransform: "invalid",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_ValidateModelList(t *testing.T) {
	tests := []struct {
		name    string
		config  *Config
		wantErr bool
		errMsg  string // partial error message to check
	}{
		{
			name: "valid list",
			config: &Config{
				ModelList: []*ModelConfig{
					{ModelName: "test1", Model: "openai/gpt-4o"},
					{ModelName: "test2", Model: "anthropic/claude"},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid entry",
			config: &Config{
				ModelList: []*ModelConfig{
					{ModelName: "test1", Model: "openai/gpt-4o"},
					{ModelName: "", Model: "anthropic/claude"}, // missing model_name
				},
			},
			wantErr: true,
			errMsg:  "model_name is required",
		},
		{
			name: "empty list",
			config: &Config{
				ModelList: []*ModelConfig{},
			},
			wantErr: false,
		},
		{
			// Load balancing: multiple entries with same model_name are allowed
			name: "duplicate model_name for load balancing",
			config: &Config{
				ModelList: []*ModelConfig{},
			},
			wantErr: false, // Changed: duplicates are allowed for load balancing
		},
		{
			// Load balancing: non-adjacent entries with same model_name are also allowed
			name: "duplicate model_name non-adjacent for load balancing",
			config: &Config{
				ModelList: []*ModelConfig{
					{ModelName: "model-a", Model: "openai/gpt-4o"},
					{ModelName: "model-b", Model: "anthropic/claude"},
					{ModelName: "model-a", Model: "openai/gpt-4-turbo"},
				},
			},
			wantErr: false, // Changed: duplicates are allowed for load balancing
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.ValidateModelList()
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateModelList() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateModelList() error = %v, want error containing %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestModelConfig_RequestTimeoutParsing(t *testing.T) {
	jsonData := `{
		"model_name": "slow-local",
		"model": "openai/local-model",
		"api_base": "http://localhost:11434/v1",
		"request_timeout": 300
	}`

	var cfg ModelConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if cfg.RequestTimeout != 300 {
		t.Fatalf("RequestTimeout = %d, want 300", cfg.RequestTimeout)
	}
}

func TestModelConfig_RequestTimeoutDefaultZeroValue(t *testing.T) {
	jsonData := `{
		"model_name": "default-timeout",
		"model": "openai/gpt-4o",
		"api_key": "test-key"
	}`

	var cfg ModelConfig
	if err := json.Unmarshal([]byte(jsonData), &cfg); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if cfg.RequestTimeout != 0 {
		t.Fatalf("RequestTimeout = %d, want 0", cfg.RequestTimeout)
	}
}
