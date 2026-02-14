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
				id, tvdbID, totalSeasons, airedSeasons, maxOwned int
				title                                            string
				updatedAt                                        time.Time
			}

			rows, err := db.Query(`
				SELECT s.id, s.tvdb_id, s.title, s.total_seasons, s.aired_seasons,
					COALESCE((SELECT MAX(sn.season_number) FROM seasons sn WHERE sn.series_id = s.id AND sn.is_owned = 1 AND sn.season_number > 0), 0),
					s.updated_at
				FROM series s
				WHERE s.tvdb_id IS NOT NULL
			`)
			if err != nil {
				return err
			}

			var seriesList []seriesRow
			for rows.Next() {
				var s seriesRow
				if err := rows.Scan(&s.id, &s.tvdbID, &s.title, &s.totalSeasons, &s.airedSeasons, &s.maxOwned, &s.updatedAt); err != nil {
					continue
				}
				seriesList = append(seriesList, s)
			}
			rows.Close() //nolint:errcheck
			if err := rows.Err(); err != nil {
				return fmt.Errorf("error reading series rows: %w", err)
			}

			var checked, updated, synced int

			for _, s := range seriesList {
				checked++

				// Get current season details from TVDB (includes aired status)
				details, err := tvdbClient.GetSeriesDetails(s.tvdbID)
				if err != nil {
					slog.Error("Failed to get TVDB seasons", "series_id", s.id, "error", err)
					continue
				}

				newTotalSeasons := len(details.Seasons)
				newAiredSeasons := tvdb.MaxAiredSeasonNumber(details.Seasons)

				// Compare with database - update if total or aired count changed
				if newTotalSeasons != s.totalSeasons || newAiredSeasons != s.airedSeasons {
					_, err = db.Exec(`
						UPDATE series
						SET total_seasons = ?, aired_seasons = ?, updated_at = CURRENT_TIMESTAMP
						WHERE id = ?
					`, newTotalSeasons, newAiredSeasons, s.id)

					if err != nil {
						slog.Error("Failed to update series", "series_id", s.id, "error", err)
						continue
					}

					// Create alert only if new aired seasons exist beyond user's max owned season
					// and user actually has owned seasons (maxOwned > 0)
					if newAiredSeasons > s.maxOwned && s.maxOwned > 0 {
						message := "New seasons available for " + s.title

						if _, err = db.Exec(`
							INSERT INTO system_alerts (type, message, created_at, dismissed)
							SELECT ?, ?, CURRENT_TIMESTAMP, 0
							WHERE NOT EXISTS (
								SELECT 1 FROM system_alerts WHERE type = ? AND message = ? AND dismissed = 0
							)
						`, "new_seasons", message, "new_seasons", message); err != nil {
							slog.Error("Failed to create alert", "series_id", s.id, "error", err)
						}
					}

					slog.Info("Detected season changes", "series", s.title,
						"old_total", s.totalSeasons, "new_total", newTotalSeasons,
						"old_aired", s.airedSeasons, "new_aired", newAiredSeasons)
					updated++
				}

				// Auto-sync full metadata for series not updated in 7+ days
				if time.Since(s.updatedAt) > 7*24*time.Hour {
					slog.Info("Auto-syncing stale series metadata", "series", s.title, "last_updated", s.updatedAt)
					if err := api.SyncSeriesMetadata(db, tvdbClient, int64(s.id), s.tvdbID); err != nil {
						slog.Error("Failed to auto-sync series", "series_id", s.id, "error", err)
					} else {
						synced++
					}
				}
			}

			slog.Info("TVDB check completed", "checked", checked, "updated", updated, "synced", synced)
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
		WriteTimeout: 120 * time.Second, // SSE handler uses ResponseController.SetWriteDeadline to override
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
