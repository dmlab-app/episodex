package database

import (
	"database/sql"
	"fmt"
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
		if err != nil && err != sql.ErrNoRows {
			return 0, fmt.Errorf("failed to check existing series: %w", err)
		}
		if err == nil {
			// Update existing series — COALESCE keeps existing values when new value is NULL
			_, err = db.Exec(`
				UPDATE series SET
					title = COALESCE(?, title),
					original_title = COALESCE(?, original_title),
					slug = COALESCE(?, slug),
					overview = COALESCE(?, overview),
					poster_url = COALESCE(?, poster_url),
					backdrop_url = COALESCE(?, backdrop_url),
					status = COALESCE(?, status),
					first_aired = COALESCE(?, first_aired),
					last_aired = COALESCE(?, last_aired),
					year = COALESCE(?, year),
					runtime = COALESCE(?, runtime),
					rating = COALESCE(?, rating),
					content_rating = COALESCE(?, content_rating),
					original_country = COALESCE(?, original_country),
					original_language = COALESCE(?, original_language),
					genres = COALESCE(?, genres),
					networks = COALESCE(?, networks),
					studios = COALESCE(?, studios),
					total_seasons = COALESCE(?, total_seasons),
					updated_at = CURRENT_TIMESTAMP
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

	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("failed to check existing season: %w", err)
	}

	if err == nil {
		// Update existing season — use COALESCE so that callers with partial data
		// (e.g. scanner with no TVDB fields) don't overwrite previously synced metadata.
		_, err = db.Exec(`
			UPDATE seasons SET
				tvdb_season_id = COALESCE(?, tvdb_season_id),
				name = COALESCE(?, name),
				overview = COALESCE(?, overview),
				poster_url = COALESCE(?, poster_url),
				first_aired = COALESCE(?, first_aired),
				episode_count = COALESCE(?, episode_count),
				folder_path = COALESCE(?, folder_path),
				voice_actor_id = COALESCE(?, voice_actor_id),
				is_owned = MAX(is_owned, ?),
				discovered_at = COALESCE(?, discovered_at),
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

	if err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("failed to check existing episode: %w", err)
	}

	if err == nil {
		// Update existing episode — use COALESCE for TVDB metadata fields
		// so that a file scan (which has no TVDB data) won't overwrite synced metadata
		_, err = db.Exec(`
			UPDATE episodes SET
				tvdb_episode_id = COALESCE(?, tvdb_episode_id),
				title = COALESCE(?, title),
				overview = COALESCE(?, overview),
				image_url = COALESCE(?, image_url),
				air_date = COALESCE(?, air_date),
				runtime = COALESCE(?, runtime),
				rating = COALESCE(?, rating),
				file_path = COALESCE(?, file_path),
				file_hash = COALESCE(?, file_hash),
				file_size = COALESCE(?, file_size),
				is_owned = MAX(is_owned, ?),
				watched_at = COALESCE(?, watched_at),
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
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Delete existing characters
	_, err = tx.Exec(`DELETE FROM series_characters WHERE series_id = ?`, seriesID)
	if err != nil {
		return fmt.Errorf("failed to delete old characters: %w", err)
	}

	// Insert new characters
	stmt, err := tx.Prepare(`
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
			return fmt.Errorf("failed to insert character %v: %w", char.CharacterName, err)
		}
	}

	return tx.Commit()
}

// UpsertArtworks inserts or updates artworks for a series or season.
// Deletes existing artworks for the series before inserting to prevent duplicates.
func (db *DB) UpsertArtworks(artworks []Artwork) error {
	if len(artworks) == 0 {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	// Delete existing artworks for this series to prevent accumulating duplicates
	seriesID := artworks[0].SeriesID
	if _, err := tx.Exec(`DELETE FROM artworks WHERE series_id = ?`, seriesID); err != nil {
		return fmt.Errorf("failed to delete existing artworks: %w", err)
	}

	stmt, err := tx.Prepare(`
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
			tvdbID := "<nil>"
			if art.TVDBArtworkID != nil {
				tvdbID = fmt.Sprintf("%d", *art.TVDBArtworkID)
			}
			return fmt.Errorf("failed to insert artwork (tvdb_id=%s): %w", tvdbID, err)
		}
	}

	return tx.Commit()
}
