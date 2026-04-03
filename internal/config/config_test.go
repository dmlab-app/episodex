package config

import (
	"testing"
)

func TestLoad(t *testing.T) {
	// Set required environment variables
	t.Setenv("MEDIA_PATH", "/test/path")

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

func TestLoadQbitEnvVars(t *testing.T) {
	t.Setenv("MEDIA_PATH", "/test/path")
	t.Setenv("QBIT_URL", "http://192.168.1.100:8080")
	t.Setenv("QBIT_USER", "admin")
	t.Setenv("QBIT_PASSWORD", "secret")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.QbitURL != "http://192.168.1.100:8080" {
		t.Errorf("Expected QbitURL=http://192.168.1.100:8080, got %s", cfg.QbitURL)
	}
	if cfg.QbitUser != "admin" {
		t.Errorf("Expected QbitUser=admin, got %s", cfg.QbitUser)
	}
	if cfg.QbitPassword != "secret" {
		t.Errorf("Expected QbitPassword=secret, got %s", cfg.QbitPassword)
	}
}

func TestLoadQbitNotConfigured(t *testing.T) {
	t.Setenv("MEDIA_PATH", "/test/path")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.QbitURL != "" {
		t.Errorf("Expected empty QbitURL, got %s", cfg.QbitURL)
	}
}

func TestLoadKinozalEnvVars(t *testing.T) {
	t.Setenv("MEDIA_PATH", "/test/path")
	t.Setenv("KINOZAL_USER", "myuser")
	t.Setenv("KINOZAL_PASSWORD", "mypass")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.KinozalUser != "myuser" {
		t.Errorf("Expected KinozalUser=myuser, got %s", cfg.KinozalUser)
	}
	if cfg.KinozalPassword != "mypass" {
		t.Errorf("Expected KinozalPassword=mypass, got %s", cfg.KinozalPassword)
	}
}

func TestLoadKinozalNotConfigured(t *testing.T) {
	t.Setenv("MEDIA_PATH", "/test/path")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.KinozalUser != "" {
		t.Errorf("Expected empty KinozalUser, got %s", cfg.KinozalUser)
	}
	if cfg.KinozalPassword != "" {
		t.Errorf("Expected empty KinozalPassword, got %s", cfg.KinozalPassword)
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
				DBPath:                    "./test.db",
				MediaPath:                 "/media",
				BackupRetention:           10,
				BackupHour:                3,
				TVDBCheckHour:             5,
				ScanIntervalHours:         1,
				TrackerCheckIntervalHours: 6,
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
		{
			name: "qbit url set without user",
			config: Config{
				DBPath:            "./test.db",
				MediaPath:         "/media",
				BackupRetention:   10,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
				QbitURL:           "http://localhost:8080",
				QbitPassword:      "secret",
			},
			wantErr: true,
		},
		{
			name: "qbit url set without password",
			config: Config{
				DBPath:            "./test.db",
				MediaPath:         "/media",
				BackupRetention:   10,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
				QbitURL:           "http://localhost:8080",
				QbitUser:          "admin",
			},
			wantErr: true,
		},
		{
			name: "qbit fully configured",
			config: Config{
				DBPath:                    "./test.db",
				MediaPath:                 "/media",
				BackupRetention:           10,
				BackupHour:                3,
				TVDBCheckHour:             5,
				ScanIntervalHours:         1,
				QbitURL:                   "http://localhost:8080",
				QbitUser:                  "admin",
				QbitPassword:              "secret",
				TrackerCheckIntervalHours: 6,
			},
			wantErr: false,
		},
		{
			name: "qbit not configured is valid",
			config: Config{
				DBPath:                    "./test.db",
				MediaPath:                 "/media",
				BackupRetention:           10,
				BackupHour:                3,
				TVDBCheckHour:             5,
				ScanIntervalHours:         1,
				TrackerCheckIntervalHours: 6,
			},
			wantErr: false,
		},
		{
			name: "kinozal fully configured",
			config: Config{
				DBPath:                    "./test.db",
				MediaPath:                 "/media",
				BackupRetention:           10,
				BackupHour:                3,
				TVDBCheckHour:             5,
				ScanIntervalHours:         1,
				KinozalUser:               "user",
				KinozalPassword:           "pass",
				TrackerCheckIntervalHours: 6,
			},
			wantErr: false,
		},
		{
			name: "kinozal user set without password",
			config: Config{
				DBPath:            "./test.db",
				MediaPath:         "/media",
				BackupRetention:   10,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
				KinozalUser:       "user",
			},
			wantErr: true,
		},
		{
			name: "kinozal password set without user",
			config: Config{
				DBPath:            "./test.db",
				MediaPath:         "/media",
				BackupRetention:   10,
				BackupHour:        3,
				TVDBCheckHour:     5,
				ScanIntervalHours: 1,
				KinozalPassword:   "pass",
			},
			wantErr: true,
		},
		{
			name: "kinozal not configured is valid",
			config: Config{
				DBPath:                    "./test.db",
				MediaPath:                 "/media",
				BackupRetention:           10,
				BackupHour:                3,
				TVDBCheckHour:             5,
				ScanIntervalHours:         1,
				TrackerCheckIntervalHours: 6,
			},
			wantErr: false,
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
