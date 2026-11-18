package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/biaobiaobiao108/gogoclaw/cmd/gogoclaw/internal"
	"github.com/biaobiaobiao108/gogoclaw/pkg/instance"
	"github.com/biaobiaobiao108/gogoclaw/pkg/memory"
)

func NewStorageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "storage",
		Short: "Inspect, back up, and restore the SQLite state database",
	}
	cmd.AddCommand(
		newCheckCommand(),
		newBackupCommand(),
		newRestoreCommand(),
	)
	return cmd
}

func newCheckCommand() *cobra.Command {
	var dbPath string
	var workspace string
	var full bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run SQLite integrity and foreign-key checks",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveDatabasePath(dbPath, workspace)
			if err != nil {
				return err
			}
			report, err := memory.CheckSQLiteIntegrity(cmd.Context(), path, full)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Database OK\nPath: %s\nSize: %d bytes\nWAL: %d bytes\nForeign-key violations: %d\nCheck duration: %d ms\n",
				report.CheckedPath,
				report.SizeBytes,
				report.WALBytes,
				report.ForeignKeyViolations,
				report.IntegrityCheckElapsedMS,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to sessions.db")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace containing sessions/sessions.db")
	cmd.Flags().BoolVar(&full, "full", false, "Run the slower full integrity check")
	return cmd
}

func newBackupCommand() *cobra.Command {
	var dbPath string
	var workspace string
	var output string
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Create a transactionally consistent SQLite backup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, err := resolveDatabasePath(dbPath, workspace)
			if err != nil {
				return err
			}
			if strings.TrimSpace(output) == "" {
				output = path + ".backup-" + time.Now().UTC().Format("20060102T150405Z")
			}
			report, err := memory.BackupSQLite(cmd.Context(), path, output)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Backup created\nSource: %s\nDestination: %s\nSize: %d bytes\nDuration: %d ms\n",
				path,
				output,
				report.SizeBytes,
				report.IntegrityCheckElapsedMS,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to sessions.db")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace containing sessions/sessions.db")
	cmd.Flags().StringVarP(&output, "output", "o", "", "Backup destination (must not already exist)")
	return cmd
}

func newRestoreCommand() *cobra.Command {
	var dbPath string
	var workspace string
	var force bool
	cmd := &cobra.Command{
		Use:   "restore <backup.db>",
		Short: "Validate and restore a SQLite backup",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			path, err := resolveDatabasePath(dbPath, workspace)
			if err != nil {
				return err
			}
			lock, err := instance.AcquireDataDirLock(internal.GetGogoclawHome())
			if err != nil {
				return fmt.Errorf("restore requires an offline instance: %w", err)
			}
			defer func() { err = errors.Join(err, lock.Release()) }()
			result, err := memory.RestoreSQLite(cmd.Context(), args[0], path, force)
			if err != nil {
				if result.SafetyBackup != "" {
					return fmt.Errorf("%w (restore safety path: %s)", err, result.SafetyBackup)
				}
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Database restored\nTarget: %s\nSafety backup: %s\n",
				result.Target,
				result.SafetyBackup,
			)
			return nil
		},
	}
	cmd.Flags().StringVar(&dbPath, "db", "", "Path to sessions.db")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace containing sessions/sessions.db")
	cmd.Flags().BoolVar(&force, "force", false, "Replace an existing database after creating a safety backup")
	return cmd
}

func resolveDatabasePath(dbPath, workspace string) (string, error) {
	dbPath = strings.TrimSpace(dbPath)
	workspace = strings.TrimSpace(workspace)
	if dbPath != "" && workspace != "" {
		return "", fmt.Errorf("use either --db or --workspace, not both")
	}
	if dbPath != "" {
		return filepath.Clean(dbPath), nil
	}
	if workspace == "" {
		cfg, err := internal.LoadConfig()
		if err != nil {
			return "", fmt.Errorf("load config to locate workspace: %w", err)
		}
		workspace = cfg.WorkspacePath()
	}
	if workspace == "" {
		return "", fmt.Errorf("workspace is not configured; pass --workspace or --db")
	}
	return filepath.Join(workspace, "sessions", "sessions.db"), nil
}
