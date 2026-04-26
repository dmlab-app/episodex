package database

import "testing"

func TestProcessingLock_BasicTryLockUnlock(t *testing.T) {
	pl := NewProcessingLock()

	if !pl.TryLock(1, 1) {
		t.Fatal("first TryLock should succeed")
	}
	if pl.TryLock(1, 1) {
		t.Error("re-acquiring same lock should fail")
	}
	if !pl.TryLock(1, 2) {
		t.Error("different season should be lockable")
	}
	pl.Unlock(1, 1)
	if !pl.TryLock(1, 1) {
		t.Error("Unlock should release the lock")
	}
}

func TestProcessingLock_SeriesLockBlocksNewSeasonLocks(t *testing.T) {
	pl := NewProcessingLock()

	if !pl.TryLockSeries(42) {
		t.Fatal("TryLockSeries should succeed on idle series")
	}
	if pl.TryLock(42, 1) {
		t.Error("TryLock should fail when series is series-locked")
	}
	if pl.TryLock(42, 99) {
		t.Error("TryLock for any season should fail when series is series-locked")
	}
	if !pl.TryLock(43, 1) {
		t.Error("TryLock for a different series should succeed")
	}
	pl.UnlockSeries(42)
	if !pl.TryLock(42, 1) {
		t.Error("TryLock should succeed after UnlockSeries")
	}
}

func TestProcessingLock_TryLockSeriesFailsWhenSeasonHeld(t *testing.T) {
	pl := NewProcessingLock()

	if !pl.TryLock(7, 3) {
		t.Fatal("TryLock should succeed")
	}
	if pl.TryLockSeries(7) {
		t.Error("TryLockSeries should fail when a season of that series is locked")
	}
	if !pl.TryLockSeries(8) {
		t.Error("TryLockSeries for a different series should succeed")
	}
	pl.Unlock(7, 3)
	if !pl.TryLockSeries(7) {
		t.Error("TryLockSeries should succeed after the season is unlocked")
	}
}

func TestProcessingLock_TryLockSeriesIdempotentFails(t *testing.T) {
	pl := NewProcessingLock()

	if !pl.TryLockSeries(5) {
		t.Fatal("first TryLockSeries should succeed")
	}
	if pl.TryLockSeries(5) {
		t.Error("second TryLockSeries on same series should fail")
	}
	pl.UnlockSeries(5)
	if !pl.TryLockSeries(5) {
		t.Error("TryLockSeries should succeed after UnlockSeries")
	}
}

func TestProcessingLock_IsLockedReflectsSeriesLock(t *testing.T) {
	pl := NewProcessingLock()

	if pl.IsLocked(11, 1) {
		t.Error("expected not-locked initially")
	}
	if !pl.TryLockSeries(11) {
		t.Fatal("TryLockSeries should succeed")
	}
	if !pl.IsLocked(11, 1) {
		t.Error("IsLocked should report true when series is series-locked")
	}
	pl.UnlockSeries(11)
	if pl.IsLocked(11, 1) {
		t.Error("IsLocked should report false after UnlockSeries")
	}
}
