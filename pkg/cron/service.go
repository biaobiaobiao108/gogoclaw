package cron

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/adhocore/gronx"

	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

type CronSchedule struct {
	Kind    string `json:"kind"`
	AtMS    *int64 `json:"atMs,omitempty"`
	EveryMS *int64 `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type CronPayload struct {
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Command string `json:"command,omitempty"`
	Channel string `json:"channel,omitempty"`
	To      string `json:"to,omitempty"`
}

type CronJobState struct {
	NextRunAtMS *int64 `json:"nextRunAtMs,omitempty"`
	LastRunAtMS *int64 `json:"lastRunAtMs,omitempty"`
	LastStatus  string `json:"lastStatus,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type CronJob struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Enabled        bool         `json:"enabled"`
	Schedule       CronSchedule `json:"schedule"`
	Payload        CronPayload  `json:"payload"`
	State          CronJobState `json:"state"`
	CreatedAtMS    int64        `json:"createdAtMs"`
	UpdatedAtMS    int64        `json:"updatedAtMs"`
	DeleteAfterRun bool         `json:"deleteAfterRun"`
}

type CronStore struct {
	Version int       `json:"version"`
	Jobs    []CronJob `json:"jobs"`
}

type JobHandler func(job *CronJob) (string, error)

// Bound concurrent work so a burst of due jobs cannot exhaust the instance.
const maxConcurrentJobs = 4

type CronService struct {
	storePath    string
	db           *memory.SQLiteStore
	dbPath       string
	tx           *sql.Tx
	store        *CronStore
	onJob        JobHandler
	onJobContext func(context.Context, *CronJob) (string, error)
	ctx          context.Context
	cancel       context.CancelFunc
	lifecycle    sync.Mutex
	workers      sync.WaitGroup
	inFlight     map[string]bool
	mu           sync.RWMutex
	running      bool
	stopChan     chan struct{}
	wakeChan     chan struct{}
	gronx        *gronx.Gronx
}

func NewCronService(storePath string, onJob JobHandler) *CronService {
	cs := &CronService{
		storePath: storePath,
		store:     &CronStore{Version: 1, Jobs: []CronJob{}},
		onJob:     onJob,
		gronx:     gronx.New(),
		wakeChan:  make(chan struct{}, 1),
	}
	// Initialize and load store on creation
	if err := cs.loadStore(); err != nil {
		log.Printf("[cron] failed to load store: %v", err)
	}
	return cs
}

func (cs *CronService) Start() error {
	cs.lifecycle.Lock()
	defer cs.lifecycle.Unlock()
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.running {
		return nil
	}

	if err := cs.beginUpdate(); err != nil {
		return fmt.Errorf("failed to load store: %w", err)
	}
	defer cs.rollbackUpdate()

	cs.recomputeNextRuns()
	if err := cs.saveStoreUnsafe(); err != nil {
		return fmt.Errorf("failed to save store: %w", err)
	}

	cs.stopChan = make(chan struct{})
	if cs.wakeChan == nil {
		cs.wakeChan = make(chan struct{}, 1)
	}
	cs.ctx, cs.cancel = context.WithCancel(context.Background())
	cs.inFlight = make(map[string]bool)
	cs.running = true
	cs.workers.Add(1)
	go func() { defer cs.workers.Done(); cs.runLoop(cs.stopChan) }()

	return nil
}

func (cs *CronService) Stop() {
	cs.lifecycle.Lock()
	defer cs.lifecycle.Unlock()
	cs.mu.Lock()
	if cs.running {
		cs.running = false
		close(cs.stopChan)
		cs.cancel()
	}
	cs.mu.Unlock()
	cs.workers.Wait()
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if cs.db != nil {
		if err := cs.db.Close(); err != nil {
			log.Printf("[cron] failed to close store: %v", err)
		}
		cs.db = nil
	}
}

