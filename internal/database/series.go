package database

import (
	"database/sql"
	"fmt"
	"log/slog"
)

// Series represents a TV series with full metadata
type Series struct {
	TVDBId           *int
	OriginalTitle    *string
	Slug             *string
	Overview         *string
	PosterURL        *string
	BackdropURL      *string
	Status           *string
	FirstAired       *string
	LastAired        *string
	Year             *int
	Runtime          *int
	Rating           *float64
	ContentRating    *string
	OriginalCountry  *string
	OriginalLanguage *string
	Genres           *string // JSON array
	Networks         *string // JSON array
	Studios          *string // JSON array
	Title            string
	ID               int64
	TotalSeasons     int
}

// Season represents a season of a series
type Season struct {
	TVDBSeasonID *int
	Name         *string
	Overview     *string
	PosterURL    *string
	FirstAired   *string
	EpisodeCount *int
	FolderPath   *string
	VoiceActorID *int
	DiscoveredAt *string
	ID           int64
	SeriesID     int64
	SeasonNumber int
	IsOwned      bool
}

// Episode represents an episode
type Episode struct {
	TVDBEpisodeID *int
	Title         *string
	Overview      *string
	ImageURL      *string
	AirDate       *string
	Runtime       *int
	Rating        *float64
	FilePath      *string
	FileHash      *string
	FileSize      *int64
	WatchedAt     *string
	ID            int64
	SeasonID      int64
	EpisodeNumber int
	IsOwned       bool
}

// Character represents a character in a series
type Character struct {
	TVDBCharacterID *int
	TVDBPersonID    *int
	CharacterName   *string
	ActorName       *string
	ImageURL        *string
	SortOrder       *int
	ID              int64
	SeriesID        int64
}

// Artwork represents artwork for series or season
type Artwork struct {
	SeriesID      *int64
	SeasonID      *int64
	TVDBArtworkID *int
	Type          *string
	ThumbnailURL  *string
	Language      *string
	Score         *float64
	Width         *int
	Height        *int
	URL           string
	ID            int64
	IsPrimary     bool
}

