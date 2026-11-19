package evolution

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/biaobiaobiao108/gogoclaw/pkg/config"
	"github.com/biaobiaobiao108/gogoclaw/pkg/logger"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/skills"
)

type RuntimeOptions struct {
	Config              config.EvolutionConfig
	Now                 func() time.Time
	Store               *Store
	PatternClusterer    PatternClusterer
	SuccessJudge        SuccessJudge
	SkillsRecaller      *SkillsRecaller
	DraftGenerator      DraftGenerator
	GeneratorFactory    func(workspace string) DraftGenerator
	SuccessJudgeFactory func(workspace string) SuccessJudge
	RecordModelUsage    func(stage, model string, usage providers.UsageInfo)
}

type Runtime struct {
	cfg                 config.EvolutionConfig
	mu                  sync.Mutex
	now                 func() time.Time
	imported            sync.Map
	store               *Store
	patternClusterer    PatternClusterer
	successJudge        SuccessJudge
	skillsRecaller      *SkillsRecaller
	draftGenerator      DraftGenerator
	generatorFactory    func(workspace string) DraftGenerator
	successJudgeFactory func(workspace string) SuccessJudge
	recordModelUsage    func(stage, model string, usage providers.UsageInfo)
}

type TurnCaseInput struct {
	Workspace             string
	WorkspaceID           string
	TurnID                string
	SessionKey            string
	AgentID               string
	Status                string
	UserMessage           string
	FinalContent          string
	ToolKinds             []string
	ToolExecutions        []ToolExecutionRecord
	ActiveSkillNames      []string
	AttemptedSkillNames   []string
	FinalSuccessfulPath   []string
	SkillContextSnapshots []SkillContextSnapshot
}

func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	now := opts.Now
	if now == nil {
		now = time.Now
	}

	patternClusterer := opts.PatternClusterer
	if patternClusterer == nil {
		patternClusterer = NewHeuristicPatternClusterer(opts.Config.EffectiveMinTaskCount(), now)
	}

	return &Runtime{
		cfg:                 opts.Config,
		now:                 now,
		store:               opts.Store,
		patternClusterer:    patternClusterer,
		successJudge:        opts.SuccessJudge,
		skillsRecaller:      opts.SkillsRecaller,
		draftGenerator:      opts.DraftGenerator,
		generatorFactory:    opts.GeneratorFactory,
		successJudgeFactory: opts.SuccessJudgeFactory,
		recordModelUsage:    opts.RecordModelUsage,
	}, nil
}

func (rt *Runtime) FinalizeTurn(ctx context.Context, input TurnCaseInput) error {
	if rt == nil || !rt.cfg.Enabled || input.Workspace == "" || shouldSkipLearningRecord(input) {
		return nil
	}

	success := input.Status == "completed"
	usedSkillNames := buildUsedSkillNames(input)
	workspaceID := input.Workspace
	createdAt := rt.now()

	record := LearningRecord{
		ID:             buildTaskRecordID(input, createdAt),
		Kind:           RecordKindTask,
		WorkspaceID:    workspaceID,
		CreatedAt:      createdAt,
		SessionKey:     input.SessionKey,
		Summary:        buildRecordSummary(input),
		FinalOutput:    summarizeText(input.FinalContent, 1200),
		Status:         RecordStatus("new"),
		Success:        &success,
		TurnStatus:     input.Status,
		ToolExecutions: learningToolOutcomes(input.ToolExecutions),
		UsedSkillNames: append([]string(nil), usedSkillNames...),
	}

	store := rt.storeForWorkspace(input.Workspace)
	if err := rt.importWorkspace(store); err != nil {
		return err
	}
	if err := store.AppendTaskRecord(ctx, record); err != nil {
		return err
	}

	if err := rt.recordSkillUsage(input, success); err != nil {
		return err
	}

	logger.DebugCF("evolution", "Recorded hot path learning record", map[string]any{
		"workspace":   input.Workspace,
		"turn_id":     input.TurnID,
		"success":     success,
		"used_skills": len(record.UsedSkillNames),
	})
	return nil
}

func buildTaskRecordID(input TurnCaseInput, createdAt time.Time) string {
	base := strings.TrimSpace(input.TurnID)
	if base == "" {
		base = "turn"
	}
	base = validSkillNameOrEmpty(base)
	if base == "" {
		base = "turn"
	}
	seed := strings.Join([]string{
		input.Workspace,
		input.SessionKey,
		input.AgentID,
		input.TurnID,
		createdAt.UTC().Format(time.RFC3339Nano),
	}, "\x00")
	sum := sha1.Sum([]byte(seed))
	return base + "-" + hex.EncodeToString(sum[:6])
}

func buildRecordSummary(input TurnCaseInput) string {
	if goal := summarizeText(input.UserMessage, 160); goal != "" {
		return goal
	}
	return fmt.Sprintf("turn %s finished with status=%s", input.TurnID, input.Status)
}

func summarizeText(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxLen <= 0 {
		return text
	}
	if utf8.RuneCountInString(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		runes := []rune(text)
		return string(runes[:maxLen])
	}
	runes := []rune(text)
	return string(runes[:maxLen-3]) + "..."
}

func buildUsedSkillNames(input TurnCaseInput) []string {
	if final := uniqueTrimmedNames(input.FinalSuccessfulPath); len(final) > 0 {
		return final
	}
	out := make([]string, 0)
	for _, exec := range input.ToolExecutions {
		if !exec.Success {
			continue
		}
		out = append(out, exec.SkillNames...)
	}
	return uniqueTrimmedNames(out)
}