func (cs *CronService) runLoop(stopChan chan struct{}) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	for {
		cs.mu.Lock()
		if err := cs.loadStore(); err != nil {
			log.Printf("[cron] failed to refresh store: %v", err)
		}
		cs.mu.Unlock()
		// every loop, recalculate the next wake time
		cs.mu.RLock()
		nextWake := cs.getNextWakeMS()
		cs.mu.RUnlock()

		var delay time.Duration
		now := time.Now().UnixMilli()

		if nextWake == nil {
			// no jobs, sleep for a long time (or until a new job is added)
			delay = time.Hour
		} else {
			diff := *nextWake - now
			if diff <= 0 {
				delay = 100 * time.Millisecond
			} else {
				delay = time.Duration(diff) * time.Millisecond
			}
		}

		if delay > time.Second {
			delay = time.Second
		}
		timer.Reset(delay)

		select {
		case <-stopChan:
			return
		case <-cs.wakeChan: // wake on new job or update
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case <-timer.C:
			cs.checkJobs()
		}
	}
}

func (cs *CronService) checkJobs() {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if !cs.running || (cs.onJob == nil && cs.onJobContext == nil) {
		return
	}
	if err := cs.beginUpdate(); err != nil {
		log.Printf("[cron] failed to load store: %v", err)
		return
	}
	defer cs.rollbackUpdate()
	now := time.Now().UnixMilli()
	slots := maxConcurrentJobs - len(cs.inFlight)
	if slots <= 0 {
		return
	}
	var due []string
	previous := make(map[string]CronJobState)
	scheduled := make(map[string]CronJob)
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.Enabled && !cs.inFlight[job.ID] && job.State.NextRunAtMS != nil && *job.State.NextRunAtMS <= now {
			due = append(due, job.ID)
			previous[job.ID] = cloneCronJob(*job).State
			job.State.NextRunAtMS = nil
			job.State.LastRunAtMS = &now
			job.State.LastStatus = "running"
			job.State.LastError = ""
			scheduled[job.ID] = cloneCronJob(*job)
			if len(due) == slots {
				break
			}
		}
	}
	if len(due) == 0 {
		return
	}
	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
		retry := time.Now().Add(time.Second).UnixMilli()
		for i := range cs.store.Jobs {
			if state, ok := previous[cs.store.Jobs[i].ID]; ok {
				cs.store.Jobs[i].State = state
				cs.store.Jobs[i].State.NextRunAtMS = &retry
			}
		}
		return
	}
	if cs.inFlight == nil {
		cs.inFlight = make(map[string]bool)
	}
	for _, id := range due {
		cs.inFlight[id] = true
		cs.workers.Add(1)
		job := scheduled[id]
		go func() { defer cs.workers.Done(); cs.executeJobSnapshot(job.ID, &job) }()
	}
}

func (cs *CronService) executeJobByID(jobID string) { cs.executeJobSnapshot(jobID, nil) }

func (cs *CronService) executeJobSnapshot(jobID string, scheduled *CronJob) {
	startTime := time.Now().UnixMilli()
	defer func() { cs.mu.Lock(); delete(cs.inFlight, jobID); cs.notify(); cs.mu.Unlock() }()

	cs.mu.RLock()
	handler, contextHandler, ctx := cs.onJob, cs.onJobContext, cs.ctx
	cs.mu.RUnlock()
	if ctx == nil {
		ctx = context.Background()
	}
	var callbackJob *CronJob
	if !retryJobState(ctx, jobID, "refresh", func() error {
		cs.mu.Lock()
		defer cs.mu.Unlock()
		if err := cs.loadStore(); err != nil {
			return err
		}
		for i := range cs.store.Jobs {
			job := &cs.store.Jobs[i]
			if job.ID == jobID && job.Enabled && (scheduled == nil || sameSchedule(job.Schedule, scheduled.Schedule)) {
				jobCopy := cloneCronJob(*job)
				callbackJob = &jobCopy
				break
			}
		}
		return nil
	}) {
		return
	}
	if callbackJob == nil {
		retryJobState(ctx, jobID, "save skipped job", func() error {
			return cs.finishSkippedJob(jobID)
		})
		return
	}

	// Log job execution start
	log.Printf("[cron] ▶ executing job '%s' (id: %s, schedule: %s, channel: %s)",
		callbackJob.Name, jobID, callbackJob.Schedule.Kind, callbackJob.Payload.Channel)

	var err error
	if ctx.Err() != nil {
		err = ctx.Err()
	} else if contextHandler != nil {
		_, err = contextHandler(ctx, callbackJob)
	} else if handler != nil {
		_, err = handler(callbackJob)
	} else {
		err = fmt.Errorf("cron job handler is unavailable")
	}

	execDuration := time.Now().UnixMilli() - startTime

	retryJobState(ctx, jobID, "save completion", func() error {
		return cs.finishJob(ctx, callbackJob, startTime, execDuration, err)
	})
}

