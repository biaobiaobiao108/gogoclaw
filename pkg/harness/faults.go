package harness

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/agent"
	"github.com/biaobiaobiao108/gogoclaw/pkg/bus"
	"github.com/biaobiaobiao108/gogoclaw/pkg/providers"
	"github.com/biaobiaobiao108/gogoclaw/pkg/session"
	"github.com/biaobiaobiao108/gogoclaw/pkg/tools"
)

// AsyncToolHandler schedules work and invokes complete exactly once when the
// work finishes. call is 1-based.
type AsyncToolHandler func(
	ctx context.Context,
	args map[string]any,
	call int,
	complete func(*tools.ToolResult),
)

// AsyncToolStub is a deterministic AsyncExecutor used to exercise the unified
// delivery path without starting a real background subagent.
type AsyncToolStub struct {
	name        string
	description string
	parameters  map[string]any
	handler     AsyncToolHandler
	before      func(context.Context)
	inline      bool

	mu      sync.Mutex
	calls   []ToolCallRecord
	pending int
	idle    chan struct{}
}

func NewAsyncToolStub(name string, handler AsyncToolHandler) *AsyncToolStub {
	return &AsyncToolStub{
		name:        name,
		description: "harness async tool " + name,
		parameters:  map[string]any{"type": "object", "additionalProperties": true},
		handler:     handler,
	}
}

// NewInlineAsyncTool invokes the callback before ExecuteAsync returns. It is
// useful for deterministic live-turn async delivery tests.
func NewInlineAsyncTool(name string, handler AsyncToolHandler) *AsyncToolStub {
	tool := NewAsyncToolStub(name, handler)
	tool.inline = true
	return tool
}

// NewAsyncTextTool completes with content after delay. A zero delay completes
// inline; a positive delay completes from a background goroutine.
func NewAsyncTextTool(name, content string, delay time.Duration) *AsyncToolStub {
	handler := func(ctx context.Context, _ map[string]any, _ int, complete func(*tools.ToolResult)) {
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-ctx.Done():
				complete(tools.ErrorResult(ctx.Err().Error()).WithError(ctx.Err()))
				return
			}
		}
		complete(tools.SilentResult(content))
	}
	if delay <= 0 {
		return NewInlineAsyncTool(name, handler)
	}
	return NewAsyncToolStub(name, handler)
}

// NewAbortAsyncTool requests a hard abort before returning an async result and
// completes later so the orphan path can be observed.
func NewAbortAsyncTool(name, content string, delay time.Duration) *AsyncToolStub {
	tool := NewAsyncTextTool(name, content, delay)
	tool.before = func(ctx context.Context) {
		if loop := agent.AgentLoopFromContext(ctx); loop != nil {
			_ = loop.InterruptHard()
		}
	}
	return tool
}

// DelayedTool waits before returning content unless the turn context is
// canceled first.
func DelayedTool(name, content string, delay time.Duration) *ToolStub {
	return NewToolStub(name, func(ctx context.Context, _ map[string]any, _ int) *tools.ToolResult {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			return tools.SilentResult(content)
		case <-ctx.Done():
			return tools.ErrorResult(ctx.Err().Error()).WithError(ctx.Err())
		}
	})
}

// ContextWaitTool blocks until the turn context is canceled and reports the
// cancellation as a tool error.
func ContextWaitTool(name string) *ToolStub {
	return NewToolStub(name, func(ctx context.Context, _ map[string]any, _ int) *tools.ToolResult {
		<-ctx.Done()
		return tools.ErrorResult(ctx.Err().Error()).WithError(ctx.Err())
	})
}

func (t *AsyncToolStub) Name() string               { return t.name }
func (t *AsyncToolStub) Description() string        { return t.description }
func (t *AsyncToolStub) Parameters() map[string]any { return t.parameters }

func (t *AsyncToolStub) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	return t.ExecuteAsync(ctx, args, nil)
}

