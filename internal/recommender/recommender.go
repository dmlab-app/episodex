// Package recommender aggregates TMDB recommendations from the user's library
// and filters them by Kinozal availability.
package recommender

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/episodex/episodex/internal/database"
	"github.com/episodex/episodex/internal/tmdb"
	"github.com/episodex/episodex/internal/tracker"
)

const (
	ratingThreshold     = 7.0
	maxFinal            = 20
	candidateBufferSize = 40
	interCallDelay      = 100 * time.Millisecond
)

// tmdbClient is the subset of the TMDB client used by the recommender.
// Defined as an interface to allow injection of fakes in tests.
type tmdbClient interface {
	FindByTVDBID(tvdbID int) (*tmdb.TMDBShow, error)
	GetRecommendations(tmdbID int) ([]tmdb.TMDBShow, error)
	GetExternalIDs(tmdbID int) (*tmdb.ExternalIDs, error)
}

// Recommender coordinates TMDB aggregation, filtering, and DB persistence.
type Recommender struct {
	db      *database.DB
	tmdb    tmdbClient
	kinozal tracker.SeasonSearcher
	mu      sync.Mutex

	// sleep is the pause between TMDB calls. Overridden in tests.
	sleep func(time.Duration)
}

// New constructs a Recommender. All three dependencies are required.
func New(db *database.DB, t tmdbClient, k tracker.SeasonSearcher) *Recommender {
	return &Recommender{
		db:      db,
		tmdb:    t,
		kinozal: k,
		sleep:   time.Sleep,
	}
}

// candidate accumulates aggregate data for a TMDB-recommended show.
type candidate struct {
	name         string
	originalName string
	overview     string
	posterPath   string
	firstAirDate string
	genreIDs     []int
	tmdbID       int
	frequency    int
	voteAverage  float64
}

// Refresh rebuilds the recommendations table by aggregating TMDB recommendations
// across all owned series, filtering by rating, owned/blacklist membership, and
// Kinozal availability, then storing the top candidates.
//
// Only a single Refresh may run at a time; if one is already in progress the
// call returns nil immediately with an info log.
func (r *Recommender) Refresh() error {
	if !r.mu.TryLock() {
		slog.Info("Recommendation refresh already in progress; skipping")
		return nil
	}
	defer r.mu.Unlock()

	ownedTVDB, ownedTitles, err := r.loadOwnedSeries()
	if err != nil {
		return fmt.Errorf("load owned series: %w", err)
	}
	if len(ownedTVDB) == 0 {
		slog.Info("Recommendation refresh: no owned series with tvdb_id; clearing recommendations")
		return r.db.ReplaceRecommendations(nil)
	}

	blacklist, err := r.db.GetBlacklistedIDs()
	if err != nil {
		return fmt.Errorf("load blacklist: %w", err)
	}

	candidates, aggHadErrors, err := r.aggregateCandidates(ownedTVDB, ownedTitles)
	if err != nil {
		return fmt.Errorf("aggregate candidates: %w", err)
	}
	if len(candidates) == 0 {
		if aggHadErrors {
			slog.Warn("Recommendation refresh: no candidates and partial TMDB errors; preserving existing recommendations")
			return nil
		}
		slog.Info("Recommendation refresh: no candidates after aggregation")
		return r.db.ReplaceRecommendations(nil)
	}

	ranked := rankCandidates(candidates)

	recs, filterHadErrors, err := r.filterAndBuild(ranked, ownedTVDB, blacklist)
	if err != nil {
		return fmt.Errorf("filter candidates: %w", err)
	}

	if len(recs) == 0 && (aggHadErrors || filterHadErrors) {
		slog.Warn("Recommendation refresh: empty result with upstream errors; preserving existing recommendations")
		return nil
	}

	if err := r.db.ReplaceRecommendations(recs); err != nil {
		return fmt.Errorf("replace recommendations: %w", err)
	}
	slog.Info("Recommendation refresh complete", "count", len(recs))
	return nil
}

