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