func (t *AsyncToolStub) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	callback tools.AsyncCallback,
) *tools.ToolResult {
	if t.before != nil {
		t.before(ctx)
	}
	t.mu.Lock()
	index := len(t.calls) + 1
	if t.pending == 0 {
		t.idle = make(chan struct{})
	}
	t.pending++
	t.calls = append(t.calls, ToolCallRecord{
		CallID:    tools.ToolCallID(ctx),
		Name:      t.name,
		Arguments: cloneArgs(args),
		Status:    "running",
		At:        time.Now().UTC(),
	})
	t.mu.Unlock()

	started := time.Now()
	var once sync.Once
	complete := func(result *tools.ToolResult) {
		once.Do(func() {
			defer func() {
				t.mu.Lock()
				t.pending--
				if t.pending == 0 {
					close(t.idle)
					t.idle = nil
				}
				t.mu.Unlock()
			}()
			if result == nil {
				result = tools.ErrorResult("harness async tool returned nil result")
			}
			t.mu.Lock()
			t.calls[index-1].Duration = time.Since(started)
			t.calls[index-1].Status = "completed"
			t.calls[index-1].IsError = result.IsError
			t.calls[index-1].Result = result.ContentForLLM()
			if result.IsError {
				t.calls[index-1].Status = "error"
				t.calls[index-1].Error = toolResultError(result)
			}
			t.mu.Unlock()
			if callback != nil {
				callback(ctx, result)
			}
		})
	}

	run := func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				complete(tools.ErrorResult(fmt.Sprintf("async tool panic: %v", recovered)).WithError(fmt.Errorf("async tool panic: %v", recovered)))
			}
		}()
		t.handler(ctx, cloneArgs(args), index, complete)
	}
	if t.inline {
		run()
	} else {
		go run()
	}
	return tools.AsyncResult("async work started")
}

func (t *AsyncToolStub) Wait(ctx context.Context) error {
	for {
		t.mu.Lock()
		if t.pending == 0 {
			t.mu.Unlock()
			return nil
		}
		idle := t.idle
		t.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (t *AsyncToolStub) Calls() []ToolCallRecord {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]ToolCallRecord, len(t.calls))
	for i, call := range t.calls {
		call.Arguments = cloneArgs(call.Arguments)
		out[i] = call
	}
	return out
}

// SessionOperation identifies an injectable session-store operation.
type SessionOperation string

const (
	SessionGetHistory      SessionOperation = "get_history"
	SessionAddMessage      SessionOperation = "add_message"
	SessionAddFullMessage  SessionOperation = "add_full_message"
	SessionGetSummary      SessionOperation = "get_summary"
	SessionSetSummary      SessionOperation = "set_summary"
	SessionSetHistory      SessionOperation = "set_history"
	SessionTruncateHistory SessionOperation = "truncate_history"
	SessionSave            SessionOperation = "save"
	SessionSetCheckpoint   SessionOperation = "set_checkpoint"
	SessionGetCheckpoint   SessionOperation = "get_checkpoint"
	SessionClearCheckpoint SessionOperation = "clear_checkpoint"
	SessionUpdateUsage     SessionOperation = "update_usage"
	SessionCommitSummary   SessionOperation = "commit_summary"
)

// SessionFault is a thread-safe sequence of injected store failures.
type SessionFault struct {
	mu       sync.Mutex
	failures map[SessionOperation][]error
	calls    map[SessionOperation]int
}

func NewSessionFault() *SessionFault {
	return &SessionFault{
		failures: make(map[SessionOperation][]error),
		calls:    make(map[SessionOperation]int),
	}
}

