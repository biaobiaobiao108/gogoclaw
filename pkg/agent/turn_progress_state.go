// GoGoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// turnProgress owns the mutable evidence, tool trace, activity, and plan state
// for one turn. Keeping it separate from turnState prevents the control-plane
// fields (lifecycle, cancellation, delivery, and subturn state) from sharing the
// same broad lock and makes the progress invariants independently testable.
type turnProgress struct {
	mu sync.RWMutex

	attemptedSkills   []string
	skillContextTrace []SkillContextSnapshot
	toolKinds         []string
	toolExecutions    []ToolExecutionRecord
	toolItems         []session.TurnToolItem
	toolBatchSeq      int
	readOnlyCalls     map[string]struct{}
	readOnlyRepeats   map[string]int
	retrievalStalls   int
	evidenceSeen      map[string]struct{}
	noProgressStreak  int
	activities        []session.TurnActivity
	activitySeq       int
	plan              []planStep
	lastFailedCall    string
	repeatedFailures  int
	endReason         TurnEndReason
}

func (p *turnProgress) snapshot() ([]planStep, []session.TurnActivity) {
	if p == nil {
		return nil, nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]planStep(nil), p.plan...), append([]session.TurnActivity(nil), p.activities...)
}

func (p *turnProgress) recordRepeatedFailure(key string, failed bool) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !failed {
		p.lastFailedCall = ""
		p.repeatedFailures = 0
		return 0
	}
	if p.lastFailedCall != key {
		p.lastFailedCall = key
		p.repeatedFailures = 0
	}
	p.repeatedFailures++
	return p.repeatedFailures
}

func (p *turnProgress) readOnlyCallSeen(key string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.readOnlyCalls[key]
	return ok
}

func (p *turnProgress) recordReadOnlyCall(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readOnlyCalls == nil {
		p.readOnlyCalls = make(map[string]struct{})
	}
	p.readOnlyCalls[key] = struct{}{}
}

func (p *turnProgress) recordReadOnlyRepeat(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.readOnlyRepeats == nil {
		p.readOnlyRepeats = make(map[string]int)
	}
	p.readOnlyRepeats[key]++
	return p.readOnlyRepeats[key]
}

func (p *turnProgress) invalidateReadOnlyCalls() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readOnlyCalls = nil
	p.readOnlyRepeats = nil
}

func (p *turnProgress) recordToolStateChange() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.readOnlyCalls = nil
	p.readOnlyRepeats = nil
	p.noProgressStreak = 0
}

func (p *turnProgress) setEndReason(reason TurnEndReason) {
	if p == nil || reason == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.endReason == "" || p.endReason == TurnEndReasonCompleted {
		p.endReason = reason
	}
}

func (p *turnProgress) endReasonSnapshot() TurnEndReason {
	if p == nil {
		return TurnEndReasonCompleted
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.endReason == "" {
		return TurnEndReasonCompleted
	}
	return p.endReason
}

func (p *turnProgress) recordRetrievalOutcome(usable bool) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if usable {
		p.retrievalStalls = 0
		return 0
	}
	p.retrievalStalls++
	return p.retrievalStalls
}

func (p *turnProgress) recordNoProgress() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.noProgressStreak++
	return p.noProgressStreak
}

func (p *turnProgress) recordEvidence(hash string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.evidenceSeen == nil {
		p.evidenceSeen = make(map[string]struct{})
	}
	if _, exists := p.evidenceSeen[hash]; exists {
		p.noProgressStreak++
		return
	}
	p.evidenceSeen[hash] = struct{}{}
	p.noProgressStreak = 0
}

func (p *turnProgress) currentNoProgressStreak() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.noProgressStreak
}

func (p *turnProgress) recordToolKind(tool string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, existing := range p.toolKinds {
		if existing == tool {
			return
		}
	}
	p.toolKinds = append(p.toolKinds, tool)
}

func (p *turnProgress) toolKindsSnapshot() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.toolKinds...)
}

func (p *turnProgress) recordToolExecution(tool string, success bool, errorSummary string, skillNames []string) {
	tool = strings.TrimSpace(tool)
	if tool == "" {
		return
	}
	p.recordToolKind(tool)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolExecutions = append(p.toolExecutions, ToolExecutionRecord{
		Name:         tool,
		Success:      success,
		ErrorSummary: strings.TrimSpace(errorSummary),
		SkillNames:   append([]string(nil), skillNames...),
	})
}

