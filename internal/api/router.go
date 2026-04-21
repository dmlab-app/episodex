// Package api provides HTTP handlers for the EpisodeX REST API.
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/episodex/episodex/internal/audio"
	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/qbittorrent"
	"github.com/episodex/episodex/internal/recommender"
	"github.com/episodex/episodex/internal/scanner"
	"github.com/episodex/episodex/internal/tracker"
	"github.com/episodex/episodex/internal/tvdb"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// Server represents the API server
type Server struct {
	db             *database.DB
	scanner        *scanner.Scanner
	tvdbClient     *tvdb.Client
	qbitClient     *qbittorrent.Client
	audioCutter    *audio.AudioCutter
	seasonSearcher tracker.SeasonSearcher
	recommender    *recommender.Recommender
	router         *chi.Mux
	mediaPath      string
	procLock       *database.ProcessingLock
}

// NewServer creates a new API server.
// mediaPath is the root directory of the media library; filesystem deletions
// are restricted to paths under this directory. Pass "" to disable the boundary
// check (e.g. in tests that don't exercise filesystem operations).
func NewServer(db *database.DB, sc *scanner.Scanner, tvdbClient *tvdb.Client, qbitClient *qbittorrent.Client, mediaPath string, opts ...ServerOption) *Server {
	s := &Server{
		db:          db,
		scanner:     sc,
		tvdbClient:  tvdbClient,
		qbitClient:  qbitClient,
		audioCutter: audio.New(),
		router:      chi.NewRouter(),
		mediaPath:   mediaPath,
		procLock:    database.NewProcessingLock(),
	}
	for _, opt := range opts {
		opt(s)
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s
}

// ServerOption configures optional Server dependencies.
type ServerOption func(*Server)

// WithSeasonSearcher sets the tracker client used to search for season torrents.
func WithSeasonSearcher(ss tracker.SeasonSearcher) ServerOption {
	return func(s *Server) {
		s.seasonSearcher = ss
	}
}

// WithProcessingLock sets a shared processing lock for concurrent access control.
func WithProcessingLock(pl *database.ProcessingLock) ServerOption {
	return func(s *Server) {
		s.procLock = pl
	}
}

// WithRecommender sets the recommender used by the recommendations endpoints.
func WithRecommender(rec *recommender.Recommender) ServerOption {
	return func(s *Server) {
		s.recommender = rec
	}
}

// ProcessingLock returns the server's processing lock for sharing with other components.
func (s *Server) ProcessingLock() *database.ProcessingLock {
	return s.procLock
}

// setupMiddleware configures middleware chain
func (s *Server) setupMiddleware() {
	s.router.Use(middleware.RequestID)
	s.router.Use(middleware.RealIP)
	s.router.Use(s.loggingMiddleware)
	s.router.Use(middleware.Recoverer)

	// CORS configuration
	s.router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"http://localhost:*", "http://127.0.0.1:*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		ExposedHeaders:   []string{"Link"},
		AllowCredentials: true,
		MaxAge:           300,
	}))
}

// setupRoutes configures all API routes
func (s *Server) setupRoutes() {
	// Health check
	s.router.Get("/api/health", s.handleHealth)

	// SSE endpoints without timeout — audio processing can take minutes
	s.router.Post("/api/series/{id}/seasons/{num}/audio/process", s.handleProcessAudioStream)
	s.router.Post("/api/series/{id}/seasons/{num}/audio/set-default", s.handleSetDefaultTrackStream)

	s.router.Route("/api", func(r chi.Router) {
		// Apply timeout to all API routes (SSE is registered above, outside this group)
		r.Use(middleware.Timeout(60 * time.Second))
		// Limit request body size to 1MB to prevent memory exhaustion
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
				next.ServeHTTP(w, r)
			})
		})

		// Series endpoints
		r.Route("/series", func(r chi.Router) {
			r.Get("/", s.handleListSeries)             // GET /api/series
			r.Post("/", s.handleCreateSeries)          // POST /api/series
			r.Get("/{id}", s.handleGetSeries)          // GET /api/series/:id
			r.Delete("/{id}", s.handleDeleteSeries)    // DELETE /api/series/:id
			r.Post("/{id}/match", s.handleMatchSeries) // POST /api/series/:id/match

			// Seasons endpoints
			r.Route("/{id}/seasons", func(r chi.Router) {
				r.Get("/", s.handleListSeasons)                              // GET /api/series/:id/seasons
				r.Get("/{num}", s.handleGetSeason)                           // GET /api/series/:id/seasons/:num
				r.Put("/{num}", s.handleUpdateSeason)                        // PUT /api/series/:id/seasons/:num
				r.Get("/{num}/audio", s.handleGetAudioTracks)                // GET /api/series/:id/seasons/:num/audio
				r.Post("/{num}/audio/preview", s.handleGenerateAudioPreview) // POST /api/series/:id/seasons/:num/audio/preview
				r.Get("/{num}/tracker", s.handleGetSeasonTracker)            // GET /api/series/:id/seasons/:num/tracker
			})
		})

		// Voice actors endpoint

		// Audio preview serving endpoint
		r.Get("/audio/preview/{hash}", s.handleServeAudioPreview) // GET /api/audio/preview/:hash

		// System endpoints
		r.Get("/alerts", s.handleGetAlerts)                  // GET /api/alerts
		r.Post("/alerts/{id}/dismiss", s.handleDismissAlert) // POST /api/alerts/:id/dismiss

		// Scan endpoints
		r.Post("/scan/trigger", s.handleTriggerScan) // POST /api/scan/trigger

		// Updates endpoints
		r.Get("/updates", s.handleGetUpdates)          // GET /api/updates
		r.Post("/updates/check", s.handleCheckUpdates) // POST /api/updates/check

		// Next seasons endpoint
		r.Get("/next-seasons", s.handleGetNextSeasons) // GET /api/next-seasons

		// Recommendations endpoints
		r.Get("/recommendations", s.handleGetRecommendations)
		r.Post("/recommendations/refresh", s.handleRefreshRecommendations)
		r.Get("/recommendations/blacklist", s.handleGetBlacklist)
		r.Post("/recommendations/blacklist", s.handleAddBlacklist)
		r.Delete("/recommendations/blacklist/{tvdb_id}", s.handleRemoveBlacklist)

		// Search endpoint
		r.Get("/search", s.handleSearch) // GET /api/search
	})

	// Serve static files
	fileServer := http.FileServer(http.Dir("./web/static"))
	s.router.Handle("/static/*", http.StripPrefix("/static", fileServer))

	// Serve index.html for root
	s.router.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "./web/templates/index.html")
	})
}

// loggingMiddleware logs HTTP requests
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		duration := time.Since(start)

		slog.Info("HTTP request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"duration", duration,
			"bytes", ww.BytesWritten(),
			"ip", r.RemoteAddr,
		)
	})
}

// Handler returns the HTTP handler
func (s *Server) Handler() http.Handler {
	return s.router
}

// handleHealth handles health check endpoint
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	// Check database connection
	if err := s.db.Ping(); err != nil {
		s.respondError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"version": "1.0.0",
		"time":    time.Now().UTC(),
	})
}

