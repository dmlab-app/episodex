// Package main is the entry point for the EpisodeX server.
package main

import (
	"context"
	"fmt"
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

			// Collect all series first to avoid holding rows open during network calls.
			// SQLite with MaxOpenConns(1) would deadlock if we query and exec in the same loop.
			type seriesRow struct {
				id, tvdbID, totalSeasons int
				title                    string
			}

			rows, err := db.Query(`
				SELECT id, tvdb_id, title, total_seasons
				FROM series
				WHERE tvdb_id IS NOT NULL
			`)
			if err != nil {
				return err
			}

			var seriesList []seriesRow
			for rows.Next() {
				var s seriesRow
				if err := rows.Scan(&s.id, &s.tvdbID, &s.title, &s.totalSeasons); err != nil {
					continue
				}
				seriesList = append(seriesList, s)
			}
			rows.Close() //nolint:errcheck
			if err := rows.Err(); err != nil {
				return fmt.Errorf("error reading series rows: %w", err)
			}

			var checked, updated int

			for _, s := range seriesList {
				checked++

				// Get current season count from TVDB
				newTotalSeasons, err := tvdbClient.GetTotalSeasons(s.tvdbID)
				if err != nil {
					slog.Error("Failed to get TVDB seasons", "series_id", s.id, "error", err)
					continue
				}

				// Compare with database
				if newTotalSeasons > s.totalSeasons {
					// Update database
					_, err = db.Exec(`
						UPDATE series
						SET total_seasons = ?, updated_at = CURRENT_TIMESTAMP
						WHERE id = ?
					`, newTotalSeasons, s.id)

					if err != nil {
						slog.Error("Failed to update series", "series_id", s.id, "error", err)
						continue
					}

					// Create alert
					message := "New seasons available for " + s.title

					if _, err = db.Exec(`
						INSERT INTO system_alerts (type, message, created_at, dismissed)
						VALUES (?, ?, CURRENT_TIMESTAMP, 0)
					`, "new_seasons", message); err != nil {
						slog.Error("Failed to create alert", "series_id", s.id, "error", err)
					}

					slog.Info("Detected new seasons", "series", s.title, "old", s.totalSeasons, "new", newTotalSeasons)
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
