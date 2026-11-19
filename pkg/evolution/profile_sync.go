package evolution

import (
	"strings"
	"time"
)

func applyDraftProfile(workspace string, draft SkillDraft, now time.Time, profile *SkillProfile, exists bool) {
	if !exists {
		origin := "evolved"
		if draft.BaseVersion != "" && draft.BaseVersion != missingSkillVersion {
			origin = "manual"
		}
		*profile = SkillProfile{
			SkillName:   draft.TargetSkillName,
			WorkspaceID: workspace,
			Origin:      origin,
		}
	}

	if profile.CurrentVersion == draft.ID {
		return
	}
	profile.SkillName = draft.TargetSkillName
	profile.WorkspaceID = workspace
	profile.CurrentVersion = draft.ID
	profile.Status = SkillStatusActive
	profile.Origin = profileOrigin(profile.Origin)
	profile.HumanSummary = draft.HumanSummary
	profile.ChangeReason = draft.HumanSummary
	profile.IntendedUseCases = append([]string(nil), draft.IntendedUseCases...)
	profile.PreferredEntryPath = append([]string(nil), draft.PreferredEntryPath...)
	profile.AvoidPatterns = append([]string(nil), draft.AvoidPatterns...)
	profile.LastUsedAt = now
	if !exists {
		profile.RetentionScore = 0.5
	}
	profile.VersionHistory = append(profile.VersionHistory, SkillVersionEntry{
		Version:   draft.ID,
		Action:    string(draft.ChangeKind),
		Timestamp: now,
		DraftID:   draft.ID,
		Summary:   draft.HumanSummary,
	})
}

func inferIntendedUseCases(rule LearningRecord) []string {
	summary := strings.TrimSpace(rule.Summary)
	if summary == "" {
		return nil
	}
	return []string{summary}
}

func inferPreferredEntryPath(rule LearningRecord) []string {
	if len(rule.WinningPath) == 0 {
		return nil
	}
	return append([]string(nil), rule.WinningPath...)
}

func inferAvoidPatterns(rule LearningRecord) []string {
	if len(rule.LateAddedSkills) == 0 || len(rule.WinningPath) <= len(rule.LateAddedSkills) {
		return nil
	}
	prefix := rule.WinningPath[:len(rule.WinningPath)-len(rule.LateAddedSkills)]
	if len(prefix) == 0 {
		return nil
	}
	return []string{
		"avoid starting with " + strings.Join(
			prefix,
			" -> ",
		) + " before using " + strings.Join(
			rule.LateAddedSkills,
			" -> ",
		),
	}
}
