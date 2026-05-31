package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResumeRemuxContainer(t *testing.T) {
	cases := []struct {
		name        string
		path        string
		wantFormat  string
		wantContent string
		wantOK      bool
	}{
		{"mp4 lowercase", "/m/Movie.mp4", "mp4", "video/mp4", true},
		{"mp4 uppercase ext", "/m/Movie.MP4", "mp4", "video/mp4", true},
		{"m4v", "/m/Movie.m4v", "mp4", "video/mp4", true},
		{"mov", "/m/Movie.mov", "mp4", "video/quicktime", true},
		{"mkv", "/m/Movie.mkv", "matroska", "video/x-matroska", true},
		{"avi", "/m/Movie.avi", "", "", false},
		{"unknown", "/m/Movie.xyz", "", "", false},
		{"no extension", "/m/Movie", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, ct, _, _, ok := resumeRemuxContainer(c.path)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v", ok, c.wantOK)
			}
			if f != c.wantFormat {
				t.Fatalf("format = %q, want %q", f, c.wantFormat)
			}
			if ct != c.wantContent {
				t.Fatalf("contentType = %q, want %q", ct, c.wantContent)
			}
		})
	}
}

func TestResumeSeekSeconds(t *testing.T) {
	cases := []struct {
		name        string
		entry       progressEntry
		fileSize    int64
		duration    float64
		wantSeconds float64
	}{
		{
			name:        "prefer seconds",
			entry:       progressEntry{Seconds: 123.4, Position: 999, Size: 1000},
			fileSize:    1000,
			duration:    300,
			wantSeconds: 123.4,
		},
		{
			name:        "fallback bytes when size matches",
			entry:       progressEntry{Position: 500, Size: 1000},
			fileSize:    1000,
			duration:    100,
			wantSeconds: 50,
		},
		{
			name:        "size mismatch → zero",
			entry:       progressEntry{Position: 500, Size: 1000},
			fileSize:    2000,
			duration:    100,
			wantSeconds: 0,
		},
		{
			name:        "no duration → zero",
			entry:       progressEntry{Position: 500, Size: 1000},
			fileSize:    1000,
			duration:    0,
			wantSeconds: 0,
		},
		{
			name:        "empty entry → zero",
			entry:       progressEntry{},
			fileSize:    1000,
			duration:    100,
			wantSeconds: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resumeSeekSeconds(c.entry, c.fileSize, c.duration)
			if got != c.wantSeconds {
				t.Fatalf("seconds = %v, want %v", got, c.wantSeconds)
			}
		})
	}
}

func TestResumeStartByte(t *testing.T) {
	cases := []struct {
		name     string
		entry    progressEntry
		fileSize int64
		duration float64
		want     int64
	}{
		{
			name:     "prefer byte position when size matches",
			entry:    progressEntry{Position: 500, Size: 1000, Seconds: 10},
			fileSize: 1000,
			duration: 100,
			want:     500,
		},
		{
			name:     "seconds convert to byte position",
			entry:    progressEntry{Seconds: 25},
			fileSize: 1000,
			duration: 100,
			want:     250,
		},
		{
			name:     "size mismatch rejects byte position",
			entry:    progressEntry{Position: 500, Size: 900},
			fileSize: 1000,
			duration: 100,
			want:     0,
		},
		{
			name:     "seconds need duration",
			entry:    progressEntry{Seconds: 25},
			fileSize: 1000,
			duration: 0,
			want:     0,
		},
		{
			name:     "end position rejected",
			entry:    progressEntry{Position: 1000, Size: 1000},
			fileSize: 1000,
			duration: 100,
			want:     0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := resumeStartByte(c.entry, c.fileSize, c.duration)
			if got != c.want {
				t.Fatalf("start byte = %d, want %d", got, c.want)
			}
		})
	}
}