// Series handlers
func (s *Server) handleListSeries(w http.ResponseWriter, _ *http.Request) {
	query := `
		SELECT s.id, s.tvdb_id, s.title, s.original_title, s.poster_url, s.status, s.total_seasons, s.created_at,
			(SELECT COUNT(*) FROM seasons sn WHERE sn.series_id = s.id AND sn.downloaded = 1) as downloaded_seasons
		FROM series s
		ORDER BY s.created_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch series")
		return
	}
	defer rows.Close() //nolint:errcheck

	series := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var tvdbID *int
		var title string
		var originalTitle, posterURL, status *string
		var totalSeasons, downloadedSeasons int
		var createdAt time.Time

		if err := rows.Scan(&id, &tvdbID, &title, &originalTitle, &posterURL, &status, &totalSeasons, &createdAt, &downloadedSeasons); err != nil {
			slog.Error("Failed to scan series row", "error", err)
			continue
		}

		item := map[string]interface{}{
			"id":                 id,
			"title":              title,
			"total_seasons":      totalSeasons,
			"downloaded_seasons": downloadedSeasons,
			"created_at":         createdAt,
		}

		if tvdbID != nil {
			item["tvdb_id"] = *tvdbID
		}
		if originalTitle != nil {
			item["original_title"] = *originalTitle
		}
		if posterURL != nil {
			item["poster_url"] = *posterURL
		}
		if status != nil {
			item["status"] = *status
		} else {
			item["status"] = "unknown"
		}

		series = append(series, item)
	}

	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading series")
		return
	}

	s.respondJSON(w, http.StatusOK, series)
}

func (s *Server) handleCreateSeries(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TVDBId *int   `json:"tvdb_id"`
		Title  string `json:"title"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	var seriesID int64
	var title, originalTitle, posterURL, status string
	var totalSeasons int

	// Determine creation mode based on request
	switch {
	case req.TVDBId != nil && *req.TVDBId > 0:
		// TVDB ID provided, fetch metadata from TVDB
		if s.tvdbClient == nil {
			s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
			return
		}

		details, err := s.tvdbClient.GetSeriesDetailsWithRussian(*req.TVDBId)
		if err != nil {
			slog.Error("Failed to fetch series from TVDB", "tvdb_id", *req.TVDBId, "error", err)
			s.respondError(w, http.StatusInternalServerError, "failed to fetch series metadata")
			return
		}

		// Name = Russian (or English fallback), OriginalName = English
		title = details.Name
		originalTitle = details.OriginalName
		posterURL = details.Image
		status = details.Status
		totalSeasons = len(details.Seasons)

		// Insert series with TVDB metadata (aired_seasons=0, corrected by SyncSeriesMetadata)
		result, err := s.db.Exec(`
			INSERT INTO series (tvdb_id, title, original_title, poster_url, status, total_seasons, aired_seasons, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, *req.TVDBId, title, originalTitle, posterURL, status, totalSeasons)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE constraint failed") {
				s.respondError(w, http.StatusConflict, "series already exists")
				return
			}
			slog.Error("Failed to create series", "tvdb_id", *req.TVDBId, "error", err)
			s.respondError(w, http.StatusInternalServerError, "failed to create series")
			return
		}

		var idErr error
		seriesID, idErr = result.LastInsertId()
		if idErr != nil {
			s.respondError(w, http.StatusInternalServerError, "failed to get series ID")
			return
		}
		slog.Info("Created series from TVDB", "id", seriesID, "tvdb_id", *req.TVDBId, "title", title)

	case req.Title != "":
		// Manual entry without TVDB metadata
		title = req.Title
		status = "unknown"

		result, err := s.db.Exec(`
			INSERT INTO series (title, status, created_at, updated_at)
			VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		`, title, status)
		if err != nil {
			s.respondError(w, http.StatusInternalServerError, "failed to create series")
			return
		}

		var idErr error
		seriesID, idErr = result.LastInsertId()
		if idErr != nil {
			s.respondError(w, http.StatusInternalServerError, "failed to get series ID")
			return
		}
		slog.Info("Created manual series", "id", seriesID, "title", title)

	default:
		s.respondError(w, http.StatusBadRequest, "either tvdb_id or title is required")
		return
	}

	// Return created series
	response := map[string]interface{}{
		"id":            seriesID,
		"title":         title,
		"status":        status,
		"total_seasons": totalSeasons,
	}

	if req.TVDBId != nil {
		response["tvdb_id"] = *req.TVDBId
	}
	if originalTitle != "" {
		response["original_title"] = originalTitle
	}
	if posterURL != "" {
		response["poster_url"] = posterURL
	}

	s.respondJSON(w, http.StatusCreated, response)
}

func (s *Server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	// Get series info with full metadata
	var seriesInfo struct {
		CreatedAt     time.Time
		Runtime       *int
		OriginalTitle *string
		Overview      *string
		PosterURL     *string
		BackdropURL   *string
		Status        *string
		TVDBId        *int
		Rating        *float64
		Year          *int
		ContentRating *string
		Genres        *string
		Networks      *string
		Title         string
		TotalSeasons  int
		ID            int
	}

	query := `
		SELECT id, tvdb_id, title, original_title, overview,
			poster_url, backdrop_url, status,
			year, runtime, rating, content_rating,
			genres, networks, total_seasons, created_at
		FROM series
		WHERE id = ?
	`

	err = s.db.QueryRow(query, id).Scan(
		&seriesInfo.ID,
		&seriesInfo.TVDBId,
		&seriesInfo.Title,
		&seriesInfo.OriginalTitle,
		&seriesInfo.Overview,
		&seriesInfo.PosterURL,
		&seriesInfo.BackdropURL,
		&seriesInfo.Status,
		&seriesInfo.Year,
		&seriesInfo.Runtime,
		&seriesInfo.Rating,
		&seriesInfo.ContentRating,
		&seriesInfo.Genres,
		&seriesInfo.Networks,
		&seriesInfo.TotalSeasons,
		&seriesInfo.CreatedAt,
	)

	if err == sql.ErrNoRows {
		s.respondError(w, http.StatusNotFound, "series not found")
		return
	}
	if err != nil {
		slog.Error("Failed to fetch series", "id", id, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch series")
		return
	}

	// Get seasons
	seasonsQuery := `
		SELECT sn.season_number, sn.folder_path, sn.downloaded, sn.track_name, sn.discovered_at
		FROM seasons sn
		WHERE sn.series_id = ?
		ORDER BY sn.season_number
	`

	rows, err := s.db.Query(seasonsQuery, id)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch seasons")
		return
	}
	defer rows.Close() //nolint:errcheck

	seasons := []map[string]interface{}{}
	for rows.Next() {
		var seasonNum int
		var folderPath, trackName *string
		var downloaded bool
		var discoveredAt *time.Time

		if err := rows.Scan(&seasonNum, &folderPath, &downloaded, &trackName, &discoveredAt); err != nil {
			slog.Error("Failed to scan season row", "error", err)
			continue
		}

		season := map[string]interface{}{
			"season_number": seasonNum,
			"downloaded":    downloaded,
			"on_disk":       folderPath != nil,
		}

		if folderPath != nil {
			season["folder_path"] = *folderPath
		}
		if trackName != nil {
			season["track_name"] = *trackName
		}
		if discoveredAt != nil {
			season["discovered_at"] = *discoveredAt
		}

		seasons = append(seasons, season)
	}

	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading seasons")
		return
	}

	// Get top 10 characters
	characters := []map[string]interface{}{}
	charactersQuery := `
		SELECT character_name, actor_name, image_url, sort_order
		FROM series_characters
		WHERE series_id = ?
		ORDER BY sort_order
		LIMIT 10
	`
	charRows, err := s.db.Query(charactersQuery, id)
	if err == nil {
		defer charRows.Close() //nolint:errcheck
		for charRows.Next() {
			var characterName, actorName, imageURL *string
			var sortOrder *int
			if err := charRows.Scan(&characterName, &actorName, &imageURL, &sortOrder); err != nil {
				slog.Error("Failed to scan character row", "error", err)
				continue
			}
			char := map[string]interface{}{}
			if characterName != nil {
				char["character_name"] = *characterName
			}
			if actorName != nil {
				char["actor_name"] = *actorName
			}
			if imageURL != nil {
				char["image_url"] = *imageURL
			}
			if sortOrder != nil {
				char["sort_order"] = *sortOrder
			}
			characters = append(characters, char)
		}
		if err := charRows.Err(); err != nil {
			slog.Error("Error iterating character rows", "error", err)
		}
	}

	// Build response
	response := map[string]interface{}{
		"id":                 seriesInfo.ID,
		"title":              seriesInfo.Title,
		"total_seasons":      seriesInfo.TotalSeasons,
		"downloaded_seasons": countDownloadedSeasons(seasons),
		"seasons":            seasons,
		"characters":         characters,
		"created_at":         seriesInfo.CreatedAt,
	}

	if seriesInfo.TVDBId != nil {
		response["tvdb_id"] = *seriesInfo.TVDBId
	}
	if seriesInfo.OriginalTitle != nil {
		response["original_title"] = *seriesInfo.OriginalTitle
	}
	if seriesInfo.Overview != nil {
		response["overview"] = *seriesInfo.Overview
	}
	if seriesInfo.PosterURL != nil {
		response["poster_url"] = *seriesInfo.PosterURL
	}
	if seriesInfo.BackdropURL != nil {
		response["backdrop_url"] = *seriesInfo.BackdropURL
	}
	if seriesInfo.Status != nil {
		response["status"] = *seriesInfo.Status
	} else {
		response["status"] = "unknown"
	}
	if seriesInfo.Year != nil {
		response["year"] = *seriesInfo.Year
	}
	if seriesInfo.Runtime != nil {
		response["runtime"] = *seriesInfo.Runtime
	}
	if seriesInfo.Rating != nil {
		response["rating"] = *seriesInfo.Rating
	}
	if seriesInfo.ContentRating != nil {
		response["content_rating"] = *seriesInfo.ContentRating
	}

	// Parse JSON fields
	if seriesInfo.Genres != nil {
		var genres []interface{}
		if err := json.Unmarshal([]byte(*seriesInfo.Genres), &genres); err == nil {
			response["genres"] = genres
		}
	}
	if seriesInfo.Networks != nil {
		var networks []interface{}
		if err := json.Unmarshal([]byte(*seriesInfo.Networks), &networks); err == nil {
			response["networks"] = networks
		}
	}

	s.respondJSON(w, http.StatusOK, response)
}

func (s *Server) handleDeleteSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	// Collect file paths and folder paths before DB deletion (CASCADE will remove records).
	// Fail if lookups error — proceeding would orphan files on disk.
	filePaths, err := s.db.GetMediaFilePathsBySeriesID(id)
	if err != nil {
		slog.Error("Failed to get media file paths for series", "id", id, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to look up media files for deletion")
		return
	}
	folderPaths, err := s.db.GetSeasonFolderPaths(id)
	if err != nil {
		slog.Error("Failed to get season folder paths for series", "id", id, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to look up season folders for deletion")
		return
	}

	// Delete media files from disk (only within media library boundary)
	var filesRemoved, foldersRemoved int
	for _, fp := range filePaths {
		if !s.isWithinMediaPath(fp) {
			slog.Warn("Skipping file outside media path", "path", fp, "media_path", s.mediaPath)
			continue
		}
		if err := os.Remove(fp); err != nil {
			slog.Warn("Failed to remove media file", "path", fp, "error", err)
		} else {
			filesRemoved++
			slog.Info("Removed media file", "path", fp)
		}
	}

	// Remove empty season folders (only within media library boundary)
	for _, fp := range folderPaths {
		if !s.isWithinMediaPath(fp) {
			slog.Warn("Skipping folder outside media path", "path", fp, "media_path", s.mediaPath)
			continue
		}
		if err := os.Remove(fp); err != nil {
			slog.Warn("Failed to remove season folder", "path", fp, "error", err)
		} else {
			foldersRemoved++
			slog.Info("Removed season folder", "path", fp)
		}
	}

	query := "DELETE FROM series WHERE id = ?"
	result, err := s.db.Exec(query, id)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to delete series")
		return
	}

	rows, err := result.RowsAffected()
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to verify deletion")
		return
	}
	if rows == 0 {
		s.respondError(w, http.StatusNotFound, "series not found")
		return
	}

	slog.Info("Deleted series", "id", id, "files_removed", filesRemoved, "folders_removed", foldersRemoved)
	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// isWithinMediaPath checks that a file path is inside the configured media library root.
// Returns true if no media path is configured (boundary check disabled).
func (s *Server) isWithinMediaPath(filePath string) bool {
	if s.mediaPath == "" {
		return true
	}
	absFile, err := filepath.EvalSymlinks(filePath)
	if err != nil {
		// File may not exist (already deleted); fall back to lexical check
		absFile = filepath.Clean(filePath)
		if !filepath.IsAbs(absFile) {
			return false
		}
	}
	absMedia, err := filepath.EvalSymlinks(s.mediaPath)
	if err != nil {
		absMedia = filepath.Clean(s.mediaPath)
		if !filepath.IsAbs(absMedia) {
			return false
		}
	}
	// When absMedia is the filesystem root ("/"), every absolute path is inside it.
	if absMedia == string(filepath.Separator) {
		return filepath.IsAbs(absFile)
	}
	return strings.HasPrefix(absFile, absMedia+string(filepath.Separator)) || absFile == absMedia
}

func (s *Server) handleMatchSeries(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	var req struct {
		TVDBId int `json:"tvdb_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TVDBId <= 0 {
		s.respondError(w, http.StatusBadRequest, "tvdb_id is required")
		return
	}

	// Check if series exists
	var currentTVDBId *int
	var title string
	err = s.db.QueryRow("SELECT tvdb_id, title FROM series WHERE id = ?", id).Scan(&currentTVDBId, &title)
	if err == sql.ErrNoRows {
		s.respondError(w, http.StatusNotFound, "series not found")
		return
	}
	if err != nil {
		slog.Error("Failed to fetch series for match", "id", id, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch series")
		return
	}

	// Check if this TVDB ID is already used by another series
	var existingSeriesID int64
	var existingTitle string
	err = s.db.QueryRow("SELECT id, title FROM series WHERE tvdb_id = ? AND id != ?", req.TVDBId, id).Scan(&existingSeriesID, &existingTitle)
	if err != nil && err != sql.ErrNoRows {
		slog.Error("Failed to check for duplicate TVDB ID", "tvdb_id", req.TVDBId, "error", err)
		s.respondError(w, http.StatusInternalServerError, "database error")
		return
	}
	if err == nil {
		// Another series already has this TVDB ID - merge seasons into existing and delete duplicate
		slog.Info("Merging duplicate series", "from_id", id, "to_id", existingSeriesID, "tvdb_id", req.TVDBId)

		tx, err := s.db.Begin()
		if err != nil {
			s.respondError(w, http.StatusInternalServerError, "failed to start transaction")
			return
		}
		defer tx.Rollback() //nolint:errcheck

		// Defer FK checks to commit time — moving seasons changes parent keys
		// referenced by media_files, which would fail mid-transaction without CASCADE
		if _, err := tx.Exec("PRAGMA defer_foreign_keys = ON"); err != nil {
			s.respondError(w, http.StatusInternalServerError, "failed to defer FK checks")
			return
		}

		// For overlapping seasons, merge all data from source into destination
		_, err = tx.Exec(`
			UPDATE seasons
			SET folder_path = COALESCE((SELECT src.folder_path FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number), folder_path),
				downloaded = MAX(downloaded, COALESCE((SELECT src.downloaded FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number), 0)),
				aired_episodes = MAX(aired_episodes, COALESCE((SELECT src.aired_episodes FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number), 0)),
				track_name = COALESCE(track_name, (SELECT src.track_name FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				discovered_at = COALESCE(discovered_at, (SELECT src.discovered_at FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				tvdb_season_id = COALESCE(tvdb_season_id, (SELECT src.tvdb_season_id FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				name = COALESCE(name, (SELECT src.name FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				poster_url = COALESCE(poster_url, (SELECT src.poster_url FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number))
			WHERE series_id = ? AND season_number IN (
				SELECT season_number FROM seasons WHERE series_id = ?
			)
		`, id, id, id, id, id, id, id, id, existingSeriesID, id)
		if err != nil {
			slog.Error("Failed to update overlapping seasons", "error", err)
			s.respondError(w, http.StatusInternalServerError, "failed to merge seasons")
			return
		}

		// Move non-overlapping seasons from current series to existing one
		_, err = tx.Exec(`
			UPDATE seasons
			SET series_id = ?
			WHERE series_id = ? AND season_number NOT IN (
				SELECT season_number FROM seasons WHERE series_id = ?
			)
		`, existingSeriesID, id, existingSeriesID)
		if err != nil {
			slog.Error("Failed to move seasons", "error", err)
			s.respondError(w, http.StatusInternalServerError, "failed to merge seasons")
			return
		}

		// Move media_files from old series to existing one
		_, err = tx.Exec("UPDATE media_files SET series_id = ? WHERE series_id = ?", existingSeriesID, id)
		if err != nil {
			slog.Error("Failed to move media files", "error", err)
			s.respondError(w, http.StatusInternalServerError, "failed to merge media files")
			return
		}

		// Delete remaining duplicate seasons (if any overlap)
		_, err = tx.Exec("DELETE FROM seasons WHERE series_id = ?", id)
		if err != nil {
			slog.Error("Failed to delete duplicate seasons", "error", err)
			s.respondError(w, http.StatusInternalServerError, "failed to merge seasons")
			return
		}

		// Delete the duplicate series
		_, err = tx.Exec("DELETE FROM series WHERE id = ?", id)
		if err != nil {
			slog.Error("Failed to delete duplicate series", "error", err)
			s.respondError(w, http.StatusInternalServerError, "failed to delete duplicate")
			return
		}

		if err := tx.Commit(); err != nil {
			s.respondError(w, http.StatusInternalServerError, "failed to commit merge")
			return
		}

		slog.Info("Merged and deleted duplicate series", "deleted_id", id, "merged_into", existingSeriesID, "title", existingTitle)

		// Return the existing series info
		s.respondJSON(w, http.StatusOK, map[string]interface{}{
			"id":      existingSeriesID,
			"tvdb_id": req.TVDBId,
			"title":   existingTitle,
			"merged":  true,
			"message": fmt.Sprintf("Seasons merged into '%s'", existingTitle),
		})
		return
	}

	// Check if TVDB client is available
	if s.tvdbClient == nil {
		s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
		return
	}

	// Fetch metadata from TVDB with Russian translation
	details, err := s.tvdbClient.GetSeriesDetailsWithRussian(req.TVDBId)
	if err != nil {
		slog.Error("Failed to fetch series from TVDB", "tvdb_id", req.TVDBId, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch series metadata from TVDB")
		return
	}

	// Update series with TVDB metadata (Name = Russian, OriginalName = English)
	// aired_seasons is not set here — SyncSeriesMetadata (called below) computes it from episodes
	_, err = s.db.Exec(`
		UPDATE series
		SET tvdb_id = ?, title = ?, original_title = ?, poster_url = ?, status = ?, total_seasons = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, req.TVDBId, details.Name, details.OriginalName, details.Image, details.Status, len(details.Seasons), id)

	if err != nil {
		slog.Error("Failed to update series", "id", id, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to update series")
		return
	}

	slog.Info("Matched series with TVDB", "series_id", id, "tvdb_id", req.TVDBId, "title", details.Name)

	// Full sync: overview, genres, networks, characters, seasons
	if err := SyncSeriesMetadata(s.db, s.tvdbClient, id, req.TVDBId); err != nil {
		slog.Warn("Full sync after match failed (basic match saved)", "series_id", id, "tvdb_id", req.TVDBId, "error", err)
	}

	// Return updated series from DB
	var updated struct {
		TVDBId        *int
		OriginalTitle *string
		PosterURL     *string
		Status        *string
		Title         string
		TotalSeasons  int
	}
	err = s.db.QueryRow(`SELECT tvdb_id, title, original_title, poster_url, status, total_seasons FROM series WHERE id = ?`, id).
		Scan(&updated.TVDBId, &updated.Title, &updated.OriginalTitle, &updated.PosterURL, &updated.Status, &updated.TotalSeasons)
	if err != nil {
		slog.Error("Failed to read series after match", "id", id, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to read updated series")
		return
	}

	response := map[string]interface{}{
		"id":            id,
		"title":         updated.Title,
		"total_seasons": updated.TotalSeasons,
	}
	if updated.TVDBId != nil {
		response["tvdb_id"] = *updated.TVDBId
	}
	if updated.OriginalTitle != nil {
		response["original_title"] = *updated.OriginalTitle
	}
	if updated.PosterURL != nil {
		response["poster_url"] = *updated.PosterURL
	}
	if updated.Status != nil {
		response["status"] = *updated.Status
	}

	s.respondJSON(w, http.StatusOK, response)
}

func (s *Server) handleGetAlerts(w http.ResponseWriter, _ *http.Request) {
	query := `
		SELECT id, type, message, created_at, dismissed
		FROM system_alerts
		WHERE dismissed = 0
		ORDER BY created_at DESC
		LIMIT 10
	`

	rows, err := s.db.Query(query)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch alerts")
		return
	}
	defer rows.Close() //nolint:errcheck

	alerts := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var alertType, message string
		var createdAt time.Time
		var dismissed bool

		if err := rows.Scan(&id, &alertType, &message, &createdAt, &dismissed); err != nil {
			slog.Error("Failed to scan alert row", "error", err)
			continue
		}

		alerts = append(alerts, map[string]interface{}{
			"id":         id,
			"type":       alertType,
			"message":    message,
			"created_at": createdAt,
			"dismissed":  dismissed,
		})
	}

	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading alerts")
		return
	}

	s.respondJSON(w, http.StatusOK, alerts)
}

func (s *Server) handleDismissAlert(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid alert ID")
		return
	}

	query := "UPDATE system_alerts SET dismissed = 1 WHERE id = ?"
	result, err := s.db.Exec(query, id)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to dismiss alert")
		return
	}

	rows, err := result.RowsAffected()
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to verify dismissal")
		return
	}
	if rows == 0 {
		s.respondError(w, http.StatusNotFound, "alert not found")
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// handleGetSeasonTracker returns the tracker URL for a season.
// First checks the database cache; if empty, queries qBittorrent and saves.
func (s *Server) handleGetSeasonTracker(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	num, err := strconv.Atoi(chi.URLParam(r, "num"))
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}

	season, err := s.db.GetSeasonBySeriesAndNumber(id, num)
	if err != nil {
		slog.Error("Failed to get season", "series_id", id, "season_number", num, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to get season")
		return
	}
	if season == nil {
		s.respondError(w, http.StatusNotFound, "season not found")
		return
	}

	// Return cached tracker_url if available
	if season.TrackerURL != nil && *season.TrackerURL != "" {
		s.respondJSON(w, http.StatusOK, map[string]interface{}{"tracker_url": *season.TrackerURL})
		return
	}

	// No cache — try to resolve from qBittorrent
	if s.qbitClient == nil || season.FolderPath == nil || *season.FolderPath == "" {
		s.respondJSON(w, http.StatusOK, map[string]interface{}{"tracker_url": nil})
		return
	}

	trackerURL, torrentHash := s.resolveTrackerFromQbit(*season.FolderPath)

	// Save to database if found
	if trackerURL != "" || torrentHash != "" {
		if _, err := s.db.Exec(`
			UPDATE seasons SET tracker_url = ?, torrent_hash = ?, updated_at = CURRENT_TIMESTAMP
			WHERE series_id = ? AND season_number = ?
		`, nilIfEmpty(trackerURL), nilIfEmpty(torrentHash), id, num); err != nil {
			slog.Error("Failed to save tracker info", "series_id", id, "season", num, "error", err)
		}
	}

	if trackerURL == "" {
		s.respondJSON(w, http.StatusOK, map[string]interface{}{"tracker_url": nil})
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]interface{}{"tracker_url": trackerURL})
}

// resolveTrackerFromQbit finds a torrent matching the folder and returns (trackerURL, hash).
func (s *Server) resolveTrackerFromQbit(folderPath string) (trackerURL, hash string) {
	torrents, err := s.qbitClient.ListTorrents()
	if err != nil {
		slog.Error("Failed to list torrents", "error", err)
		return "", ""
	}

	matched := qbittorrent.FindTorrentByFolder(torrents, folderPath)
	if matched == nil {
		return "", ""
	}

	props, err := s.qbitClient.GetTorrentProperties(matched.Hash)
	if err != nil {
		slog.Error("Failed to get torrent properties", "hash", matched.Hash, "error", err)
		return "", matched.Hash
	}

	// Validate comment is a valid HTTP(S) URL
	if props.Comment != "" {
		parsed, parseErr := url.Parse(props.Comment)
		if parseErr == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
			return props.Comment, matched.Hash
		}
	}

	return "", matched.Hash
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Helper methods for JSON responses
func (s *Server) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		slog.Error("Failed to encode JSON response", "error", err)
	}
}

func (s *Server) respondError(w http.ResponseWriter, status int, message string) {
	s.respondJSON(w, status, map[string]interface{}{
		"error": message,
	})
}

// isValidHash checks if the string is a valid hex hash (prevents path traversal)
func isValidHash(h string) bool {
	if h == "" || len(h) > 128 {
		return false
	}
	for _, c := range h {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// countDownloadedSeasons counts seasons where downloaded == true
func countDownloadedSeasons(seasons []map[string]interface{}) int {
	count := 0
	for _, s := range seasons {
		if dl, ok := s["downloaded"].(bool); ok && dl {
			count++
		}
	}
	return count
}

// Scan handler
func (s *Server) handleTriggerScan(w http.ResponseWriter, _ *http.Request) {
	if s.scanner == nil {
		s.respondError(w, http.StatusServiceUnavailable, "scanner not configured")
		return
	}

	slog.Info("Manual scan triggered")

	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic in scan", "error", r)
			}
		}()
		if err := s.scanner.Scan(); err != nil {
			slog.Error("Scan failed", "error", err)
		}
	}()

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Scan started",
	})
}

// Updates handlers
func (s *Server) handleGetUpdates(w http.ResponseWriter, _ *http.Request) {
	// Shows seasons where aired_episodes (from TVDB API) > max_episode_on_disk (highest episode ever on disk).
	// Also shows new seasons beyond max downloaded season.
	query := `
		SELECT s.id, s.tvdb_id, s.title, s.original_title, s.poster_url, s.status,
			s.aired_seasons,
			(SELECT MAX(sn.season_number) FROM seasons sn WHERE sn.series_id = s.id AND sn.downloaded = 1 AND sn.season_number > 0) as max_downloaded
		FROM series s
		WHERE s.status != 'Ended'
		AND (SELECT COUNT(*) FROM seasons sn WHERE sn.series_id = s.id AND sn.downloaded = 1 AND sn.season_number > 0) > 0
		AND EXISTS (
			SELECT 1 FROM seasons sn
			WHERE sn.series_id = s.id AND sn.season_number > 0
			AND (
				(sn.downloaded = 1 AND sn.aired_episodes > COALESCE(sn.max_episode_on_disk, 0))
				OR (sn.downloaded = 0 AND sn.aired_episodes > 0
					AND sn.season_number > (SELECT MAX(sn2.season_number) FROM seasons sn2 WHERE sn2.series_id = s.id AND sn2.downloaded = 1 AND sn2.season_number > 0))
			)
		)
		ORDER BY s.updated_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch updates")
		return
	}

	type updateRow struct {
		id            int
		tvdbID        *int
		title         string
		originalTitle *string
		posterURL     *string
		status        *string
		airedSeasons  int
		maxDownloaded *int
	}
	var collected []updateRow
	for rows.Next() {
		var r updateRow
		if err := rows.Scan(&r.id, &r.tvdbID, &r.title, &r.originalTitle, &r.posterURL, &r.status, &r.airedSeasons, &r.maxDownloaded); err != nil {
			slog.Error("Failed to scan updates row", "error", err)
			continue
		}
		collected = append(collected, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close() //nolint:errcheck
		s.respondError(w, http.StatusInternalServerError, "error reading updates")
		return
	}
	rows.Close() //nolint:errcheck

	updates := make([]map[string]interface{}, 0)
	for _, r := range collected {
		maxDownloadedNum := 0
		if r.maxDownloaded != nil {
			maxDownloadedNum = *r.maxDownloaded
		}

		newSeasons := s.findNewEpisodes(r.id, maxDownloadedNum)
		if len(newSeasons) == 0 {
			continue
		}

		update := map[string]interface{}{
			"id":             r.id,
			"title":          r.title,
			"aired_seasons":  r.airedSeasons,
			"max_downloaded": maxDownloadedNum,
			"new_seasons":    newSeasons,
		}

		if r.tvdbID != nil {
			update["tvdb_id"] = *r.tvdbID
		}
		if r.originalTitle != nil {
			update["original_title"] = *r.originalTitle
		}
		if r.posterURL != nil {
			update["poster_url"] = *r.posterURL
		}
		if r.status != nil {
			update["status"] = *r.status
		} else {
			update["status"] = "unknown"
		}

		updates = append(updates, update)
	}

	s.respondJSON(w, http.StatusOK, updates)
}

// seasonUpdate represents a season with new episodes for the Updates response.
type seasonUpdate struct {
	SeasonNumber  int `json:"season_number"`
	AiredEpisodes int `json:"aired_episodes"`
	NewEpisodes   int `json:"new_episodes"`
}

// findNewEpisodes returns seasons with new episodes:
// - downloaded season: aired_episodes > max_episode_on_disk
// - new season beyond max downloaded: aired_episodes > 0
func (s *Server) findNewEpisodes(seriesID, maxDownloaded int) []seasonUpdate {
	rows, err := s.db.Query(`
		SELECT sn.season_number, sn.aired_episodes, sn.downloaded, COALESCE(sn.max_episode_on_disk, 0)
		FROM seasons sn
		WHERE sn.series_id = ? AND sn.season_number > 0 AND sn.aired_episodes > 0
		AND (
			(sn.downloaded = 1 AND sn.aired_episodes > COALESCE(sn.max_episode_on_disk, 0))
			OR (sn.downloaded = 0 AND sn.season_number > ? AND sn.aired_episodes > 0)
		)
		ORDER BY sn.season_number
	`, seriesID, maxDownloaded)
	if err != nil {
		slog.Warn("Failed to query seasons for updates", "series_id", seriesID, "error", err)
		return nil
	}
	defer rows.Close() //nolint:errcheck

	var result []seasonUpdate
	for rows.Next() {
		var seasonNum, airedEps, maxOnDisk int
		var downloaded bool
		if err := rows.Scan(&seasonNum, &airedEps, &downloaded, &maxOnDisk); err != nil {
			continue
		}
		newEps := airedEps - maxOnDisk
		if !downloaded {
			newEps = airedEps
		}
		result = append(result, seasonUpdate{
			SeasonNumber:  seasonNum,
			AiredEpisodes: airedEps,
			NewEpisodes:   newEps,
		})
	}
	if result == nil {
		result = []seasonUpdate{}
	}
	return result
}

func (s *Server) handleCheckUpdates(w http.ResponseWriter, _ *http.Request) {
	slog.Info("Manual TVDB check triggered")

	if s.tvdbClient == nil {
		s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
		return
	}

	// Run check in background (includes auto-sync for stale series)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic in TVDB check", "error", r)
			}
		}()
		CheckForTVDBUpdates(s.db, s.tvdbClient, true)
	}()

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Check started",
	})
}