func retryJobState(ctx context.Context, jobID, operation string, run func() error) bool {
	for {
		if err := run(); err == nil {
			return true
		} else {
			log.Printf("[cron] failed to %s for job %s: %v", operation, jobID, err)
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

func (cs *CronService) finishSkippedJob(jobID string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.beginUpdate(); err != nil {
		return err
	}
	defer cs.rollbackUpdate()
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID && job.State.LastStatus == "running" {
			job.State.LastStatus = "skipped"
			if err := cs.saveStoreUnsafe(); err != nil {
				job.State.LastStatus = "running"
				return err
			}
			break
		}
	}
	return nil
}

func (cs *CronService) finishJob(ctx context.Context, callbackJob *CronJob, startTime, execDuration int64, err error) (saveErr error) {
	jobID := callbackJob.ID
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.beginUpdate(); err != nil {
		return err
	}
	defer cs.rollbackUpdate()

	var job *CronJob
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			job = &cs.store.Jobs[i]
			break
		}
	}
	if job == nil {
		log.Printf("[cron] job %s disappeared before state update", jobID)
		return nil
	}

	// Keep the last committed state visible while an update is retried.
	before := append([]CronJob(nil), cs.store.Jobs...)
	defer func() {
		if saveErr != nil {
			cs.store.Jobs = before
		}
	}()
	if err != nil && ctx.Err() != nil {
		job.State.LastStatus = "unknown"
		job.State.LastError = "execution interrupted; external effects may have occurred"
		if job.Enabled && job.State.NextRunAtMS == nil && sameSchedule(job.Schedule, callbackJob.Schedule) {
			if job.Schedule.Kind == "at" {
				job.Enabled = false
			} else {
				job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, time.Now().UnixMilli())
			}
		}
		return cs.saveStoreUnsafe()
	}

	job.State.LastRunAtMS = &startTime
	job.UpdatedAtMS = time.Now().UnixMilli()

	if err != nil {
		job.State.LastStatus = "error"
		job.State.LastError = err.Error()
		log.Printf("[cron] ✗ job '%s' failed after %dms: %v", job.Name, execDuration, err)
	} else {
		job.State.LastStatus = "ok"
		job.State.LastError = ""
	}

	// Preserve scheduling changes made while this execution was running.
	if job.State.NextRunAtMS != nil || !sameSchedule(job.Schedule, callbackJob.Schedule) || job.Enabled != callbackJob.Enabled || job.DeleteAfterRun != callbackJob.DeleteAfterRun {
		return cs.saveStoreUnsafe()
	}

	// Compute next run time
	var nextRunStr string
	if job.Schedule.Kind == "at" {
		if err != nil {
			job.Enabled = false
			job.State.NextRunAtMS = nil
			nextRunStr = "(failed)"
		} else if job.DeleteAfterRun {
			for i := range cs.store.Jobs {
				if cs.store.Jobs[i].ID == job.ID {
					cs.store.Jobs = append(cs.store.Jobs[:i], cs.store.Jobs[i+1:]...)
					break
				}
			}
			nextRunStr = "(deleted)"
		} else {
			job.Enabled = false
			job.State.NextRunAtMS = nil
			nextRunStr = "(disabled)"
		}
	} else {
		nextRun := cs.computeNextRun(&job.Schedule, time.Now().UnixMilli())
		job.State.NextRunAtMS = nextRun
		if nextRun != nil {
			nextRunStr = time.UnixMilli(*nextRun).Format("2006-01-02 15:04:05")
		} else {
			nextRunStr = "(none)"
		}
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		return err
	}
	if err == nil {
		log.Printf("[cron] ✓ job '%s' completed in %dms, next run: %s", job.Name, execDuration, nextRunStr)
	}

	return nil
}