func TestResumeSeekFromRange(t *testing.T) {
	// 1 час фильма = 1 GB файл; 1 секунда ≈ 290 KiB.
	const (
		duration = 3600.0
		fileSize = int64(1) << 30
	)

	cases := []struct {
		name       string
		header     string
		curSeek    float64
		wantOK     bool
		wantAround float64 // ±5 c
	}{
		{"empty header", "", 0, false, 0},
		{"bytes=0-", "bytes=0-", 0, false, 0},
		{"not bytes prefix", "items=0-100", 0, false, 0},
		{"closed narrow probe", "bytes=100000000-100065535", 0, false, 0},
		{"closed wide range as seek", "bytes=536870912-1073741823", 0, true, 1800}, // ровно середина
		{"open seek forward", "bytes=536870912-", 0, true, 1800},
		{"open seek backward allowed", "bytes=200000000-", 1800, true, 670},            // ≈ 11 мин — назад
		{"open seek tiny forward delta rejected", "bytes=536870912-", 1799, false, 0},  // 1 с < 3 с
		{"open seek tiny backward delta rejected", "bytes=536870912-", 1801, false, 0}, // 1 с < 3 с
		{"open seek small but sufficient delta", "bytes=536870912-", 1795, true, 1800}, // 5 с >= 3
		{"zero start byte", "bytes=0-1000", 0, false, 0},
		{"malformed range", "bytes=abc-def", 0, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := resumeSeekFromRange(c.header, fileSize, duration, c.curSeek)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (got=%v)", ok, c.wantOK, got)
			}
			if !ok {
				return
			}
			if got < c.wantAround-5 || got > c.wantAround+5 {
				t.Fatalf("seconds = %v, want ~%v", got, c.wantAround)
			}
		})
	}
}

func TestExtractST(t *testing.T) {
	cases := []struct {
		name string
		data string
		want string
	}{
		{
			name: "standard CRLF",
			data: "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nST: urn:schemas-upnp-org:device:MediaServer:1\r\nMX: 1\r\n\r\n",
			want: "urn:schemas-upnp-org:device:MediaServer:1",
		},
		{
			name: "no ST header",
			data: "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\n\r\n",
			want: "",
		},
		{
			name: "ssdp:all",
			data: "M-SEARCH * HTTP/1.1\r\nST: ssdp:all\r\n\r\n",
			want: "ssdp:all",
		},
		{
			name: "ST with extra whitespace",
			data: "M-SEARCH * HTTP/1.1\r\nST:   upnp:rootdevice   \r\n\r\n",
			want: "upnp:rootdevice",
		},
		{
			name: "empty input",
			data: "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractST(c.data)
			if got != c.want {
				t.Fatalf("extractST = %q, want %q", got, c.want)
			}
		})
	}
}

func TestSetDLNATimeSeekHeadersWithOffset(t *testing.T) {
	cases := []struct {
		name              string
		duration          float64
		seekSeconds       float64
		wantContentDur    string
		wantTimeSeekRange string
	}{
		{
			name:              "start of file",
			duration:          3600,
			seekSeconds:       0,
			wantContentDur:    "01:00:00.000",
			wantTimeSeekRange: "npt=00:00:00.000-01:00:00.000/01:00:00.000",
		},
		{
			name:              "mid file",
			duration:          3600,
			seekSeconds:       1800,
			wantContentDur:    "01:00:00.000",
			wantTimeSeekRange: "npt=00:30:00.000-01:00:00.000/01:00:00.000",
		},
		{
			name:              "seek past end clamped",
			duration:          3600,
			seekSeconds:       9999,
			wantContentDur:    "01:00:00.000",
			wantTimeSeekRange: "npt=01:00:00.000-01:00:00.000/01:00:00.000",
		},
		{
			name:              "negative seek treated as zero",
			duration:          3600,
			seekSeconds:       -10,
			wantContentDur:    "01:00:00.000",
			wantTimeSeekRange: "npt=00:00:00.000-01:00:00.000/01:00:00.000",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			setDLNATimeSeekHeadersWithOffset(w, c.duration, c.seekSeconds)
			if got := w.Header().Get("Content-Duration"); got != c.wantContentDur {
				t.Fatalf("Content-Duration = %q, want %q", got, c.wantContentDur)
			}
			if got := w.Header().Get("X-Content-Duration"); got != "3600.000" {
				t.Fatalf("X-Content-Duration = %q, want 3600.000", got)
			}
			if got := w.Header().Get("X-AvailableSeekRange"); got != "1 npt=00:00:00.000-01:00:00.000" {
				t.Fatalf("X-AvailableSeekRange = %q, want full duration", got)
			}
			if got := w.Header().Get("TimeSeekRange.dlna.org"); got != c.wantTimeSeekRange {
				t.Fatalf("TimeSeekRange = %q, want %q", got, c.wantTimeSeekRange)
			}
			// X-Seek-Range присутствует и содержит обе позиции.
			xseek := w.Header().Get("X-Seek-Range")
			if !strings.HasPrefix(xseek, "npt=") {
				t.Fatalf("X-Seek-Range = %q, want prefix npt=", xseek)
			}
		})
	}

	t.Run("zero duration does nothing", func(t *testing.T) {
		w := httptest.NewRecorder()
		setDLNATimeSeekHeadersWithOffset(w, 0, 100)
		if w.Header().Get("Content-Duration") != "" {
			t.Fatalf("expected no headers for zero duration")
		}
		if w.Header().Get("X-Content-Duration") != "" {
			t.Fatalf("expected no x-content-duration for zero duration")
		}
		if w.Header().Get("X-AvailableSeekRange") != "" {
			t.Fatalf("expected no available seek range for zero duration")
		}
	})
}