// loadOwnedSeries returns a set of owned tvdb_ids and a matching map of titles,
// pulled from the series table.
func (r *Recommender) loadOwnedSeries() (map[int]bool, map[int]string, error) {
	rows, err := r.db.Query(`SELECT tvdb_id, title FROM series WHERE tvdb_id IS NOT NULL`)
	if err != nil {
		return nil, nil, fmt.Errorf("query series: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	owned := make(map[int]bool)
	titles := make(map[int]string)
	for rows.Next() {
		var tvdbID int
		var title string
		if err := rows.Scan(&tvdbID, &title); err != nil {
			return nil, nil, fmt.Errorf("scan series: %w", err)
		}
		owned[tvdbID] = true
		titles[tvdbID] = title
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate series: %w", err)
	}
	return owned, titles, nil
}

// aggregateCandidates fans out over owned series, calling TMDB to gather
// recommendation candidates, keyed by TMDB id. Returns an error if any TMDB
// call failed and no recommendations endpoint succeeded — this prevents a
// partial TMDB outage from silently wiping the existing recommendations
// table. A successful /find returning no TMDB match does not count as a
// successful recommendation fetch, because it does not exercise the
// /recommendations endpoint. The second return value indicates whether any
// TMDB call errored, so the caller can preserve existing rows when the
// final result is empty and any upstream dependency was partially degraded.
func (r *Recommender) aggregateCandidates(owned map[int]bool, titles map[int]string) (map[int]*candidate, bool, error) {
	candidates := make(map[int]*candidate)
	tmdbErrors := 0
	recsSucceeded := 0
	for tvdbID := range owned {
		show, err := r.tmdb.FindByTVDBID(tvdbID)
		r.sleep(interCallDelay)
		if err != nil {
			tmdbErrors++
			slog.Warn("TMDB find failed", "tvdb_id", tvdbID, "title", titles[tvdbID], "error", err)
			continue
		}
		if show == nil {
			continue
		}

		recs, err := r.tmdb.GetRecommendations(show.ID)
		r.sleep(interCallDelay)
		if err != nil {
			tmdbErrors++
			slog.Warn("TMDB recommendations failed", "tmdb_id", show.ID, "title", titles[tvdbID], "error", err)
			continue
		}
		recsSucceeded++

		for _, rec := range recs {
			c, ok := candidates[rec.ID]
			if !ok {
				c = &candidate{
					tmdbID:       rec.ID,
					name:         rec.Name,
					originalName: rec.OriginalName,
					overview:     rec.Overview,
					posterPath:   rec.PosterPath,
					firstAirDate: rec.FirstAirDate,
					voteAverage:  rec.VoteAverage,
					genreIDs:     rec.GenreIDs,
				}
				candidates[rec.ID] = c
			}
			c.frequency++
		}
	}
	if tmdbErrors > 0 && recsSucceeded == 0 {
		return nil, true, fmt.Errorf("all TMDB lookups with errors; %d errors, 0 successful recommendation fetches", tmdbErrors)
	}
	return candidates, tmdbErrors > 0, nil
}

// rankedCandidate pairs a candidate with its computed score.
type rankedCandidate struct {
	c     *candidate
	score float64
}

// rankCandidates filters by rating and sorts by score DESC.
func rankCandidates(candidates map[int]*candidate) []rankedCandidate {
	ranked := make([]rankedCandidate, 0, len(candidates))
	for _, c := range candidates {
		if c.voteAverage <= ratingThreshold {
			continue
		}
		ranked = append(ranked, rankedCandidate{
			c:     c,
			score: float64(c.frequency) * c.voteAverage,
		})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].c.tmdbID < ranked[j].c.tmdbID
	})
	return ranked
}

