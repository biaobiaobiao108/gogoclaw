package evolution

import (
	"math"
	"slices"
	"time"
)

// ArchiveSuggestions reports idle generated skills without changing their files
// or profiles. The retention score loses half its value per 90 idle days.
func (rt *Runtime) ArchiveSuggestions(workspace string) ([]string, error) {
	profiles, err := rt.storeForWorkspace(workspace).LoadProfiles()
	if err != nil {
		return nil, err
	}
	now := rt.now()
	suggestions := make([]string, 0)
	for _, profile := range profiles {
		if profile.WorkspaceID == workspace && shouldSuggestArchive(profile, now) {
			suggestions = append(suggestions, profile.SkillName)
		}
	}
	slices.Sort(suggestions)
	return suggestions, nil
}

func shouldSuggestArchive(profile SkillProfile, now time.Time) bool {
	if profile.Origin != "evolved" || profile.Status == SkillStatusDeleted || profile.LastUsedAt.IsZero() {
		return false
	}
	const halfLife = 90 * 24 * time.Hour
	idle := now.Sub(profile.LastUsedAt)
	return idle >= halfLife && profile.RetentionScore*math.Exp2(-float64(idle)/float64(halfLife)) < 0.3
}

func usesDefaultWorkspaceState(paths Paths, workspace string) bool {
	return paths.RootDir == NewPaths(workspace, "").RootDir
}
