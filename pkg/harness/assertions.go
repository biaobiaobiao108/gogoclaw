package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	runtimeevents "github.com/biaobiaobiao108/gogoclaw/pkg/events"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
)

// ToolSequence returns the executed tool names in order.
func (r *Result) ToolSequence() []string {
	if r == nil {
		return nil
	}
	out := make([]string, 0, len(r.ToolCalls))
	for _, call := range r.ToolCalls {
		if call.Status == "skipped" {
			continue
		}
		out = append(out, call.Name)
	}
	return out
}

// EventKinds returns the observed runtime event kinds in order.
func (r *Result) EventKinds() []runtimeevents.Kind {
	if r == nil {
		return nil
	}
	out := make([]runtimeevents.Kind, 0, len(r.Events))
	for _, evt := range r.Events {
		out = append(out, evt.Kind)
	}
	return out
}

// PhaseSequence returns the accepted turn lifecycle transitions in order.
func (r *Result) PhaseSequence() []agent.TurnPhase {
	if r == nil || r.TurnEnd == nil || len(r.TurnEnd.PhaseHistory) == 0 {
		return nil
	}
	out := make([]agent.TurnPhase, 0, len(r.TurnEnd.PhaseHistory)+1)
	out = append(out, r.TurnEnd.PhaseHistory[0].From)
	for _, transition := range r.TurnEnd.PhaseHistory {
		out = append(out, transition.To)
	}
	return out
}

