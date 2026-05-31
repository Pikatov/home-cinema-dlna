package main

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
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
	msg := fmt.Sprintf(format, args...)
	lastLogMu.Lock()
	defer lastLogMu.Unlock()
	if msg == lastLogMsg && time.Since(lastLogAt) < 2*time.Second {
		return
	}
	lastLogMsg = msg
	lastLogAt = time.Now()
	log.Print(msg)
}

// generateUUIDv4 returns a random UUID v4 string.
func generateUUIDv4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t := uint64(time.Now().UnixNano())
		for i := range b {
			b[i] = byte(t >> ((i % 8) * 8))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// loadOrCreateUUID reads the persisted UUID from dir, or generates and saves a new one.
func loadOrCreateUUID(dir string) string {
	path := filepath.Join(dir, "server_uuid")
	if data, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(data)); isValidUUID(id) {
			return id
		}
	}
	id := generateUUIDv4()
	_ = os.WriteFile(path, []byte(id+"\n"), 0600)
	return id
}

func isValidUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	dashes := [4]int{8, 13, 18, 23}
	di := 0
	for i := 0; i < 36; i++ {
		if di < 4 && i == dashes[di] {
			if s[i] != '-' {
				return false
			}
			di++
			continue
		}
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
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
	invalidateBrowseCacheWithNotify(true)
}

func invalidateBrowseCacheQuiet() {
	invalidateBrowseCacheWithNotify(false)
}

func invalidateBrowseCacheWithNotify(notify bool) {
	browseCacheMu.Lock()
	browseCache = make(map[string]browseCacheEntry)
	browseCacheMu.Unlock()
	if notify {
		scheduleBrowseNotification()
	}
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
	case ".mp4", ".mkv", ".avi", ".m4v", ".mov":
		return true
	default:
		return false
	}
}

// extractXMLTag находит первое значение в простом теге <name>value</name>
// внутри XML-строки. Поддерживает только плоский text content без вложенных
// тегов и не разбирает атрибуты — этого достаточно для SOAP-запросов
// ContentDirectory.Browse, где ObjectID/BrowseFlag всегда плоские.
// Возвращает "" если тег не найден.
func extractXMLTag(s, name string) string {
	open := "<" + name + ">"
	close := "</" + name + ">"
	i := strings.Index(s, open)
	if i < 0 {
		return ""
	}
	i += len(open)
	j := strings.Index(s[i:], close)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}

func stringsTrimDefault(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	return s
}
