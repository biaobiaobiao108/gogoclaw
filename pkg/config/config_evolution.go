package config

import (
	"encoding/json"
	"strings"
)

type EvolutionConfig struct {
	Enabled         bool     `json:"enabled,omitempty"`
	Mode            string   `json:"mode,omitempty"`
	StateDir        string   `json:"state_dir,omitempty"`
	MinTaskCount    int      `json:"min_task_count,omitempty"`
	MinSuccessRatio float64  `json:"min_success_ratio,omitempty"`
	ColdPathTrigger string   `json:"cold_path_trigger,omitempty"`
	ColdPathTimes   []string `json:"cold_path_times,omitempty"`
	// Deprecated: use MinTaskCount.
	MinCaseCount int `json:"min_case_count,omitempty"`
	// Deprecated: use MinSuccessRatio.
	MinSuccessRate float64 `json:"min_success_rate,omitempty"`
}

func (c EvolutionConfig) MarshalJSON() ([]byte, error) {
	out := struct {
		Enabled         bool     `json:"enabled,omitempty"`
		Mode            string   `json:"mode,omitempty"`
		StateDir        string   `json:"state_dir,omitempty"`
		MinTaskCount    int      `json:"min_task_count,omitempty"`
		MinSuccessRatio float64  `json:"min_success_ratio,omitempty"`
		ColdPathTrigger string   `json:"cold_path_trigger,omitempty"`
		ColdPathTimes   []string `json:"cold_path_times,omitempty"`
	}{
		Enabled:         c.Enabled,
		Mode:            c.Mode,
		StateDir:        c.StateDir,
		MinTaskCount:    c.EffectiveMinTaskCount(),
		MinSuccessRatio: c.EffectiveMinSuccessRatio(),
		ColdPathTrigger: strings.TrimSpace(c.ColdPathTrigger),
		ColdPathTimes:   c.EffectiveColdPathTimes(),
	}
	if !out.Enabled {
		out.Mode = ""
		out.ColdPathTrigger = ""
		out.ColdPathTimes = nil
	}
	return json.Marshal(out)
}

func (c EvolutionConfig) EffectiveMode() string {
	if !c.Enabled {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.Mode)) {
	case "draft":
		return "draft"
	case "apply":
		return "apply"
	case "", "observe":
		return "observe"
	default:
		return "observe"
	}
}

func (c EvolutionConfig) RunsColdPathAutomatically() bool {
	return c.RunsColdPathAfterTurn() || c.RunsColdPathScheduled()
}

func (c EvolutionConfig) ColdPathTriggerMode() string {
	if c.EffectiveMode() != "draft" && c.EffectiveMode() != "apply" {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(c.ColdPathTrigger)) {
	case "", "after_turn":
		return "after_turn"
	case "scheduled":
		return "scheduled"
	case "manual", "none", "off":
		return "manual"
	default:
		return "after_turn"
	}
}

func (c EvolutionConfig) RunsColdPathAfterTurn() bool {
	return c.ColdPathTriggerMode() == "after_turn"
}

func (c EvolutionConfig) RunsColdPathScheduled() bool {
	return c.ColdPathTriggerMode() == "scheduled"
}

func (c EvolutionConfig) EffectiveMinTaskCount() int {
	if c.MinTaskCount > 0 {
		return c.MinTaskCount
	}
	if c.MinCaseCount > 0 {
		return c.MinCaseCount
	}
	return 2
}

func (c EvolutionConfig) EffectiveMinSuccessRatio() float64 {
	if c.MinSuccessRatio > 0 {
		return c.MinSuccessRatio
	}
	if c.MinSuccessRate > 0 {
		return c.MinSuccessRate
	}
	return 0.7
}

func (c EvolutionConfig) EffectiveColdPathTimes() []string {
	out := make([]string, 0, len(c.ColdPathTimes))
	for _, value := range c.ColdPathTimes {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func (c EvolutionConfig) AllowsDraftPublication() bool {
	return c.EffectiveMode() == "apply"
}