// Verify evaluates a declarative expectation and returns all mismatches.
func (r *Result) Verify(expect Expect) error {
	if r == nil {
		return errors.New("harness: nil result")
	}
	var problems []string
	if r.Err != nil && !expect.AllowError && expect.ErrorContains == "" {
		problems = append(problems, fmt.Sprintf("unexpected error: %v", r.Err))
	}
	if !expect.AllowInvalidTrace {
		problems = append(problems, r.ValidateTrace()...)
	}
	if expect.Final != "" && r.Content != expect.Final {
		problems = append(problems, fmt.Sprintf("final content = %q, want %q", r.Content, expect.Final))
	}
	if expect.FinalExact != nil && r.Content != *expect.FinalExact {
		problems = append(problems, fmt.Sprintf("final content = %q, want exactly %q", r.Content, *expect.FinalExact))
	}
	for _, want := range expect.FinalContains {
		if !strings.Contains(r.Content, want) {
			problems = append(problems, fmt.Sprintf("final content does not contain %q", want))
		}
	}
	if expect.NoError && r.Err != nil {
		problems = append(problems, fmt.Sprintf("unexpected error: %v", r.Err))
	}
	if expect.ErrorContains != "" && (r.Err == nil || !strings.Contains(r.Err.Error(), expect.ErrorContains)) {
		problems = append(problems, fmt.Sprintf("error = %v, want substring %q", r.Err, expect.ErrorContains))
	}
	for path, want := range expect.WorkspaceFiles {
		full, err := workspacePath(r.workspace, path)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		contents, err := os.ReadFile(full)
		if err != nil {
			problems = append(problems, fmt.Sprintf("workspace file %q: %v", path, err))
		} else if !strings.Contains(string(contents), want) {
			problems = append(problems, fmt.Sprintf("workspace file %q does not contain %q", path, want))
		}
	}
	for _, path := range expect.WorkspaceFilesAbsent {
		full, err := workspacePath(r.workspace, path)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if _, err := os.Stat(full); !errors.Is(err, os.ErrNotExist) {
			if err == nil {
				problems = append(problems, fmt.Sprintf("workspace file %q exists", path))
			} else {
				problems = append(problems, fmt.Sprintf("workspace file %q: %v", path, err))
			}
		}
	}
	if expect.ModelCalls > 0 && len(r.ModelCalls) != expect.ModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want %d", len(r.ModelCalls), expect.ModelCalls))
	}
	if expect.ModelCallsExact != nil && len(r.ModelCalls) != *expect.ModelCallsExact {
		problems = append(problems, fmt.Sprintf("model calls = %d, want exactly %d", len(r.ModelCalls), *expect.ModelCallsExact))
	}
	if expect.MinModelCalls > 0 && len(r.ModelCalls) < expect.MinModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at least %d", len(r.ModelCalls), expect.MinModelCalls))
	}
	if expect.MaxModelCalls > 0 && len(r.ModelCalls) > expect.MaxModelCalls {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at most %d", len(r.ModelCalls), expect.MaxModelCalls))
	}
	if expect.MaxModelCallsExact != nil && len(r.ModelCalls) > *expect.MaxModelCallsExact {
		problems = append(problems, fmt.Sprintf("model calls = %d, want at most %d", len(r.ModelCalls), *expect.MaxModelCallsExact))
	}
	if expect.TurnCount > 0 && len(r.Turns) != expect.TurnCount {
		problems = append(problems, fmt.Sprintf("turn count = %d, want %d", len(r.Turns), expect.TurnCount))
	}
	if expect.AsyncFollowUps > 0 && len(r.AsyncFollowUps) != expect.AsyncFollowUps {
		problems = append(problems, fmt.Sprintf("async follow-ups = %d, want %d", len(r.AsyncFollowUps), expect.AsyncFollowUps))
	}
	if expect.MinAsyncFollowUps > 0 && len(r.AsyncFollowUps) < expect.MinAsyncFollowUps {
		problems = append(problems, fmt.Sprintf("async follow-ups = %d, want at least %d", len(r.AsyncFollowUps), expect.MinAsyncFollowUps))
	}
	if expect.MaxAsyncFollowUps != nil && len(r.AsyncFollowUps) > *expect.MaxAsyncFollowUps {
		problems = append(problems, fmt.Sprintf("async follow-ups = %d, want at most %d", len(r.AsyncFollowUps), *expect.MaxAsyncFollowUps))
	}
	for _, needle := range expect.FollowUpContains {
		found := false
		for _, followUp := range r.AsyncFollowUps {
			if strings.Contains(followUp.Content, needle) {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("async follow-ups do not contain %q", needle))
		}
	}
	if expect.Tools != nil {
		got := r.ToolSequence()
		if len(got) != len(expect.Tools) {
			problems = append(problems, fmt.Sprintf("tool sequence = %v, want %v", got, expect.Tools))
		} else {
			for i := range got {
				if got[i] != expect.Tools[i] {
					problems = append(problems, fmt.Sprintf("tool sequence = %v, want %v", got, expect.Tools))
					break
				}
			}
		}
	}
	for _, forbidden := range expect.NoTools {
		for _, name := range r.ToolSequence() {
			if name == forbidden {
				problems = append(problems, fmt.Sprintf("forbidden tool %q executed", forbidden))
				break
			}
		}
	}
	problems = append(problems, verifyToolExpectations(r.ToolCalls, expect.ToolCalls)...)
	if expect.TurnStatus != "" {
		if r.TurnEnd == nil {
			problems = append(problems, "missing turn end event")
		} else if r.TurnEnd.Status != expect.TurnStatus {
			problems = append(problems, fmt.Sprintf("turn status = %s, want %s", r.TurnEnd.Status, expect.TurnStatus))
		}
	}
	if expect.TurnReason != "" {
		if r.TurnEnd == nil {
			problems = append(problems, "missing turn end event")
		} else if r.TurnEnd.Reason != expect.TurnReason {
			problems = append(problems, fmt.Sprintf("turn reason = %s, want %s", r.TurnEnd.Reason, expect.TurnReason))
		}
	}
	if expect.Phases != nil {
		got := r.PhaseSequence()
		if len(got) != len(expect.Phases) {
			problems = append(problems, fmt.Sprintf("phase sequence = %v, want %v", got, expect.Phases))
		} else {
			for i := range got {
				if got[i] != expect.Phases[i] {
					problems = append(problems, fmt.Sprintf("phase sequence = %v, want %v", got, expect.Phases))
					break
				}
			}
		}
	}
	for _, kind := range expect.Events {
		if !r.hasEvent(kind) {
			problems = append(problems, fmt.Sprintf("missing event %s", kind))
		}
	}
	if len(expect.EventSequence) > 0 && !containsSubsequence(r.EventKinds(), expect.EventSequence) {
		problems = append(problems, fmt.Sprintf("event kinds %v do not contain %v", r.EventKinds(), expect.EventSequence))
	}
	if expect.ExactEventSequence != nil {
		got := r.EventKinds()
		if !reflect.DeepEqual(got, expect.ExactEventSequence) {
			problems = append(problems, fmt.Sprintf("event kinds = %v, want exactly %v", got, expect.ExactEventSequence))
		}
	}
	problems = append(problems, verifyEventExpectations(r.Events, expect.EventDetails)...)
	problems = append(problems, verifyModelExpectations(r.ModelCalls, expect.ModelDetails)...)
	if expect.GoldenTrace != "" {
		want, err := LoadTrace(expect.GoldenTrace)
		if err != nil {
			problems = append(problems, fmt.Sprintf("load golden trace: %v", err))
		} else if err := CompareTrace(r.Snapshot(), want); err != nil {
			problems = append(problems, err.Error())
		}
	}
	for _, needle := range expect.HistoryContains {
		if !messagesContainAll(r.History, []string{needle}) {
			problems = append(problems, fmt.Sprintf("history does not contain %q", needle))
		}
	}
	for _, needle := range expect.OutboundContains {
		found := false
		for _, msg := range r.Outbound {
			if strings.Contains(msg, needle) {
				found = true
				break
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("outbound messages do not contain %q", needle))
		}
	}
	if expect.NoOutbound && len(r.Outbound) > 0 {
		problems = append(problems, fmt.Sprintf("expected no outbound messages, got %d", len(r.Outbound)))
	}
	if expect.Iterations > 0 {
		if r.TurnEnd == nil {
			problems = append(problems, "missing turn end event for iterations check")
		} else if r.TurnEnd.Iterations != expect.Iterations {
			problems = append(problems, fmt.Sprintf("iterations = %d, want %d", r.TurnEnd.Iterations, expect.Iterations))
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("harness %s failed: %s", r.Name, strings.Join(problems, "; "))
	}
	return nil
}

