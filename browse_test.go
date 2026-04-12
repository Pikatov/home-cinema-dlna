package main

import "testing"

func TestAverageBitrateMbps(t *testing.T) {
	got := averageBitrateMbps(90*1024*1024, 30)
	want := 25.165824
	if got < want-0.001 || got > want+0.001 {
		t.Fatalf("averageBitrateMbps = %f, want about %f", got, want)
	}
}

func TestShouldPreferTVResource(t *testing.T) {
	prevEnabled := tvStreamEnabled
	prevFirst := tvStreamFirst
	prevAuto := tvAutoFirst
	prevMbps := tvAutoFirstMbps
	defer func() {
		tvStreamEnabled = prevEnabled
		tvStreamFirst = prevFirst
		tvAutoFirst = prevAuto
		tvAutoFirstMbps = prevMbps
	}()

	tvStreamEnabled = true
	tvStreamFirst = false
	tvAutoFirst = true
	tvAutoFirstMbps = 18

	if !shouldPreferTVResource("movie.mkv", 9*1024*1024*1024, 3600) {
		t.Fatal("expected TV resource to be preferred for a high bitrate file")
	}
	if shouldPreferTVResource("movie.mkv", 2*1024*1024*1024, 3600) {
		t.Fatal("did not expect TV resource to be preferred for a lower bitrate file")
	}

	tvAutoFirst = false
	if shouldPreferTVResource("movie.mkv", 9*1024*1024*1024, 3600) {
		t.Fatal("did not expect TV resource to be preferred when auto-first is disabled")
	}

	tvStreamFirst = true
	if !shouldPreferTVResource("movie.mkv", 2*1024*1024*1024, 3600) {
		t.Fatal("expected explicit tv-stream-first to override bitrate heuristic")
	}
}