func (cs *CronService) computeNextRun(schedule *CronSchedule, nowMS int64) *int64 {
	switch schedule.Kind {
	case "at":
		if schedule.AtMS != nil && *schedule.AtMS > nowMS {
			return schedule.AtMS
		}
		return nil
	case "every":
		if schedule.EveryMS == nil || *schedule.EveryMS <= 0 {
			return nil
		}
		next := nowMS + *schedule.EveryMS
		return &next
	case "cron":
		if schedule.Expr == "" {
			return nil
		}

		// Use gronx to calculate next run time
		now := time.UnixMilli(nowMS)
		nextTime, err := gronx.NextTickAfter(schedule.Expr, now, false)
		if err != nil {
			log.Printf("[cron] failed to compute next run for expr '%s': %v", schedule.Expr, err)
			return nil
		}

		nextMS := nextTime.UnixMilli()
		return &nextMS
	default:
		log.Printf("[cron] unknown schedule kind '%s'", schedule.Kind)
		return nil
	}
}

// wake up the loop to re-evaluate next wake time immediately (e.g. after add/update/remove jobs)
func (cs *CronService) notify() {
	select {
	case cs.wakeChan <- struct{}{}:
	default:
		// if the channel is full, it means the loop will wake up soon anyway, so we can skip sending
	}
}

func (cs *CronService) recomputeNextRuns() {
	now := time.Now().UnixMilli()
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.State.LastStatus == "running" {
			job.State.LastStatus = "unknown"
			job.State.LastError = "process stopped during execution; external effects may have occurred"
			if job.Schedule.Kind == "at" {
				job.Enabled = false
				job.State.NextRunAtMS = nil
			}
		}
		if job.Enabled {
			// Enabled one-shot jobs are still pending, including those due during downtime.
			if job.Schedule.Kind == "at" {
				job.State.NextRunAtMS = job.Schedule.AtMS
			} else if job.State.NextRunAtMS == nil {
				job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, now)
			}
		}
	}
}

func (cs *CronService) getNextWakeMS() *int64 {
	if len(cs.inFlight) >= maxConcurrentJobs {
		return nil
	}
	var nextWake *int64
	for _, job := range cs.store.Jobs {
		if job.Enabled && !cs.inFlight[job.ID] && (cs.onJob != nil || cs.onJobContext != nil) && job.State.NextRunAtMS != nil {
			if nextWake == nil || *job.State.NextRunAtMS < *nextWake {
				next := *job.State.NextRunAtMS
				nextWake = &next
			}
		}
	}
	return nextWake
}

func (cs *CronService) Load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.loadStore()
}

func (cs *CronService) SetOnJob(handler JobHandler) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.onJob = handler
	cs.notify()
}

func (cs *CronService) SetContextOnJob(handler func(context.Context, *CronJob) (string, error)) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.onJobContext = handler
	cs.notify()
}

func (cs *CronService) stateDB() (*memory.SQLiteStore, error) {
	if cs.db != nil && cs.dbPath == cs.storePath {
		return cs.db, nil
	}
	if cs.db != nil {
		_ = cs.db.Close()
		cs.db = nil
	}
	db, err := memory.NewSQLiteStore(cs.storePath)
	if err != nil {
		return nil, err
	}
	cs.db, cs.dbPath = db, cs.storePath
	return db, nil
}

