package state

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAtomicSave(t *testing.T) {
	// Create temp workspace
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	// Test SetLastChannel
	err = sm.SetLastChannel("test-channel")
	if err != nil {
		t.Fatalf("SetLastChannel failed: %v", err)
	}

	// Verify the channel was saved
	lastChannel := sm.GetLastChannel()
	if lastChannel != "test-channel" {
		t.Errorf("Expected channel 'test-channel', got '%s'", lastChannel)
	}

	// Verify timestamp was updated
	if sm.GetTimestamp().IsZero() {
		t.Error("Expected timestamp to be updated")
	}

	// Verify state file exists
	stateFile := filepath.Join(tmpDir, "sessions", "sessions.db")
	if _, err := os.Stat(stateFile); os.IsNotExist(err) {
		t.Error("Expected state file to exist")
	}

	// Create a new manager to verify persistence
	sm2 := NewManager(tmpDir)
	if sm2.GetLastChannel() != "test-channel" {
		t.Errorf("Expected persistent channel 'test-channel', got '%s'", sm2.GetLastChannel())
	}
}

func TestSetLastChatID(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	// Test SetLastChatID
	err = sm.SetLastChatID("test-chat-id")
	if err != nil {
		t.Fatalf("SetLastChatID failed: %v", err)
	}

	// Verify the chat ID was saved
	lastChatID := sm.GetLastChatID()
	if lastChatID != "test-chat-id" {
		t.Errorf("Expected chat ID 'test-chat-id', got '%s'", lastChatID)
	}

	// Verify timestamp was updated
	if sm.GetTimestamp().IsZero() {
		t.Error("Expected timestamp to be updated")
	}

	// Create a new manager to verify persistence
	sm2 := NewManager(tmpDir)
	if sm2.GetLastChatID() != "test-chat-id" {
		t.Errorf("Expected persistent chat ID 'test-chat-id', got '%s'", sm2.GetLastChatID())
	}
}

func TestConcurrentAccess(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	// Test concurrent writes
	done := make(chan bool, 10)
	for i := range 10 {
		go func(idx int) {
			channel := fmt.Sprintf("channel-%d", idx)
			sm.SetLastChannel(channel)
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for range 10 {
		<-done
	}

	// Verify the final state is consistent
	lastChannel := sm.GetLastChannel()
	if lastChannel == "" {
		t.Error("Expected non-empty channel after concurrent writes")
	}

	reloaded := NewManager(tmpDir)
	if reloaded.GetLastChannel() != lastChannel {
		t.Fatalf("persisted channel = %q, want %q", reloaded.GetLastChannel(), lastChannel)
	}
}

func TestNewManager_ExistingState(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create initial state
	sm1 := NewManager(tmpDir)
	sm1.SetLastChannel("existing-channel")
	sm1.SetLastChatID("existing-chat-id")

	// Create new manager with same workspace
	sm2 := NewManager(tmpDir)

	// Verify state was loaded
	if sm2.GetLastChannel() != "existing-channel" {
		t.Errorf("Expected channel 'existing-channel', got '%s'", sm2.GetLastChannel())
	}

	if sm2.GetLastChatID() != "existing-chat-id" {
		t.Errorf("Expected chat ID 'existing-chat-id', got '%s'", sm2.GetLastChatID())
	}
}

func TestNewManager_EmptyWorkspace(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "state-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	sm := NewManager(tmpDir)

	// Verify default state
	if sm.GetLastChannel() != "" {
		t.Errorf("Expected empty channel, got '%s'", sm.GetLastChannel())
	}

	if sm.GetLastChatID() != "" {
		t.Errorf("Expected empty chat ID, got '%s'", sm.GetLastChatID())
	}

	if !sm.GetTimestamp().IsZero() {
		t.Error("Expected zero timestamp for new state")
	}
}

func TestNewManager_MkdirFailureDoesNotCrash(t *testing.T) {
	if os.Getenv("BE_CRASHER") == "1" {
		tmpDir := os.Getenv("CRASH_DIR")

		statePath := filepath.Join(tmpDir, "state")
		if err := os.WriteFile(statePath, []byte("I'm a file, not a folder"), 0o644); err != nil {
			fmt.Printf("setup failed: %v", err)
			os.Exit(0)
		}

		NewManager(tmpDir)
		os.Exit(0)
	}

	tmpDir, err := os.MkdirTemp("", "state-crash-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cmd := exec.Command(os.Args[0], "-test.run=TestNewManager_MkdirFailureDoesNotCrash")
	cmd.Env = append(os.Environ(), "BE_CRASHER=1", "CRASH_DIR="+tmpDir)

	err = cmd.Run()
	if err != nil {
		t.Fatalf("NewManager should not crash when state dir creation fails, got: %v", err)
	}
}