// filterAndBuild walks ranked candidates in order, resolves TVDB ids,
// excludes owned/blacklisted shows, requires a Kinozal S01 torrent,
// and stops once maxFinal entries are collected or candidateBufferSize probed.
// Returns an error if every external_ids probe failed or every Kinozal
// search errored, so a partial outage on either dependency does not silently
// wipe the recommendations table. The second return value indicates whether
// any external_ids or Kinozal call errored, so the caller can preserve
// existing rows when the final result is empty and any upstream dependency
// was partially degraded.
func (r *Recommender) filterAndBuild(ranked []rankedCandidate, owned, blacklist map[int]bool) ([]database.Recommendation, bool, error) {
	recs := make([]database.Recommendation, 0, maxFinal)
	seenTVDB := make(map[int]bool)
	probed := 0
	extErrors := 0
	extSucceeded := 0
	kinozalAttempts := 0
	kinozalErrors := 0
	kinozalSucceeded := 0
	for _, rc := range ranked {
		if len(recs) >= maxFinal || probed >= candidateBufferSize {
			break
		}
		probed++

		ext, err := r.tmdb.GetExternalIDs(rc.c.tmdbID)
		r.sleep(interCallDelay)
		if err != nil {
			extErrors++
			slog.Warn("TMDB external_ids failed", "tmdb_id", rc.c.tmdbID, "error", err)
			continue
		}
		extSucceeded++
		if ext == nil || ext.TVDBId == 0 {
			continue
		}
		if owned[ext.TVDBId] || blacklist[ext.TVDBId] {
			continue
		}
		if seenTVDB[ext.TVDBId] {
			continue
		}

		kinozalAttempts++
		anyOK := false
		anyErr := false
		torrent, err := r.kinozal.FindSeasonTorrent(rc.c.name, 1)
		if err != nil {
			slog.Warn("Kinozal search failed", "title", rc.c.name, "error", err)
			torrent = nil
			anyErr = true
		} else {
			anyOK = true
		}
		if torrent == nil && rc.c.originalName != "" && rc.c.originalName != rc.c.name {
			fallback, fallbackErr := r.kinozal.FindSeasonTorrent(rc.c.originalName, 1)
			if fallbackErr != nil {
				slog.Warn("Kinozal search (fallback) failed", "title", rc.c.originalName, "error", fallbackErr)
				anyErr = true
			} else {
				anyOK = true
				torrent = fallback
			}
		}
		if anyOK {
			kinozalSucceeded++
		}
		if anyErr {
			kinozalErrors++
		}
		if torrent == nil {
			continue
		}

		seenTVDB[ext.TVDBId] = true
		recs = append(recs, buildRecommendation(rc, ext.TVDBId, torrent))
	}
	if probed > 0 && extErrors > 0 && extSucceeded == 0 {
		return nil, true, fmt.Errorf("all %d TMDB external_ids lookups failed", extErrors)
	}
	if kinozalAttempts > 0 && kinozalErrors > 0 && kinozalSucceeded == 0 {
		return nil, true, fmt.Errorf("all %d Kinozal searches failed", kinozalErrors)
	}
	return recs, extErrors > 0 || kinozalErrors > 0, nil
}

// buildRecommendation materializes a DB row from a ranked candidate + torrent.
func buildRecommendation(rc rankedCandidate, tvdbID int, torrent *tracker.SeasonSearchResult) database.Recommendation {
	genres := ""
	if len(rc.c.genreIDs) > 0 {
		if b, err := json.Marshal(rc.c.genreIDs); err == nil {
			genres = string(b)
		}
	}
	return database.Recommendation{
		TVDBID:        tvdbID,
		TMDBID:        rc.c.tmdbID,
		Title:         rc.c.name,
		OriginalTitle: rc.c.originalName,
		Overview:      rc.c.overview,
		PosterURL:     tmdb.PosterURL(rc.c.posterPath),
		Year:          parseYear(rc.c.firstAirDate),
		Rating:        rc.c.voteAverage,
		Genres:        genres,
		Score:         rc.score,
		TrackerURL:    torrent.DetailsURL,
		TorrentTitle:  torrent.Title,
		TorrentSize:   torrent.Size,
	}
}

// parseYear extracts the year prefix from a TMDB first_air_date ("YYYY-MM-DD").
func parseYear(date string) int {
	if len(date) < 4 {
		return 0
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return 0
	}
	return y
}