// Next seasons handler
func (s *Server) handleGetNextSeasons(w http.ResponseWriter, _ *http.Request) {
	// Clear expired cache entries (older than 7 days)
	if _, err := s.db.ClearExpiredCache(7 * 24 * time.Hour); err != nil {
		slog.Warn("Failed to clear expired next-season cache", "error", err)
	}

	// Get all non-ended series with their max downloaded season.
	// Include series with no downloaded seasons (they get S01).
	query := `
		SELECT s.id, s.tvdb_id, s.title, s.original_title, s.poster_url, s.status,
			s.aired_seasons,
			(SELECT MAX(sn.season_number) FROM seasons sn WHERE sn.series_id = s.id AND sn.downloaded = 1 AND sn.season_number > 0) as max_downloaded
		FROM series s
		WHERE s.status != 'Ended'
		ORDER BY s.title
	`

	rows, err := s.db.Query(query)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch series")
		return
	}
	defer rows.Close() //nolint:errcheck

	type seriesRow struct {
		id            int64
		tvdbID        *int
		title         string
		originalTitle *string
		posterURL     *string
		status        *string
		airedSeasons  int
		maxDownloaded *int
	}
	var series []seriesRow
	for rows.Next() {
		var r seriesRow
		if err := rows.Scan(&r.id, &r.tvdbID, &r.title, &r.originalTitle, &r.posterURL, &r.status, &r.airedSeasons, &r.maxDownloaded); err != nil {
			slog.Error("Failed to scan next-seasons row", "error", err)
			continue
		}
		series = append(series, r)
	}
	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading series")
		return
	}

	results := make([]map[string]interface{}, 0)
	for _, sr := range series {
		// Build list of candidate seasons to search for:
		// - If user has downloads: only (maxDownloaded+1) — the single next season
		// - If disk is empty: try airedSeasons first, fall back to older if not found
		var candidates []int
		if sr.maxDownloaded != nil {
			next := *sr.maxDownloaded + 1
			if next > sr.airedSeasons {
				continue // already has everything
			}
			candidates = []int{next}
		} else {
			// Nothing on disk — try newest first, fall back to older
			if sr.airedSeasons < 1 {
				continue
			}
			for n := sr.airedSeasons; n >= 1; n-- {
				candidates = append(candidates, n)
			}
		}

		entry := map[string]interface{}{
			"id":            sr.id,
			"title":         sr.title,
			"aired_seasons": sr.airedSeasons,
		}
		if sr.tvdbID != nil {
			entry["tvdb_id"] = *sr.tvdbID
		}
		if sr.originalTitle != nil {
			entry["original_title"] = *sr.originalTitle
		}
		if sr.posterURL != nil {
			entry["poster_url"] = *sr.posterURL
		}
		if sr.status != nil {
			entry["status"] = *sr.status
		}

		// Try each candidate season in order until we find a torrent.
		// For single candidate (user has downloads), we accept "not found" result.
		// For multi-candidate (empty disk), we fall through to previous season if not found.
		foundSeason := candidates[0]
		var foundResult *tracker.SeasonSearchResult
		for _, seasonNum := range candidates {
			cached, cErr := s.db.GetCachedNextSeason(sr.id, seasonNum)
			if cErr != nil {
				slog.Warn("Failed to check next-season cache", "series_id", sr.id, "error", cErr)
			}
			if cached != nil {
				isNegative := cached.TrackerURL == ""
				if !(isNegative && time.Since(cached.CachedAt) > 24*time.Hour) {
					// Valid cache hit
					if !isNegative {
						foundSeason = seasonNum
						foundResult = &tracker.SeasonSearchResult{
							DetailsURL: cached.TrackerURL,
							Title:      cached.Title,
							Size:       cached.Size,
						}
						break
					}
					// Negative cache: season known to have no torrent — try next candidate
					continue
				}
			}

			if s.seasonSearcher == nil {
				break
			}

			searchFailed := false
			result, err := s.seasonSearcher.FindSeasonTorrent(sr.title, seasonNum)
			if err != nil {
				slog.Warn("Failed to search for season torrent", "series", sr.title, "season", seasonNum, "error", err)
				searchFailed = true
			}
			if result == nil && !searchFailed && sr.originalTitle != nil && *sr.originalTitle != sr.title {
				result, err = s.seasonSearcher.FindSeasonTorrent(*sr.originalTitle, seasonNum)
				if err != nil {
					slog.Warn("Failed to search for season torrent (fallback)", "series", *sr.originalTitle, "season", seasonNum, "error", err)
					searchFailed = true
				}
			}

			if !searchFailed {
				cacheEntry := &database.NextSeasonCache{
					SeriesID:     sr.id,
					SeasonNumber: seasonNum,
				}
				if result != nil {
					cacheEntry.TrackerURL = result.DetailsURL
					cacheEntry.Title = result.Title
					cacheEntry.Size = result.Size
				}
				if cErr := s.db.SaveCachedNextSeason(cacheEntry); cErr != nil {
					slog.Warn("Failed to cache next-season result", "series_id", sr.id, "error", cErr)
				}
			}

			if result != nil {
				foundSeason = seasonNum
				foundResult = result
				break
			}
			// Not found — for multi-candidate (empty disk) keep trying older seasons
		}

		entry["next_season"] = foundSeason
		if foundResult != nil {
			entry["tracker_url"] = foundResult.DetailsURL
			entry["torrent_title"] = foundResult.Title
			entry["torrent_size"] = foundResult.Size
		}

		results = append(results, entry)
	}

	s.respondJSON(w, http.StatusOK, results)
}

