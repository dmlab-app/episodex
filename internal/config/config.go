// Package config handles application configuration.
package config

import (
	"fmt"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

// Config holds application configuration
type Config struct {
	// Server settings
	Port string
	Host string

	// Database settings
	DBPath string

	// Backup settings
	BackupPath      string
	BackupRetention int
	BackupHour      int

	// Media settings
	MediaPath string

	// TVDB API
	TVDBApiKey string

	// Scanning settings
	ScanIntervalHours int
	TVDBCheckHour     int

	// qBittorrent settings
	QbitURL      string
	QbitUser     string
	QbitPassword string
}

// Load loads configuration from environment variables
func Load() (*Config, error) {
	// Try to load .env file, ignore error if it doesn't exist
	_ = godotenv.Load()

	cfg := &Config{
		Port:              getEnv("PORT", "8080"),
		Host:              getEnv("HOST", "0.0.0.0"),
		DBPath:            getEnv("DB_PATH", "./data/episodex.db"),
		BackupPath:        getEnv("BACKUP_PATH", "./data/backups"),
		BackupRetention:   getEnvAsInt("BACKUP_RETENTION", 10),
		BackupHour:        getEnvAsInt("BACKUP_HOUR", 3),
		MediaPath:         getEnv("MEDIA_PATH", "/Volumes/Plex/TV Show"),
		TVDBApiKey:        getEnv("TVDB_API_KEY", ""),
		ScanIntervalHours: getEnvAsInt("SCAN_INTERVAL_HOURS", 1),
		TVDBCheckHour:     getEnvAsInt("TVDB_CHECK_HOUR", 5),
		QbitURL:           getEnv("QBIT_URL", ""),
		QbitUser:          getEnv("QBIT_USER", ""),
		QbitPassword:      getEnv("QBIT_PASSWORD", ""),
	}

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// Validate checks if required configuration values are set
func (c *Config) Validate() error {
	if c.DBPath == "" {
		return fmt.Errorf("DB_PATH is required")
	}

	if c.MediaPath == "" {
		return fmt.Errorf("MEDIA_PATH is required")
	}

	if c.BackupRetention < 1 {
		return fmt.Errorf("BACKUP_RETENTION must be at least 1")
	}

	if c.BackupHour < 0 || c.BackupHour > 23 {
		return fmt.Errorf("BACKUP_HOUR must be between 0 and 23")
	}

	if c.TVDBCheckHour < 0 || c.TVDBCheckHour > 23 {
		return fmt.Errorf("TVDB_CHECK_HOUR must be between 0 and 23")
	}

	if c.ScanIntervalHours < 1 {
		return fmt.Errorf("SCAN_INTERVAL_HOURS must be at least 1")
	}

	if c.QbitURL != "" {
		if c.QbitUser == "" {
			return fmt.Errorf("QBIT_USER is required when QBIT_URL is set")
		}
		if c.QbitPassword == "" {
			return fmt.Errorf("QBIT_PASSWORD is required when QBIT_URL is set")
		}
	}

	return nil
}

// getEnv gets environment variable or returns default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvAsInt gets environment variable as int or returns default value
func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
