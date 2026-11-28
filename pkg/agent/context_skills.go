package agent

import (
	"fmt"
	"strings"
)

func (cb *ContextBuilder) buildSkillsSummary(allowed []string) string {
	if cb.skillsLoader == nil {
		return ""
	}
	if allowed == nil {
		return cb.skillsLoader.BuildSkillsSummary()
	}
	allowedSet := cleanAllowedSet(allowed)
	if len(allowedSet) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, "<skills>")
	for _, s := range cb.skillsLoader.ListSkills() {
		if _, ok := allowedSet[strings.ToLower(strings.TrimSpace(s.Name))]; !ok {
			continue
		}
		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", xmlEscapeForPrompt(s.Name)))
		lines = append(
			lines,
			fmt.Sprintf("    <description>%s</description>", xmlEscapeForPrompt(s.Description)),
		)
		lines = append(
			lines,
			fmt.Sprintf("    <location>%s</location>", xmlEscapeForPrompt(s.Path)),
		)
		lines = append(lines, fmt.Sprintf("    <source>%s</source>", xmlEscapeForPrompt(s.Source)))
		lines = append(lines, "  </skill>")
	}
	if len(lines) == 1 {
		return ""
	}
	lines = append(lines, "</skills>")
	return strings.Join(lines, "\n")
}

func xmlEscapeForPrompt(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

func (cb *ContextBuilder) buildActiveSkillsContext(skillNames []string) string {
	ordered := cb.ResolveActiveSkillsForContext(skillNames)
	if len(ordered) == 0 {
		return ""
	}

	content := cb.skillsLoader.LoadSkillsForContext(ordered)
	if strings.TrimSpace(content) == "" {
		return ""
	}

	return fmt.Sprintf(`# Active Skills

The following skills are active for this request. Follow them when relevant.

%s`, content)
}

func (cb *ContextBuilder) ResolveActiveSkillsForContext(skillNames []string) []string {
	if cb.skillsLoader == nil || len(skillNames) == 0 {
		return nil
	}

	var ordered []string
	seen := make(map[string]struct{}, len(skillNames))
	for _, name := range skillNames {
		canonical, ok := cb.ResolveSkillName(name)
		if !ok {
			continue
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		ordered = append(ordered, canonical)
	}
	if len(ordered) == 0 {
		return nil
	}
	return ordered
}

func (cb *ContextBuilder) buildActiveSkillsPromptParts(skillNames []string) []PromptPart {
	skillsText := cb.buildActiveSkillsContext(skillNames)
	if strings.TrimSpace(skillsText) == "" {
		return nil
	}

	return []PromptPart{
		{
			ID:      "capability.active_skills",
			Layer:   PromptLayerCapability,
			Slot:    PromptSlotActiveSkill,
			Source:  PromptSource{ID: PromptSourceActiveSkills, Name: "skill:active"},
			Title:   "active skills",
			Content: skillsText,
			Stable:  false,
			Cache:   PromptCacheNone,
		},
	}
}

func (cb *ContextBuilder) ListSkillNames() []string {
	if cb.skillsLoader == nil {
		return nil
	}

	allSkills := cb.skillsLoader.ListSkills()
	names := make([]string, 0, len(allSkills))
	for _, skill := range allSkills {
		names = append(names, skill.Name)
	}
	return names
}

func (cb *ContextBuilder) ResolveSkillName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || cb.skillsLoader == nil {
		return "", false
	}

	for _, skill := range cb.skillsLoader.ListSkills() {
		if strings.EqualFold(skill.Name, name) {
			return skill.Name, true
		}
	}

	return "", false
}

// GetSkillsInfo returns information about loaded skills.
func (cb *ContextBuilder) GetSkillsInfo() map[string]any {
	allSkills := cb.skillsLoader.ListSkills()
	skillNames := make([]string, 0, len(allSkills))
	for _, s := range allSkills {
		skillNames = append(skillNames, s.Name)
	}
	return map[string]any{
		"total":     len(allSkills),
		"available": len(allSkills),
		"names":     skillNames,
	}
}
