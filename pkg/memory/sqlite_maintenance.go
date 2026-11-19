package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SQLiteIntegrityReport summarizes the checks most relevant to a session
// database before backup, restore, or startup.
type SQLiteIntegrityReport struct {
	OK                      bool
	QuickCheck              []string
	ForeignKeyViolations    int
	SizeBytes               int64
	WALBytes                int64
	SharedMemoryBytes       int64
	CheckedPath             string
	FullIntegrityCheckUsed  bool
	IntegrityCheckElapsedMS int64
}

// RestoreSQLiteResult records the safety copy created before a restore.
type RestoreSQLiteResult struct {
	Target       string
	SafetyBackup string
}

// CheckSQLiteIntegrity opens the database read-only and runs quick_check plus
// foreign_key_check. Set full to run the more expensive integrity_check.
func CheckSQLiteIntegrity(ctx context.Context, path string, full bool) (SQLiteIntegrityReport, error) {
	ctx = sqliteContext(ctx)
	if strings.TrimSpace(path) == "" {
		return SQLiteIntegrityReport{}, errors.New("memory: database path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: stat database: %w", err)
	}
	if info.IsDir() {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: database path is a directory: %s", path)
	}

	params := url.Values{
		"mode":    {"ro"},
		"_pragma": {"query_only(1)", "busy_timeout(5000)"},
	}
	dsn, err := sqliteFileDSN(path, params)
	if err != nil {
		return SQLiteIntegrityReport{}, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: open database for integrity check: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	started := time.Now()
	checkName := "quick_check"
	if full {
		checkName = "integrity_check"
	}
	rows, err := db.QueryContext(ctx, "PRAGMA "+checkName)
	if err != nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: run %s: %w", checkName, err)
	}
	var messages []string
	for rows.Next() {
		var message string
		if err := rows.Scan(&message); err != nil {
			rows.Close()
			return SQLiteIntegrityReport{}, fmt.Errorf("memory: scan %s: %w", checkName, err)
		}
		messages = append(messages, message)
	}
	if err := rows.Close(); err != nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: close %s rows: %w", checkName, err)
	}
	if err := rows.Err(); err != nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: iterate %s: %w", checkName, err)
	}

	var fkViolations int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_foreign_key_check").Scan(&fkViolations); err != nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: run foreign_key_check: %w", err)
	}

	report := SQLiteIntegrityReport{
		QuickCheck:              messages,
		ForeignKeyViolations:    fkViolations,
		SizeBytes:               info.Size(),
		WALBytes:                fileSize(path + "-wal"),
		SharedMemoryBytes:       fileSize(path + "-shm"),
		CheckedPath:             path,
		FullIntegrityCheckUsed:  full,
		IntegrityCheckElapsedMS: time.Since(started).Milliseconds(),
	}
	report.OK = fkViolations == 0 && len(messages) == 1 && strings.EqualFold(strings.TrimSpace(messages[0]), "ok")
	if !report.OK {
		return report, fmt.Errorf("memory: integrity check failed: %s", strings.Join(messages, "; "))
	}
	return report, nil
}

// BackupSQLite creates a transactionally consistent copy using SQLite's VACUUM
// INTO. The source is checked first and the resulting file is checked again.
func BackupSQLite(ctx context.Context, source, destination string) (SQLiteIntegrityReport, error) {
	ctx = sqliteContext(ctx)
	if _, err := CheckSQLiteIntegrity(ctx, source, false); err != nil {
		return SQLiteIntegrityReport{}, err
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return SQLiteIntegrityReport{}, err
	}
	destinationAbs, err := filepath.Abs(destination)
	if err != nil {
		return SQLiteIntegrityReport{}, err
	}
	if strings.EqualFold(sourceAbs, destinationAbs) {
		return SQLiteIntegrityReport{}, errors.New("memory: backup destination must differ from source")
	}
	if _, err := os.Stat(destination); err == nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: backup destination already exists: %s", destination)
	} else if !errors.Is(err, os.ErrNotExist) {
		return SQLiteIntegrityReport{}, err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: create backup directory: %w", err)
	}

	if err := snapshotSQLite(ctx, sourceAbs, destinationAbs); err != nil {
		_ = os.Remove(destination)
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: create backup: %w", err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		_ = os.Remove(destination)
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: secure backup: %w", err)
	}
	report, err := CheckSQLiteIntegrity(ctx, destination, false)
	if err != nil {
		_ = os.Remove(destination)
		return SQLiteIntegrityReport{}, fmt.Errorf("memory: validate backup: %w", err)
	}
	return report, nil
}

func snapshotSQLite(ctx context.Context, source, destination string) error {
	params := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "foreign_keys(1)"}}
	dsn, err := sqliteFileDSN(source, params)
	if err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", destination)
	return err
}