// Seasons handlers
func (s *Server) handleListSeasons(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	// Get series info including poster
	var totalSeasons int
	var seriesPosterURL *string
	err = s.db.QueryRow(`SELECT total_seasons, poster_url FROM series WHERE id = ?`, sid).Scan(&totalSeasons, &seriesPosterURL)
	if err == sql.ErrNoRows {
		s.respondError(w, http.StatusNotFound, "series not found")
		return
	}
	if err != nil {
		slog.Error("Failed to fetch series for seasons", "id", sid, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch series")
		return
	}

	// Get owned seasons from seasons table with voice actor JOIN (includes cached poster_url)
	query := `
		SELECT sn.season_number, sn.folder_path, sn.downloaded, sn.track_name, sn.discovered_at, sn.poster_url
		FROM seasons sn
		WHERE sn.series_id = ?
		ORDER BY sn.season_number
	`

	rows, err := s.db.Query(query, sid)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch seasons")
		return
	}
	defer rows.Close() //nolint:errcheck

	// Build map of owned seasons
	downloadedSeasons := make(map[int]map[string]interface{})
	for rows.Next() {
		var seasonNum int
		var folderPath, trackName, seasonPosterURL *string
		var downloaded bool
		var discoveredAt *time.Time

		if err := rows.Scan(&seasonNum, &folderPath, &downloaded, &trackName, &discoveredAt, &seasonPosterURL); err != nil {
			slog.Error("Failed to scan season detail row", "error", err)
			continue
		}

		season := map[string]interface{}{
			"season_number": seasonNum,
			"downloaded":    downloaded,
			"on_disk":       folderPath != nil,
		}

		if folderPath != nil {
			season["folder_path"] = *folderPath
		}
		if trackName != nil {
			season["track_name"] = *trackName
		}
		if discoveredAt != nil {
			season["discovered_at"] = *discoveredAt
		}

		// Use cached season poster, fall back to series poster
		if seasonPosterURL != nil && *seasonPosterURL != "" {
			season["image"] = *seasonPosterURL
		} else if seriesPosterURL != nil {
			season["image"] = *seriesPosterURL
		}

		downloadedSeasons[seasonNum] = season
	}

	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading seasons")
		return
	}

	// Build response with all seasons (owned and missing)
	seasons := []map[string]interface{}{}
	maxSeasons := totalSeasons
	// Ensure maxSeasons includes all owned season numbers (they may exceed totalSeasons)
	for num := range downloadedSeasons {
		if num > maxSeasons {
			maxSeasons = num
		}
	}

	for i := 1; i <= maxSeasons; i++ {
		if season, exists := downloadedSeasons[i]; exists {
			// Owned season
			seasons = append(seasons, season)
		} else {
			// Missing season - locked
			lockedSeason := map[string]interface{}{
				"season_number": i,
				"downloaded":    false,
				"on_disk":       false,
			}

			if seriesPosterURL != nil {
				lockedSeason["image"] = *seriesPosterURL
			}

			seasons = append(seasons, lockedSeason)
		}
	}

	s.respondJSON(w, http.StatusOK, seasons)
}

