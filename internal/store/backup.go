package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	moderncsqlite "modernc.org/sqlite"
)

type sqliteBackuper interface {
	NewBackup(string) (*moderncsqlite.Backup, error)
	NewRestore(string) (*moderncsqlite.Backup, error)
}

// Backup creates a consistent SQLite snapshot at destinationPath. The online
// backup API includes committed pages that are still in the source database's
// WAL, producing a standalone database file with no sidecar requirement.
func Backup(sourcePath, destinationPath string) (err error) {
	if err := validateDatabaseSource(sourcePath); err != nil {
		return err
	}
	if err := validateDistinctDatabasePaths(sourcePath, destinationPath); err != nil {
		return err
	}
	if err := prepareDatabaseDestination(destinationPath); err != nil {
		return err
	}

	source, err := openMaintenanceDatabase(sourcePath)
	if err != nil {
		return fmt.Errorf("opening SQLite source %s: %w", sourcePath, err)
	}
	defer func() {
		if closeErr := source.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("closing SQLite source %s: %w", sourcePath, closeErr)
		}
	}()

	if err := runOnlineBackup(source, destinationPath, false); err != nil {
		return fmt.Errorf("creating SQLite snapshot %s: %w", destinationPath, err)
	}
	if err := restrictDatabasePermissions(destinationPath); err != nil {
		return fmt.Errorf("restricting SQLite snapshot permissions: %w", err)
	}
	return nil
}

// Restore replaces destinationPath with the contents of sourcePath using
// SQLite's online restore API. The destination is kept at the same path, so
// callers do not need to manipulate WAL sidecar files themselves.
func Restore(destinationPath, sourcePath string) (err error) {
	if err := validateDatabaseSource(sourcePath); err != nil {
		return err
	}
	if err := validateDistinctDatabasePaths(sourcePath, destinationPath); err != nil {
		return err
	}
	if dir := filepath.Dir(destinationPath); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating SQLite destination dir: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("restricting SQLite destination dir permissions: %w", err)
		}
	}

	destination, err := openMaintenanceDatabase(destinationPath)
	if err != nil {
		return fmt.Errorf("opening SQLite destination %s: %w", destinationPath, err)
	}
	defer func() {
		if closeErr := destination.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("closing SQLite destination %s: %w", destinationPath, closeErr)
		}
	}()

	if err := runOnlineBackup(destination, sourcePath, true); err != nil {
		return fmt.Errorf("restoring SQLite snapshot %s: %w", sourcePath, err)
	}

	// Keep the active store in the same journal mode used by Open, including
	// when restore created the destination database for the first time.
	if _, err := destination.Exec("PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("enabling WAL after restore: %w", err)
	}
	if err := restrictDatabasePermissions(destinationPath); err != nil {
		return fmt.Errorf("restricting SQLite destination permissions: %w", err)
	}
	return nil
}

func validateDatabaseSource(path string) error {
	if path == "" {
		return fmt.Errorf("SQLite database path is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("SQLite database %s does not exist", path)
		}
		return fmt.Errorf("checking SQLite database %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("SQLite database %s is not a regular file", path)
	}
	return nil
}

func validateDistinctDatabasePaths(sourcePath, destinationPath string) error {
	if destinationPath == "" {
		return fmt.Errorf("SQLite destination path is empty")
	}

	sourceAbs, err := filepath.Abs(sourcePath)
	if err != nil {
		return fmt.Errorf("resolving SQLite source path: %w", err)
	}
	destinationAbs, err := filepath.Abs(destinationPath)
	if err != nil {
		return fmt.Errorf("resolving SQLite destination path: %w", err)
	}
	if sourceAbs == destinationAbs {
		return fmt.Errorf("SQLite source and destination must be different files")
	}

	if destinationInfo, err := os.Stat(destinationPath); err == nil {
		sourceInfo, sourceErr := os.Stat(sourcePath)
		if sourceErr == nil && os.SameFile(sourceInfo, destinationInfo) {
			return fmt.Errorf("SQLite source and destination must be different files")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking SQLite destination %s: %w", destinationPath, err)
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if sourceAbs+suffix == destinationAbs {
			return fmt.Errorf("SQLite destination cannot be a source sidecar file")
		}
	}
	return nil
}

func prepareDatabaseDestination(path string) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating SQLite destination dir: %w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("restricting SQLite destination dir permissions: %w", err)
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		candidate := path + suffix
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing previous SQLite destination %s: %w", candidate, err)
		}
	}
	return nil
}

func openMaintenanceDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func runOnlineBackup(db *sql.DB, remotePath string, restore bool) error {
	conn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer conn.Close()

	return conn.Raw(func(driverConn any) error {
		backuper, ok := driverConn.(sqliteBackuper)
		if !ok {
			return fmt.Errorf("SQLite driver does not support online backup")
		}

		var backup *moderncsqlite.Backup
		if restore {
			backup, err = backuper.NewRestore(remotePath)
		} else {
			backup, err = backuper.NewBackup(remotePath)
		}
		if err != nil {
			return err
		}

		more, stepErr := backup.Step(-1)
		for stepErr == nil && more {
			more, stepErr = backup.Step(-1)
		}
		finishErr := backup.Finish()
		if stepErr != nil {
			return fmt.Errorf("copying SQLite pages: %w", stepErr)
		}
		if finishErr != nil {
			return fmt.Errorf("finishing SQLite backup: %w", finishErr)
		}
		return nil
	})
}