// ValidateTrace checks structural invariants that every completed scenario
// should satisfy regardless of its functional expectations.
func (r *Result) ValidateTrace() []string {
	if r == nil {
		return []string{"nil result"}
	}
	var problems []string
	if len(r.Turns) == 0 {
		problems = append(problems, "trace contains no turns")
	}
	turnStarts := make(map[string]int)
	turnEnds := make(map[string]int)
	turnParents := make(map[string]string)
	rootStarts, rootEnds := 0, 0
	for _, evt := range r.Events {
		if evt.Scope.TurnID != "" && evt.Correlation.ParentTurnID != "" {
			if existing := turnParents[evt.Scope.TurnID]; existing != "" && existing != evt.Correlation.ParentTurnID {
				problems = append(problems, fmt.Sprintf("turn %s has conflicting parents", evt.Scope.TurnID))
			}
			turnParents[evt.Scope.TurnID] = evt.Correlation.ParentTurnID
		}
		switch evt.Kind {
		case runtimeevents.KindAgentTurnStart:
			turnStarts[evt.Scope.TurnID]++
			if evt.Correlation.ParentTurnID == "" {
				rootStarts++
			}
		case runtimeevents.KindAgentTurnEnd:
			turnEnds[evt.Scope.TurnID]++
			if evt.Correlation.ParentTurnID == "" {
				rootEnds++
			}
		}
	}
	if rootStarts < len(r.Turns) {
		problems = append(problems, fmt.Sprintf("root turn start events = %d, recorded turns = %d", rootStarts, len(r.Turns)))
	}
	if rootEnds < len(r.Turns) {
		problems = append(problems, fmt.Sprintf("root turn end events = %d, recorded turns = %d", rootEnds, len(r.Turns)))
	}
	for turnID, count := range turnStarts {
		if count != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d start events", turnID, count))
		}
		if turnEnds[turnID] != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d end events", turnID, turnEnds[turnID]))
		}
	}
	for turnID, count := range turnEnds {
		if turnStarts[turnID] != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d start events", turnID, turnStarts[turnID]))
		}
		if count != 1 {
			problems = append(problems, fmt.Sprintf("turn %s has %d end events", turnID, count))
		}
	}
	for turnID, parentID := range turnParents {
		if _, ok := turnStarts[parentID]; !ok {
			problems = append(problems, fmt.Sprintf("turn %s references missing parent %s", turnID, parentID))
		}
		seen := map[string]bool{turnID: true}
		for current := parentID; current != ""; current = turnParents[current] {
			if seen[current] {
				problems = append(problems, fmt.Sprintf("turn hierarchy contains a cycle at %s", current))
				break
			}
			seen[current] = true
		}
	}
	for i, turn := range r.Turns {
		if turn.TurnEnd == nil {
			problems = append(problems, fmt.Sprintf("turn %d is missing TurnEnd payload", i+1))
			continue
		}
		phases := phaseSequence(turn.TurnEnd.PhaseHistory)
		if len(phases) == 0 {
			problems = append(problems, fmt.Sprintf("turn %d has no lifecycle history", i+1))
		} else {
			if phases[0] != agent.TurnPhaseCreated {
				problems = append(problems, fmt.Sprintf("turn %d starts at %s, want created", i+1, phases[0]))
			}
			if last := phases[len(phases)-1]; !agent.IsTerminalTurnPhase(last) {
				problems = append(problems, fmt.Sprintf("turn %d ends at non-terminal phase %s", i+1, last))
			}
		}
		for transitionIndex, transition := range turn.TurnEnd.PhaseHistory {
			if !agent.ValidTurnPhaseTransition(transition.From, transition.To) {
				problems = append(problems, fmt.Sprintf("turn %d phase history %d has invalid transition %s -> %s", i+1, transitionIndex+1, transition.From, transition.To))
			}
		}
		wantPhase := turnStatusPhase(turn.TurnEnd.Status)
		if wantPhase != "" && len(phases) > 0 && phases[len(phases)-1] != wantPhase {
			problems = append(problems, fmt.Sprintf("turn %d status %s ended in phase %s, want %s", i+1, turn.TurnEnd.Status, phases[len(phases)-1], wantPhase))
		}
	}
	for _, call := range r.ToolCalls {
		if call.Status == "started" || call.Status == "running" {
			problems = append(problems, fmt.Sprintf("tool call %s (%s) has no terminal record", call.CallID, call.Name))
		}
	}
	return problems
}