// RestoreSQLite validates a backup, creates a safety copy of the current
// database, then atomically replaces the target.
func RestoreSQLite(ctx context.Context, backup, target string, overwrite bool) (RestoreSQLiteResult, error) {
	ctx = sqliteContext(ctx)
	if _, err := CheckSQLiteIntegrity(ctx, backup, false); err != nil {
		return RestoreSQLiteResult{}, fmt.Errorf("memory: validate restore source: %w", err)
	}
	sourcePath, err := filepath.Abs(backup)
	if err != nil {
		return RestoreSQLiteResult{}, err
	}
	targetPath, err := filepath.Abs(target)
	if err != nil {
		return RestoreSQLiteResult{}, err
	}
	if strings.EqualFold(sourcePath, targetPath) {
		return RestoreSQLiteResult{}, errors.New("memory: restore source and target must differ")
	}
	targetExists := false
	if _, err := os.Stat(target); err == nil {
		targetExists = true
		if !overwrite {
			return RestoreSQLiteResult{}, fmt.Errorf("memory: target exists; use --force to replace it: %s", target)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreSQLiteResult{}, err
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return RestoreSQLiteResult{}, fmt.Errorf("memory: create target directory: %w", err)
	}
	temp, err := os.CreateTemp(filepath.Dir(target), ".restore-*.db")
	if err != nil {
		return RestoreSQLiteResult{}, fmt.Errorf("memory: create restore staging file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return RestoreSQLiteResult{}, err
	}
	if err := temp.Close(); err != nil {
		return RestoreSQLiteResult{}, err
	}
	if err := snapshotSQLite(ctx, sourcePath, tempPath); err != nil {
		return RestoreSQLiteResult{}, fmt.Errorf("memory: snapshot restore source: %w", err)
	}
	temp, err = os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return RestoreSQLiteResult{}, err
	}
	if err := errors.Join(temp.Sync(), temp.Close()); err != nil {
		return RestoreSQLiteResult{}, err
	}
	if _, err := CheckSQLiteIntegrity(ctx, tempPath, false); err != nil {
		return RestoreSQLiteResult{}, fmt.Errorf("memory: validate staged restore: %w", err)
	}

	result := RestoreSQLiteResult{Target: target}
	var moved []string
	var sidecarBackup string
	rollback := func(cause error) (RestoreSQLiteResult, error) {
		for _, suffix := range moved {
			if err := os.Rename(sidecarBackup+suffix, target+suffix); err != nil {
				cause = errors.Join(cause, fmt.Errorf("restore original SQLite sidecar %s: %w", suffix, err))
			}
		}
		return result, cause
	}
	if targetExists {
		result.SafetyBackup = target + ".before-restore-" + time.Now().UTC().Format("20060102T150405.000000000Z")
		if _, checkErr := CheckSQLiteIntegrity(ctx, target, false); checkErr == nil {
			if _, err := BackupSQLite(ctx, target, result.SafetyBackup); err != nil {
				return result, fmt.Errorf("memory: create restore safety backup: %w", err)
			}
			if err := checkpointRestoreTarget(ctx, target); err != nil {
				return result, fmt.Errorf("memory: checkpoint restore target: %w", err)
			}
		} else {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.SafetyBackup += ".damaged"
			if err := copySQLiteFile(target, result.SafetyBackup); err != nil {
				return result, fmt.Errorf("memory: preserve damaged database: %w", err)
			}
		}
	} else {
		result.SafetyBackup = target + ".before-restore-" + time.Now().UTC().Format("20060102T150405.000000000Z")
	}
	// Keep healthy snapshots standalone even when original sidecars exist.
	sidecarBackup = result.SafetyBackup + ".original"
	if strings.HasSuffix(result.SafetyBackup, ".damaged") {
		sidecarBackup = result.SafetyBackup
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(target + suffix); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return rollback(err)
		}
		if err := os.Rename(target+suffix, sidecarBackup+suffix); err != nil {
			return rollback(fmt.Errorf("memory: preserve target%s: %w", suffix, err))
		}
		moved = append(moved, suffix)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		return rollback(fmt.Errorf("memory: install restored database: %w", err))
	}
	if !targetExists && len(moved) == 0 {
		result.SafetyBackup = ""
	}
	return result, nil
}

// Checkpoint a healthy target before moving its old sidecars. If the process
// stops before the replacement, the original main file still has all commits.
func checkpointRestoreTarget(ctx context.Context, path string) error {
	params := url.Values{"mode": {"rw"}, "_pragma": {"busy_timeout(5000)"}}
	db, err := sql.Open("sqlite", path+"?"+params.Encode())
	if err != nil {
		return err
	}
	var busy, walPages, checkpointed int
	checkErr := db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &walPages, &checkpointed)
	if checkErr == nil && busy != 0 {
		checkErr = errors.New("database is busy")
	}
	return errors.Join(checkErr, db.Close())
}

func copySQLiteFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	if copyErr == nil {
		copyErr = out.Sync()
	}
	if err := errors.Join(copyErr, out.Close()); err != nil {
		return errors.Join(err, os.Remove(target))
	}
	return nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