func shouldSkipLearningRecord(input TurnCaseInput) bool {
	if strings.EqualFold(strings.TrimSpace(input.SessionKey), "heartbeat") {
		return true
	}
	return false
}

func uniqueTrimmedNames(values []string) []string {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func (rt *Runtime) RunColdPathOnce(ctx context.Context, workspace string) error {
	if rt == nil || !rt.cfg.Enabled || workspace == "" {
		return nil
	}

	mode := rt.cfg.EffectiveMode()
	runID := fmt.Sprintf("%d", rt.now().UnixNano())
	if mode == "" || mode == "observe" {
		logger.DebugCF("evolution", "Skipped cold path run", map[string]any{
			"workspace": workspace,
			"mode":      mode,
			"run_id":    runID,
		})
		return nil
	}

	logger.InfoCF("evolution", "Started cold path run", map[string]any{
		"workspace": workspace,
		"mode":      mode,
		"run_id":    runID,
	})

	unlock, err := rt.lockWorkspace(workspace)
	if err != nil {
		return err
	}
	defer unlock()

	if rt.recordModelUsage != nil {
		ctx = context.WithValue(ctx, modelUsageKey{}, rt.recordModelUsage)
	}
	store := rt.storeForWorkspace(workspace)
	if err := rt.importWorkspace(store); err != nil {
		return err
	}
	evidenceLimit := max(maxColdPathEvidence, rt.cfg.EffectiveMinTaskCount())
	taskRecords, err := store.loadPendingTasks(ctx, evidenceLimit)
	if err != nil {
		return err
	}
	patternRecords, err := store.LoadPatternRecords()
	if err != nil {
		return err
	}
	logger.DebugCF("evolution", "Loaded evolution records", map[string]any{
		"workspace":     workspace,
		"task_count":    len(taskRecords),
		"pattern_count": len(patternRecords),
		"run_id":        runID,
	})

	admittedCount := 0
	newRuleCount := 0
	if rt.patternClusterer != nil {
		var plans []llmCluster
		clusterer, planned := rt.patternClusterer.(*LLMPatternClusterer)
		inputs := taskRecords
		if planned {
			plans, inputs = clusterer.planPatterns(ctx, workspace, taskRecords, patternRecords, rt.cfg.EffectiveMinSuccessRatio())
		}
		recordsForOrganizer, evidenceRecordsForOrganizer, inputErr := rt.recordsForColdPathInputs(
			ctx,
			workspace,
			inputs,
		)
		if inputErr != nil {
			return inputErr
		}
		decisions := evidenceRecordsForOrganizer
		if planned && len(taskRecords) == evidenceLimit {
			// Rotate a full window so ungrouped rows cannot hide newer evidence.
			now := rt.now().UTC()
			for i := range taskRecords {
				taskRecords[i].ConsideredAt = &now
			}
			decisions = mergeLearningRecordsByID(taskRecords, decisions)
		}
		if !planned {
			recordsForOrganizer = rt.filterRecordsByMinSuccessRatio(workspace, evidenceRecordsForOrganizer, recordsForOrganizer)
		}
		if err := store.SaveTaskDecisions(decisions); err != nil {
			return err
		}
		admittedCount = countTaskLearningRecords(recordsForOrganizer)
		logger.DebugCF("evolution", "Admitted task records for cold path", map[string]any{
			"workspace":       workspace,
			"admitted_tasks":  admittedCount,
			"organizer_input": len(recordsForOrganizer),
			"task_ids":        joinRecordIDs(recordsForOrganizer),
			"run_id":          runID,
		})
		var rules []LearningRecord
		var clusteredTaskIDs []string
		if planned {
			rules, clusteredTaskIDs = clusterer.validateAndBuildPatternsWithEvidence(
				workspace, plans, recordsForOrganizer, evidenceRecordsForOrganizer, patternRecords, rt.cfg.EffectiveMinSuccessRatio(),
			)
		} else if len(evidenceRecordsForOrganizer) == 0 || (len(patternRecords) == 0 && admittedCount < rt.cfg.EffectiveMinTaskCount()) {
			// Keep gathering evidence until there are enough successful samples.
		} else if clusterer, ok := rt.patternClusterer.(evidencePatternClusterer); ok {
			rules, clusteredTaskIDs, err = clusterer.BuildPatternsWithEvidence(
				ctx,
				workspace,
				recordsForOrganizer,
				evidenceRecordsForOrganizer,
				patternRecords,
				rt.cfg.EffectiveMinSuccessRatio(),
			)
		} else if admittedCount > 0 {
			rules, clusteredTaskIDs, err = rt.patternClusterer.BuildPatterns(
				ctx,
				workspace,
				recordsForOrganizer,
				patternRecords,
			)
		}
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		newRuleCount = countNewPatterns(patternRecords, rules, workspace)
		logger.DebugCF("evolution", "Built learning patterns", map[string]any{
			"workspace":      workspace,
			"pattern_count":  len(rules),
			"new_patterns":   newRuleCount,
			"admitted_tasks": admittedCount,
			"patterns":       summarizePatternRecords(rules),
			"run_id":         runID,
		})
		if len(rules) > 0 {
			merged := mergePatternRecords(patternRecords, rules, workspace)
			if mergeErr := store.MergePatternRecords(rules); mergeErr != nil {
				return mergeErr
			}
			patternRecords = merged
		}
		if len(clusteredTaskIDs) > 0 {
			if markErr := markTaskRecordsClustered(store, clusteredTaskIDs); markErr != nil {
				return markErr
			}
		}
	}

	generator := rt.draftGeneratorForWorkspace(workspace)
	if generator == nil {
		logger.DebugCF("evolution", "Skipped drafting because no draft generator is available", map[string]any{
			"workspace": workspace,
			"run_id":    runID,
		})
		return nil
	}

	recaller := rt.skillsRecallerForWorkspace(workspace)
	readyRules := filterReadyRules(patternRecords, workspace)
	if len(readyRules) == 0 {
		logger.DebugCF("evolution", "Finished cold path run without ready patterns", map[string]any{
			"workspace":      workspace,
			"record_count":   len(taskRecords),
			"new_patterns":   newRuleCount,
			"admitted_tasks": admittedCount,
			"run_id":         runID,
		})
		return nil
	}

	existingDrafts, err := store.LoadDrafts()
	if err != nil {
		return err
	}
	processedRules := 0
	const maxDraftsPerRun = 8
	for _, rule := range readyRules {
		if processedRules == maxDraftsPerRun {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		version := evidenceVersion(rule)
		if hasDraftForEvidence(existingDrafts, workspace, rule.ID, version, rule.UpdatedAt) {
			continue
		}
		evidenceTasks, err := store.loadTaskEvidence(ctx, recentEvidenceIDs(patternEvidenceIDs(rule)))
		if err != nil {
			return err
		}
		evidence := draftEvidenceForRule(rule, evidenceTasks)
		rule = enrichRuleWithDraftEvidence(rule, evidence)
		if shouldWaitForMoreDraftEvidence(existingDrafts, rule) {
			continue
		}
		matches, err := recaller.RecallSimilarSkills(rule)
		if err != nil {
			return err
		}
		logger.DebugCF("evolution", "Generating skill draft", map[string]any{
			"workspace":           workspace,
			"pattern_id":          rule.ID,
			"matched_skill_count": len(matches),
			"pattern_info":        summarizePatternRecord(rule),
			"run_id":              runID,
		})

		draft, err := generateDraftWithEvidence(ctx, generator, rule, matches, evidence)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		draft.ID = "draft-" + rule.ID + "-" + version[:12]
		draft.EvidenceVersion = version
		draft.EvidenceIDs = append([]string(nil), recentEvidenceIDs(patternEvidenceIDs(rule))...)
		draft.EvidenceCount = len(patternEvidenceIDs(rule))
		draft.EvidenceSuccesses = len(rule.TaskRecordIDs)
		draft.EvidenceRoute = draftEvidenceRoute(rule)
		draft = rt.finalizeDraft(workspace, rule, matches, evidence, draft)
		if draft.Status == DraftStatusCandidate {
			current, exists, loadErr := loadCurrentSkillBody(workspace, draft.TargetSkillName)
			if loadErr == nil {
				_, loadErr = checkedRenderedDraft(draft, current, exists)
			}
			if loadErr != nil {
				draft.Status = DraftStatusQuarantined
				draft.ScanFindings = append(draft.ScanFindings, loadErr.Error())
			} else {
				draft.BaseVersion = skillVersion(current, exists)
			}
		}
		changes := []SkillDraft{draft}
		if draft.Status == DraftStatusCandidate {
			for _, previous := range existingDrafts {
				if previous.WorkspaceID == workspace && previous.SourceRecordID == rule.ID &&
					previous.Status == DraftStatusCandidate && previous.PublishedVersion == "" && previous.ID != draft.ID {
					previous.Status = DraftStatusSuperseded
					changes = append(changes, previous)
				}
			}
		}
		if err := store.SaveDrafts(changes); err != nil {
			return err
		}
		processedRules++
	}

	logger.InfoCF("evolution", "Finished cold path run", map[string]any{
		"workspace":          workspace,
		"ready_patterns":     len(readyRules),
		"processed_patterns": processedRules,
		"new_patterns":       newRuleCount,
		"run_id":             runID,
	})
	return nil
}

const maxColdPathEvidence = 48

func (rt *Runtime) recordsForColdPathInputs(
	ctx context.Context,
	workspace string,
	records []LearningRecord,
) ([]LearningRecord, []LearningRecord, error) {
	var candidates []LearningRecord
	for _, record := range records {
		if record.WorkspaceID == workspace && coldPathEvidenceRejectReason(record) == "" {
			candidates = append(candidates, record)
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if (a.ConsideredAt == nil) != (b.ConsideredAt == nil) {
			return a.ConsideredAt == nil
		}
		if a.ConsideredAt != nil && !a.ConsideredAt.Equal(*b.ConsideredAt) {
			return a.ConsideredAt.Before(*b.ConsideredAt)
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	judge := rt.successJudgeForWorkspace(workspace)
	judgeKey := ""
	if keyed, ok := judge.(interface{ cacheKey() string }); ok {
		judgeKey = keyed.cacheKey()
	}
	now := rt.now().UTC()
	var pending []int
	for i := range candidates {
		record := &candidates[i]
		record.ConsideredAt = &now
		if record.TurnStatus != "" {
			completed := record.TurnStatus == "completed"
			record.Success = &completed
		}
		if record.Success == nil || !*record.Success || judge == nil {
			continue
		}
		if judgeKey != "" && record.JudgedBy == judgeKey && record.JudgedSuccess != nil {
			record.Success = record.JudgedSuccess
			continue
		}
		pending = append(pending, i)
	}
	if err := judgePendingTasks(ctx, judge, judgeKey, candidates, pending); err != nil {
		return nil, nil, err
	}
	var admitted []LearningRecord
	for _, record := range candidates {
		if record.Success != nil && *record.Success {
			admitted = append(admitted, record)
		}
	}
	return admitted, candidates, nil
}

func judgePendingTasks(ctx context.Context, judge SuccessJudge, judgeKey string, records []LearningRecord, pending []int) error {
	batch, batched := judge.(batchSuccessJudge)
	batchSize := 1
	if batched {
		batchSize = successJudgeBatchSize
	}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		indices := pending[:min(batchSize, len(pending))]
		var decisions []TaskSuccessDecision
		var err error
		if batched {
			selected := make([]LearningRecord, len(indices))
			for i, index := range indices {
				selected[i] = records[index]
			}
			decisions, err = batch.judgeTaskRecords(ctx, selected)
		} else {
			var decision TaskSuccessDecision
			decision, err = judge.JudgeTaskRecord(ctx, records[indices[0]])
			decisions = []TaskSuccessDecision{decision}
		}
		if err != nil {
			return err
		}
		known := 0
		for i, index := range indices {
			record := &records[index]
			if decisions[i].Deferred {
				record.Success, record.JudgedSuccess, record.JudgedBy = nil, nil, ""
				continue
			}
			record.Success = &decisions[i].Success
			record.JudgedSuccess, record.JudgedBy = record.Success, judgeKey
			known++
		}
		pending = pending[len(indices):]
		if known == 0 {
			for _, index := range pending {
				records[index].Success = nil
			}
			break
		}
	}
	return ctx.Err()
}

func learningToolOutcomes(executions []ToolExecutionRecord) []ToolExecutionRecord {
	var outcomes []ToolExecutionRecord
	for _, execution := range executions[:min(len(executions), 16)] {
		outcomes = append(outcomes, ToolExecutionRecord{Name: execution.Name, Success: execution.Success})
	}
	return outcomes
}

func (rt *Runtime) filterRecordsByMinSuccessRatio(
	workspace string,
	allRecords []LearningRecord,
	admittedRecords []LearningRecord,
) []LearningRecord {
	minRatio := rt.cfg.EffectiveMinSuccessRatio()
	if minRatio <= 0 {
		return admittedRecords
	}

	type successStats struct {
		success int
		total   int
	}
	statsByKey := make(map[string]successStats)
	for _, record := range allRecords {
		key, ok := coldPathSuccessRatioKey(workspace, record)
		if !ok {
			continue
		}
		stats := statsByKey[key]
		stats.total++
		if record.Success != nil && *record.Success {
			stats.success++
		}
		statsByKey[key] = stats
	}

	out := make([]LearningRecord, 0, len(admittedRecords))
	for _, record := range admittedRecords {
		if !isTaskRecordKind(record.Kind) {
			out = append(out, record)
			continue
		}
		key, ok := coldPathSuccessRatioKey(workspace, record)
		if !ok {
			continue
		}
		stats := statsByKey[key]
		if stats.total == 0 {
			continue
		}
		ratio := float64(stats.success) / float64(stats.total)
		if ratio < minRatio {
			logger.DebugCF("evolution", "Rejected task record below cold path success ratio", map[string]any{
				"workspace":         workspace,
				"record_id":         record.ID,
				"success_ratio":     ratio,
				"min_success_ratio": minRatio,
				"success_count":     stats.success,
				"total_count":       stats.total,
			})
			continue
		}
		out = append(out, record)
	}
	return out
}

func coldPathSuccessRatioKey(workspace string, record LearningRecord) (string, bool) {
	if !isTaskRecordKind(record.Kind) || record.WorkspaceID != workspace {
		return "", false
	}
	if record.Status != "" && record.Status != RecordStatus("new") {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(record.SessionKey), "heartbeat") {
		return "", false
	}
	if strings.EqualFold(strings.TrimSpace(record.FinalOutput), "HEARTBEAT_OK") {
		return "", false
	}
	if strings.TrimSpace(record.Summary) == "" {
		return "", false
	}
	key := heuristicClusterKey(record)
	if key == "" {
		return "", false
	}
	return key, true
}

func coldPathEvidenceRejectReason(record LearningRecord) string {
	if !isTaskRecordKind(record.Kind) {
		return "not a task record"
	}
	if record.Success == nil {
		return "task success unknown"
	}
	if record.Status != "" && record.Status != RecordStatus("new") {
		return "task already processed"
	}
	if strings.EqualFold(strings.TrimSpace(record.SessionKey), "heartbeat") {
		return "heartbeat session"
	}
	if strings.EqualFold(strings.TrimSpace(record.FinalOutput), "HEARTBEAT_OK") {
		return "heartbeat output"
	}
	if strings.TrimSpace(record.Summary) == "" {
		return "missing summary"
	}
	if *record.Success && strings.TrimSpace(record.FinalOutput) == "" && len(record.ToolExecutions) == 0 {
		return "missing completion evidence"
	}
	return ""
}

func (rt *Runtime) storeForWorkspace(workspace string) *Store {
	paths := NewPaths(workspace, rt.cfg.StateDir)
	if rt.store != nil && rt.store.paths.RootDir == paths.RootDir && rt.store.paths.Workspace == paths.Workspace {
		return rt.store
	}
	return NewStore(paths)
}

func (rt *Runtime) skillsRecallerForWorkspace(workspace string) *SkillsRecaller {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.skillsRecaller == nil || rt.skillsRecaller.workspace != workspace {
		rt.skillsRecaller = NewSkillsRecaller(workspace)
	}
	return rt.skillsRecaller
}

func (rt *Runtime) draftGeneratorForWorkspace(workspace string) DraftGenerator {
	if rt.generatorFactory != nil {
		if generator := rt.generatorFactory(workspace); generator != nil {
			return generator
		}
	}
	if rt.draftGenerator != nil {
		return rt.draftGenerator
	}
	return NewDefaultDraftGenerator(workspace)
}

func (rt *Runtime) successJudgeForWorkspace(workspace string) SuccessJudge {
	if rt.successJudgeFactory != nil {
		if judge := rt.successJudgeFactory(workspace); judge != nil {
			return judge
		}
	}
	if rt.successJudge != nil {
		return rt.successJudge
	}
	return &HeuristicSuccessJudge{}
}

func (rt *Runtime) finalizeDraft(
	workspace string,
	rule LearningRecord,
	matches []skills.SkillInfo,
	evidence DraftEvidence,
	draft SkillDraft,
) SkillDraft {
	if draft.ID == "" {
		draft.ID = "draft-" + rule.ID
	}
	if draft.CreatedAt.IsZero() {
		draft.CreatedAt = rt.now()
	}
	draft.WorkspaceID = workspace
	draft.SourceRecordID = rule.ID
	draft.MatchedSkillRefs = collectSkillRefs(matches)

	draft, normalizationNotes := rt.normalizeDraftForWorkspace(workspace, rule, matches, evidence, draft)
	review := ReviewDraft(draft)
	draft.Status = review.Status
	draft.ReviewNotes = append([]string(nil), review.ReviewNotes...)
	draft.ReviewNotes = append(draft.ReviewNotes, normalizationNotes...)
	if len(review.Findings) == 0 {
		draft.ScanFindings = nil
		return draft
	}
	draft.ScanFindings = append([]string(nil), review.Findings...)
	return draft
}

func (rt *Runtime) normalizeDraftForWorkspace(
	workspace string,
	rule LearningRecord,
	matches []skills.SkillInfo,
	evidence DraftEvidence,
	draft SkillDraft,
) (SkillDraft, []string) {
	target := strings.TrimSpace(draft.TargetSkillName)
	if workspace == "" || target == "" {
		return draft, nil
	}

	notes := make([]string, 0, 4)
	if combinedTarget := inferCombinedSkillName(rule); combinedTarget != "" && combinedTarget != target {
		originalTarget := target
		draft.TargetSkillName = combinedTarget
		target = combinedTarget
		notes = append(notes, fmt.Sprintf(
			"retargeted draft from %q to combined shortcut skill %q because the winning path was a stable multi-skill chain",
			originalTarget,
			combinedTarget,
		))
	}

	skillPath := filepath.Join(workspace, "skills", target, "SKILL.md")
	_, err := os.Stat(skillPath)
	hasExisting := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return draft, notes
	}

	if combinedTarget := inferCombinedSkillName(rule); combinedTarget != "" && combinedTarget == target {
		draft.HumanSummary = buildCombinedSkillHumanSummary(target, rule, hasExisting)
		draft.PreferredEntryPath = []string{target}
		draft.AvoidPatterns = appendUniqueStrings(
			draft.AvoidPatterns,
			buildCombinedSkillAvoidPattern(target, rule),
		)
		if hasExisting {
			draft.ChangeKind = ChangeKindAppend
			draft.BodyOrPatch = synthesizeCombinedSkillAppendBody(target, draft, rule, matches, evidence)
			notes = append(notes, "normalized combined shortcut draft to append onto the existing combined skill")
		} else {
			draft.ChangeKind = ChangeKindCreate
			draft.BodyOrPatch = synthesizeCombinedSkillDocument(target, draft, rule, matches, evidence)
			notes = append(notes, "normalized combined shortcut draft to create a new standalone shortcut skill")
		}
		return draft, notes
	}

	if !hasExisting {
		switch draft.ChangeKind {
		case ChangeKindAppend, ChangeKindMerge, ChangeKindReplace:
			draft.ChangeKind = ChangeKindCreate
			notes = append(notes, "normalized change_kind to create because target skill did not exist")
			if !looksLikeSkillDocument(draft.BodyOrPatch) {
				draft.BodyOrPatch = synthesizeSkillDocumentFromPartialDraft(target, draft, rule, evidence)
				notes = append(notes, "synthesized full skill document because draft body was partial")
			}
		}
		return draft, notes
	}

	if draft.ChangeKind == ChangeKindCreate && !looksLikeSkillDocument(draft.BodyOrPatch) {
		draft.ChangeKind = ChangeKindAppend
		notes = append(notes, "normalized change_kind to append because target skill already existed")
	}
	return draft, notes
}

func looksLikeSkillDocument(body string) bool {
	body = strings.TrimSpace(body)
	return strings.HasPrefix(body, "---\n") && strings.Contains(body, "\n# ")
}

func synthesizeSkillDocumentFromPartialDraft(
	target string,
	draft SkillDraft,
	rule LearningRecord,
	evidence DraftEvidence,
) string {
	description := strings.TrimSpace(draft.HumanSummary)
	if description == "" {
		description = fmt.Sprintf("Learned workflow for %s.", target)
	}

	bodyContent := strings.TrimSpace(draft.BodyOrPatch)
	if bodyContent == "" {
		bodyContent = "No learned content was generated."
	}
	if strings.HasPrefix(bodyContent, "# ") {
		return buildSkillDocument(target, description, bodyContent)
	}

	body := strings.Join([]string{
		"# " + titleCaseSkillName(target),
		"",
		"## Start Here",
		synthesizedStartHereLine(rule, target),
		"",
		"## Learned Evolution",
		bodyContent,
		"",
		"## Expected Result",
		synthesizedExpectedResultLine(evidence),
		"",
		"## Source Evidence",
		synthesizedEvidenceLine(rule, evidence),
		"",
	}, "\n")
	return buildSkillDocument(target, description, body)
}

func synthesizeCombinedSkillDocument(
	target string,
	draft SkillDraft,
	rule LearningRecord,
	matches []skills.SkillInfo,
	evidence DraftEvidence,
) string {
	description := strings.TrimSpace(draft.HumanSummary)
	if description == "" {
		description = buildCombinedSkillHumanSummary(target, rule, false)
	}

	body := strings.Join([]string{
		"# " + titleCaseSkillName(target),
		"",
		"## When To Use",
		synthesizedCombinedWhenToUseLine(rule, target),
		"",
		"## Procedure",
		synthesizedCombinedStartHereLine(rule, target),
		synthesizedCombinedProcedure(matches, rule),
		"",
		"## Source Skills",
		synthesizedComponentBreakdown(matches),
		"",
		"## Learned Context",
		synthesizedCombinedLearnedContent(draft.BodyOrPatch, rule),
		"",
		"## Expected Result",
		synthesizedExpectedResultLine(evidence),
		"",
		"## Source Evidence",
		synthesizedEvidenceLine(rule, evidence),
		"",
	}, "\n")
	return buildSkillDocument(target, description, body)
}

func synthesizeCombinedSkillAppendBody(
	target string,
	draft SkillDraft,
	rule LearningRecord,
	matches []skills.SkillInfo,
	evidence DraftEvidence,
) string {
	lines := []string{
		"## Learned Shortcut Update",
		fmt.Sprintf("- Shortcut skill: `%s`", target),
		fmt.Sprintf("- Task summary: %s", fallbackEvolutionSummary(rule)),
		fmt.Sprintf("- Wrapped path: %s", synthesizedWrappedPathLine(rule)),
		"- Guidance: prefer this shortcut directly instead of replaying the whole path when the task matches.",
		fmt.Sprintf("- Expected result: %s", synthesizedExpectedResultLine(evidence)),
		fmt.Sprintf("- Evidence: %s", synthesizedEvidenceLine(rule, evidence)),
		"",
		"### Source Skills",
		synthesizedComponentBreakdown(matches),
		"",
		synthesizedCombinedLearnedContent(draft.BodyOrPatch, rule),
		"",
	}
	return strings.Join(lines, "\n")
}

func synthesizedStartHereLine(rule LearningRecord, target string) string {
	if len(rule.WinningPath) > 0 {
		return fmt.Sprintf(
			"Start with `%s` for tasks like `%s`.",
			strings.Join(rule.WinningPath, " -> "),
			strings.TrimSpace(rule.Summary),
		)
	}
	if summary := strings.TrimSpace(rule.Summary); summary != "" {
		return fmt.Sprintf("Use `%s` when the task matches `%s`.", target, summary)
	}
	return fmt.Sprintf("Use `%s` for the learned task pattern.", target)
}

func synthesizedCombinedStartHereLine(rule LearningRecord, target string) string {
	return fmt.Sprintf("Use `%s` directly when the task matches `%s`.", target, fallbackEvolutionSummary(rule))
}

func synthesizedCombinedWhenToUseLine(rule LearningRecord, target string) string {
	if len(rule.WinningPath) == 0 {
		return fmt.Sprintf("Use `%s` when the learned task pattern appears again.", target)
	}
	return fmt.Sprintf(
		"Use `%s` as a direct shortcut instead of replaying `%s` step by step.",
		target,
		strings.Join(rule.WinningPath, " -> "),
	)
}

func synthesizedCombinedProcedure(matches []skills.SkillInfo, rule LearningRecord) string {
	components := synthesizedComponentBreakdown(matches)
	if !strings.HasPrefix(strings.TrimSpace(components), "- `") {
		if len(rule.WinningPath) == 0 {
			return "Use the learned shortcut directly and keep the response focused on the requested result."
		}
		return fmt.Sprintf(
			"Apply the recorded path `%s`, then return the final result with only the necessary explanation.",
			strings.Join(rule.WinningPath, " -> "),
		)
	}
	return "Follow the source skill guidance below as one compact procedure, then return the final result without replaying unnecessary discovery steps."
}

func synthesizedExpectedResultLine(evidence DraftEvidence) string {
	if excerpt := firstFinalOutputExcerpt(evidence, 360); excerpt != "" {
		return excerpt
	}
	return "Return the completed result for the matched task without restating unrelated discovery steps."
}

func synthesizedEvidenceLine(rule LearningRecord, evidence DraftEvidence) string {
	if len(evidence.TaskRecords) > 0 {
		ids := make([]string, 0, len(evidence.TaskRecords))
		for _, task := range evidence.TaskRecords {
			if id := strings.TrimSpace(task.ID); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			return "learned from task records: " + strings.Join(ids, ", ")
		}
	}
	if len(rule.TaskRecordIDs) > 0 {
		return "learned from task records: " + strings.Join(rule.TaskRecordIDs, ", ")
	}
	return "learned from the pattern record."
}

func synthesizedWrappedPathLine(rule LearningRecord) string {
	if len(rule.WinningPath) == 0 {
		return "No explicit wrapped path was recorded."
	}
	return strings.Join(rule.WinningPath, " -> ")
}

func synthesizedCombinedLearnedContent(body string, rule LearningRecord) string {
	content := strings.TrimSpace(stripSkillFrontmatter(body))
	if content == "" {
		return fmt.Sprintf(
			"Learned from `%s`; use this shortcut directly when the same task pattern appears again.",
			fallbackEvolutionSummary(rule),
		)
	}
	content = removeVerboseCombinedSections(content)
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return fmt.Sprintf(
			"Learned from `%s`; use this shortcut directly when the same task pattern appears again.",
			fallbackEvolutionSummary(rule),
		)
	}
	content = trimAtReadableBoundary(content, 1200)
	return "- Learned task: " + fallbackEvolutionSummary(rule) + "\n- Reusable guidance: " + content
}

func stripSkillFrontmatter(body string) string {
	trimmed := strings.TrimSpace(body)
	if !strings.HasPrefix(trimmed, "---\n") {
		return trimmed
	}
	rest := strings.TrimPrefix(trimmed, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return trimmed
	}
	return strings.TrimSpace(rest[end+5:])
}

func removeVerboseCombinedSections(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			normalized := strings.ToLower(title)
			switch normalized {
			case "component skill breakdown", "source skills", "wrapped path", "start here", "when to use", "procedure":
				skip = true
				continue
			default:
				skip = false
			}
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func fallbackEvolutionSummary(rule LearningRecord) string {
	if summary := strings.TrimSpace(rule.Summary); summary != "" {
		return summary
	}
	if len(rule.WinningPath) > 0 {
		return strings.Join(rule.WinningPath, " -> ")
	}
	return "the learned task pattern"
}

func buildCombinedSkillHumanSummary(target string, rule LearningRecord, hasExisting bool) string {
	_ = hasExisting
	summary := fallbackEvolutionSummary(rule)
	if strings.TrimSpace(summary) == "" || summary == "the learned task pattern" {
		summary = titleCaseSkillName(target)
	}
	return fmt.Sprintf("Use this skill to %s when the task requires this workflow.", sentenceFragment(summary))
}

func buildCombinedSkillAvoidPattern(target string, rule LearningRecord) string {
	if len(rule.WinningPath) == 0 {
		return fmt.Sprintf("avoid bypassing `%s` when the same learned task pattern appears again", target)
	}
	return fmt.Sprintf("avoid replaying %s before trying `%s` directly", strings.Join(rule.WinningPath, " -> "), target)
}

func collectSkillRefs(matches []skills.SkillInfo) []string {
	if len(matches) == 0 {
		return nil
	}

	refs := make([]string, 0, len(matches))
	for _, match := range matches {
		if strings := match.Path; strings != "" {
			refs = append(refs, strings)
			continue
		}
		refs = append(refs, match.Source+":"+match.Name)
	}
	return refs
}

func countTaskLearningRecords(records []LearningRecord) int {
	count := 0
	for _, record := range records {
		if isTaskRecordKind(record.Kind) {
			count++
		}
	}
	return count
}

func joinRecordIDs(records []LearningRecord) string {
	if len(records) == 0 {
		return ""
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record.ID) == "" {
			continue
		}
		ids = append(ids, record.ID)
	}
	return strings.Join(ids, ",")
}

func summarizePatternRecords(records []LearningRecord) string {
	if len(records) == 0 {
		return ""
	}
	parts := make([]string, 0, len(records))
	for _, record := range records {
		parts = append(parts, summarizePatternRecord(record))
	}
	return strings.Join(parts, " | ")
}

func summarizePatternRecord(record LearningRecord) string {
	label := strings.TrimSpace(record.ID)
	if label == "" {
		label = "unknown-pattern"
	}

	path := strings.Join(record.WinningPath, " -> ")
	if path == "" {
		path = strings.TrimSpace(record.Summary)
	}
	if path == "" {
		path = "no-summary"
	}

	return fmt.Sprintf("%s[%s]", label, path)
}

func recentEvidenceIDs(ids []string) []string {
	const maxEvidenceSamples = 12
	return ids[max(0, len(ids)-maxEvidenceSamples):]
}

func draftEvidenceForRule(rule LearningRecord, taskRecords []LearningRecord) DraftEvidence {
	if len(rule.TaskRecordIDs) == 0 || len(taskRecords) == 0 {
		return DraftEvidence{}
	}
	idSet := make(map[string]struct{}, len(rule.TaskRecordIDs))
	for _, id := range rule.TaskRecordIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		idSet[id] = struct{}{}
	}
	if len(idSet) == 0 {
		return DraftEvidence{}
	}
	tasks := make([]LearningRecord, 0, len(idSet))
	for _, task := range taskRecords {
		if rule.WorkspaceID != "" && task.WorkspaceID != rule.WorkspaceID {
			continue
		}
		if _, ok := idSet[task.ID]; !ok {
			continue
		}
		tasks = append(tasks, task)
	}
	return DraftEvidence{TaskRecords: tasks}
}

func generateDraftWithEvidence(
	ctx context.Context,
	generator DraftGenerator,
	rule LearningRecord,
	matches []skills.SkillInfo,
	evidence DraftEvidence,
) (SkillDraft, error) {
	if generator == nil {
		return SkillDraft{}, nil
	}
	if evidenceAware, ok := generator.(EvidenceAwareDraftGenerator); ok {
		return evidenceAware.GenerateDraftWithEvidence(ctx, rule, matches, evidence)
	}
	return generator.GenerateDraft(ctx, rule, matches)
}

func countNewPatterns(existing, patterns []LearningRecord, workspace string) int {
	existingIDs := make(map[string]struct{}, len(existing))
	for _, pattern := range existing {
		if !isPatternRecordKind(pattern.Kind) || pattern.WorkspaceID != workspace {
			continue
		}
		existingIDs[pattern.ID] = struct{}{}
	}
	count := 0
	for _, pattern := range patterns {
		if pattern.WorkspaceID != workspace {
			continue
		}
		if _, ok := existingIDs[pattern.ID]; ok {
			continue
		}
		count++
	}
	return count
}

func mergePatternRecords(existing, updates []LearningRecord, workspace string) []LearningRecord {
	out := append([]LearningRecord(nil), existing...)
	indexByID := make(map[string]int, len(out))
	for i, pattern := range out {
		indexByID[pattern.ID] = i
	}
	for _, update := range updates {
		if update.WorkspaceID != workspace {
			continue
		}
		if idx, ok := indexByID[update.ID]; ok {
			out[idx] = update
			continue
		}
		indexByID[update.ID] = len(out)
		out = append(out, update)
	}
	return out
}

func markTaskRecordsClustered(store *Store, ids []string) error {
	if store == nil || len(ids) == 0 {
		return nil
	}
	return store.MarkTaskRecordsClustered(ids)
}

func filterReadyRules(records []LearningRecord, workspace string) []LearningRecord {
	seen := make(map[string]LearningRecord)
	for _, record := range records {
		if !isPatternRecordKind(record.Kind) || record.WorkspaceID != workspace ||
			record.Status != RecordStatus("ready") {
			continue
		}
		seen[record.ID] = record
	}

	out := make([]LearningRecord, 0, len(seen))
	for _, record := range seen {
		out = append(out, record)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func profileOrigin(origin string) string {
	if origin == "manual" {
		return origin
	}
	return "evolved"
}

func appendUniqueStrings(existing []string, values ...string) []string {
	seen := make(map[string]struct{}, len(existing))
	for _, value := range existing {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		existing = append(existing, value)
		seen[value] = struct{}{}
	}
	return existing
}

type skillUsageSummary struct {
	All []string
}

func buildSkillUsage(input TurnCaseInput) skillUsageSummary {
	capacity := len(input.ActiveSkillNames) + len(input.AttemptedSkillNames) + len(input.FinalSuccessfulPath)
	for _, snapshot := range input.SkillContextSnapshots {
		capacity += len(snapshot.SkillNames)
	}
	for _, exec := range input.ToolExecutions {
		capacity += len(exec.SkillNames)
	}

	all := make([]string, 0, capacity)
	all = append(all, input.ActiveSkillNames...)
	all = append(all, input.AttemptedSkillNames...)
	all = append(all, input.FinalSuccessfulPath...)
	for _, snapshot := range input.SkillContextSnapshots {
		all = append(all, snapshot.SkillNames...)
	}
	for _, exec := range input.ToolExecutions {
		all = append(all, exec.SkillNames...)
	}
	return skillUsageSummary{All: uniqueTrimmedNames(all)}
}

func (rt *Runtime) recordSkillUsage(input TurnCaseInput, success bool) error {
	usage := buildSkillUsage(input)
	if len(usage.All) == 0 {
		return nil
	}

	store := rt.storeForWorkspace(input.Workspace)
	seen := make(map[string]struct{}, len(usage.All))
	for _, skillName := range usage.All {
		skillName = strings.TrimSpace(skillName)
		if skillName == "" {
			continue
		}
		if _, ok := seen[skillName]; ok {
			continue
		}
		seen[skillName] = struct{}{}

		if err := rt.touchSkillProfile(store, input, skillName, success); err != nil {
			return err
		}
	}
	return nil
}

func (rt *Runtime) touchSkillProfile(store *Store, input TurnCaseInput, skillName string, success bool) error {
	now := rt.now()
	return store.UpdateProfile(input.Workspace, skillName, func(profile *SkillProfile, exists bool) error {
		if !exists {
			*profile = SkillProfile{
				SkillName:      skillName,
				WorkspaceID:    input.Workspace,
				Status:         SkillStatusActive,
				Origin:         "manual",
				HumanSummary:   skillName,
				RetentionScore: 0.2,
			}
		}

		profile.SkillName = skillName
		profile.WorkspaceID = input.Workspace
		if profile.Status == SkillStatusCold || profile.Status == SkillStatusArchived || profile.Status == "" {
			profile.Status = SkillStatusActive
		}
		if profile.Origin == "" {
			profile.Origin = "manual"
		}
		if strings.TrimSpace(profile.HumanSummary) == "" {
			profile.HumanSummary = skillName
		}
		profile.LastUsedAt = now
		profile.UseCount++
		profile.RetentionScore = nextRetentionScore(profile.RetentionScore, success)
		return nil
	})
}

func nextRetentionScore(current float64, success bool) float64 {
	increment := -0.1
	if success {
		increment = 0.1
	}
	current += increment
	return min(1, max(0, current))
}
