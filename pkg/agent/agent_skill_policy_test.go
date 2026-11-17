package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
)

func TestAgentSkillRestrictionPreservesEmptyAndIntersectsProfile(t *testing.T) {
	agent := &AgentInstance{SkillsFilter: []string{"alpha"}}
	profile := config.EffectiveTurnProfile{Enabled: true, SkillsMode: config.TurnProfileModeCustom, AllowedSkills: []string{"beta"}}
	allowed := allowedAgentSkills(agent, profile)
	if allowed == nil || len(allowed) != 0 {
		t.Fatalf("disjoint restrictions must deny all skills: %#v", allowed)
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "skills", "beta", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: beta\ndescription: test\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cb := NewContextBuilder(workspace)
	if got := cb.buildSkillsSummary(nil); !strings.Contains(got, "beta") {
		t.Fatalf("missing skill in unrestricted catalog: %q", got)
	}
	if got := cb.buildSkillsSummary(allowed); got != "" {
		t.Fatalf("empty allowlist produced a catalog: %q", got)
	}
	data, err := json.Marshal(config.AgentConfig{Skills: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	var restored config.AgentConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Skills == nil {
		t.Fatalf("empty skills restriction lost: %s", data)
	}
}