func (p *turnProgress) toolExecutionsSnapshot() []ToolExecutionRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.toolExecutions) == 0 {
		return nil
	}
	out := make([]ToolExecutionRecord, 0, len(p.toolExecutions))
	for _, execution := range p.toolExecutions {
		out = append(out, ToolExecutionRecord{
			Name:         execution.Name,
			Success:      execution.Success,
			ErrorSummary: execution.ErrorSummary,
			SkillNames:   append([]string(nil), execution.SkillNames...),
		})
	}
	return out
}

func (p *turnProgress) startActivity(turnID, kind, title, toolCallID, tool, detail string) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activitySeq++
	id := fmt.Sprintf("%s-activity-%d", turnID, p.activitySeq)
	p.activities = append(p.activities, session.TurnActivity{
		ID: id, Kind: kind, Status: "running", Title: title,
		ToolCallID: toolCallID, Tool: tool, Detail: detail, StartedAt: time.Now().UTC(),
	})
	return id
}

func (p *turnProgress) markActivityStarted(id string, startedAt time.Time) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.activities {
		if p.activities[i].ID == id {
			p.activities[i].StartedAt = startedAt.UTC()
			return
		}
	}
}

func (p *turnProgress) finishActivity(id string, update func(*session.TurnActivity)) {
	if p == nil || id == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.activities {
		if p.activities[i].ID != id {
			continue
		}
		update(&p.activities[i])
		now := time.Now().UTC()
		if p.activities[i].CompletedAt == nil {
			p.activities[i].CompletedAt = &now
			p.activities[i].DurationMS = now.Sub(p.activities[i].StartedAt).Milliseconds()
		}
		return
	}
}

func (p *turnProgress) finishRunningActivities(status, detail string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now().UTC()
	for i := range p.activities {
		activity := &p.activities[i]
		if activity.Status != "running" {
			continue
		}
		activity.Status = status
		if detail != "" {
			activity.Detail = strings.TrimSpace(activity.Detail + "\n" + detail)
		}
		activity.CompletedAt = &now
		activity.DurationMS = now.Sub(activity.StartedAt).Milliseconds()
	}
}

func (p *turnProgress) activitiesSnapshot() []session.TurnActivity {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]session.TurnActivity, 0, len(p.activities))
	for _, activity := range p.activities {
		activity.Artifacts = append([]string(nil), activity.Artifacts...)
		out = append(out, activity)
	}
	return out
}

func (p *turnProgress) recordAttemptedSkills(skillNames []string) {
	if len(skillNames) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, skillName := range skillNames {
		skillName = strings.TrimSpace(skillName)
		if skillName == "" {
			continue
		}
		seen := false
		for _, existing := range p.attemptedSkills {
			if existing == skillName {
				seen = true
				break
			}
		}
		if !seen {
			p.attemptedSkills = append(p.attemptedSkills, skillName)
		}
	}
}

func (p *turnProgress) attemptedSkillsSnapshot() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]string(nil), p.attemptedSkills...)
}

func (p *turnProgress) recordSkillContextSnapshot(trigger string, skillNames []string) {
	filtered := make([]string, 0, len(skillNames))
	for _, skillName := range skillNames {
		skillName = strings.TrimSpace(skillName)
		if skillName != "" {
			filtered = append(filtered, skillName)
		}
	}
	if len(filtered) == 0 {
		return
	}
	p.recordAttemptedSkills(filtered)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.skillContextTrace = append(p.skillContextTrace, SkillContextSnapshot{
		Sequence:   len(p.skillContextTrace) + 1,
		Trigger:    trigger,
		SkillNames: append([]string(nil), filtered...),
	})
}

func (p *turnProgress) latestSkillContextSnapshot() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.skillContextTrace) == 0 {
		return nil
	}
	return append([]string(nil), p.skillContextTrace[len(p.skillContextTrace)-1].SkillNames...)
}

func (p *turnProgress) skillContextSnapshotsSnapshot() []SkillContextSnapshot {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.skillContextTrace) == 0 {
		return nil
	}
	snapshots := make([]SkillContextSnapshot, 0, len(p.skillContextTrace))
	for _, snapshot := range p.skillContextTrace {
		snapshots = append(snapshots, SkillContextSnapshot{
			Sequence:   snapshot.Sequence,
			Trigger:    snapshot.Trigger,
			SkillNames: append([]string(nil), snapshot.SkillNames...),
		})
	}
	return snapshots
}

func (p *turnProgress) setPlan(steps []planStep) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.plan = append([]planStep(nil), steps...)
}

