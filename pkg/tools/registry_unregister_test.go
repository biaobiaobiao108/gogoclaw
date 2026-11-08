package tools

import "testing"

func TestToolRegistryUnregisterInvalidatesVersion(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(&mockRegistryTool{name: "dynamic", desc: "dynamic"})
	version := registry.Version()
	if !registry.HasRegistered("dynamic") {
		t.Fatal("registered tool is missing")
	}
	if !registry.Unregister("dynamic") {
		t.Fatal("Unregister() = false, want true")
	}
	if registry.HasRegistered("dynamic") {
		t.Fatal("unregistered tool is still present")
	}
	if registry.Version() <= version {
		t.Fatal("registry version did not change after Unregister")
	}
	if registry.Unregister("dynamic") {
		t.Fatal("second Unregister() = true, want false")
	}
}