func verifyToolExpectations(records []ToolCallRecord, expectations []ToolCallExpectation) []string {
	var problems []string
	used := make(map[int]bool)
	for expectationIndex, expectation := range expectations {
		matched := -1
		for i, record := range records {
			if used[i] || !toolRecordMatches(record, expectation) {
				continue
			}
			matched = i
			break
		}
		if matched < 0 {
			problems = append(problems, fmt.Sprintf("tool expectation %d (%s/%s) did not match any call", expectationIndex+1, expectation.CallID, expectation.Name))
			continue
		}
		used[matched] = true
		record := records[matched]
		if expectation.ErrorContains != "" && !strings.Contains(record.Error, expectation.ErrorContains) {
			problems = append(problems, fmt.Sprintf("tool %s error = %q, want substring %q", record.Name, record.Error, expectation.ErrorContains))
		}
		if expectation.ResultContains != "" && !strings.Contains(record.Result, expectation.ResultContains) {
			problems = append(problems, fmt.Sprintf("tool %s result does not contain %q", record.Name, expectation.ResultContains))
		}
	}
	return problems
}

func toolRecordMatches(record ToolCallRecord, expectation ToolCallExpectation) bool {
	if expectation.CallID != "" && record.CallID != expectation.CallID {
		return false
	}
	if expectation.Name != "" && record.Name != expectation.Name {
		return false
	}
	if expectation.Status != "" && record.Status != expectation.Status {
		return false
	}
	if expectation.IsError != nil && record.IsError != *expectation.IsError {
		return false
	}
	for key, want := range expectation.Arguments {
		got, ok := record.Arguments[key]
		if !ok || !reflect.DeepEqual(got, want) {
			return false
		}
	}
	return true
}

