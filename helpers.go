package main

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type browseCacheEntry struct {
	payload string
	count   int
	expires time.Time
}

var (
	lastLogMu  sync.Mutex
	lastLogMsg string
	lastLogAt  time.Time
)

var (
	browseNotifyMu    sync.Mutex
	browseNotifyAt    time.Time
	browseNotifyTimer *time.Timer
)

func resolveDefaultMediaDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/Users/Shared/Movies"
	}
	return filepath.Join(home, "Movies")
}

func resolveDefaultDataDir() string {
	if v := strings.TrimSpace(os.Getenv("HOMECINEMA_DATA_DIR")); v != "" {
		return v
	}
	if dir, err := os.UserConfigDir(); err == nil && dir != "" {
		return filepath.Join(dir, "HomeCinema")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".homecinema")
}

func setDataDir(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = defaultDataDir
	}
	dataDir = dir
	logFilePath = filepath.Join(dataDir, logFileName)
	progressFile = filepath.Join(dataDir, progressFileName)
	_ = os.MkdirAll(dataDir, 0700)
}

func logOnce(format string, args ...interface{}) {
	msg := sprintf(format, args...)
	lastLogMu.Lock()
	defer lastLogMu.Unlock()
	if msg == lastLogMsg && time.Since(lastLogAt) < 2*time.Second {
		return
	}
	lastLogMsg = msg
	lastLogAt = time.Now()
	log.Print(msg)
}

func getLocalIP() string {
	addrs, _ := net.InterfaceAddrs()
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return "127.0.0.1"
}

func initLogger() {
	f, err := os.OpenFile(logFilePath, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err == nil {
		multi := io.MultiWriter(os.Stdout, f)
		log.SetOutput(multi)
	}
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func redactPath(p string) string {
	p = filepath.Clean(p)
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(p, prefix) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

func getMediaDir() string {
	mediaDirMu.RLock()
	defer mediaDirMu.RUnlock()
	return currentMediaDir
}

func setMediaDir(path string) {
	mediaDirMu.Lock()
	currentMediaDir = path
	mediaDirMu.Unlock()
}

func invalidateBrowseCache() {
	browseCacheMu.Lock()
	browseCache = make(map[string]browseCacheEntry)
	browseCacheMu.Unlock()
	scheduleBrowseNotification()
}

func scheduleBrowseNotification() {
	const minInterval = 5 * time.Second

	browseNotifyMu.Lock()
	defer browseNotifyMu.Unlock()

	now := time.Now()
	if since := now.Sub(browseNotifyAt); since >= minInterval && browseNotifyTimer == nil {
		browseNotifyAt = now
		updateID := bumpBrowseUpdateID()
		go notifyContentDirectory(updateID)
		return
	}
	if browseNotifyTimer != nil {
		return
	}

	delay := minInterval - now.Sub(browseNotifyAt)
	if delay < 0 {
		delay = 0
	}
	browseNotifyTimer = time.AfterFunc(delay, func() {
		browseNotifyMu.Lock()
		browseNotifyAt = time.Now()
		browseNotifyTimer = nil
		browseNotifyMu.Unlock()

		updateID := bumpBrowseUpdateID()
		go notifyContentDirectory(updateID)
	})
}

func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("Ошибка формата JSON: %v", err)
	}
}

func safeMediaRelPath(rel string) (string, bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", true
	}
	if strings.Contains(rel, "\x00") {
		return "", false
	}
	rel = strings.TrimPrefix(rel, "/")
	rel = filepath.ToSlash(rel)
	for _, seg := range strings.Split(rel, "/") {
		if seg == ".." {
			return "", false
		}
	}

	clean := path.Clean("/" + rel)
	clean = strings.TrimPrefix(clean, "/")
	if clean == "." {
		return "", true
	}
	return clean, true
}

func safeMediaRelPathFromURL(rel string) (string, bool) {
	decoded, err := url.PathUnescape(rel)
	if err != nil {
		return "", false
	}
	return safeMediaRelPath(decoded)
}

func safeJoinUnderBase(baseDir, rel string) (string, bool) {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", false
	}

	targetAbs := baseAbs
	if rel != "" {
		targetAbs = filepath.Join(baseAbs, filepath.FromSlash(rel))
	}

	relToBase, err := filepath.Rel(baseAbs, targetAbs)
	if err != nil || relToBase == ".." || strings.HasPrefix(relToBase, ".."+string(os.PathSeparator)) {
		return "", false
	}

	baseReal := baseAbs
	if br, err := filepath.EvalSymlinks(baseAbs); err == nil {
		baseReal = br
	}
	if tr, err := filepath.EvalSymlinks(targetAbs); err == nil {
		relReal, err := filepath.Rel(baseReal, tr)
		if err != nil || relReal == ".." || strings.HasPrefix(relReal, ".."+string(os.PathSeparator)) {
			return "", false
		}
		targetAbs = tr
	}

	return targetAbs, true
}

func isVideoExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp4", ".mkv", ".avi", ".m4v":
		return true
	default:
		return false
	}
}

func stringsTrimPrefix(s, prefix string) string {
	return strings.TrimPrefix(s, prefix)
}

func stringsTrimDefault(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}