func (p *turnProgress) beginToolBatch(turnID string, iteration int, calls []providers.ToolCall, policies []tools.ToolPolicy) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.toolBatchSeq++
	batchID := fmt.Sprintf("%s:tools:%d:%d", turnID, iteration, p.toolBatchSeq)
	now := time.Now().UTC()
	for i, call := range calls {
		policy := policies[i]
		sideEffect := toolItemSideEffectPossible
		if policy.ReadOnly {
			sideEffect = toolItemSideEffectNone
		}
		p.toolItems = append(p.toolItems, session.TurnToolItem{
			BatchID:              batchID,
			Index:                i,
			CallID:               call.ID,
			Tool:                 call.Name,
			ArgumentsFingerprint: toolCallFingerprint(call.Name, call.Arguments),
			Status:               toolItemStatusPending,
			SideEffect:           sideEffect,
			ReadOnly:             policy.ReadOnly,
			Idempotent:           policy.Idempotent,
			Destructive:          policy.Destructive,
			CreatedAt:            now,
		})
	}
	if overflow := len(p.toolItems) - maxTurnToolItems; overflow > 0 {
		p.toolItems = append([]session.TurnToolItem(nil), p.toolItems[overflow:]...)
	}
	return batchID
}

func (p *turnProgress) updateToolItem(batchID string, index int, update func(*session.TurnToolItem)) {
	if p == nil || batchID == "" || update == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.toolItems {
		item := &p.toolItems[i]
		if item.BatchID == batchID && item.Index == index {
			update(item)
			return
		}
	}
}

func (p *turnProgress) toolItemsSnapshot() []session.TurnToolItem {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]session.TurnToolItem(nil), p.toolItems...)
}

func (ts *turnState) recordRepeatedFailure(key string, failed bool) int {
	return ts.progress.recordRepeatedFailure(key, failed)
}

func (ts *turnState) readOnlyCallSeen(key string) bool {
	return ts.progress.readOnlyCallSeen(key)
}

func (ts *turnState) recordReadOnlyCall(key string) {
	ts.progress.recordReadOnlyCall(key)
}

func (ts *turnState) recordReadOnlyRepeat(key string) int {
	return ts.progress.recordReadOnlyRepeat(key)
}

func (ts *turnState) invalidateReadOnlyCalls() {
	ts.progress.invalidateReadOnlyCalls()
}

func (ts *turnState) recordToolStateChange() {
	ts.progress.recordToolStateChange()
}

func (ts *turnState) setEndReason(reason TurnEndReason) {
	ts.progress.setEndReason(reason)
}

func (ts *turnState) endReasonSnapshot() TurnEndReason {
	return ts.progress.endReasonSnapshot()
}

func (ts *turnState) recordRetrievalOutcome(usable bool) int {
	return ts.progress.recordRetrievalOutcome(usable)
}

func (ts *turnState) recordNoProgress() int {
	return ts.progress.recordNoProgress()
}

func (ts *turnState) recordEvidence(hash string) {
	ts.progress.recordEvidence(hash)
}

func (ts *turnState) recordToolKind(tool string) {
	ts.progress.recordToolKind(tool)
}

func (ts *turnState) toolKindsSnapshot() []string {
	return ts.progress.toolKindsSnapshot()
}

func (ts *turnState) recordToolExecution(tool string, success bool, errorSummary string, skillNames []string) {
	ts.progress.recordToolExecution(tool, success, errorSummary, skillNames)
}

func (ts *turnState) toolExecutionsSnapshot() []ToolExecutionRecord {
	return ts.progress.toolExecutionsSnapshot()
}

func (ts *turnState) startTurnActivity(kind, title, toolCallID, tool, detail string) string {
	return ts.progress.startActivity(ts.turnID, kind, title, toolCallID, tool, detail)
}

func (ts *turnState) finishTurnActivity(id string, update func(*session.TurnActivity)) {
	ts.progress.finishActivity(id, update)
}

func (ts *turnState) finishRunningActivities(status, detail string) {
	ts.progress.finishRunningActivities(status, detail)
}

func (ts *turnState) activitiesSnapshot() []session.TurnActivity {
	return ts.progress.activitiesSnapshot()
}

func (ts *turnState) recordAttemptedSkills(skillNames []string) {
	ts.progress.recordAttemptedSkills(skillNames)
}

func (ts *turnState) attemptedSkillsSnapshot() []string {
	return ts.progress.attemptedSkillsSnapshot()
}

func (ts *turnState) recordSkillContextSnapshot(trigger string, skillNames []string) {
	ts.progress.recordSkillContextSnapshot(trigger, skillNames)
}

func (ts *turnState) latestSkillContextSnapshot() []string {
	return ts.progress.latestSkillContextSnapshot()
}

func (ts *turnState) skillContextSnapshotsSnapshot() []SkillContextSnapshot {
	return ts.progress.skillContextSnapshotsSnapshot()
}

func (ts *turnState) setPlan(steps []planStep) {
	ts.progress.setPlan(steps)
}
