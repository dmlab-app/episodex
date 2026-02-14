// Package api provides HTTP handlers for the EpisodeX REST API.
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"

	"github.com/episodex/episodex/internal/audio"
	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/scanner"
	"github.com/episodex/episodex/internal/tvdb"
)

// Server represents the API server
type Server struct {
	db          *database.DB
	scanner     *scanner.Scanner
	tvdbClient  *tvdb.Client
	audioCutter *audio.AudioCutter
	router      *chi.Mux
}

// NewServer creates a new API server
func NewServer(db *database.DB, sc *scanner.Scanner, tvdbClient *tvdb.Client) *Server {
	s := &Server{
		db:          db,
		scanner:     sc,
		tvdbClient:  tvdbClient,
		audioCutter: audio.New(),
		router:      chi.NewRouter(),
	}

	s.setupMiddleware()
	s.setupRoutes()

	return s
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

	// SSE endpoint without timeout — audio processing can take minutes
	s.router.Post("/api/series/{id}/seasons/{num}/audio/process", s.handleProcessAudioStream)

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
			r.Get("/", s.handleListSeries)                   // GET /api/series
			r.Post("/", s.handleCreateSeries)                // POST /api/series
			r.Get("/{id}", s.handleGetSeries)                // GET /api/series/:id
			r.Delete("/{id}", s.handleDeleteSeries)          // DELETE /api/series/:id
			r.Post("/{id}/match", s.handleMatchSeries)       // POST /api/series/:id/match
			r.Post("/{id}/sync", s.handleSyncSeriesFromTVDB) // POST /api/series/:id/sync

			// Seasons endpoints
			r.Route("/{id}/seasons", func(r chi.Router) {
				r.Get("/", s.handleListSeasons)                              // GET /api/series/:id/seasons
				r.Get("/{num}", s.handleGetSeason)                           // GET /api/series/:id/seasons/:num
				r.Put("/{num}", s.handleUpdateSeason)                        // PUT /api/series/:id/seasons/:num
				r.Post("/{num}/rescan", s.handleRescanSeason)                // POST /api/series/:id/seasons/:num/rescan
				r.Get("/{num}/audio", s.handleGetAudioTracks)                // GET /api/series/:id/seasons/:num/audio
				r.Post("/{num}/audio/preview", s.handleGenerateAudioPreview) // POST /api/series/:id/seasons/:num/audio/preview
			})
		})

		// Voice actors endpoint
		r.Get("/voices", s.handleListVoices) // GET /api/voices

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
			(SELECT COUNT(*) FROM seasons sn WHERE sn.series_id = s.id AND sn.is_owned = 1) as watched_seasons
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
		var totalSeasons, watchedSeasons int
		var createdAt time.Time

		if err := rows.Scan(&id, &tvdbID, &title, &originalTitle, &posterURL, &status, &totalSeasons, &createdAt, &watchedSeasons); err != nil {
			slog.Error("Failed to scan series row", "error", err)
			continue
		}

		item := map[string]interface{}{
			"id":              id,
			"title":           title,
			"total_seasons":   totalSeasons,
			"watched_seasons": watchedSeasons,
			"created_at":      createdAt,
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

		// Insert series with TVDB metadata
		result, err := s.db.Exec(`
			INSERT INTO series (tvdb_id, title, original_title, poster_url, status, total_seasons, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
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
		CreatedAt        time.Time
		LastAired        *string
		Runtime          *int
		OriginalTitle    *string
		Slug             *string
		Overview         *string
		PosterURL        *string
		BackdropURL      *string
		Status           *string
		FirstAired       *string
		TVDBId           *int
		Studios          *string
		Rating           *float64
		Year             *int
		ContentRating    *string
		OriginalCountry  *string
		OriginalLanguage *string
		Genres           *string
		Networks         *string
		Title            string
		TotalSeasons     int
		ID               int
	}

	query := `
		SELECT id, tvdb_id, title, original_title, slug, overview,
			poster_url, backdrop_url, status, first_aired, last_aired,
			year, runtime, rating, content_rating,
			original_country, original_language,
			genres, networks, studios, total_seasons, created_at
		FROM series
		WHERE id = ?
	`

	err = s.db.QueryRow(query, id).Scan(
		&seriesInfo.ID,
		&seriesInfo.TVDBId,
		&seriesInfo.Title,
		&seriesInfo.OriginalTitle,
		&seriesInfo.Slug,
		&seriesInfo.Overview,
		&seriesInfo.PosterURL,
		&seriesInfo.BackdropURL,
		&seriesInfo.Status,
		&seriesInfo.FirstAired,
		&seriesInfo.LastAired,
		&seriesInfo.Year,
		&seriesInfo.Runtime,
		&seriesInfo.Rating,
		&seriesInfo.ContentRating,
		&seriesInfo.OriginalCountry,
		&seriesInfo.OriginalLanguage,
		&seriesInfo.Genres,
		&seriesInfo.Networks,
		&seriesInfo.Studios,
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

	// Get seasons from seasons table with voice actor JOIN
	seasonsQuery := `
		SELECT sn.season_number, sn.folder_path, sn.is_owned, sn.voice_actor_id, va.name, sn.discovered_at
		FROM seasons sn
		LEFT JOIN voice_actors va ON sn.voice_actor_id = va.id
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
		var folderPath, voiceActorName *string
		var isOwned bool
		var voiceActorID *int
		var discoveredAt *time.Time

		if err := rows.Scan(&seasonNum, &folderPath, &isOwned, &voiceActorID, &voiceActorName, &discoveredAt); err != nil {
			slog.Error("Failed to scan season row", "error", err)
			continue
		}

		season := map[string]interface{}{
			"season_number": seasonNum,
			"owned":         isOwned,
		}

		if folderPath != nil {
			season["folder_path"] = *folderPath
		}
		if voiceActorID != nil {
			season["voice_actor_id"] = *voiceActorID
		}
		if voiceActorName != nil {
			season["voice_actor_name"] = *voiceActorName
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

	// Artwork fallback: if poster_url or backdrop_url is missing, try artworks table
	posterURL := seriesInfo.PosterURL
	backdropURL := seriesInfo.BackdropURL
	if posterURL == nil || *posterURL == "" {
		var url *string
		//nolint:errcheck // artwork fallback is best-effort
		s.db.QueryRow(`
			SELECT url FROM artworks
			WHERE series_id = ? AND type = 'poster'
			ORDER BY score DESC, is_primary DESC
			LIMIT 1
		`, id).Scan(&url)
		if url != nil {
			posterURL = url
		}
	}
	if backdropURL == nil || *backdropURL == "" {
		var url *string
		//nolint:errcheck // artwork fallback is best-effort
		s.db.QueryRow(`
			SELECT url FROM artworks
			WHERE series_id = ? AND type = 'background'
			ORDER BY score DESC, is_primary DESC
			LIMIT 1
		`, id).Scan(&url)
		if url != nil {
			backdropURL = url
		}
	}

	// Build response
	response := map[string]interface{}{
		"id":              seriesInfo.ID,
		"title":           seriesInfo.Title,
		"total_seasons":   seriesInfo.TotalSeasons,
		"watched_seasons": countOwnedSeasons(seasons),
		"seasons":         seasons,
		"characters":      characters,
		"created_at":      seriesInfo.CreatedAt,
	}

	if seriesInfo.TVDBId != nil {
		response["tvdb_id"] = *seriesInfo.TVDBId
	}
	if seriesInfo.OriginalTitle != nil {
		response["original_title"] = *seriesInfo.OriginalTitle
	}
	if seriesInfo.Slug != nil {
		response["slug"] = *seriesInfo.Slug
	}
	if seriesInfo.Overview != nil {
		response["overview"] = *seriesInfo.Overview
	}
	if posterURL != nil {
		response["poster_url"] = *posterURL
	}
	if backdropURL != nil {
		response["backdrop_url"] = *backdropURL
	}
	if seriesInfo.Status != nil {
		response["status"] = *seriesInfo.Status
	} else {
		response["status"] = "unknown"
	}
	if seriesInfo.FirstAired != nil {
		response["first_aired"] = *seriesInfo.FirstAired
	}
	if seriesInfo.LastAired != nil {
		response["last_aired"] = *seriesInfo.LastAired
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
	if seriesInfo.OriginalCountry != nil {
		response["original_country"] = *seriesInfo.OriginalCountry
	}
	if seriesInfo.OriginalLanguage != nil {
		response["original_language"] = *seriesInfo.OriginalLanguage
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
	if seriesInfo.Studios != nil {
		var studios []interface{}
		if err := json.Unmarshal([]byte(*seriesInfo.Studios), &studios); err == nil {
			response["studios"] = studios
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

	slog.Info("Deleted series", "id", id)
	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
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
	var existingSeriesID int
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
				is_owned = MAX(is_owned, (SELECT src.is_owned FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				voice_actor_id = COALESCE(voice_actor_id, (SELECT src.voice_actor_id FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				discovered_at = COALESCE(discovered_at, (SELECT src.discovered_at FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				tvdb_season_id = COALESCE(tvdb_season_id, (SELECT src.tvdb_season_id FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				name = COALESCE(name, (SELECT src.name FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				poster_url = COALESCE(poster_url, (SELECT src.poster_url FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number)),
				episode_count = COALESCE(episode_count, (SELECT src.episode_count FROM seasons src WHERE src.series_id = ? AND src.season_number = seasons.season_number))
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

	// Return updated series
	response := map[string]interface{}{
		"id":             id,
		"tvdb_id":        req.TVDBId,
		"title":          details.Name,
		"original_title": details.OriginalName,
		"poster_url":     details.Image,
		"status":         details.Status,
		"total_seasons":  len(details.Seasons),
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

// countOwnedSeasons counts seasons where owned == true
func countOwnedSeasons(seasons []map[string]interface{}) int {
	count := 0
	for _, s := range seasons {
		if owned, ok := s["owned"].(bool); ok && owned {
			count++
		}
	}
	return count
}

// Scan handler
func (s *Server) handleTriggerScan(w http.ResponseWriter, _ *http.Request) {
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
	// Query series that have unwatched seasons (total_seasons > owned count)
	query := `
		SELECT s.id, s.tvdb_id, s.title, s.original_title, s.poster_url, s.status,
			s.total_seasons,
			(SELECT COUNT(*) FROM seasons sn WHERE sn.series_id = s.id AND sn.is_owned = 1) as watched_seasons
		FROM series s
		WHERE s.total_seasons > (SELECT COUNT(*) FROM seasons sn WHERE sn.series_id = s.id AND sn.is_owned = 1)
		ORDER BY s.updated_at DESC
	`

	rows, err := s.db.Query(query)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch updates")
		return
	}
	defer rows.Close() //nolint:errcheck

	updates := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var tvdbID *int
		var title string
		var originalTitle, posterURL, status *string
		var totalSeasons, watchedSeasons int

		if err := rows.Scan(&id, &tvdbID, &title, &originalTitle, &posterURL, &status, &totalSeasons, &watchedSeasons); err != nil {
			slog.Error("Failed to scan updates row", "error", err)
			continue
		}

		update := map[string]interface{}{
			"id":              id,
			"title":           title,
			"total_seasons":   totalSeasons,
			"watched_seasons": watchedSeasons,
			"new_seasons":     totalSeasons - watchedSeasons,
		}

		if tvdbID != nil {
			update["tvdb_id"] = *tvdbID
		}
		if originalTitle != nil {
			update["original_title"] = *originalTitle
		}
		if posterURL != nil {
			update["poster_url"] = *posterURL
		}
		if status != nil {
			update["status"] = *status
		} else {
			update["status"] = "unknown"
		}

		updates = append(updates, update)
	}

	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading updates")
		return
	}

	s.respondJSON(w, http.StatusOK, updates)
}

func (s *Server) handleCheckUpdates(w http.ResponseWriter, _ *http.Request) {
	slog.Info("Manual TVDB check triggered")

	if s.tvdbClient == nil {
		s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
		return
	}

	// Run check in background
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic in TVDB check", "error", r)
			}
		}()
		// Collect all series with TVDB IDs first, then close rows
		type seriesCheck struct {
			ID           int
			TVDBId       int
			Title        string
			TotalSeasons int
		}

		rows, err := s.db.Query(`
			SELECT id, tvdb_id, title, total_seasons
			FROM series
			WHERE tvdb_id IS NOT NULL
		`)
		if err != nil {
			slog.Error("Failed to fetch series for TVDB check", "error", err)
			return
		}

		var toCheck []seriesCheck
		for rows.Next() {
			var sc seriesCheck
			if err := rows.Scan(&sc.ID, &sc.TVDBId, &sc.Title, &sc.TotalSeasons); err != nil {
				slog.Error("Failed to scan series check row", "error", err)
				continue
			}
			toCheck = append(toCheck, sc)
		}
		rows.Close() //nolint:errcheck

		if err := rows.Err(); err != nil {
			slog.Error("Error iterating series for TVDB check", "error", err)
			return
		}

		var checked, updated, errored int

		for _, sc := range toCheck {
			checked++

			// Get current season count from TVDB
			newTotalSeasons, err := s.tvdbClient.GetTotalSeasons(sc.TVDBId)
			if err != nil {
				slog.Error("Failed to get TVDB seasons", "series_id", sc.ID, "tvdb_id", sc.TVDBId, "error", err)
				errored++
				continue
			}

			// Compare with database
			if newTotalSeasons > sc.TotalSeasons {
				// Update database
				_, err = s.db.Exec(`
					UPDATE series
					SET total_seasons = ?, updated_at = CURRENT_TIMESTAMP
					WHERE id = ?
				`, newTotalSeasons, sc.ID)

				if err != nil {
					slog.Error("Failed to update series seasons", "series_id", sc.ID, "error", err)
					errored++
					continue
				}

				// Create alert
				newSeasons := newTotalSeasons - sc.TotalSeasons
				message := fmt.Sprintf("New seasons available for %s: %d new season(s)", sc.Title, newSeasons)

				_, err = s.db.Exec(`
					INSERT INTO system_alerts (type, message, created_at, dismissed)
					VALUES (?, ?, CURRENT_TIMESTAMP, 0)
				`, "new_seasons", message)

				if err != nil {
					slog.Error("Failed to create alert", "series_id", sc.ID, "error", err)
				}

				slog.Info("Detected new seasons", "series", sc.Title, "old", sc.TotalSeasons, "new", newTotalSeasons)
				updated++
			}
		}

		slog.Info("TVDB check completed", "checked", checked, "updated", updated, "errors", errored)
	}()

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Check started",
	})
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
	if err != nil {
		s.respondError(w, http.StatusNotFound, "series not found")
		return
	}

	// Get owned seasons from seasons table with voice actor JOIN (includes cached poster_url)
	query := `
		SELECT sn.season_number, sn.folder_path, sn.is_owned, sn.voice_actor_id, va.name, sn.discovered_at, sn.poster_url
		FROM seasons sn
		LEFT JOIN voice_actors va ON sn.voice_actor_id = va.id
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
	ownedSeasons := make(map[int]map[string]interface{})
	for rows.Next() {
		var seasonNum int
		var folderPath, voiceActorName, seasonPosterURL *string
		var isOwned bool
		var voiceActorID *int
		var discoveredAt *time.Time

		if err := rows.Scan(&seasonNum, &folderPath, &isOwned, &voiceActorID, &voiceActorName, &discoveredAt, &seasonPosterURL); err != nil {
			slog.Error("Failed to scan season detail row", "error", err)
			continue
		}

		season := map[string]interface{}{
			"season_number": seasonNum,
			"owned":         isOwned,
		}

		if folderPath != nil {
			season["folder_path"] = *folderPath
		}
		if voiceActorID != nil {
			season["voice_actor_id"] = *voiceActorID
		}
		if voiceActorName != nil {
			season["voice_actor_name"] = *voiceActorName
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

		ownedSeasons[seasonNum] = season
	}

	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading seasons")
		return
	}

	// Build response with all seasons (owned and missing)
	seasons := []map[string]interface{}{}
	maxSeasons := totalSeasons
	// Ensure maxSeasons includes all owned season numbers (they may exceed totalSeasons)
	for num := range ownedSeasons {
		if num > maxSeasons {
			maxSeasons = num
		}
	}

	for i := 1; i <= maxSeasons; i++ {
		if season, exists := ownedSeasons[i]; exists {
			// Owned season
			seasons = append(seasons, season)
		} else {
			// Missing season - locked
			lockedSeason := map[string]interface{}{
				"season_number": i,
				"owned":         false,
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

	if err != nil || folderPath == nil {
		s.respondError(w, http.StatusNotFound, "season not found or no folder path")
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
		var processed bool
		err := s.db.QueryRow(`
			SELECT COUNT(*) > 0 FROM processed_files WHERE file_path = ?
		`, filePath).Scan(&processed)
		if err != nil {
			processed = false
		}

		files = append(files, map[string]interface{}{
			"path":      filePath,
			"name":      filepath.Base(filePath),
			"processed": processed,
		})
	}

	// Get audio tracks from first file (assuming all files have same structure)
	var audioTracks []map[string]interface{}
	if len(sortedPaths) > 0 {
		tracks := results[sortedPaths[0]]
		for _, track := range tracks {
			audioTracks = append(audioTracks, map[string]interface{}{
				"id":       track.ID,
				"codec":    track.Codec,
				"language": track.Language,
				"name":     track.Name,
				"default":  track.Default,
				"channels": track.Channels,
			})
		}
	}

	response := map[string]interface{}{
		"files":        files,
		"audio_tracks": audioTracks,
		"folder_path":  *folderPath,
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
	var isOwned bool
	var voiceActorID *int
	var voiceActorName *string
	var discoveredAt *time.Time
	err = s.db.QueryRow(`
		SELECT sn.folder_path, sn.is_owned, sn.voice_actor_id, va.name, sn.discovered_at
		FROM seasons sn
		LEFT JOIN voice_actors va ON sn.voice_actor_id = va.id
		WHERE sn.series_id = ? AND sn.season_number = ?
	`, sid, snum).Scan(&folderPath, &isOwned, &voiceActorID, &voiceActorName, &discoveredAt)

	if err != nil {
		s.respondError(w, http.StatusNotFound, "season not found")
		return
	}

	response := map[string]interface{}{
		"season_number": snum,
		"folder_path":   folderPath,
		"owned":         isOwned,
	}

	if voiceActorID != nil {
		response["voice_actor_id"] = *voiceActorID
	}
	if voiceActorName != nil {
		response["voice_actor_name"] = *voiceActorName
	}
	if discoveredAt != nil {
		response["discovered_at"] = *discoveredAt
	}

	s.respondJSON(w, http.StatusOK, response)
}

// handleUpdateSeason updates a season's voice_actor_id
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
		VoiceActorID *int `json:"voice_actor_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Verify season exists
	var exists bool
	err = s.db.QueryRow(`
		SELECT COUNT(*) > 0 FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, sid, snum).Scan(&exists)
	if err != nil || !exists {
		s.respondError(w, http.StatusNotFound, "season not found")
		return
	}

	// Treat voice_actor_id <= 0 as "clear"
	if req.VoiceActorID != nil && *req.VoiceActorID <= 0 {
		req.VoiceActorID = nil
	}

	// Verify voice actor exists if provided
	if req.VoiceActorID != nil {
		var voiceExists bool
		err := s.db.QueryRow(`SELECT COUNT(*) > 0 FROM voice_actors WHERE id = ?`, *req.VoiceActorID).Scan(&voiceExists)
		if err != nil || !voiceExists {
			s.respondError(w, http.StatusBadRequest, "invalid voice actor ID")
			return
		}
	}

	// Update voice_actor_id
	_, err = s.db.Exec(`
		UPDATE seasons SET voice_actor_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE series_id = ? AND season_number = ?
	`, req.VoiceActorID, sid, snum)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to update season")
		return
	}

	slog.Info("Updated season voice", "series_id", sid, "season", snum, "voice_actor_id", req.VoiceActorID)

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
	})
}

// handleListVoices returns all voice actor studios
func (s *Server) handleListVoices(w http.ResponseWriter, _ *http.Request) {
	rows, err := s.db.Query(`SELECT id, name FROM voice_actors ORDER BY name`)
	if err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to fetch voices")
		return
	}
	defer rows.Close() //nolint:errcheck

	voices := []map[string]interface{}{}
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			slog.Error("Failed to scan voice actor row", "error", err)
			continue
		}
		voices = append(voices, map[string]interface{}{
			"id":   id,
			"name": name,
		})
	}

	if err := rows.Err(); err != nil {
		s.respondError(w, http.StatusInternalServerError, "error reading voices")
		return
	}

	s.respondJSON(w, http.StatusOK, voices)
}

// handleRescanSeason forces a rescan of a specific season to detect file changes
func (s *Server) handleRescanSeason(w http.ResponseWriter, r *http.Request) {
	seriesID := chi.URLParam(r, "id")
	seasonNum := chi.URLParam(r, "num")

	// Convert parameters to appropriate types
	sid, err := strconv.ParseInt(seriesID, 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	snum, err := strconv.Atoi(seasonNum)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid season number")
		return
	}

	// Check if season exists and has a folder path
	var folderPath *string
	err = s.db.QueryRow(`
		SELECT folder_path FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, sid, snum).Scan(&folderPath)

	if err != nil {
		s.respondError(w, http.StatusNotFound, "season not found")
		return
	}
	if folderPath == nil {
		s.respondError(w, http.StatusBadRequest, "season has no folder path")
		return
	}

	slog.Info("Manual rescan triggered", "series_id", sid, "season", snum)

	// Run rescan asynchronously
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("Panic in rescan", "series_id", sid, "season", snum, "error", r)
			}
		}()
		if err := s.scanner.RescanSeason(sid, snum); err != nil {
			slog.Error("Rescan failed", "series_id", sid, "season", snum, "error", err)
		} else {
			slog.Info("Rescan completed", "series_id", sid, "season", snum)
		}
	}()

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Rescan started",
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

	var req struct {
		TrackID      int  `json:"track_id"`
		KeepOriginal bool `json:"keep_original"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.TrackID <= 0 {
		s.respondError(w, http.StatusBadRequest, "track_id is required")
		return
	}

	// Validate series ID and season number before database query
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

		// Check if already processed
		var alreadyProcessed bool
		_ = s.db.QueryRow(`SELECT COUNT(*) > 0 FROM processed_files WHERE file_path = ?`, filePath).Scan(&alreadyProcessed)

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

		// Process file
		err := s.audioCutter.RemoveAudioTracks(filePath, req.TrackID, req.KeepOriginal)
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

		// Mark as processed in database
		_, err = s.db.Exec(`
			INSERT INTO processed_files (file_path, series_id, season_number, track_kept, processed_at)
			VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, filePath, sid, snum, req.TrackID)

		if err != nil {
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

// Helper function to marshal JSON without error handling (panics on error)
func mustJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