// UpsertSeries creates or updates a series with full metadata
func (db *DB) UpsertSeries(series *Series) (int64, error) {
	// Check if series with tvdb_id exists
	if series.TVDBId != nil {
		var existingID int64
		err := db.QueryRow(`SELECT id FROM series WHERE tvdb_id = ?`, *series.TVDBId).Scan(&existingID)
		if err == nil {
			// Update existing series
			_, err = db.Exec(`
				UPDATE series SET
					title = ?, original_title = ?, slug = ?, overview = ?,
					poster_url = ?, backdrop_url = ?, status = ?,
					first_aired = ?, last_aired = ?, year = ?, runtime = ?,
					rating = ?, content_rating = ?, original_country = ?,
					original_language = ?, genres = ?, networks = ?, studios = ?,
					total_seasons = ?, updated_at = CURRENT_TIMESTAMP
				WHERE id = ?
			`, series.Title, series.OriginalTitle, series.Slug, series.Overview,
				series.PosterURL, series.BackdropURL, series.Status,
				series.FirstAired, series.LastAired, series.Year, series.Runtime,
				series.Rating, series.ContentRating, series.OriginalCountry,
				series.OriginalLanguage, series.Genres, series.Networks, series.Studios,
				series.TotalSeasons, existingID)
			if err != nil {
				return 0, fmt.Errorf("failed to update series: %w", err)
			}
			return existingID, nil
		}
	}

	// Insert new series
	result, err := db.Exec(`
		INSERT INTO series (
			tvdb_id, title, original_title, slug, overview,
			poster_url, backdrop_url, status,
			first_aired, last_aired, year, runtime,
			rating, content_rating, original_country,
			original_language, genres, networks, studios,
			total_seasons, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, series.TVDBId, series.Title, series.OriginalTitle, series.Slug, series.Overview,
		series.PosterURL, series.BackdropURL, series.Status,
		series.FirstAired, series.LastAired, series.Year, series.Runtime,
		series.Rating, series.ContentRating, series.OriginalCountry,
		series.OriginalLanguage, series.Genres, series.Networks, series.Studios,
		series.TotalSeasons)

	if err != nil {
		return 0, fmt.Errorf("failed to insert series: %w", err)
	}

	return result.LastInsertId()
}

// UpsertSeason creates or updates a season
func (db *DB) UpsertSeason(season *Season) (int64, error) {
	// Check if season exists
	var existingID int64
	err := db.QueryRow(`
		SELECT id FROM seasons
		WHERE series_id = ? AND season_number = ?
	`, season.SeriesID, season.SeasonNumber).Scan(&existingID)

	if err == nil {
		// Update existing season
		_, err = db.Exec(`
			UPDATE seasons SET
				tvdb_season_id = ?, name = ?, overview = ?, poster_url = ?,
				first_aired = ?, episode_count = ?, folder_path = ?,
				voice_actor_id = ?, is_owned = ?, discovered_at = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, season.TVDBSeasonID, season.Name, season.Overview, season.PosterURL,
			season.FirstAired, season.EpisodeCount, season.FolderPath,
			season.VoiceActorID, season.IsOwned, season.DiscoveredAt, existingID)
		if err != nil {
			return 0, fmt.Errorf("failed to update season: %w", err)
		}
		return existingID, nil
	}

	// Insert new season
	result, err := db.Exec(`
		INSERT INTO seasons (
			series_id, tvdb_season_id, season_number, name, overview,
			poster_url, first_aired, episode_count, folder_path,
			voice_actor_id, is_owned, discovered_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, season.SeriesID, season.TVDBSeasonID, season.SeasonNumber, season.Name, season.Overview,
		season.PosterURL, season.FirstAired, season.EpisodeCount, season.FolderPath,
		season.VoiceActorID, season.IsOwned, season.DiscoveredAt)

	if err != nil {
		return 0, fmt.Errorf("failed to insert season: %w", err)
	}

	return result.LastInsertId()
}

// UpsertEpisode creates or updates an episode
func (db *DB) UpsertEpisode(episode *Episode) (int64, error) {
	// Check if episode exists
	var existingID int64
	err := db.QueryRow(`
		SELECT id FROM episodes
		WHERE season_id = ? AND episode_number = ?
	`, episode.SeasonID, episode.EpisodeNumber).Scan(&existingID)

	if err == nil {
		// Update existing episode
		_, err = db.Exec(`
			UPDATE episodes SET
				tvdb_episode_id = ?, title = ?, overview = ?, image_url = ?,
				air_date = ?, runtime = ?, rating = ?,
				file_path = ?, file_hash = ?, file_size = ?,
				is_owned = ?, watched_at = ?,
				updated_at = CURRENT_TIMESTAMP
			WHERE id = ?
		`, episode.TVDBEpisodeID, episode.Title, episode.Overview, episode.ImageURL,
			episode.AirDate, episode.Runtime, episode.Rating,
			episode.FilePath, episode.FileHash, episode.FileSize,
			episode.IsOwned, episode.WatchedAt, existingID)
		if err != nil {
			return 0, fmt.Errorf("failed to update episode: %w", err)
		}
		return existingID, nil
	}

	// Insert new episode
	result, err := db.Exec(`
		INSERT INTO episodes (
			season_id, tvdb_episode_id, episode_number, title, overview,
			image_url, air_date, runtime, rating,
			file_path, file_hash, file_size,
			is_owned, watched_at,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
	`, episode.SeasonID, episode.TVDBEpisodeID, episode.EpisodeNumber, episode.Title, episode.Overview,
		episode.ImageURL, episode.AirDate, episode.Runtime, episode.Rating,
		episode.FilePath, episode.FileHash, episode.FileSize,
		episode.IsOwned, episode.WatchedAt)

	if err != nil {
		return 0, fmt.Errorf("failed to insert episode: %w", err)
	}

	return result.LastInsertId()
}

// GetSeasonByID retrieves a season by its ID
func (db *DB) GetSeasonByID(seasonID int64) (*Season, error) {
	var season Season
	err := db.QueryRow(`
		SELECT id, series_id, tvdb_season_id, season_number, name, overview,
			poster_url, first_aired, episode_count, folder_path,
			voice_actor_id, is_owned, discovered_at
		FROM seasons WHERE id = ?
	`, seasonID).Scan(
		&season.ID, &season.SeriesID, &season.TVDBSeasonID, &season.SeasonNumber,
		&season.Name, &season.Overview, &season.PosterURL, &season.FirstAired,
		&season.EpisodeCount, &season.FolderPath, &season.VoiceActorID,
		&season.IsOwned, &season.DiscoveredAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get season: %w", err)
	}
	return &season, nil
}

// GetSeasonBySeriesAndNumber retrieves a season by series ID and season number
func (db *DB) GetSeasonBySeriesAndNumber(seriesID int64, seasonNumber int) (*Season, error) {
	var season Season
	err := db.QueryRow(`
		SELECT id, series_id, tvdb_season_id, season_number, name, overview,
			poster_url, first_aired, episode_count, folder_path,
			voice_actor_id, is_owned, discovered_at
		FROM seasons WHERE series_id = ? AND season_number = ?
	`, seriesID, seasonNumber).Scan(
		&season.ID, &season.SeriesID, &season.TVDBSeasonID, &season.SeasonNumber,
		&season.Name, &season.Overview, &season.PosterURL, &season.FirstAired,
		&season.EpisodeCount, &season.FolderPath, &season.VoiceActorID,
		&season.IsOwned, &season.DiscoveredAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get season: %w", err)
	}
	return &season, nil
}

// UpsertCharacters inserts or updates characters for a series
func (db *DB) UpsertCharacters(seriesID int64, characters []Character) error {
	// Delete existing characters
	_, err := db.Exec(`DELETE FROM series_characters WHERE series_id = ?`, seriesID)
	if err != nil {
		return fmt.Errorf("failed to delete old characters: %w", err)
	}

	// Insert new characters
	stmt, err := db.Prepare(`
		INSERT INTO series_characters (
			series_id, tvdb_character_id, tvdb_person_id,
			character_name, actor_name, image_url, sort_order
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare character insert: %w", err)
	}
	defer stmt.Close() //nolint:errcheck // closing prepared statement

	for _, char := range characters {
		_, err := stmt.Exec(
			seriesID, char.TVDBCharacterID, char.TVDBPersonID,
			char.CharacterName, char.ActorName, char.ImageURL, char.SortOrder,
		)
		if err != nil {
			slog.Error("Failed to insert character", "error", err)
		}
	}

	return nil
}

// UpsertArtworks inserts or updates artworks for a series or season
func (db *DB) UpsertArtworks(artworks []Artwork) error {
	stmt, err := db.Prepare(`
		INSERT INTO artworks (
			series_id, season_id, tvdb_artwork_id, type,
			url, thumbnail_url, language, score,
			width, height, is_primary
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare artwork insert: %w", err)
	}
	defer stmt.Close() //nolint:errcheck // closing prepared statement

	for _, art := range artworks {
		_, err := stmt.Exec(
			art.SeriesID, art.SeasonID, art.TVDBArtworkID, art.Type,
			art.URL, art.ThumbnailURL, art.Language, art.Score,
			art.Width, art.Height, art.IsPrimary,
		)
		if err != nil {
			slog.Error("Failed to insert artwork", "error", err)
		}
	}

	return nil
}
