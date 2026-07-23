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

	if !shouldPreferTVResource("movie.mkv", 9*1024*1024*1024, 3600, videoMeta{}) {
		t.Fatal("expected TV resource to be preferred for a high bitrate file")
	}
	if shouldPreferTVResource("movie.mkv", 2*1024*1024*1024, 3600, videoMeta{}) {
		t.Fatal("did not expect TV resource to be preferred for a lower bitrate file")
	}
	if !shouldPreferTVResource("Show.S01E01.1080P.10bit.HDR.h265.WEB-DLRip.mkv", 2*1024*1024*1024, 3600, videoMeta{}) {
		t.Fatal("expected TV resource to be preferred for HEVC/HDR/10-bit MKV even at a lower bitrate")
	}
	if !shouldPreferTVResource("Movie.WEB-DLRip.mkv", 2*1024*1024*1024, 0, videoMeta{}) {
		t.Fatal("expected TV resource to be preferred for a large MKV while duration is still unknown")
	}
	if shouldPreferTVResource("Clip.mkv", 100*1024*1024, 0, videoMeta{}) {
		t.Fatal("did not expect TV resource to be preferred for a small MKV while duration is unknown")
	}
	if shouldPreferTVResource("Movie.mp4", 2*1024*1024*1024, 0, videoMeta{}) {
		t.Fatal("did not expect TV resource to be preferred for MP4 solely because duration is unknown")
	}

	tvAutoFirst = false
	if shouldPreferTVResource("movie.mkv", 9*1024*1024*1024, 3600, videoMeta{}) {
		t.Fatal("did not expect TV resource to be preferred when auto-first is disabled")
	}
	if shouldPreferTVResource("Show.S01E01.1080P.10bit.HDR.h265.WEB-DLRip.mkv", 2*1024*1024*1024, 3600, videoMeta{}) {
		t.Fatal("did not expect compatibility heuristic to override disabled auto-first")
	}

	tvStreamFirst = true
	if !shouldPreferTVResource("movie.mkv", 2*1024*1024*1024, 3600, videoMeta{}) {
		t.Fatal("expected explicit tv-stream-first to override bitrate heuristic")
	}
}

func TestShouldPreferTVResourceDefaultPrefersCompatibleTVStream(t *testing.T) {
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
	tvAutoFirstMbps = 8

	if !shouldPreferTVResource("Show.S01E01.1080P.10bit.HDR.h265.WEB-DLRip.mkv", 9*1024*1024*1024, 3600, videoMeta{}) {
		t.Fatal("default resource order should prefer the compatible TV stream for heavy HEVC/HDR MKV files")
	}
}

// TestShouldTranscodeForTVCompatibilityByCodec покрывает MKV-рипы без HEVC/10bit
// меток в имени файла: раньше такие файлы уходили напрямую и не запускались
// на ТВ, чей декодер не тянет HEVC/10-бит/DTS. Реальный кодек из ffprobe
// должен форсировать TV-поток независимо от имени файла.
func TestShouldTranscodeForTVCompatibilityByCodec(t *testing.T) {
	cases := []struct {
		name string
		path string
		meta videoMeta
		want bool
	}{
		{"plain name, hevc video", "/m/Movie.2020.1080p.mkv", videoMeta{CodecProbed: true, VideoCodec: "hevc"}, true},
		{"plain name, h264+dts audio", "/m/Movie.2020.1080p.mkv", videoMeta{CodecProbed: true, VideoCodec: "h264", AudioCodec: "dts"}, true},
		{"plain name, 10bit pix_fmt", "/m/Movie.2020.1080p.mkv", videoMeta{CodecProbed: true, VideoCodec: "h264", PixFmt: "yuv420p10le"}, true},
		{"plain name, fully compatible h264+ac3", "/m/Movie.2020.1080p.mkv", videoMeta{CodecProbed: true, VideoCodec: "h264", AudioCodec: "ac3"}, false},
		{"not yet probed falls back to filename", "/m/Movie.2020.1080p.h265.mkv", videoMeta{}, true},
		{"not yet probed, no hint in filename", "/m/Movie.2020.1080p.mkv", videoMeta{}, false},
		{"mp4 never forces TV transcode", "/m/Movie.mp4", videoMeta{CodecProbed: true, VideoCodec: "hevc"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shouldTranscodeForTVCompatibility(c.path, c.meta); got != c.want {
				t.Fatalf("shouldTranscodeForTVCompatibility(%q, %+v) = %v, want %v", c.path, c.meta, got, c.want)
			}
		})
	}
}