// Search handler
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	if query == "" {
		s.respondJSON(w, http.StatusOK, []interface{}{})
		return
	}

	if s.tvdbClient == nil {
		s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
		return
	}

	results, err := s.tvdbClient.SearchSeries(query)
	if err != nil {
		slog.Error("TVDB search failed", "query", query, "error", err)
		s.respondError(w, http.StatusInternalServerError, "search failed")
		return
	}

	// Format results for API response
	response := make([]map[string]interface{}, 0, len(results))
	for _, result := range results {
		response = append(response, map[string]interface{}{
			"id":     result.TVDBId,
			"name":   result.Name,
			"poster": result.Image,
			"year":   result.Year,
			"status": result.Status,
		})
	}

	s.respondJSON(w, http.StatusOK, response)
}

// Audio handlers
func (s *Server) handleGetAudioTracks(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}
	snum, err := strconv.Atoi(chi.URLParam(r, "num"))
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}

	// Get folder path from seasons table
	var folderPath *string
	err = s.db.QueryRow(`
		SELECT folder_path FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, sid, snum).Scan(&folderPath)

	if err == sql.ErrNoRows {
		s.respondError(w, http.StatusNotFound, "season not found")
		return
	}
	if err != nil {
		slog.Error("Failed to fetch season for audio tracks", "series_id", sid, "season", snum, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch season")
		return
	}
	if folderPath == nil {
		s.respondError(w, http.StatusNotFound, "season has no folder path")
		return
	}

	// Scan folder for MKV files
	results, err := s.audioCutter.ScanFolderAudioTracks(*folderPath)
	if err != nil {
		slog.Error("Failed to scan audio tracks", "folder", *folderPath, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to scan audio tracks")
		return
	}

	// Collect and sort file paths for deterministic ordering
	sortedPaths := make([]string, 0, len(results))
	for fp := range results {
		sortedPaths = append(sortedPaths, fp)
	}
	sort.Strings(sortedPaths)

	// Get files and their processed status
	files := []map[string]interface{}{}
	for _, filePath := range sortedPaths {
		processed, err := s.db.IsFileProcessed(filePath)
		if err != nil {
			processed = false
		}

		files = append(files, map[string]interface{}{
			"path":      filePath,
			"name":      filepath.Base(filePath),
			"processed": processed,
		})
	}

	// Aggregate audio tracks across all files by name.
	// Track IDs differ between files, so name is the stable identifier.
	type trackInfo struct {
		Name      string
		Language  string
		Codec     string
		Channels  int
		IsDefault bool
		FileCount int
	}
	tracksByKey := map[string]*trackInfo{} // keyed by lowercase name
	var trackOrder []string                // preserve first-seen order (lowercase keys)

	totalFiles := len(sortedPaths)
	for _, fp := range sortedPaths {
		seen := map[string]bool{}
		for _, track := range results[fp] {
			key := strings.ToLower(track.Name)
			if seen[key] {
				continue
			}
			seen[key] = true
			ti, exists := tracksByKey[key]
			if !exists {
				ti = &trackInfo{
					Name:     track.Name, // keep original casing from first file
					Language: track.Language,
					Codec:    track.Codec,
					Channels: track.Channels,
				}
				tracksByKey[key] = ti
				trackOrder = append(trackOrder, key)
			}
			ti.FileCount++
			if track.Default {
				ti.IsDefault = true
			}
		}
	}

	// Get track name history for this series to flag matches
	trackHistory, _ := s.db.GetTrackNamesForSeries(sid)
	historySet := make(map[string]bool, len(trackHistory))
	for _, h := range trackHistory {
		historySet[strings.ToLower(h)] = true
	}

	var audioTracks []map[string]interface{}
	for _, key := range trackOrder {
		ti := tracksByKey[key]
		audioTracks = append(audioTracks, map[string]interface{}{
			"name":            ti.Name,
			"codec":           ti.Codec,
			"language":        ti.Language,
			"default":         ti.IsDefault,
			"channels":        ti.Channels,
			"file_count":      ti.FileCount,
			"total_files":     totalFiles,
			"matched_history": historySet[key],
		})
	}

	isProcessing := s.procLock.IsLocked(sid, int64(snum))

	response := map[string]interface{}{
		"files":               files,
		"audio_tracks":        audioTracks,
		"folder_path":         *folderPath,
		"track_names_history": trackHistory,
		"processing":          isProcessing,
	}

	s.respondJSON(w, http.StatusOK, response)
}

// handleGetSeason returns details for a specific season
func (s *Server) handleGetSeason(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}
	snum, err := strconv.Atoi(chi.URLParam(r, "num"))
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}

	var folderPath *string
	var downloaded bool
	var trackName *string
	var discoveredAt *time.Time
	err = s.db.QueryRow(`
		SELECT sn.folder_path, sn.downloaded, sn.track_name, sn.discovered_at
		FROM seasons sn
		WHERE sn.series_id = ? AND sn.season_number = ?
	`, sid, snum).Scan(&folderPath, &downloaded, &trackName, &discoveredAt)

	if err == sql.ErrNoRows {
		s.respondError(w, http.StatusNotFound, "season not found")
		return
	}
	if err != nil {
		slog.Error("Failed to fetch season", "series_id", sid, "season", snum, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch season")
		return
	}

	response := map[string]interface{}{
		"season_number": snum,
		"folder_path":   folderPath,
		"downloaded":    downloaded,
		"on_disk":       folderPath != nil,
	}

	if trackName != nil {
		response["track_name"] = *trackName
	}
	if discoveredAt != nil {
		response["discovered_at"] = *discoveredAt
	}

	// Include track name history for this series
	trackHistory, _ := s.db.GetTrackNamesForSeries(sid)
	if len(trackHistory) > 0 {
		response["track_names_history"] = trackHistory
	}

	s.respondJSON(w, http.StatusOK, response)
}

// handleUpdateSeason updates a season's track_name
func (s *Server) handleUpdateSeason(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}
	snum, err := strconv.Atoi(chi.URLParam(r, "num"))
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}

	var req struct {
		TrackName *string `json:"track_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	_, err = s.db.Exec(`
		UPDATE seasons SET track_name = ?, updated_at = CURRENT_TIMESTAMP
		WHERE series_id = ? AND season_number = ?
	`, req.TrackName, sid, snum)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to update season")
		return
	}

	slog.Info("Updated season track", "series_id", sid, "season", snum, "track_name", req.TrackName)

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// handleGenerateAudioPreview generates a 30-second preview of an audio track
func (s *Server) handleGenerateAudioPreview(w http.ResponseWriter, r *http.Request) {
	sid, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}
	snum, err := strconv.Atoi(chi.URLParam(r, "num"))
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}

	var req struct {
		FilePath   string `json:"file_path"`
		TrackIndex int    `json:"track_index"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Validate file path is within the season's folder to prevent path traversal
	var folderPath *string
	err = s.db.QueryRow(`
		SELECT folder_path FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, sid, snum).Scan(&folderPath)
	if err != nil || folderPath == nil {
		s.respondError(w, http.StatusNotFound, "season not found or no folder path")
		return
	}

	absFolder, err := filepath.EvalSymlinks(*folderPath)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to resolve folder path")
		return
	}
	absFile, err := filepath.EvalSymlinks(req.FilePath)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid file path")
		return
	}
	if !strings.HasPrefix(absFile, absFolder+string(filepath.Separator)) {
		s.respondError(w, http.StatusBadRequest, "file path is outside season folder")
		return
	}

	// Generate preview using audioCutter
	previewHash, err := s.audioCutter.GeneratePreview(req.FilePath, req.TrackIndex, 30)
	if err != nil {
		slog.Error("Failed to generate preview", "file", req.FilePath, "track", req.TrackIndex, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to generate preview")
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"preview_url": fmt.Sprintf("/api/audio/preview/%s", previewHash),
		"hash":        previewHash,
	})
}

// handleServeAudioPreview serves the generated audio preview file
func (s *Server) handleServeAudioPreview(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")

	// Validate hash format to prevent path traversal
	if !isValidHash(hash) {
		s.respondError(w, http.StatusBadRequest, "invalid hash format")
		return
	}

	filePath, err := s.audioCutter.GetPreviewPath(hash)
	if err != nil {
		s.respondError(w, http.StatusNotFound, "preview not found")
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Disposition", "inline; filename=preview.mp3")
	http.ServeFile(w, r, filePath)
}

// handleProcessAudioStream processes audio with SSE progress updates.
// This endpoint is registered outside the /api group to bypass timeout middleware,
// so we apply body size limit directly here.
func (s *Server) handleProcessAudioStream(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit

	seriesID := chi.URLParam(r, "id")
	seasonNum := chi.URLParam(r, "num")

	slog.Info("Audio processing started", "series_id", seriesID, "season", seasonNum)

	var req struct {
		TrackName    string `json:"track_name"`
		KeepOriginal bool   `json:"keep_original"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TrackName == "" {
		s.respondError(w, http.StatusBadRequest, "track_name is required")
		return
	}

	sid, err := strconv.ParseInt(seriesID, 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}
	snum, err := strconv.ParseInt(seasonNum, 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}

	// Get folder path from seasons table
	var folderPath *string
	err = s.db.QueryRow(`
		SELECT folder_path FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, sid, snum).Scan(&folderPath)

	if err != nil || folderPath == nil {
		s.respondError(w, http.StatusNotFound, "season not found or no folder path")
		return
	}

	// Prevent concurrent processing of the same season
	if !s.procLock.TryLock(sid, snum) {
		s.respondError(w, http.StatusConflict, "this season is already being processed")
		return
	}
	defer s.procLock.Unlock(sid, snum)

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Scan folder for MKV files before setting SSE headers so errors return proper JSON
	results, err := s.audioCutter.ScanFolderAudioTracks(*folderPath)
	if err != nil {
		slog.Error("Failed to scan folder", "folder", *folderPath, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to scan folder")
		return
	}

	// Remove torrent from qBittorrent before processing to avoid file locks.
	// Try cached torrent_hash first, fall back to folder match.
	if s.qbitClient != nil {
		deleted := false
		var torrentHash *string
		s.db.QueryRow(`SELECT torrent_hash FROM seasons WHERE series_id = ? AND season_number = ?`,
			sid, snum).Scan(&torrentHash) //nolint:errcheck

		if torrentHash != nil && *torrentHash != "" {
			if err := s.qbitClient.DeleteTorrent(*torrentHash); err != nil {
				slog.Warn("Failed to delete torrent by cached hash, will try folder match", "hash", *torrentHash, "error", err)
			} else {
				slog.Info("Deleted torrent before audio processing", "hash", *torrentHash)
				deleted = true
			}
		}

		// Fallback: find by folder match if hash was missing or stale
		if !deleted && folderPath != nil {
			if torrents, err := s.qbitClient.ListTorrents(); err == nil {
				if matched := qbittorrent.FindTorrentByFolder(torrents, *folderPath); matched != nil {
					if err := s.qbitClient.DeleteTorrent(matched.Hash); err != nil {
						slog.Warn("Failed to delete torrent by folder match", "hash", matched.Hash, "error", err)
					} else {
						slog.Info("Deleted torrent before audio processing", "hash", matched.Hash)
						_, _ = s.db.Exec(`UPDATE seasons SET torrent_hash = ? WHERE series_id = ? AND season_number = ?`,
							matched.Hash, sid, snum)
					}
				}
			}
		}
	}

	// Set SSE headers after all validation — once set, respondError cannot override Content-Type
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Disable write timeout for SSE — audio processing can take minutes
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("Failed to disable write deadline for SSE", "error", err)
	}

	files := make([]string, 0, len(results))
	for filePath := range results {
		files = append(files, filePath)
	}
	sort.Strings(files)

	// Send start event
	startEvent := map[string]interface{}{
		"type":  "start",
		"total": len(files),
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(startEvent)) //nolint:errcheck
	flusher.Flush()

	// Save track selection to season (single source of truth)
	s.db.Exec(`UPDATE seasons SET track_name = ?, updated_at = CURRENT_TIMESTAMP WHERE series_id = ? AND season_number = ?`,
		req.TrackName, sid, snum) //nolint:errcheck

	// Process files
	successCount := 0
	errorCount := 0
	skippedCount := 0

	for idx, filePath := range files {
		// Stop if client disconnected
		select {
		case <-r.Context().Done():
			slog.Info("Client disconnected, stopping audio processing")
			return
		default:
		}

		alreadyProcessed, _ := s.db.IsFileProcessed(filePath)

		if alreadyProcessed {
			skippedCount++
			event := map[string]interface{}{
				"type":    "file_done",
				"file":    filepath.Base(filePath),
				"status":  "skipped",
				"message": "Already processed",
				"current": idx + 1,
				"total":   len(files),
			}
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(event)) //nolint:errcheck
			flusher.Flush()
			continue
		}

		// Send progress event
		progressEvent := map[string]interface{}{
			"type":    "progress",
			"file":    filepath.Base(filePath),
			"current": idx + 1,
			"total":   len(files),
		}
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(progressEvent)) //nolint:errcheck
		flusher.Flush()

		err := s.audioCutter.RemoveAudioTracks(filePath, req.TrackName, req.KeepOriginal)
		if err != nil {
			errorCount++
			slog.Error("Failed to process audio file", "file", filePath, "error", err)
			event := map[string]interface{}{
				"type":    "file_done",
				"file":    filepath.Base(filePath),
				"status":  "error",
				"message": "processing failed",
				"current": idx + 1,
				"total":   len(files),
			}
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(event)) //nolint:errcheck
			flusher.Flush()
			continue
		}

		if err := s.db.InsertProcessedFile(filePath, sid, int(snum), req.TrackName); err != nil {
			slog.Error("Failed to log processed file", "file", filePath, "error", err)
		}

		successCount++
		event := map[string]interface{}{
			"type":    "file_done",
			"file":    filepath.Base(filePath),
			"status":  "success",
			"current": idx + 1,
			"total":   len(files),
		}
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(event)) //nolint:errcheck
		flusher.Flush()
	}

	// Send complete event
	completeEvent := map[string]interface{}{
		"type":    "complete",
		"success": successCount,
		"errors":  errorCount,
		"skipped": skippedCount,
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(completeEvent)) //nolint:errcheck
	flusher.Flush()
}

// handleSetDefaultTrackStream sets the default audio track on all MKV files with SSE progress.
// This endpoint is registered outside the /api group to bypass timeout middleware.
func (s *Server) handleSetDefaultTrackStream(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit

	seriesID := chi.URLParam(r, "id")
	seasonNum := chi.URLParam(r, "num")

	slog.Info("Set default track started", "series_id", seriesID, "season", seasonNum)

	var req struct {
		TrackName string `json:"track_name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TrackName == "" {
		s.respondError(w, http.StatusBadRequest, "track_name is required")
		return
	}

	sid, err := strconv.ParseInt(seriesID, 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}
	snum, err := strconv.ParseInt(seasonNum, 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}
	_ = snum // used only for route validation

	var folderPath *string
	err = s.db.QueryRow(`
		SELECT folder_path FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, sid, snum).Scan(&folderPath)

	if err != nil || folderPath == nil {
		s.respondError(w, http.StatusNotFound, "season not found or no folder path")
		return
	}

	if !s.procLock.TryLock(sid, snum) {
		s.respondError(w, http.StatusConflict, "this season is already being processed")
		return
	}
	defer s.procLock.Unlock(sid, snum)

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	results, err := s.audioCutter.ScanFolderAudioTracks(*folderPath)
	if err != nil {
		slog.Error("Failed to scan folder", "folder", *folderPath, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to scan folder")
		return
	}

	// Set SSE headers after all validation
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("Failed to disable write deadline for SSE", "error", err)
	}

	files := make([]string, 0, len(results))
	for filePath := range results {
		files = append(files, filePath)
	}
	sort.Strings(files)

	// Send start event
	startEvent := map[string]interface{}{
		"type":  "start",
		"total": len(files),
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(startEvent)) //nolint:errcheck
	flusher.Flush()

	successCount := 0
	errorCount := 0

	for idx, filePath := range files {
		select {
		case <-r.Context().Done():
			slog.Info("Client disconnected, stopping set-default processing")
			return
		default:
		}

		// Send progress event
		progressEvent := map[string]interface{}{
			"type":    "progress",
			"file":    filepath.Base(filePath),
			"current": idx + 1,
			"total":   len(files),
		}
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(progressEvent)) //nolint:errcheck
		flusher.Flush()

		err := s.audioCutter.SetDefaultAudioTrack(filePath, req.TrackName)
		if err != nil {
			errorCount++
			slog.Error("Failed to set default track", "file", filePath, "error", err)
			event := map[string]interface{}{
				"type":    "file_done",
				"file":    filepath.Base(filePath),
				"status":  "error",
				"message": "failed to set default",
				"current": idx + 1,
				"total":   len(files),
			}
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(event)) //nolint:errcheck
			flusher.Flush()
			continue
		}

		successCount++
		event := map[string]interface{}{
			"type":    "file_done",
			"file":    filepath.Base(filePath),
			"status":  "success",
			"current": idx + 1,
			"total":   len(files),
		}
		fmt.Fprintf(w, "data: %s\n\n", mustJSON(event)) //nolint:errcheck
		flusher.Flush()
	}

	completeEvent := map[string]interface{}{
		"type":    "complete",
		"success": successCount,
		"errors":  errorCount,
		"skipped": 0,
	}
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(completeEvent)) //nolint:errcheck
	flusher.Flush()
}

// Helper function to marshal JSON without error handling (panics on error)
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Recommendations handlers

// handleGetRecommendations returns current recommendations. Returns an empty
// array (not 503) when the feature is disabled so the UI degrades gracefully.
func (s *Server) handleGetRecommendations(w http.ResponseWriter, _ *http.Request) {
	if s.recommender == nil {
		s.respondJSON(w, http.StatusOK, []map[string]interface{}{})
		return
	}
	recs, err := s.db.GetRecommendations()
	if err != nil {
		slog.Error("Failed to fetch recommendations", "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch recommendations")
		return
	}
	out := make([]map[string]interface{}, 0, len(recs))
	for _, r := range recs {
		entry := map[string]interface{}{
			"tvdb_id":       r.TVDBID,
			"tmdb_id":       r.TMDBID,
			"title":         r.Title,
			"score":         r.Score,
			"rating":        r.Rating,
			"tracker_url":   r.TrackerURL,
			"torrent_title": r.TorrentTitle,
			"torrent_size":  r.TorrentSize,
			"created_at":    r.CreatedAt,
		}
		if r.OriginalTitle != "" {
			entry["original_title"] = r.OriginalTitle
		}
		if r.Overview != "" {
			entry["overview"] = r.Overview
		}
		if r.PosterURL != "" {
			entry["poster_url"] = r.PosterURL
		}
		if r.Year != 0 {
			entry["year"] = r.Year
		}
		if r.Genres != "" {
			var ids []int
			if jsonErr := json.Unmarshal([]byte(r.Genres), &ids); jsonErr == nil {
				entry["genres"] = ids
			}
		}
		out = append(out, entry)
	}
	s.respondJSON(w, http.StatusOK, out)
}

// handleRefreshRecommendations triggers a background refresh of recommendations.
// Returns 503 if the feature is disabled, 202 Accepted on success.
func (s *Server) handleRefreshRecommendations(w http.ResponseWriter, _ *http.Request) {
	if s.recommender == nil {
		s.respondError(w, http.StatusServiceUnavailable, "recommendations feature not configured")
		return
	}
	go func() {
		if err := s.recommender.Refresh(); err != nil {
			slog.Error("Recommendation refresh failed", "error", err)
		}
	}()
	s.respondJSON(w, http.StatusAccepted, map[string]interface{}{"status": "accepted"})
}

// handleGetBlacklist returns all blacklisted shows.
func (s *Server) handleGetBlacklist(w http.ResponseWriter, _ *http.Request) {
	entries, err := s.db.GetBlacklist()
	if err != nil {
		slog.Error("Failed to fetch blacklist", "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to fetch blacklist")
		return
	}
	out := make([]map[string]interface{}, 0, len(entries))
	for _, e := range entries {
		out = append(out, map[string]interface{}{
			"tvdb_id":        e.TVDBID,
			"title":          e.Title,
			"blacklisted_at": e.BlacklistedAt,
		})
	}
	s.respondJSON(w, http.StatusOK, out)
}

// handleAddBlacklist adds a show to the blacklist.
func (s *Server) handleAddBlacklist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TVDBID int    `json:"tvdb_id"`
		Title  string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.TVDBID <= 0 {
		s.respondError(w, http.StatusBadRequest, "tvdb_id required")
		return
	}
	if err := s.db.AddToBlacklist(body.TVDBID, body.Title); err != nil {
		slog.Error("Failed to add to blacklist", "tvdb_id", body.TVDBID, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to add to blacklist")
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}

// handleRemoveBlacklist removes a show from the blacklist.
func (s *Server) handleRemoveBlacklist(w http.ResponseWriter, r *http.Request) {
	tvdbID, err := strconv.Atoi(chi.URLParam(r, "tvdb_id"))
	if err != nil || tvdbID <= 0 {
		s.respondError(w, http.StatusBadRequest, "invalid tvdb_id")
		return
	}
	if err := s.db.RemoveFromBlacklist(tvdbID); err != nil {
		slog.Error("Failed to remove from blacklist", "tvdb_id", tvdbID, "error", err)
		s.respondError(w, http.StatusInternalServerError, "failed to remove from blacklist")
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]interface{}{"status": "ok"})
}
