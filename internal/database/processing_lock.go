package database

import (
	"fmt"
	"sync"
)

// ProcessingLock prevents concurrent audio processing of the same season.
type ProcessingLock struct {
	mu   sync.Mutex
	busy map[string]bool
}

// NewProcessingLock creates a new ProcessingLock.
func NewProcessingLock() *ProcessingLock {
	return &ProcessingLock{busy: make(map[string]bool)}
}

func seasonKey(seriesID, seasonNum int64) string {
	return fmt.Sprintf("%d:%d", seriesID, seasonNum)
}

// TryLock attempts to acquire the lock for a season. Returns true if acquired.
func (pl *ProcessingLock) TryLock(seriesID, seasonNum int64) bool {
	key := seasonKey(seriesID, seasonNum)
	pl.mu.Lock()
	defer pl.mu.Unlock()
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

// IsLocked returns true if the season is currently being processed.
func (pl *ProcessingLock) IsLocked(seriesID, seasonNum int64) bool {
	key := seasonKey(seriesID, seasonNum)
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.busy[key]
}