func TestParseTimeSeekRangeStart(t *testing.T) {
	cases := []struct {
		name     string
		header   string
		duration float64
		want     float64
		wantOK   bool
	}{
		{"empty", "", 3600, 0, false},
		{"seconds", "npt=90-", 3600, 90, true},
		{"clock", "npt=00:30:00.000-", 3600, 1800, true},
		{"with end and total", "npt=00:10:00.000-01:00:00.000/01:00:00.000", 3600, 600, true},
		{"clamp past end", "npt=02:00:00.000-", 3600, 3600, true},
		{"zero duration", "npt=90-", 0, 0, false},
		{"malformed", "bytes=90-", 3600, 0, false},
		{"now unsupported", "npt=now-", 3600, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := parseTimeSeekRangeStart(c.header, c.duration)
			if ok != c.wantOK {
				t.Fatalf("ok = %v, want %v (got=%v)", ok, c.wantOK, got)
			}
			if got != c.want {
				t.Fatalf("seconds = %v, want %v", got, c.want)
			}
		})
	}
}

func TestExtractXMLTag(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		tag  string
		want string
	}{
		{"basic", "<a>hi</a>", "a", "hi"},
		{"with surrounding", "junk<ObjectID>0</ObjectID>more", "ObjectID", "0"},
		{"empty value", "<x></x>", "x", ""},
		{"missing tag", "<a>hi</a>", "b", ""},
		{"unclosed", "<a>no end", "a", ""},
		{"nested similar names not supported", "<a><a>inner</a></a>", "a", "<a>inner"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractXMLTag(c.xml, c.tag)
			if got != c.want {
				t.Fatalf("extractXMLTag = %q, want %q", got, c.want)
			}
		})
	}
}

func TestTVStreamKey(t *testing.T) {
	cases := []struct {
		name       string
		filePath   string
		remoteAddr string
		wantSuffix string
	}{
		{"ipv4 with port", "/m/a.mp4", "192.168.1.10:54321", "192.168.1.10"},
		{"ipv6 with port", "/m/a.mp4", "[fe80::1]:54321", "fe80::1"},
		{"no port (fallback)", "/m/a.mp4", "192.168.1.10", "192.168.1.10"},
		{"empty addr", "/m/a.mp4", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tvStreamKey(c.filePath, c.remoteAddr)
			if !strings.HasPrefix(got, c.filePath+"\x00") {
				t.Fatalf("key %q must start with file+NUL", got)
			}
			host := strings.TrimPrefix(got, c.filePath+"\x00")
			if host != c.wantSuffix {
				t.Fatalf("host = %q, want %q", host, c.wantSuffix)
			}
		})
	}

	t.Run("different clients get different keys", func(t *testing.T) {
		a := tvStreamKey("/m/x.mp4", "10.0.0.1:1234")
		b := tvStreamKey("/m/x.mp4", "10.0.0.2:5678")
		if a == b {
			t.Fatal("expected distinct keys for different clients")
		}
	})

	t.Run("same client different ports → same key", func(t *testing.T) {
		a := tvStreamKey("/m/x.mp4", "10.0.0.1:1234")
		b := tvStreamKey("/m/x.mp4", "10.0.0.1:9999")
		if a != b {
			t.Fatalf("expected same key for same client different ports: %q vs %q", a, b)
		}
	})
}