func (cs *CronService) loadStore() error {
	store := &CronStore{
		Version: 1,
		Jobs:    []CronJob{},
	}

	db, err := cs.stateDB()
	if err != nil {
		return err
	}
	var data []byte
	if cs.tx != nil {
		err = cs.tx.QueryRow(`SELECT value FROM app_state WHERE namespace = 'cron' AND state_key = 'jobs'`).Scan(&data)
		if errors.Is(err, sql.ErrNoRows) {
			err = nil
		}
	} else {
		data, err = db.GetAppState(nil, "cron", "jobs")
	}
	if err != nil {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, store); err != nil {
			return err
		}
	}
	cs.store = store
	return nil
}

func (cs *CronService) beginUpdate() error {
	db, err := cs.stateDB()
	if err != nil {
		return err
	}
	cs.tx, err = db.BeginAppStateUpdate(nil)
	if err != nil {
		return err
	}
	if err = cs.loadStore(); err != nil {
		cs.rollbackUpdate()
	}
	return err
}

func (cs *CronService) rollbackUpdate() {
	if cs.tx != nil {
		_ = cs.tx.Rollback()
		cs.tx = nil
	}
}

func (cs *CronService) saveStoreUnsafe() error {
	data, err := json.Marshal(cs.store)
	if err != nil {
		return err
	}
	if cs.tx == nil {
		return fmt.Errorf("cron: update without transaction")
	}
	if _, err := cs.tx.Exec(`INSERT INTO app_state(namespace, state_key, value) VALUES ('cron', 'jobs', ?) ON CONFLICT(namespace, state_key) DO UPDATE SET value = excluded.value`, data); err != nil {
		return err
	}
	err = cs.tx.Commit()
	cs.tx = nil
	return err
}

func (cs *CronService) AddJob(
	name string,
	schedule CronSchedule,
	message string,
	channel, to string,
) (*CronJob, error) {
	return cs.AddJobWithCommand(name, schedule, message, channel, to, "")
}

// AddJobWithCommand persists the complete job before it can be scheduled.
func (cs *CronService) AddJobWithCommand(name string, schedule CronSchedule, message, channel, to, command string) (*CronJob, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.beginUpdate(); err != nil {
		return nil, err
	}
	defer cs.rollbackUpdate()

	now := time.Now().UnixMilli()

	// One-time tasks (at) should be deleted after execution
	deleteAfterRun := (schedule.Kind == "at")

	job := CronJob{
		ID:       generateID(),
		Name:     name,
		Enabled:  true,
		Schedule: schedule,
		Payload: CronPayload{
			Kind:    "agent_turn",
			Message: message,
			Command: command,
			Channel: channel,
			To:      to,
		},
		State: CronJobState{
			NextRunAtMS: cs.computeNextRun(&schedule, now),
		},
		CreatedAtMS:    now,
		UpdatedAtMS:    now,
		DeleteAfterRun: deleteAfterRun,
	}

	cs.store.Jobs = append(cs.store.Jobs, job)
	if err := cs.saveStoreUnsafe(); err != nil {
		cs.store.Jobs = cs.store.Jobs[:len(cs.store.Jobs)-1]
		return nil, err
	}

	cs.notify()

	return &job, nil
}

func (cs *CronService) GetJob(jobID string) (*CronJob, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			jobCopy := cloneCronJob(cs.store.Jobs[i])
			return &jobCopy, true
		}
	}
	return nil, false
}

func (cs *CronService) UpdateJob(job *CronJob) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.beginUpdate(); err != nil {
		return err
	}
	defer cs.rollbackUpdate()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			previous := cs.store.Jobs[i]
			updated := cloneCronJob(*job)
			updated.State = cloneCronJob(previous).State
			now := time.Now().UnixMilli()
			updated.UpdatedAtMS = now
			if updated.Enabled {
				if previous.Enabled != updated.Enabled || !sameSchedule(previous.Schedule, updated.Schedule) {
					updated.State.NextRunAtMS = cs.computeNextRun(&updated.Schedule, now)
				}
			} else {
				updated.State.NextRunAtMS = nil
			}
			cs.store.Jobs[i] = updated

			if err := cs.saveStoreUnsafe(); err != nil {
				cs.store.Jobs[i] = previous
				return err
			}
			cs.notify()
			return nil
		}
	}
	return fmt.Errorf("job not found")
}

