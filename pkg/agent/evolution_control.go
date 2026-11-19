package agent

import (
	"context"

	"github.com/biaobiaobiao108/gogoclaw/pkg/evolution"
)

type EvolutionOverview struct {
	Mode    string                   `json:"mode"`
	Run     evolution.ColdPathStatus `json:"run"`
	Drafts  []evolution.SkillDraft   `json:"drafts"`
	Archive []string                 `json:"archive_suggestions"`
}

func (al *AgentLoop) EvolutionOverview() (EvolutionOverview, error) {
	b := al.currentEvolutionBridge()
	if b == nil {
		return EvolutionOverview{}, evolution.ErrDraftAction
	}
	drafts, err := b.runtime.Drafts(b.workspace)
	if err != nil {
		return EvolutionOverview{}, err
	}
	archive, err := b.runtime.ArchiveSuggestions(b.workspace)
	if err != nil {
		return EvolutionOverview{}, err
	}
	for i := range drafts {
		drafts[i].BodyOrPatch = ""
		drafts[i].BackupPath = ""
		drafts[i].EvidenceIDs = nil
	}
	return EvolutionOverview{
		Mode: b.cfg.EffectiveMode(), Run: b.coldPathRunner.Status(b.workspace), Drafts: drafts, Archive: archive,
	}, nil
}

func (al *AgentLoop) ReviewEvolutionDraft(id string) (evolution.DraftReview, error) {
	b := al.currentEvolutionBridge()
	if b == nil {
		return evolution.DraftReview{}, evolution.ErrDraftAction
	}
	review, err := b.runtime.ReviewDraft(b.workspace, id)
	review.Draft.BackupPath = ""
	return review, err
}

func (al *AgentLoop) RunEvolution() error {
	b := al.currentEvolutionBridge()
	if b == nil || !b.coldPathRunner.Trigger(b.workspace) {
		return evolution.ErrDraftAction
	}
	return nil
}

func (al *AgentLoop) ActOnEvolutionDraft(ctx context.Context, id, action, token string) error {
	b := al.currentEvolutionBridge()
	if b == nil {
		return evolution.ErrDraftAction
	}
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return evolution.ErrDraftAction
	}
	b.wg.Add(1)
	b.closeMu.Unlock()
	defer b.wg.Done()
	return b.runtime.ActOnDraft(ctx, b.workspace, id, action, token)
}
