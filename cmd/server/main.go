// Package main is the entry point for the EpisodeX server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/episodex/episodex/internal/api"
	"github.com/episodex/episodex/internal/config"
	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/scanner"
	"github.com/episodex/episodex/internal/scheduler"
	"github.com/episodex/episodex/internal/tvdb"
)

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	slog.Info("Starting EpisodeX server...")

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	slog.Info("Configuration loaded", "port", cfg.Port, "db_path", cfg.DBPath)

	// Initialize database
	db, err := database.New(cfg.DBPath)
	if err != nil {
		slog.Error("Failed to initialize database", "error", err)
		os.Exit(1)
	}
	defer db.Close() //nolint:errcheck

	// Initialize backup manager
	backupManager := database.NewBackupManager(db, cfg.DBPath, cfg.BackupPath, cfg.BackupRetention)

	// Initialize TVDB client
	var tvdbClient *tvdb.Client
	if cfg.TVDBApiKey != "" {
		tvdbClient = tvdb.NewClient(cfg.TVDBApiKey)
		if err := tvdbClient.Login(); err != nil {
			slog.Warn("Failed to login to TVDB", "error", err)
			tvdbClient = nil
		} else {
			slog.Info("TVDB client initialized successfully")
		}
	} else {
		slog.Warn("TVDB API key not configured, TVDB features will be disabled")
	}

	// Initialize scanner with TVDB client
	mediaScanner := scanner.New(db, tvdbClient, cfg.MediaPath)

	// Initialize scheduler
	sch := scheduler.New()

	// Schedule daily backup
	sch.AddTask(scheduler.Task{
		Name:     "database_backup",
		Schedule: &scheduler.DailySchedule{Hour: cfg.BackupHour},
		Handler: func(_ context.Context) error {
			return backupManager.Backup()
		},
	})

	// Schedule hourly scan
	sch.AddTask(scheduler.Task{
		Name:     "media_scan",
		Schedule: &scheduler.IntervalSchedule{Interval: time.Duration(cfg.ScanIntervalHours) * time.Hour},
		Handler: func(_ context.Context) error {
			return mediaScanner.Scan()
		},
	})

	// Schedule daily TVDB check
	sch.AddTask(scheduler.Task{
		Name:     "tvdb_check",
		Schedule: &scheduler.DailySchedule{Hour: cfg.TVDBCheckHour},
		Handler: func(_ context.Context) error {
			if tvdbClient == nil {
				slog.Info("TVDB check skipped - client not configured")
				return nil
			}

			slog.Info("Running scheduled TVDB check")

			// Get all series with TVDB IDs
			rows, err := db.Query(`
				SELECT id, tvdb_id, title, total_seasons
				FROM series
				WHERE tvdb_id IS NOT NULL
			`)
			if err != nil {
				return err
			}
			defer rows.Close() //nolint:errcheck

			var checked, updated int

			for rows.Next() {
				var id, tvdbID, totalSeasons int
				var title string

				if err := rows.Scan(&id, &tvdbID, &title, &totalSeasons); err != nil {
					continue
				}

				checked++

				// Get current season count from TVDB
				newTotalSeasons, err := tvdbClient.GetTotalSeasons(tvdbID)
				if err != nil {
					slog.Error("Failed to get TVDB seasons", "series_id", id, "error", err)
					continue
				}

				// Compare with database
				if newTotalSeasons > totalSeasons {
					// Update database
					_, err = db.Exec(`
						UPDATE series
						SET total_seasons = ?, updated_at = CURRENT_TIMESTAMP
						WHERE id = ?
					`, newTotalSeasons, id)

					if err != nil {
						slog.Error("Failed to update series", "series_id", id, "error", err)
						continue
					}

					// Create alert
					newSeasons := newTotalSeasons - totalSeasons
					message := "New seasons available for " + title

					if _, err = db.Exec(`
						INSERT INTO system_alerts (type, message, created_at, dismissed)
						VALUES (?, ?, CURRENT_TIMESTAMP, 0)
					`, "new_seasons", message); err != nil {
						slog.Error("Failed to create alert", "series_id", id, "error", err)
					}

					_ = newSeasons // Use the variable to avoid compiler warning

					slog.Info("Detected new seasons", "series", title, "old", totalSeasons, "new", newTotalSeasons)
					updated++
				}
			}

			slog.Info("TVDB check completed", "checked", checked, "updated", updated)
			return nil
		},
	})

	// Start scheduler
	sch.StartAsync()
	defer sch.Stop()

	// Initialize HTTP server
	apiServer := api.NewServer(db, mediaScanner, tvdbClient)
	httpServer := &http.Server{
		Addr:         cfg.Host + ":" + cfg.Port,
		Handler:      apiServer.Handler(),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start HTTP server in goroutine
	go func() {
		slog.Info("HTTP server starting", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down server...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(ctx); err != nil {
		slog.Error("Server forced to shutdown", "error", err)
	}

	slog.Info("Server stopped gracefully")
}
