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
	"github.com/episodex/episodex/internal/audio"
	"github.com/episodex/episodex/internal/config"
	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/qbittorrent"
	"github.com/episodex/episodex/internal/recommender"
	"github.com/episodex/episodex/internal/scanner"
	"github.com/episodex/episodex/internal/scheduler"
	"github.com/episodex/episodex/internal/tmdb"
	"github.com/episodex/episodex/internal/tracker"
	"github.com/episodex/episodex/internal/tracker/kinozal"
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

	// Shared processing lock for concurrent access control
	procLock := database.NewProcessingLock()

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

	// Schedule hourly scan + startup sync for unsynced series.
	// SyncUnsyncedSeries runs after each scan so that series discovered by the
	// scanner (which only stores basic metadata) get fully synced from TVDB.
	// It runs regardless of scan outcome because it operates on series already
	// in the database (scan failure should not block TVDB metadata sync).
	sch.AddTask(scheduler.Task{
		Name:     "media_scan",
		Schedule: &scheduler.IntervalSchedule{Interval: time.Duration(cfg.ScanIntervalHours) * time.Hour},
		Handler: func(_ context.Context) error {
			scanErr := mediaScanner.Scan()
			if scanErr != nil {
				slog.Error("Media scan failed", "error", scanErr)
			}
			if tvdbClient != nil {
				api.SyncUnsyncedSeries(db, tvdbClient)
			}
			return scanErr
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
			result := api.CheckForTVDBUpdates(db, tvdbClient, true)
			if result.Skipped {
				slog.Info("TVDB check skipped: another sync is in progress")
			}
			return nil
		},
	})

	// Initialize qBittorrent client (optional)
	var qbitClient *qbittorrent.Client
	if cfg.QbitURL != "" {
		qbitClient = qbittorrent.NewClient(cfg.QbitURL, cfg.QbitUser, cfg.QbitPassword)
		if err := qbitClient.Login(); err != nil {
			slog.Warn("Failed to login to qBittorrent, will retry on demand", "error", err)
		} else {
			slog.Info("qBittorrent client initialized successfully")
		}
	}

	// Initialize tracker registry and checker
	trackerRegistry := tracker.NewRegistry()
	if cfg.KinozalUser != "" {
		kzClient := kinozal.NewClient(cfg.KinozalUser, cfg.KinozalPassword)
		if err := kzClient.Login(); err != nil {
			slog.Warn("Failed to login to Kinozal, will retry on demand", "error", err)
		} else {
			slog.Info("Kinozal client initialized successfully")
		}
		trackerRegistry.Register(kzClient)
	}

	if qbitClient == nil && len(trackerRegistry.Clients()) > 0 {
		slog.Warn("Tracker clients configured but qBittorrent not set — tracker checker disabled")
	}
	if qbitClient != nil && len(trackerRegistry.Clients()) > 0 {
		trackerChecker := tracker.NewChecker(db, trackerRegistry, qbitClient)
		sch.AddTask(scheduler.Task{
			Name:     "tracker_check",
			Schedule: &scheduler.IntervalSchedule{Interval: time.Duration(cfg.TrackerCheckIntervalHours) * time.Hour},
			Handler: func(_ context.Context) error {
				results := trackerChecker.Check()
				redownloaded := 0
				for _, r := range results {
					if r.Redownloaded {
						redownloaded++
					}
				}
				if redownloaded > 0 {
					slog.Info("Tracker check completed", "checked", len(results), "redownloaded", redownloaded)
				}
				return nil
			},
		})

		audioCutter := audio.New()
		postProcessor := tracker.NewPostDownloadProcessor(db, qbitClient, audioCutter, procLock)
		sch.AddTask(scheduler.Task{
			Name:     "post_download_processing",
			Schedule: &scheduler.IntervalSchedule{Interval: time.Duration(cfg.TrackerCheckIntervalHours) * time.Hour},
			Handler: func(_ context.Context) error {
				results := postProcessor.ProcessCompleted()
				processed := 0
				for _, r := range results {
					processed += r.Processed
				}
				if processed > 0 {
					slog.Info("Post-download processing completed", "processed", processed)
				}
				return nil
			},
		})
	}

	// Initialize recommendation refresh (optional — requires TMDB key + Kinozal searcher)
	if cfg.TMDBApiKey == "" {
		slog.Info("TMDB API key not configured, recommendations feature disabled")
	} else {
		var kzSearcher tracker.SeasonSearcher
		for _, c := range trackerRegistry.Clients() {
			if kz, ok := c.(*kinozal.Client); ok {
				kzSearcher = kz.SeasonSearcher()
				break
			}
		}
		if kzSearcher == nil {
			slog.Info("Kinozal client not configured, recommendations feature disabled")
		} else {
			tmdbClient := tmdb.NewClient(cfg.TMDBApiKey)
			rec := recommender.New(db, tmdbClient, kzSearcher)
			sch.AddTask(scheduler.Task{
				Name:     "recommendation_refresh",
				Schedule: &scheduler.IntervalSchedule{Interval: 24 * time.Hour},
				Handler: func(_ context.Context) error {
					return rec.Refresh()
				},
			})
			slog.Info("Recommendations feature enabled")
		}
	}

	// Start scheduler
	sch.StartAsync()
	defer sch.Stop()

	// Run TVDB check on startup (in background, non-blocking)
	if tvdbClient != nil {
		go func() {
			slog.Info("Running startup TVDB check")
			result := api.CheckForTVDBUpdates(db, tvdbClient, true)
			if result.Skipped {
				slog.Info("Startup TVDB check skipped: another sync is in progress")
			} else {
				slog.Info("Startup TVDB check completed", "checked", result.Checked, "updated", result.Updated)
			}
		}()
	}

	// Initialize HTTP server
	var serverOpts []api.ServerOption
	if clients := trackerRegistry.Clients(); len(clients) > 0 {
		if kz, ok := clients[0].(*kinozal.Client); ok {
			serverOpts = append(serverOpts, api.WithSeasonSearcher(kz.SeasonSearcher()))
		}
	}
	serverOpts = append(serverOpts, api.WithProcessingLock(procLock))
	apiServer := api.NewServer(db, mediaScanner, tvdbClient, qbitClient, cfg.MediaPath, serverOpts...)
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
