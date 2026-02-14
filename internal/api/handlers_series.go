package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// handleSyncSeriesFromTVDB syncs series metadata from TVDB
func (s *Server) handleSyncSeriesFromTVDB(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		s.respondError(w, http.StatusBadRequest, "invalid series ID")
		return
	}

	if s.tvdbClient == nil {
		s.respondError(w, http.StatusServiceUnavailable, "TVDB client not configured")
		return
	}

	// Get series tvdb_id
	var tvdbID *int
	err = s.db.QueryRow(`SELECT tvdb_id FROM series WHERE id = ?`, id).Scan(&tvdbID)
	if err != nil || tvdbID == nil {
		s.respondError(w, http.StatusNotFound, "series not found or no TVDB ID")
		return
	}

	if err := SyncSeriesMetadata(s.db, s.tvdbClient, id, *tvdbID); err != nil {
		s.respondError(w, http.StatusInternalServerError, "failed to sync series metadata")
		return
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Synced series ID %d from TVDB", id),
	})
}