func (f *SessionFault) Fail(operation SessionOperation, times int, err error) *SessionFault {
	if times <= 0 {
		return f
	}
	if err == nil {
		err = errors.New("injected session failure")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := 0; i < times; i++ {
		f.failures[operation] = append(f.failures[operation], err)
	}
	return f
}

func (f *SessionFault) Calls(operation SessionOperation) int {
	if f == nil {
		return 0
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[operation]
}

func (f *SessionFault) next(operation SessionOperation) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[operation]++
	failures := f.failures[operation]
	if len(failures) == 0 {
		return nil
	}
	err := failures[0]
	f.failures[operation] = failures[1:]
	return err
}

// SessionFaultStore wraps a SessionStore and injects failures at well-defined
// persistence boundaries.
type SessionFaultStore struct {
	inner session.SessionStore
	fault *SessionFault
}

func NewSessionFaultStore(inner session.SessionStore, fault *SessionFault) *SessionFaultStore {
	if fault == nil {
		fault = NewSessionFault()
	}
	return &SessionFaultStore{inner: inner, fault: fault}
}

func (s *SessionFaultStore) AddMessage(sessionKey, role, content string) {
	if s.fault.next(SessionAddMessage) != nil {
		return
	}
	s.inner.AddMessage(sessionKey, role, content)
}

func (s *SessionFaultStore) AddFullMessage(sessionKey string, msg providers.Message) {
	if s.fault.next(SessionAddFullMessage) != nil {
		return
	}
	s.inner.AddFullMessage(sessionKey, msg)
}

func (s *SessionFaultStore) AddFullMessageChecked(sessionKey string, msg providers.Message) error {
	if err := s.fault.next(SessionAddFullMessage); err != nil {
		return err
	}
	if checked, ok := s.inner.(session.CheckedStore); ok {
		return checked.AddFullMessageChecked(sessionKey, msg)
	}
	s.inner.AddFullMessage(sessionKey, msg)
	return nil
}

func (s *SessionFaultStore) GetHistory(sessionKey string) []providers.Message {
	if s.fault.next(SessionGetHistory) != nil {
		return nil
	}
	return s.inner.GetHistory(sessionKey)
}

// FaultFreeHistory lets the harness snapshot persisted state without consuming
// or triggering an injected read failure.
func (s *SessionFaultStore) FaultFreeHistory(sessionKey string) []providers.Message {
	if s == nil || s.inner == nil {
		return nil
	}
	return s.inner.GetHistory(sessionKey)
}

func (s *SessionFaultStore) GetHistoryChecked(sessionKey string) ([]providers.Message, error) {
	if err := s.fault.next(SessionGetHistory); err != nil {
		return nil, err
	}
	if checked, ok := s.inner.(session.CheckedStore); ok {
		return checked.GetHistoryChecked(sessionKey)
	}
	return s.inner.GetHistory(sessionKey), nil
}

func (s *SessionFaultStore) GetSummary(sessionKey string) string {
	if s.fault.next(SessionGetSummary) != nil {
		return ""
	}
	return s.inner.GetSummary(sessionKey)
}

func (s *SessionFaultStore) GetSummaryChecked(sessionKey string) (string, error) {
	if err := s.fault.next(SessionGetSummary); err != nil {
		return "", err
	}
	if checked, ok := s.inner.(session.CheckedStore); ok {
		return checked.GetSummaryChecked(sessionKey)
	}
	return s.inner.GetSummary(sessionKey), nil
}

func (s *SessionFaultStore) SetSummary(sessionKey, summary string) {
	if s.fault.next(SessionSetSummary) != nil {
		return
	}
	s.inner.SetSummary(sessionKey, summary)
}

func (s *SessionFaultStore) SetSummaryChecked(sessionKey, summary string) error {
	if err := s.fault.next(SessionSetSummary); err != nil {
		return err
	}
	if checked, ok := s.inner.(session.CheckedStore); ok {
		return checked.SetSummaryChecked(sessionKey, summary)
	}
	s.inner.SetSummary(sessionKey, summary)
	return nil
}

func (s *SessionFaultStore) SetHistory(sessionKey string, history []providers.Message) {
	if s.fault.next(SessionSetHistory) != nil {
		return
	}
	s.inner.SetHistory(sessionKey, history)
}

func (s *SessionFaultStore) SetHistoryChecked(sessionKey string, history []providers.Message) error {
	if err := s.fault.next(SessionSetHistory); err != nil {
		return err
	}
	if checked, ok := s.inner.(session.CheckedStore); ok {
		return checked.SetHistoryChecked(sessionKey, history)
	}
	s.inner.SetHistory(sessionKey, history)
	return nil
}

func (s *SessionFaultStore) TruncateHistory(sessionKey string, keepLast int) {
	if s.fault.next(SessionTruncateHistory) != nil {
		return
	}
	s.inner.TruncateHistory(sessionKey, keepLast)
}

func (s *SessionFaultStore) Save(sessionKey string) error {
	if err := s.fault.next(SessionSave); err != nil {
		return err
	}
	return s.inner.Save(sessionKey)
}

func (s *SessionFaultStore) ListSessions() []string {
	return s.inner.ListSessions()
}

func (s *SessionFaultStore) Close() error {
	return s.inner.Close()
}

func (s *SessionFaultStore) EnsureSessionMetadata(sessionKey string, scope *session.SessionScope, aliases []string) {
	if store, ok := s.inner.(session.MetadataAwareSessionStore); ok {
		store.EnsureSessionMetadata(sessionKey, scope, aliases)
	}
}

func (s *SessionFaultStore) ResolveSessionKey(sessionKey string) string {
	if store, ok := s.inner.(session.MetadataAwareSessionStore); ok {
		return store.ResolveSessionKey(sessionKey)
	}
	return sessionKey
}

func (s *SessionFaultStore) GetSessionScope(sessionKey string) *session.SessionScope {
	if store, ok := s.inner.(session.MetadataAwareSessionStore); ok {
		return store.GetSessionScope(sessionKey)
	}
	return nil
}

func (s *SessionFaultStore) SetTurnCheckpoint(sessionKey string, checkpoint session.TurnCheckpoint) error {
	if err := s.fault.next(SessionSetCheckpoint); err != nil {
		return err
	}
	store, ok := s.inner.(session.TurnCheckpointStore)
	if !ok {
		return errors.New("harness: inner session store does not support checkpoints")
	}
	return store.SetTurnCheckpoint(sessionKey, checkpoint)
}

func (s *SessionFaultStore) GetTurnCheckpoint(sessionKey string) (session.TurnCheckpoint, error) {
	if err := s.fault.next(SessionGetCheckpoint); err != nil {
		return session.TurnCheckpoint{}, err
	}
	store, ok := s.inner.(session.TurnCheckpointStore)
	if !ok {
		return session.TurnCheckpoint{}, errors.New("harness: inner session store does not support checkpoints")
	}
	return store.GetTurnCheckpoint(sessionKey)
}

func (s *SessionFaultStore) ClearTurnCheckpoints(sessionKey string) error {
	if err := s.fault.next(SessionClearCheckpoint); err != nil {
		return err
	}
	store, ok := s.inner.(session.TurnCheckpointStore)
	if !ok {
		return errors.New("harness: inner session store does not support checkpoints")
	}
	return store.ClearTurnCheckpoints(sessionKey)
}

func (s *SessionFaultStore) UpdateTurnUsage(sessionKey, turnID string, usage *bus.ContextUsage) error {
	if err := s.fault.next(SessionUpdateUsage); err != nil {
		return err
	}
	store, ok := s.inner.(session.TurnUsageStore)
	if !ok {
		return errors.New("harness: inner session store does not support turn usage")
	}
	return store.UpdateTurnUsage(sessionKey, turnID, usage)
}

func (s *SessionFaultStore) CommitSummary(
	sessionKey string,
	consumed []providers.Message,
	summary string,
	newSummary string,
) (bool, error) {
	if err := s.fault.next(SessionCommitSummary); err != nil {
		return false, err
	}
	store, ok := s.inner.(session.SummaryCommitStore)
	if !ok {
		return false, errors.New("harness: inner session store does not support summary commits")
	}
	return store.CommitSummary(sessionKey, consumed, summary, newSummary)
}
