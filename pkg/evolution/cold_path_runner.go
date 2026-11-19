package evolution

import (
	"context"
	"errors"
	"sync"
	"time"
)

type coldPathRuntime interface {
	RunColdPathOnce(ctx context.Context, workspace string) error
}

type ColdPathRunner struct {
	runtime coldPathRuntime
	async   func(func())
	onError func(error)
	ctx     context.Context
	cancel  context.CancelFunc

	mu        sync.Mutex
	wg        sync.WaitGroup
	closeOnce sync.Once
	closed    bool
	running   map[string]workspaceRunState
}

func NewColdPathRunner(runtime coldPathRuntime) *ColdPathRunner {
	return NewColdPathRunnerWithErrorHandler(runtime, nil)
}

func NewColdPathRunnerWithErrorHandler(runtime coldPathRuntime, onError func(error)) *ColdPathRunner {
	if onError == nil {
		onError = func(error) {}
	}
	ctx, cancel := context.WithCancel(context.Background())

	return &ColdPathRunner{
		runtime: runtime,
		async: func(run func()) {
			go run()
		},
		onError: onError,
		ctx:     ctx,
		cancel:  cancel,
		running: make(map[string]workspaceRunState),
	}
}

type workspaceRunState struct {
	running   bool
	pending   bool
	lastRun   time.Time
	lastError string
}

type ColdPathStatus struct {
	Running   bool       `json:"running"`
	LastRun   *time.Time `json:"last_run,omitempty"`
	LastError string     `json:"last_error,omitempty"`
}

func (r *ColdPathRunner) Status(workspace string) ColdPathStatus {
	if r == nil {
		return ColdPathStatus{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	state := r.running[workspace]
	status := ColdPathStatus{Running: state.running, LastError: state.lastError}
	if !state.lastRun.IsZero() {
		status.LastRun = &state.lastRun
	}
	return status
}

func (r *ColdPathRunner) Trigger(workspace string) bool {
	if r == nil || r.runtime == nil || workspace == "" {
		return false
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return false
	}
	state, exists := r.running[workspace]
	if exists && state.running {
		state.pending = true
		r.running[workspace] = state
		r.mu.Unlock()
		return true
	}
	state.running = true
	r.running[workspace] = state
	r.wg.Add(1)
	r.mu.Unlock()

	r.async(func() {
		defer r.wg.Done()
		r.runWorkspace(workspace)
	})

	return true
}

func (r *ColdPathRunner) runWorkspace(workspace string) {
	for {
		err := r.runtime.RunColdPathOnce(r.ctx, workspace)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.onError(err)
		}

		r.mu.Lock()
		state, exists := r.running[workspace]
		if !exists || r.closed {
			delete(r.running, workspace)
			r.mu.Unlock()
			return
		}
		state.lastRun = time.Now()
		state.lastError = ""
		if err != nil {
			state.lastError = err.Error()
		}
		if state.pending {
			state.pending = false
			r.running[workspace] = state
			r.mu.Unlock()
			continue
		}
		state.running = false
		r.running[workspace] = state
		r.mu.Unlock()
		return
	}
}

func (r *ColdPathRunner) Close() error {
	if r == nil {
		return nil
	}

	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		r.mu.Unlock()
		r.cancel()
	})
	r.wg.Wait()
	return nil
}
