package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestProgressStorePreservesDurationForByteProgress(t *testing.T) {
	ps := newProgressStore(filepath.Join(t.TempDir(), "progress.json"))

	ps.RecordBytes("movie", 50*1024*1024, 500*1024*1024, 7200)
	entry, ok := ps.GetEntry("movie")
	if !ok {
		t.Fatal("expected progress entry")
	}
	if entry.DurationSeconds != 7200 {
		t.Fatalf("duration = %v, want 7200", entry.DurationSeconds)
	}
}

func TestProgressStoreIgnoresLargeBackwardJump(t *testing.T) {
	ps := newProgressStore(filepath.Join(t.TempDir(), "progress.json"))

	ps.RecordBytes("movie", 400*1024*1024, 500*1024*1024, 7200)
	ps.RecordBytes("movie", 8*1024*1024, 500*1024*1024, 7200)

	entry, ok := ps.GetEntry("movie")
	if !ok {
		t.Fatal("expected progress entry")
	}
	if entry.Position != 400*1024*1024 {
		t.Fatalf("position = %d, want %d", entry.Position, 400*1024*1024)
	}
}

func TestProgressStoreSaveAndLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	ps := newProgressStore(path)
	ps.RecordSeconds("movie", 123, 3600)
	if err := ps.SaveNow(); err != nil {
		t.Fatalf("SaveNow() error = %v", err)
	}

	loaded := newProgressStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	entry, ok := loaded.GetEntry("movie")
	if !ok {
		t.Fatal("expected loaded progress entry")
	}
	if entry.Seconds != 123 {
		t.Fatalf("seconds = %v, want 123", entry.Seconds)
	}
}

func TestProgressStoreDropsOldEntriesOnLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	ps := newProgressStore(path)
	ps.mu.Lock()
	ps.data["old"] = progressEntry{Seconds: 10, Updated: time.Now().AddDate(0, 0, -8)}
	ps.mu.Unlock()
	if err := ps.SaveNow(); err != nil {
		t.Fatalf("SaveNow() error = %v", err)
	}

	loaded := newProgressStore(path)
	if err := loaded.Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := loaded.GetEntry("old"); ok {
		t.Fatal("expected old progress to be deleted")
	}
}

func TestProgressStoreSummaryAndClearAll(t *testing.T) {
	ps := newProgressStore(filepath.Join(t.TempDir(), "progress.json"))
	now := time.Now()

	ps.mu.Lock()
	ps.data["movie-1"] = progressEntry{Seconds: 120, Updated: now.Add(-time.Minute)}
	ps.data["movie-2"] = progressEntry{Seconds: 240, Updated: now}
	ps.mu.Unlock()

	summary := ps.Summary()
	if summary.Count != 2 {
		t.Fatalf("summary.Count = %d, want 2", summary.Count)
	}
	if !summary.LastUpdated.Equal(now) {
		t.Fatalf("summary.LastUpdated = %v, want %v", summary.LastUpdated, now)
	}

	cleared := ps.ClearAll()
	if cleared != 2 {
		t.Fatalf("cleared = %d, want 2", cleared)
	}
	if summary = ps.Summary(); summary.Count != 0 {
		t.Fatalf("summary.Count after clear = %d, want 0", summary.Count)
	}
}

func TestProgressTimecodeInvalidationDoesNotNotify(t *testing.T) {
	prevCache := browseCache
	prevUpdateID := browseUpdateID
	prevTimer := browseNotifyTimer
	prevNotifyAt := browseNotifyAt
	defer func() {
		browseCacheMu.Lock()
		browseCache = prevCache
		browseCacheMu.Unlock()
		browseNotifyMu.Lock()
		if browseNotifyTimer != nil {
			browseNotifyTimer.Stop()
		}
		browseNotifyTimer = prevTimer
		browseNotifyAt = prevNotifyAt
		browseNotifyMu.Unlock()
		browseUpdateID = prevUpdateID
	}()

	browseCacheMu.Lock()
	browseCache = map[string]browseCacheEntry{
		"": {payload: "cached", count: 1, expires: time.Now().Add(time.Hour)},
	}
	browseCacheMu.Unlock()

	maybeInvalidateBrowseCache(
		progressEntry{Seconds: 10},
		progressEntry{Seconds: 11},
		3600,
	)

	if _, _, ok := getBrowseCache(""); ok {
		t.Fatal("expected browse cache to be invalidated")
	}
	if got := currentBrowseUpdateID(); got != prevUpdateID {
		t.Fatalf("browse update ID = %d, want %d", got, prevUpdateID)
	}
}
