package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	// Set required environment variables
	os.Setenv("MEDIA_PATH", "/test/path")
	defer os.Unsetenv("MEDIA_PATH")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.MediaPath != "/test/path" {
		t.Errorf("Expected MEDIA_PATH=/test/path, got %s", cfg.MediaPath)
	}

	if cfg.Port != "8080" {
		t.Errorf("Expected default Port=8080, got %s", cfg.Port)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				DBPath:            "./test.db",
				MediaPath:         "/media",
				BackupRetention:   10,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
			},
			wantErr: false,
		},
		{
			name: "empty db path",
			config: Config{
				MediaPath:         "/media",
				BackupRetention:   10,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
			},
			wantErr: true,
		},
		{
			name: "empty media path",
			config: Config{
				DBPath:            "./test.db",
				BackupRetention:   10,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
			},
			wantErr: true,
		},
		{
			name: "invalid backup retention",
			config: Config{
				DBPath:            "./test.db",
				MediaPath:         "/media",
				BackupRetention:   0,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
