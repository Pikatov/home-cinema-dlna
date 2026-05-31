package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeMediaRelPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
		ok   bool
		out  string
	}{
		{name: "empty", in: "", ok: true, out: ""},
		{name: "nested", in: "Movies/Film.mkv", ok: true, out: "Movies/Film.mkv"},
		{name: "normalized", in: "/Movies/Film.mkv", ok: true, out: "Movies/Film.mkv"},
		{name: "traversal", in: "../secret", ok: false},
		{name: "null", in: "bad\x00path", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := safeMediaRelPath(tt.in)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.out {
				t.Fatalf("out = %q, want %q", got, tt.out)
			}
		})
	}
}

func TestSafeJoinUnderBase(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "movie.mkv")
	if err := os.WriteFile(file, []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}

	joined, ok := safeJoinUnderBase(base, "movie.mkv")
	if !ok {
		t.Fatalf("safeJoinUnderBase() = %q, %v", joined, ok)
	}
	realJoined, err := filepath.EvalSymlinks(joined)
	if err != nil {
		t.Fatal(err)
	}
	realFile, err := filepath.EvalSymlinks(file)
	if err != nil {
		t.Fatal(err)
	}
	if realJoined != realFile {
		t.Fatalf("safeJoinUnderBase() real path = %q, want %q", realJoined, realFile)
	}

	if _, ok := safeJoinUnderBase(base, "../outside.mkv"); ok {
		t.Fatal("expected traversal to be rejected")
	}
}

func TestParseRange(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		size      int64
		wantStart int64
		wantEnd   int64
		wantOK    bool
	}{
		{name: "full tail", header: "bytes=100-", size: 1000, wantStart: 100, wantEnd: 999, wantOK: true},
		{name: "fixed", header: "bytes=100-199", size: 1000, wantStart: 100, wantEnd: 199, wantOK: true},
		{name: "suffix", header: "bytes=-100", size: 1000, wantStart: 900, wantEnd: 999, wantOK: true},
		{name: "multi", header: "bytes=0-1,5-6", size: 1000, wantOK: false},
		{name: "end clamped to size-1", header: "bytes=100-2000", size: 1000, wantStart: 100, wantEnd: 999, wantOK: true},
		{name: "start at last byte", header: "bytes=999-999", size: 1000, wantStart: 999, wantEnd: 999, wantOK: true},
		{name: "start past end → reject", header: "bytes=1000-", size: 1000, wantOK: false},
		{name: "start past end closed → reject", header: "bytes=1000-2000", size: 1000, wantOK: false},
		{name: "suffix larger than size", header: "bytes=-2000", size: 1000, wantStart: 0, wantEnd: 999, wantOK: true},
		{name: "end < start", header: "bytes=500-100", size: 1000, wantOK: false},
		{name: "missing prefix", header: "100-200", size: 1000, wantOK: false},
		{name: "malformed", header: "bytes=abc-def", size: 1000, wantOK: false},
		{name: "empty bytes=", header: "bytes=", size: 1000, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, ok := parseRange(tt.header, tt.size)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if start != tt.wantStart || end != tt.wantEnd {
				t.Fatalf("range = %d-%d, want %d-%d", start, end, tt.wantStart, tt.wantEnd)
			}
		})
	}
}
