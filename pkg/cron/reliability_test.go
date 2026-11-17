package cron

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func awaitSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for job")
	}
}

func TestConcurrentJobsAndScheduleUpdate(t *testing.T) {
	firstStarted := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	secondRan := make(chan struct{})
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), func(job *CronJob) (string, error) {
		if job.Name == "first" {
			close(firstStarted)
			<-release
		} else {
			close(secondRan)
		}
		return "", nil
	})
	at := time.Now().Add(250 * time.Millisecond).UnixMilli()
	first, err := cs.AddJob("first", CronSchedule{Kind: "at", AtMS: &at}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = cs.AddJob("second", CronSchedule{Kind: "at", AtMS: &at}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = cs.Start(); err != nil {
		t.Fatal(err)
	}
	defer cs.Stop()
	defer releaseOnce.Do(func() { close(release) })
	awaitSignal(t, firstStarted)
	awaitSignal(t, secondRan)
	future := time.Now().Add(time.Hour).UnixMilli()
	job, _ := cs.GetJob(first.ID)
	job.Schedule = CronSchedule{Kind: "at", AtMS: &future}
	if err := cs.UpdateJob(job); err != nil {
		t.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		got, ok := cs.GetJob(first.ID)
		if ok && got.State.LastRunAtMS != nil {
			if got.State.NextRunAtMS == nil || *got.State.NextRunAtMS != future {
				t.Fatalf("updated schedule lost: %+v", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("job did not finish")
}

func TestStopWaitsForCanceledJobBeforeRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	started := make(chan struct{})
	canceled := make(chan struct{})
	release := make(chan struct{})
	cs := NewCronService(path, nil)
	cs.SetContextOnJob(func(ctx context.Context, _ *CronJob) (string, error) {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return "", ctx.Err()
	})
	at := time.Now().Add(200 * time.Millisecond).UnixMilli()
	job, err := cs.AddJob("job", CronSchedule{Kind: "at", AtMS: &at}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Start(); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, started)
	stopped := make(chan struct{})
	go func() { cs.Stop(); close(stopped) }()
	awaitSignal(t, canceled)
	select {
	case <-stopped:
		t.Fatal("Stop returned before handler exited")
	default:
	}
	close(release)
	awaitSignal(t, stopped)
	reloaded := NewCronService(path, nil)
	got, ok := reloaded.GetJob(job.ID)
	if !ok || got.Enabled || got.State.LastStatus != "unknown" {
		t.Fatalf("interrupted job state: %+v", got)
	}
}

func TestNoHandlerDoesNotConsumeJobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(path, nil)
	at := time.Now().Add(-time.Second).UnixMilli()
	job, err := cs.AddJob("job", CronSchedule{Kind: "at", AtMS: &at}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Start(); err != nil {
		t.Fatal(err)
	}
	cs.checkJobs()
	cs.Stop()
	got, ok := NewCronService(path, nil).GetJob(job.ID)
	if !ok || !got.Enabled || got.State.LastStatus != "" {
		t.Fatalf("job consumed without handler: %+v", got)
	}
}

func TestMutationWriteFailuresRollback(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	every := int64(60000)
	job, err := cs.AddJob("job", CronSchedule{Kind: "every", EveryMS: &every}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cs.storePath = filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(cs.storePath, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddJob("new", CronSchedule{Kind: "every", EveryMS: &every}, "", "", ""); err == nil {
		t.Fatal("expected add error")
	}
	update := *job
	update.Name = "changed"
	if err := cs.UpdateJob(&update); err == nil {
		t.Fatal("expected update error")
	}
	if _, err := cs.EnableJob(job.ID, false); err == nil {
		t.Fatal("expected enable error")
	}
	if _, err := cs.RemoveJob(job.ID); err == nil {
		t.Fatal("expected remove error")
	}
	jobs := cs.ListJobs(true)
	if len(jobs) != 1 || jobs[0].Name != "job" || !jobs[0].Enabled {
		t.Fatalf("mutations not rolled back: %+v", jobs)
	}
}

func TestFailedOneShotIsRetained(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), func(*CronJob) (string, error) { return "", errors.New("delivery failed") })
	at := time.Now().Add(time.Hour).UnixMilli()
	job, err := cs.AddJob("job", CronSchedule{Kind: "at", AtMS: &at}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cs.mu.Lock()
	cs.store.Jobs[0].State.NextRunAtMS = nil
	cs.mu.Unlock()
	cs.executeJobByID(job.ID)
	got, ok := cs.GetJob(job.ID)
	if !ok || got.Enabled || got.State.LastStatus != "error" {
		t.Fatalf("failed job not retained: %+v", got)
	}
}

func TestRestartMarksInterruptedOneShotUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(path, nil)
	at := time.Now().Add(-time.Minute).UnixMilli()
	job, err := cs.AddJob("job", CronSchedule{Kind: "at", AtMS: &at}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	cs.mu.Lock()
	cs.store.Jobs[0].State.LastStatus = "running"
	cs.store.Jobs[0].State.NextRunAtMS = nil
	if err := cs.saveStoreUnsafe(); err != nil {
		cs.mu.Unlock()
		t.Fatal(err)
	}
	cs.mu.Unlock()
	ran := make(chan struct{}, 1)
	restarted := NewCronService(path, func(*CronJob) (string, error) { ran <- struct{}{}; return "", nil })
	if err := restarted.Start(); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	got, ok := restarted.GetJob(job.ID)
	if !ok || got.Enabled || got.State.LastStatus != "unknown" {
		t.Fatalf("unexpected recovered job: %+v", got)
	}
	select {
	case <-ran:
		t.Fatal("interrupted one-shot ran again")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestJobConcurrencyIsBounded(t *testing.T) {
	started := make(chan struct{}, maxConcurrentJobs+1)
	release := make(chan struct{})
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), func(*CronJob) (string, error) { started <- struct{}{}; <-release; return "", nil })
	at := time.Now().Add(300 * time.Millisecond).UnixMilli()
	for i := 0; i < maxConcurrentJobs+1; i++ {
		if _, err := cs.AddJob("job", CronSchedule{Kind: "at", AtMS: &at}, "", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := cs.Start(); err != nil {
		t.Fatal(err)
	}
	defer cs.Stop()
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	for i := 0; i < maxConcurrentJobs; i++ {
		awaitSignal(t, started)
	}
	select {
	case <-started:
		t.Fatal("concurrency limit exceeded")
	case <-time.After(200 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	awaitSignal(t, started)
}

func TestAddCommandIsPersistedBeforeScheduling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(path, nil)
	at := time.Now().Add(time.Hour).UnixMilli()
	job, err := cs.AddJobWithCommand("command", CronSchedule{Kind: "at", AtMS: &at}, "run", "cli", "direct", "echo ready")
	if err != nil {
		t.Fatal(err)
	}
	got, ok := NewCronService(path, nil).GetJob(job.ID)
	if !ok || got.Payload.Command != "echo ready" {
		t.Fatalf("persisted command: %+v", got)
	}
}
