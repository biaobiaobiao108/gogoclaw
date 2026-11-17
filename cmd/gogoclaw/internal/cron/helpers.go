package cron

import (
	"fmt"
	"time"

	"github.com/biaobiaobiao108/gogoclaw/pkg/cron"
)

func cronListCmd(storePath string) error {
	cs := cron.NewCronService(storePath, nil)
	if err := cs.Load(); err != nil {
		return fmt.Errorf("load scheduled jobs: %w", err)
	}
	jobs := cs.ListJobs(true) // Show all jobs, including disabled

	if len(jobs) == 0 {
		fmt.Println("No scheduled jobs.")
		return nil
	}

	fmt.Println("\nScheduled Jobs:")
	fmt.Println("----------------")
	for _, job := range jobs {
		var schedule string
		if job.Schedule.Kind == "every" && job.Schedule.EveryMS != nil {
			schedule = fmt.Sprintf("every %ds", *job.Schedule.EveryMS/1000)
		} else if job.Schedule.Kind == "cron" {
			schedule = job.Schedule.Expr
		} else {
			schedule = "one-time"
		}

		nextRun := "scheduled"
		if job.State.NextRunAtMS != nil {
			nextTime := time.UnixMilli(*job.State.NextRunAtMS)
			nextRun = nextTime.Format("2006-01-02 15:04")
		}

		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}

		fmt.Printf("  %s (%s)\n", job.Name, job.ID)
		fmt.Printf("    Schedule: %s\n", schedule)
		fmt.Printf("    Status: %s\n", status)
		fmt.Printf("    Next run: %s\n", nextRun)
		if job.State.LastStatus != "" {
			fmt.Printf("    Last status: %s\n", job.State.LastStatus)
		}
		if job.State.LastError != "" {
			fmt.Printf("    Last error: %s\n", job.State.LastError)
		}
	}
	return nil
}

func cronRemoveCmd(storePath, jobID string) error {
	cs := cron.NewCronService(storePath, nil)
	removed, err := cs.RemoveJob(jobID)
	if err != nil {
		return fmt.Errorf("remove job: %w", err)
	}
	if removed {
		fmt.Printf("✓ Removed job %s\n", jobID)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
	return nil
}

func cronSetJobEnabled(storePath, jobID string, enabled bool) error {
	cs := cron.NewCronService(storePath, nil)
	job, err := cs.EnableJob(jobID, enabled)
	if err != nil {
		return fmt.Errorf("change job: %w", err)
	}
	if job != nil {
		fmt.Printf("✓ Job '%s' enabled\n", job.Name)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
	return nil
}