func verifyEventExpectations(events []runtimeevents.Event, expectations []EventExpectation) []string {
	var problems []string
	for expectationIndex, expectation := range expectations {
		matched := false
		for _, evt := range events {
			if expectation.Kind != "" && evt.Kind != expectation.Kind {
				continue
			}
			if expectation.TurnID != "" && evt.Scope.TurnID != expectation.TurnID {
				continue
			}
			payload, _ := json.Marshal(snapshotPayload(evt.Payload))
			if !containsAll(string(payload), expectation.PayloadContains) {
				continue
			}
			matched = true
			break
		}
		if !matched {
			problems = append(problems, fmt.Sprintf("event expectation %d did not match kind=%s turn=%s payload=%v", expectationIndex+1, expectation.Kind, expectation.TurnID, expectation.PayloadContains))
		}
	}
	return problems
}

func verifyModelExpectations(calls []ModelCall, expectations []ModelCallExpectation) []string {
	var problems []string
	cursor := 0
	for expectationIndex, expectation := range expectations {
		matched := -1
		for i := cursor; i < len(calls); i++ {
			call := calls[i]
			if expectation.Model != "" && call.Model != expectation.Model {
				continue
			}
			if len(expectation.ToolNames) > 0 && !reflect.DeepEqual(call.ToolNames, expectation.ToolNames) {
				continue
			}
			if !messagesContainAll(call.Messages, expectation.Contains) {
				continue
			}
			matched = i
			break
		}
		if matched < 0 {
			problems = append(problems, fmt.Sprintf("model expectation %d did not match model=%s contains=%v tools=%v", expectationIndex+1, expectation.Model, expectation.Contains, expectation.ToolNames))
			continue
		}
		cursor = matched + 1
	}
	return problems
}

func messagesContainAll(messages []providers.Message, values []string) bool {
	if len(values) == 0 {
		return true
	}
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(message.Content)
		builder.WriteByte('\n')
	}
	return containsAll(builder.String(), values)
}

func containsAll(value string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}

func phaseSequence(history []agent.TurnPhaseTransition) []agent.TurnPhase {
	if len(history) == 0 {
		return nil
	}
	out := make([]agent.TurnPhase, 0, len(history)+1)
	out = append(out, history[0].From)
	for _, transition := range history {
		out = append(out, transition.To)
	}
	return out
}

func turnStatusPhase(status agent.TurnEndStatus) agent.TurnPhase {
	switch status {
	case agent.TurnEndStatusCompleted:
		return agent.TurnPhaseCompleted
	case agent.TurnEndStatusError:
		return agent.TurnPhaseFailed
	case agent.TurnEndStatusAborted:
		return agent.TurnPhaseAborted
	default:
		return ""
	}
}

func (r *Result) hasEvent(kind runtimeevents.Kind) bool {
	for _, evt := range r.Events {
		if evt.Kind == kind {
			return true
		}
	}
	return false
}

func containsSubsequence(got, want []runtimeevents.Kind) bool {
	if len(want) == 0 {
		return true
	}
	index := 0
	for _, kind := range got {
		if kind == want[index] {
			index++
			if index == len(want) {
				return true
			}
		}
	}
	return false
}