func cloneCronJob(job CronJob) CronJob {
	clone := job
	if job.Schedule.AtMS != nil {
		atMS := *job.Schedule.AtMS
		clone.Schedule.AtMS = &atMS
	}
	if job.Schedule.EveryMS != nil {
		everyMS := *job.Schedule.EveryMS
		clone.Schedule.EveryMS = &everyMS
	}
	if job.State.NextRunAtMS != nil {
		nextRunAtMS := *job.State.NextRunAtMS
		clone.State.NextRunAtMS = &nextRunAtMS
	}
	if job.State.LastRunAtMS != nil {
		lastRunAtMS := *job.State.LastRunAtMS
		clone.State.LastRunAtMS = &lastRunAtMS
	}
	return clone
}

func sameSchedule(a, b CronSchedule) bool {
	return a.Kind == b.Kind &&
		sameInt64(a.AtMS, b.AtMS) &&
		sameInt64(a.EveryMS, b.EveryMS) &&
		a.Expr == b.Expr &&
		a.TZ == b.TZ
}

func sameInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (cs *CronService) RemoveJob(jobID string) (bool, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.beginUpdate(); err != nil {
		return false, err
	}
	defer cs.rollbackUpdate()
	return cs.removeJobCheckedUnsafe(jobID)
}
func (cs *CronService) removeJobCheckedUnsafe(jobID string) (bool, error) {
	for i, job := range cs.store.Jobs {
		if job.ID == jobID {
			old := cs.store.Jobs
			jobs := make([]CronJob, 0, len(old)-1)
			jobs = append(jobs, old[:i]...)
			jobs = append(jobs, old[i+1:]...)
			cs.store.Jobs = jobs
			if err := cs.saveStoreUnsafe(); err != nil {
				cs.store.Jobs = old
				return false, err
			}
			cs.notify()
			return true, nil
		}
	}
	return false, nil
}
func (cs *CronService) EnableJob(jobID string, enabled bool) (*CronJob, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	if err := cs.beginUpdate(); err != nil {
		return nil, err
	}
	defer cs.rollbackUpdate()
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			previous := cloneCronJob(*job)
			job.Enabled = enabled
			job.UpdatedAtMS = time.Now().UnixMilli()
			if enabled {
				job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, time.Now().UnixMilli())
			} else {
				job.State.NextRunAtMS = nil
			}
			if err := cs.saveStoreUnsafe(); err != nil {
				*job = previous
				return nil, err
			}
			result := cloneCronJob(*job)
			cs.notify()
			return &result, nil
		}
	}
	return nil, nil
}

func (cs *CronService) ListJobs(includeDisabled bool) []CronJob {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	if includeDisabled {
		jobs := make([]CronJob, len(cs.store.Jobs))
		for i, job := range cs.store.Jobs {
			jobs[i] = cloneCronJob(job)
		}
		return jobs
	}

	var enabled []CronJob
	for _, job := range cs.store.Jobs {
		if job.Enabled {
			enabled = append(enabled, cloneCronJob(job))
		}
	}

	return enabled
}

func (cs *CronService) Status() map[string]any {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var enabledCount int
	for _, job := range cs.store.Jobs {
		if job.Enabled {
			enabledCount++
		}
	}

	return map[string]any{
		"enabled":      cs.running,
		"jobs":         len(cs.store.Jobs),
		"nextWakeAtMS": cs.getNextWakeMS(),
	}
}

func generateID() string {
	// Use crypto/rand for better uniqueness under concurrent access
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
