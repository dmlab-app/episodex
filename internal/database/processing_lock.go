package database

import (
	"fmt"
	"strings"
	"sync"
)

// ProcessingLock prevents concurrent audio processing of the same season,
// and provides a series-wide block used by series-delete to fence the scanner
// out of upserting any season for the series being deleted.
type ProcessingLock struct {
	mu         sync.Mutex
	busy       map[string]bool
	busySeries map[int64]bool
}

// NewProcessingLock creates a new ProcessingLock.
func NewProcessingLock() *ProcessingLock {
	return &ProcessingLock{
		busy:       make(map[string]bool),
		busySeries: make(map[int64]bool),
	}
}

func seasonKey(seriesID, seasonNum int64) string {
	return fmt.Sprintf("%d:%d", seriesID, seasonNum)
}

func seriesPrefix(seriesID int64) string {
	return fmt.Sprintf("%d:", seriesID)
}

// TryLock attempts to acquire the lock for a season. Returns true if acquired.
// Fails if the season is busy OR if the whole series is series-locked.
func (pl *ProcessingLock) TryLock(seriesID, seasonNum int64) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.busySeries[seriesID] {
		return false
	}
	key := seasonKey(seriesID, seasonNum)
	if pl.busy[key] {
		return false
	}
	pl.busy[key] = true
	return true
}

// Unlock releases the lock for a season.
func (pl *ProcessingLock) Unlock(seriesID, seasonNum int64) {
	key := seasonKey(seriesID, seasonNum)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	delete(pl.busy, key)
}

// IsLocked returns true if the season is currently being processed
// (or its series is series-locked).
func (pl *ProcessingLock) IsLocked(seriesID, seasonNum int64) bool {
	key := seasonKey(seriesID, seasonNum)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.busy[key] || pl.busySeries[seriesID]
}

// TryLockSeries acquires a series-wide lock that fences out all subsequent
// per-season TryLock calls for that series. Fails if any season of the series
// is currently busy or if the series is already series-locked. Used by
// handleDeleteSeries so a concurrent scan cannot upsert a previously unknown
// season after the delete handler has snapshotted folder paths.
func (pl *ProcessingLock) TryLockSeries(seriesID int64) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if pl.busySeries[seriesID] {
		return false
	}
	prefix := seriesPrefix(seriesID)
	for k := range pl.busy {
		if strings.HasPrefix(k, prefix) {
			return false
		}
	}
	pl.busySeries[seriesID] = true
	return true
}

// UnlockSeries releases the series-wide lock.
func (pl *ProcessingLock) UnlockSeries(seriesID int64) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	delete(pl.busySeries, seriesID)
}
