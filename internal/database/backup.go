package database

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// BackupManager handles database backups
type BackupManager struct {
	db         *DB
	dbPath     string
	backupPath string
	retention  int
}

// NewBackupManager creates a new backup manager
func NewBackupManager(db *DB, dbPath, backupPath string, retention int) *BackupManager {
	return &BackupManager{
		db:         db,
		dbPath:     dbPath,
		backupPath: backupPath,
		retention:  retention,
	}
}

// Backup performs a full database backup
func (bm *BackupManager) Backup() error {
	// Ensure backup directory exists
	if err := os.MkdirAll(bm.backupPath, 0o755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Generate backup filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	backupFile := filepath.Join(bm.backupPath, fmt.Sprintf("episodex_%s.db", timestamp))

	slog.Info("Starting database backup", "file", backupFile)

	// Use VACUUM INTO for an atomic, consistent backup that includes WAL contents.
	// A simple file copy would miss pending WAL writes.
	if _, err := bm.db.Exec("VACUUM INTO ?", backupFile); err != nil {
		bm.createAlert("backup_failed", fmt.Sprintf("Backup failed: %v", err))
		return fmt.Errorf("failed to vacuum into backup: %w", err)
	}

	// Get file size
	fileInfo, err := os.Stat(backupFile)
	if err != nil {
		return fmt.Errorf("failed to get backup file info: %w", err)
	}

	// Check integrity of the backup
	integrityOK, err := bm.checkIntegrity(backupFile)
	if err != nil {
		bm.createAlert("backup_failed", fmt.Sprintf("Integrity check failed: %v", err))
		return fmt.Errorf("failed to check integrity: %w", err)
	}

	if !integrityOK {
		bm.createAlert("backup_corrupt", "Backup file is corrupted")
		slog.Warn("Backup integrity check failed", "file", backupFile)
	} else {
		slog.Info("Backup completed successfully", "file", backupFile, "size", fileInfo.Size())
	}

	if err := bm.rotateBackups(); err != nil {
		slog.Warn("Failed to rotate backups", "error", err)
	}

	return nil
}

// checkIntegrity performs PRAGMA integrity_check on the backup
func (bm *BackupManager) checkIntegrity(backupFile string) (bool, error) {
	// Open backup file directly without running migrations —
	// we only need to check integrity, not modify the backup
	sqlDB, err := sql.Open("sqlite", backupFile)
	if err != nil {
		return false, err
	}
	defer sqlDB.Close() //nolint:errcheck // closing temporary integrity-check connection

	var result string
	err = sqlDB.QueryRow("PRAGMA integrity_check").Scan(&result)
	if err != nil {
		return false, err
	}

	return result == "ok", nil
}

// rotateBackups removes old backups keeping only the most recent ones
func (bm *BackupManager) rotateBackups() error {
	// Get all backup files
	files, err := filepath.Glob(filepath.Join(bm.backupPath, "episodex_*.db"))
	if err != nil {
		return err
	}

	if len(files) <= bm.retention {
		return nil // Nothing to rotate
	}

	// Sort files by modification time (newest first)
	sort.Slice(files, func(i, j int) bool {
		infoI, errI := os.Stat(files[i])
		infoJ, errJ := os.Stat(files[j])
		if errI != nil || errJ != nil {
			return false
		}
		return infoI.ModTime().After(infoJ.ModTime())
	})

	// Remove old backups
	filesToDelete := files[bm.retention:]
	for _, file := range filesToDelete {
		if err := os.Remove(file); err != nil {
			slog.Warn("Failed to remove old backup", "file", file, "error", err)
		} else {
			slog.Info("Removed old backup", "file", file)
		}
	}

	return nil
}

// createAlert creates a system alert
func (bm *BackupManager) createAlert(alertType, message string) {
	query := `
		INSERT INTO system_alerts (type, message, created_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
	`

	_, err := bm.db.Exec(query, alertType, message)
	if err != nil {
		slog.Error("Failed to create alert", "error", err)
	}
}
