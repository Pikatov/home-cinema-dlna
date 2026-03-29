package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/koron/go-ssdp"
)

const (
	defaultServerPort = "8080"
	friendlyName      = "Home Cinema"
	manufacturerName  = "Home Cinema"
	modelName         = "HomeCinemaStreamer"
	appVersion        = "1.3"
	uuid              = "673f-431d-90b6-homecinema-001"
	logFileName       = "server.log"
	browseCacheTTL    = 5 * time.Second
	burstAliveCount   = 5
	progressFileName  = "progress.json"
)

var (
	objectIDRe = regexp.MustCompile(`<ObjectID>(.*?)</ObjectID>`)
	flagRe     = regexp.MustCompile(`<BrowseFlag>(.*?)</BrowseFlag>`)
	callbackRe = regexp.MustCompile(`<([^>]+)>`)

	serverPort      = defaultServerPort
	startedAt       = time.Now()
	defaultMediaDir = resolveDefaultMediaDir()
	mediaDirMu      sync.RWMutex
	currentMediaDir = defaultMediaDir
	defaultDataDir  = resolveDefaultDataDir()
	dataDir         = defaultDataDir
	logFilePath     = filepath.Join(dataDir, logFileName)
	progressFile    = filepath.Join(dataDir, progressFileName)
	remoteControlOK = false
	browseCacheMu   sync.RWMutex
	browseCache     = make(map[string]browseCacheEntry)
)

var (
	streamDebugEnabled bool
	streamDebugHeaders bool
	streamDebugEvery   = 15 * time.Second
	streamSlowRead     = 200 * time.Millisecond
	streamSlowWrite    = 200 * time.Millisecond

	streamSeq            uint64
	activeStreamRequests int64
)

var browseUpdateID uint32

var (
	tvStreamEnabled  = true
	tvStreamFirst    = false
	tvVideoCRF       = 22
	tvVideoMaxrateMb = 10
	tvVideoBufsizeMb = 20
	tvVideoPreset    = "veryfast"
	tvAudioKbps      = 192
	tvAudioChannels  = 2
	tvContentType    = "video/mpeg"
	tvDLNAFeatures   = "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
)

var (
	warmupMetaEnabled  = true
	warmupMetaThrottle time.Duration
	warmupMetaMaxFiles int
)

type browseCacheEntry struct {
	payload string
	count   int
	expires time.Time
}

type upnpEventSub struct {
	sid      string
	callback string
	expires  time.Time
	seq      uint32
}

var (
	eventSubsMu sync.Mutex
	eventSubs   = make(map[string]*upnpEventSub)
)

func bumpBrowseUpdateID() uint32 {
	return atomic.AddUint32(&browseUpdateID, 1)
}

func currentBrowseUpdateID() uint32 {
	return atomic.LoadUint32(&browseUpdateID)
}

func newSID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// fallback: достаточно уникально для локалки
		return fmt.Sprintf("uuid:%d", time.Now().UnixNano())
	}
	return "uuid:" + hex.EncodeToString(b[:])
}

func parseTimeout(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 30 * time.Minute
	}
	upper := strings.ToUpper(h)
	if strings.HasPrefix(upper, "SECOND-") {
		n, err := strconv.Atoi(strings.TrimPrefix(upper, "SECOND-"))
		if err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Minute
}

func notifyContentDirectory(updateID uint32) {
	now := time.Now()
	type target struct {
		sid      string
		callback string
		seq      uint32
	}

	var targets []target
	eventSubsMu.Lock()
	for sid, sub := range eventSubs {
		if now.After(sub.expires) {
			delete(eventSubs, sid)
			continue
		}
		targets = append(targets, target{sid: sid, callback: sub.callback, seq: sub.seq})
		sub.seq++
	}
	eventSubsMu.Unlock()

	if len(targets) == 0 {
		return
	}

	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+"\n"+
		`<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">`+"\n"+
		`  <e:property><SystemUpdateID>%d</SystemUpdateID></e:property>`+"\n"+
		`</e:propertyset>`, updateID)

	client := &http.Client{Timeout: 2 * time.Second}
	for _, t := range targets {
		req, err := http.NewRequest("NOTIFY", t.callback, strings.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")
		req.Header.Set("NT", "upnp:event")
		req.Header.Set("NTS", "upnp:propchange")
		req.Header.Set("SID", t.sid)
		req.Header.Set("SEQ", strconv.FormatUint(uint64(t.seq), 10))
		_, _ = client.Do(req)
	}
}

func handleEventContentDirectory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "SUBSCRIBE":
			sid := strings.TrimSpace(r.Header.Get("SID"))
			timeout := parseTimeout(r.Header.Get("TIMEOUT"))
			expires := time.Now().Add(timeout)

			if sid == "" {
				cb := r.Header.Get("CALLBACK")
				m := callbackRe.FindStringSubmatch(cb)
				if len(m) < 2 {
					http.Error(w, "Missing CALLBACK", http.StatusPreconditionFailed)
					return
				}
				sid = newSID()
				sub := &upnpEventSub{
					sid:      sid,
					callback: strings.TrimSpace(m[1]),
					expires:  expires,
				}
				eventSubsMu.Lock()
				eventSubs[sid] = sub
				eventSubsMu.Unlock()

				w.Header().Set("SID", sid)
				w.Header().Set("TIMEOUT", fmt.Sprintf("Second-%d", int(timeout.Seconds())))
				w.WriteHeader(http.StatusOK)

				// initial event
				go notifyContentDirectory(currentBrowseUpdateID())
				return
			}

			// renew
			eventSubsMu.Lock()
			sub, ok := eventSubs[sid]
			if ok {
				sub.expires = expires
			}
			eventSubsMu.Unlock()
			if !ok {
				http.Error(w, "Unknown SID", http.StatusPreconditionFailed)
				return
			}

			w.Header().Set("SID", sid)
			w.Header().Set("TIMEOUT", fmt.Sprintf("Second-%d", int(timeout.Seconds())))
			w.WriteHeader(http.StatusOK)
			return

		case "UNSUBSCRIBE":
			sid := strings.TrimSpace(r.Header.Get("SID"))
			if sid == "" {
				http.Error(w, "Missing SID", http.StatusPreconditionFailed)
				return
			}
			eventSubsMu.Lock()
			delete(eventSubs, sid)
			eventSubsMu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// ── ffprobe ───────────────────────────────────────────────────────────────────

// ffprobeBin ищет ffprobe по стандартным путям — нужно для работы внутри .app
// где PATH не содержит /opt/homebrew/bin.
func ffprobeBin() string {
	for _, p := range []string{
		"/opt/homebrew/bin/ffprobe",
		"/usr/local/bin/ffprobe",
		"/usr/bin/ffprobe",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "ffprobe"
}

var ffprobeExe = ffprobeBin()

func ffmpegBin() string {
	for _, p := range []string{
		"/opt/homebrew/bin/ffmpeg",
		"/usr/local/bin/ffmpeg",
		"/usr/bin/ffmpeg",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "ffmpeg"
}

var ffmpegExe = ffmpegBin()

func isExecutable(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir() && (info.Mode()&0111) != 0
}

func resolveExec(bin string) (string, bool) {
	if bin == "" {
		return "", false
	}
	if strings.ContainsRune(bin, filepath.Separator) {
		return bin, isExecutable(bin)
	}
	p, err := exec.LookPath(bin)
	if err != nil {
		return "", false
	}
	return p, true
}

type videoMeta struct {
	DurationSeconds float64
}

var (
	metaCacheMu sync.RWMutex
	metaCache   = make(map[string]videoMeta)
)

var (
	metaWarmMu  sync.Mutex
	metaWarm    = make(map[string]bool)
	metaWarmSem = make(chan struct{}, 2)
)

func getVideoMetaCached(filePath string) (videoMeta, bool) {
	metaCacheMu.RLock()
	m, ok := metaCache[filePath]
	metaCacheMu.RUnlock()
	return m, ok
}

func warmVideoMetaAsync(filePath string) {
	if filePath == "" {
		return
	}
	if _, ok := getVideoMetaCached(filePath); ok {
		return
	}
	metaWarmMu.Lock()
	if metaWarm[filePath] {
		metaWarmMu.Unlock()
		return
	}
	metaWarm[filePath] = true
	metaWarmMu.Unlock()
	go func() {
		metaWarmSem <- struct{}{}
		defer func() { <-metaWarmSem }()
		_ = getVideoMeta(filePath)
		metaWarmMu.Lock()
		delete(metaWarm, filePath)
		metaWarmMu.Unlock()
	}()
}

func getVideoMeta(filePath string) videoMeta {
	metaCacheMu.RLock()
	if m, ok := metaCache[filePath]; ok {
		metaCacheMu.RUnlock()
		return m
	}
	metaCacheMu.RUnlock()

	var m videoMeta
	out, err := exec.Command(ffprobeExe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath,
	).Output()
	if err == nil {
		if secs, err2 := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err2 == nil && secs > 0 {
			m.DurationSeconds = secs
		}
	}

	metaCacheMu.Lock()
	metaCache[filePath] = m
	metaCacheMu.Unlock()
	return m
}

func warmupMetaCache(dir string) {
	if !warmupMetaEnabled {
		return
	}
	go func() {
		errStop := errors.New("warmup done")
		count := 0
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".mp4" || ext == ".mkv" || ext == ".avi" {
				getVideoMeta(path)
				count++
				if warmupMetaThrottle > 0 {
					time.Sleep(warmupMetaThrottle)
				}
				if warmupMetaMaxFiles > 0 && count >= warmupMetaMaxFiles {
					return errStop
				}
				if streamDebugEnabled && count%50 == 0 {
					log.Printf("DBG warmup: %d файлов (в процессе) в %s", count, redactPath(dir))
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, errStop) {
			log.Printf("⚠️ warmup ffprobe: %v", err)
		}
		log.Printf("✅ ffprobe кеш: %d файлов в %s", count, redactPath(dir))
	}()
}

// ── Прогресс просмотра ────────────────────────────────────────────────────────

type progressEntry struct {
	Position        int64     `json:"position,omitempty"` // byte offset (for /video Range)
	Size            int64     `json:"size,omitempty"`     // file size for Position validation
	Seconds         float64   `json:"seconds,omitempty"`  // elapsed seconds (for /tv realtime)
	DurationSeconds float64   `json:"durationSeconds,omitempty"`
	Updated         time.Time `json:"updated"`
}

var (
	progressMu   sync.RWMutex
	progressData = make(map[string]progressEntry)
)

var progressSaveCh = make(chan struct{}, 1)

func loadProgress() {
	data, err := os.ReadFile(progressFile)
	if err != nil {
		return
	}
	progressMu.Lock()
	defer progressMu.Unlock()
	_ = json.Unmarshal(data, &progressData)

	// Удаляем записи старше 7 дней
	cutoff := time.Now().AddDate(0, 0, -7)
	cleaned := 0
	for k, v := range progressData {
		if v.Updated.Before(cutoff) {
			delete(progressData, k)
			cleaned++
		}
	}
	if cleaned > 0 {
		log.Printf("🗑️ Очищено старых записей прогресса: %d", cleaned)
	}
	log.Printf("📖 Прогресс: %d файлов", len(progressData))
}

func saveProgress() {
	progressMu.RLock()
	snapshot := make(map[string]progressEntry, len(progressData))
	for k, v := range progressData {
		snapshot[k] = v
	}
	progressMu.RUnlock()
	data, _ := json.Marshal(snapshot)
	_ = os.WriteFile(progressFile, data, 0600)
}

func requestProgressSave() {
	select {
	case progressSaveCh <- struct{}{}:
	default:
	}
}

func runProgressSaver() {
	go func() {
		var (
			timer       *time.Timer
			timerC      <-chan time.Time
			lastWrite   time.Time
			pending     bool
			debounce    = 300 * time.Millisecond
			maxInterval = 5 * time.Second
		)
		for {
			select {
			case <-progressSaveCh:
				pending = true

				if !lastWrite.IsZero() && time.Since(lastWrite) >= maxInterval {
					saveProgress()
					lastWrite = time.Now()
					pending = false
					if timer != nil {
						if !timer.Stop() {
							select {
							case <-timer.C:
							default:
							}
						}
						timerC = nil
						timer = nil
					}
					continue
				}

				if timer == nil {
					timer = time.NewTimer(debounce)
					timerC = timer.C
				} else {
					if !timer.Stop() {
						select {
						case <-timer.C:
						default:
						}
					}
					timer.Reset(debounce)
				}
			case <-timerC:
				if pending {
					saveProgress()
					lastWrite = time.Now()
					pending = false
				}
				timerC = nil
				timer = nil
			}
		}
	}()
}

func recordProgress(filename string, position, size int64) {
	recordProgressBytes(filename, position, size)
}

func recordProgressBytes(key string, position, size int64) {
	if size <= 0 || position <= 0 || position >= size {
		return
	}
	progressMu.Lock()
	progressData[key] = progressEntry{Position: position, Size: size, Updated: time.Now()}
	progressMu.Unlock()
	requestProgressSave()
	// Сбрасываем browse-кеш чтобы таймкод обновился при следующем открытии папки
	invalidateBrowseCache()
}

func recordProgressSeconds(key string, seconds float64, durationSeconds float64) {
	// Для /tv хотим быстрый таймкод: сохраняем почти сразу, но отсечём совсем первые секунды
	// и самый конец (часто это авто-stop/выход).
	if seconds < 1 {
		return
	}
	if durationSeconds > 0 && seconds > durationSeconds-5 {
		return
	}
	progressMu.Lock()
	progressData[key] = progressEntry{Seconds: seconds, DurationSeconds: durationSeconds, Updated: time.Now()}
	progressMu.Unlock()
	requestProgressSave()
	invalidateBrowseCache()
}

func getProgress(filename string, size int64) int64 {
	progressMu.RLock()
	entry, ok := progressData[filename]
	progressMu.RUnlock()
	if !ok || entry.Size != size {
		return 0
	}
	return entry.Position
}

func getProgressEntry(key string) (progressEntry, bool) {
	progressMu.RLock()
	entry, ok := progressData[key]
	progressMu.RUnlock()
	return entry, ok
}

// formatTimecode переводит байтовую позицию в строку вида "1:23:45"
func formatTimecode(pos, size int64, durationSecs float64) string {
	if durationSecs <= 0 || size <= 0 {
		return ""
	}
	secs := durationSecs * float64(pos) / float64(size)
	h := int(secs) / 3600
	m := (int(secs) % 3600) / 60
	s := int(secs) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func formatSecondsTimecode(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	total := int(seconds + 0.5)
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func progressKeyFromRelPath(rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return ""
	}
	return filepath.ToSlash(rel)
}

func getProgressTimecode(relPath string, size int64, durationSecs float64) string {
	key := progressKeyFromRelPath(relPath)
	if key == "" {
		return ""
	}

	entry, ok := getProgressEntry(key)
	if !ok {
		// fallback на старый формат (ключ = имя файла)
		entry, ok = getProgressEntry(filepath.Base(key))
	}
	if !ok {
		return ""
	}

	if entry.Seconds > 0 {
		return formatSecondsTimecode(entry.Seconds)
	}
	if entry.Position > 0 && entry.Size == size && durationSecs > 0 {
		return formatTimecode(entry.Position, size, durationSecs)
	}
	return ""
}

func formatDLNADuration(seconds float64) string {
	if seconds <= 0 {
		return ""
	}
	totalMillis := int64(seconds * 1000)
	h := totalMillis / (3600 * 1000)
	m := (totalMillis / (60 * 1000)) % 60
	s := (totalMillis / 1000) % 60
	ms := totalMillis % 1000
	if h > 99 {
		h = 99
	}
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

func setDLNATimeSeekHeaders(w http.ResponseWriter, durationSeconds float64) {
	if durationSeconds <= 0 {
		return
	}
	dur := formatDLNADuration(durationSeconds)
	if dur == "" {
		return
	}
	w.Header().Set("Content-Duration", dur)
	w.Header().Set("TimeSeekRange.dlna.org", fmt.Sprintf("npt=00:00:00.000-%s/%s", dur, dur))
	w.Header().Set("X-Seek-Range", fmt.Sprintf("npt=0-%.0f", durationSeconds))
}

// ── Helpers ───────────────────────────────────────────────────────────────────

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

var (
	lastLogMu  sync.Mutex
	lastLogMsg string
	lastLogAt  time.Time
)

// logOnce пишет лог только если сообщение отличается от предыдущего
// или прошло больше 2 секунд — убирает дубли от параллельных соединений Samsung.
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
	// Очищаем лог при каждом запуске; если не получается (например, .app в /Applications),
	// работаем только через stdout/stderr.
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
	updateID := bumpBrowseUpdateID()
	go notifyContentDirectory(updateID)
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	mediaDirFlag := flag.String("media-dir", defaultMediaDir, "Путь к медиатеке")
	portFlag := flag.String("port", defaultServerPort, "HTTP порт сервера")
	dataDirFlag := flag.String("data-dir", defaultDataDir, "Папка для логов/прогресса (HOMECINEMA_DATA_DIR)")
	allowRemoteControlFlag := flag.Bool("allow-remote-control", false, "Разрешить менять папку медиатеки не только с localhost (НЕ рекомендуется)")
	debugStreamFlag := flag.Bool("debug-stream", false, "Подробный лог /video (Range/скорость/медленные read/write)")
	debugStreamHeadersFlag := flag.Bool("debug-stream-headers", false, "При --debug-stream логировать важные DLNA/HTTP заголовки запроса")
	debugEveryFlag := flag.Duration("debug-stream-every", 15*time.Second, "Как часто логировать прогресс стрима при --debug-stream")
	slowWriteFlag := flag.Duration("debug-slow-write", 200*time.Millisecond, "Логировать, если запись в сеть дольше этого порога (при --debug-stream)")
	slowReadFlag := flag.Duration("debug-slow-read", 200*time.Millisecond, "Логировать, если чтение файла дольше этого порога (при --debug-stream)")
	streamBufMBFlag := flag.Int("stream-buf-mb", 4, "Размер буфера выдачи (МБ), можно уменьшать при подвисаниях")
	warmupMetaFlag := flag.Bool("warmup-meta", true, "Прогреть кеш длительности (ffprobe) при старте/смене папки (может грузить диск/CPU)")
	warmupMetaThrottleFlag := flag.Duration("warmup-meta-throttle", 0, "Пауза между ffprobe вызовами при прогреве (например 150ms)")
	warmupMetaMaxFlag := flag.Int("warmup-meta-max", 0, "Максимум файлов для прогрева (0 = все)")
	tvStreamFlag := flag.Bool("tv-stream", true, "Добавить TV-версию потока (ffmpeg) как альтернативный <res> (уменьшает тормоза по Wi‑Fi)")
	tvStreamFirstFlag := flag.Bool("tv-stream-first", false, "Ставить TV-поток (ffmpeg) первым <res> (ТВ чаще выбирает первый ресурс, но может пропасть прогресс/длительность)")
	tvCRFFlag := flag.Int("tv-crf", 22, "CRF для TV-потока (выше = меньше битрейт/качество)")
	tvMaxrateFlag := flag.Int("tv-maxrate-mbps", 10, "Максимальный видеобитрейт TV-потока (Mbps)")
	tvBufsizeFlag := flag.Int("tv-bufsize-mbps", 20, "VBV bufsize для TV-потока (Mbps)")
	tvPresetFlag := flag.String("tv-preset", "veryfast", "Preset для TV-потока (ffmpeg x264)")
	tvAudioKbpsFlag := flag.Int("tv-audio-kbps", 192, "Аудиобитрейт TV-потока (kbps, AAC)")
	tvAudioChFlag := flag.Int("tv-audio-ch", 2, "Аудиоканалы TV-потока (обычно 2)")
	progressEveryFlag := flag.Duration("progress-every", 1*time.Second, "Как часто сохранять прогресс во время стрима (быстрее обновляет таймкод в названии)")
	flag.Parse()

	serverPort = *portFlag
	setMediaDir(*mediaDirFlag)
	setDataDir(*dataDirFlag)
	remoteControlOK = *allowRemoteControlFlag
	streamDebugEnabled = *debugStreamFlag
	streamDebugHeaders = *debugStreamHeadersFlag
	streamDebugEvery = *debugEveryFlag
	streamSlowWrite = *slowWriteFlag
	streamSlowRead = *slowReadFlag
	warmupMetaEnabled = *warmupMetaFlag
	warmupMetaThrottle = *warmupMetaThrottleFlag
	warmupMetaMaxFiles = *warmupMetaMaxFlag
	if *streamBufMBFlag < 1 {
		*streamBufMBFlag = 1
	}
	if *streamBufMBFlag > 16 {
		*streamBufMBFlag = 16
	}
	streamBufSize = *streamBufMBFlag * 1024 * 1024
	streamBufPool = sync.Pool{
		New: func() interface{} {
			return make([]byte, streamBufSize)
		},
	}

	tvStreamEnabled = *tvStreamFlag
	tvStreamFirst = *tvStreamFirstFlag
	tvVideoCRF = *tvCRFFlag
	tvVideoMaxrateMb = *tvMaxrateFlag
	tvVideoBufsizeMb = *tvBufsizeFlag
	tvVideoPreset = strings.TrimSpace(*tvPresetFlag)
	tvAudioKbps = *tvAudioKbpsFlag
	tvAudioChannels = *tvAudioChFlag
	if tvStreamEnabled {
		if p, ok := resolveExec(ffmpegExe); ok {
			ffmpegExe = p
		} else {
			log.Printf("⚠️ ffmpeg не найден. Отключаю --tv-stream (можно включить после установки ffmpeg).")
			tvStreamEnabled = false
		}
	}
	if tvVideoMaxrateMb < 2 {
		tvVideoMaxrateMb = 2
	}
	if tvVideoBufsizeMb < tvVideoMaxrateMb {
		tvVideoBufsizeMb = tvVideoMaxrateMb * 2
	}
	if tvVideoCRF < 16 {
		tvVideoCRF = 16
	}
	if tvVideoCRF > 30 {
		tvVideoCRF = 30
	}
	if tvAudioKbps < 64 {
		tvAudioKbps = 64
	}
	if tvAudioKbps > 384 {
		tvAudioKbps = 384
	}
	if tvAudioChannels < 1 {
		tvAudioChannels = 1
	}
	if tvAudioChannels > 6 {
		tvAudioChannels = 6
	}
	if *progressEveryFlag < 250*time.Millisecond {
		*progressEveryFlag = 250 * time.Millisecond
	}
	progressUpdateEvery = *progressEveryFlag

	initLogger()
	loadProgress()
	runProgressSaver()
	invalidateBrowseCache()
	warmupMetaCache(getMediaDir())

	ip := getLocalIP()
	serverAddr := fmt.Sprintf("http://%s:%s", ip, serverPort)

	go startSSDP(ip)
	go respondMSearch(ip)

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		statusHandler(w, r, ip)
	})
	http.HandleFunc("/set-media-dir", handleFolderSelection)
	http.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		fmt.Fprintf(w, deviceDescription, friendlyName, manufacturerName, modelName, uuid)
	})
	http.HandleFunc("/ctl/ContentDirectory", handleContentDirectory(ip))
	http.HandleFunc("/evt/ContentDirectory", handleEventContentDirectory())
	http.HandleFunc("/video/", func(w http.ResponseWriter, r *http.Request) {
		relPath := strings.TrimPrefix(r.URL.Path, "/video/")
		filePath := filepath.Join(getMediaDir(), relPath)
		serveVideo(w, r, filePath, relPath)
	})
	http.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		relPath := strings.TrimPrefix(r.URL.Path, "/tv/")
		filePath := filepath.Join(getMediaDir(), relPath)
		serveTVStream(w, r, filePath, relPath)
	})

	log.Printf("📡 СЕРВЕР ЗАПУЩЕН | %s | %s", friendlyName, serverAddr)
	log.Fatal(http.ListenAndServe(":"+serverPort, nil))
}

// ── SSDP ──────────────────────────────────────────────────────────────────────

func startSSDP(ip string) {
	location := fmt.Sprintf("http://%s:%s/desc.xml", ip, serverPort)
	adverts := []struct {
		st  string
		usn string
	}{
		{"urn:schemas-upnp-org:device:MediaServer:1", "uuid:" + uuid + "::urn:schemas-upnp-org:device:MediaServer:1"},
		{"upnp:rootdevice", "uuid:" + uuid},
		{"urn:schemas-upnp-org:service:ContentDirectory:1", "uuid:" + uuid + "::urn:schemas-upnp-org:service:ContentDirectory:1"},
	}

	var ads []*ssdp.Advertiser
	for _, a := range adverts {
		ad, err := ssdp.Advertise(a.st, a.usn, location, manufacturerName, 1800, ssdp.AdvertiseHost(), ssdp.TTL(4))
		if err != nil {
			log.Printf("Ошибка SSDP (%s): %v", a.st, err)
			continue
		}
		ads = append(ads, ad)
	}
	if len(ads) == 0 {
		log.Printf("Ошибка SSDP: ни одно объявление не запущено")
		return
	}

	for i := 0; i < burstAliveCount; i++ {
		for _, ad := range ads {
			if err := ad.Alive(); err != nil {
				log.Printf("Ошибка SSDP burst: %v", err)
			}
		}
		time.Sleep(400 * time.Millisecond)
	}

	fastAnnounce := time.NewTicker(5 * time.Second)
	time.AfterFunc(1*time.Minute, func() { fastAnnounce.Stop() })
	fastTicker := time.NewTicker(15 * time.Second)
	time.AfterFunc(2*time.Minute, func() { fastTicker.Stop() })
	slowTicker := time.NewTicker(60 * time.Second)
	defer slowTicker.Stop()
	defer fastTicker.Stop()

	for {
		select {
		case <-fastAnnounce.C:
			for _, ad := range ads {
				_ = ad.Alive()
			}
		case <-fastTicker.C:
			for _, ad := range ads {
				_ = ad.Alive()
			}
		case <-slowTicker.C:
			for _, ad := range ads {
				if err := ad.Alive(); err != nil {
					log.Printf("Ошибка SSDP: не удалось обновить анонс: %v", err)
				}
			}
		}
	}
}

func respondMSearch(ip string) {
	addr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		log.Printf("SSDP resolve error: %v", err)
		return
	}
	iface := primaryInterface()
	conn, err := net.ListenMulticastUDP("udp4", iface, addr)
	if err != nil {
		conn, err = net.ListenMulticastUDP("udp4", nil, addr)
	}
	if err != nil {
		log.Printf("SSDP listen error: %v", err)
		return
	}
	if err := conn.SetReadBuffer(64 * 1024); err != nil {
		log.Printf("SSDP set buffer error: %v", err)
	}

	buf := make([]byte, 2048)
	location := fmt.Sprintf("http://%s:%s/desc.xml", ip, serverPort)

	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("SSDP read error: %v", err)
			continue
		}
		data := strings.ToUpper(string(buf[:n]))
		if !strings.Contains(data, "M-SEARCH") || !strings.Contains(data, "ST:") {
			continue
		}

		st := "urn:schemas-upnp-org:device:MediaServer:1"
		if strings.Contains(data, "ST: SSDP:ALL") {
			st = "ssdp:all"
		} else if strings.Contains(data, "CONTENTDIRECTORY") {
			st = "urn:schemas-upnp-org:service:ContentDirectory:1"
		}

		res := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
			"CACHE-CONTROL: max-age=1800\r\n"+
			"DATE: %s\r\n"+
			"EXT:\r\n"+
			"LOCATION: %s\r\n"+
			"SERVER: MacOS/13.0 UPnP/1.0 DLNADOC/1.50 HomeCinema/%s\r\n"+
			"ST: %s\r\n"+
			"USN: uuid:%s::%s\r\n"+
			"\r\n", time.Now().UTC().Format(time.RFC1123), location, appVersion, st, uuid, st)

		if _, err := conn.WriteToUDP([]byte(res), src); err != nil {
			log.Printf("SSDP write error: %v", err)
		} else {
			log.Printf("📣 M-SEARCH ответ: %s -> %s", st, src)
		}
	}
}

func primaryInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return &iface
			}
		}
	}
	return nil
}

// ── Content Directory ─────────────────────────────────────────────────────────

func handleContentDirectory(ip string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)

		objIDMatch := objectIDRe.FindStringSubmatch(bodyStr)
		flagMatch := flagRe.FindStringSubmatch(bodyStr)

		objID := "0"
		if len(objIDMatch) > 1 {
			objID = objIDMatch[1]
		}
		flag := ""
		if len(flagMatch) > 1 {
			flag = flagMatch[1]
		}

		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.Header().Set("Server", fmt.Sprintf("Linux/2.6 UPnP/1.0 DLNADOC/1.50 HomeCinema/%s", appVersion))

		if flag == "BrowseMetadata" {
			logOnce("⚙️ МЕТАДАННЫЕ: ID=%s", objID)
			meta := fmt.Sprintf(`&lt;container id="%s" parentID="-1" restricted="1"&gt;&lt;dc:title&gt;Folder&lt;/dc:title&gt;&lt;upnp:class&gt;object.container.storageFolder&lt;/upnp:class&gt;&lt;/container&gt;`, objID)
			fmt.Fprintf(w, soapResponse, meta, 1, 1, currentBrowseUpdateID())
			return
		}

		if flag == "BrowseDirectChildren" {
			relPath := ""
			if objID != "0" && !strings.HasPrefix(objID, "vid-") {
				b, err := base64.RawURLEncoding.DecodeString(objID)
				if err == nil {
					relPath = string(b)
				}
			}

			if payload, count, ok := getBrowseCache(relPath); ok {
				logOnce("⚡️ КЕШ: /%s (%d)", relPath, count)
				fmt.Fprintf(w, soapResponse, payload, count, count, currentBrowseUpdateID())
				return
			}

			dir := getMediaDir()
			logOnce("📂 ПАПКА: /%s", relPath)

			files, err := os.ReadDir(filepath.Join(dir, relPath))
			if err != nil {
				log.Printf("❌ ОШИБКА ЧТЕНИЯ ПАПКИ: %v", err)
				fmt.Fprintf(w, soapResponse, "", 0, 0, currentBrowseUpdateID())
				return
			}

			var items []string
			count := 0

			for _, f := range files {
				if strings.HasPrefix(f.Name(), ".") {
					continue
				}

				childRelPath := filepath.Join(relPath, f.Name())
				displayTitle := f.Name()
				if strings.HasSuffix(strings.ToLower(displayTitle), ".mp4.mp4") {
					displayTitle = displayTitle[:len(displayTitle)-4]
				}
				displayTitle = strings.ReplaceAll(displayTitle, "&", "&amp;")

				if f.IsDir() {
					childID := base64.RawURLEncoding.EncodeToString([]byte(childRelPath))
					item := fmt.Sprintf(`&lt;container id="%s" parentID="%s" restricted="1"&gt;&lt;dc:title&gt;%s&lt;/dc:title&gt;&lt;upnp:class&gt;object.container.storageFolder&lt;/upnp:class&gt;&lt;/container&gt;`,
						childID, objID, displayTitle)
					items = append(items, item)
					count++
				} else {
					ext := strings.ToLower(filepath.Ext(f.Name()))
					if ext != ".mp4" && ext != ".mkv" && ext != ".avi" {
						continue
					}

					info, _ := f.Info()
					fileHash := crc32.ChecksumIEEE([]byte(childRelPath))
					stableID := fmt.Sprintf("%d", fileHash)

					parts := strings.Split(childRelPath, string(filepath.Separator))
					for i, p := range parts {
						parts[i] = url.PathEscape(p)
					}
					escapedRel := strings.Join(parts, "/")
					fileURL := fmt.Sprintf("http://%s:%s/video/%s", ip, serverPort, escapedRel)
					tvURL := fmt.Sprintf("http://%s:%s/tv/%s", ip, serverPort, escapedRel)

					// Добавляем таймкод в название если есть прогресс
					title := displayTitle
					fullPath := filepath.Join(dir, childRelPath)
					mimeType, dlnaProfile := detectContentType(fullPath)
					proto := fmt.Sprintf("http-get:*:%s:%s", mimeType, dlnaProfile)
					meta, _ := getVideoMetaCached(fullPath)
					// В Browse не запускаем ffprobe "на всякий случай" — он может грузить диск.
					// Если нужен байтовый прогресс, а duration ещё нет — прогреем асинхронно.
					key := progressKeyFromRelPath(childRelPath)
					if entry, ok := getProgressEntry(key); ok && entry.Seconds <= 0 && entry.Position > 0 && entry.Size == info.Size() && meta.DurationSeconds <= 0 {
						warmVideoMetaAsync(fullPath)
					}
					if tc := getProgressTimecode(childRelPath, info.Size(), meta.DurationSeconds); tc != "" {
						title = fmt.Sprintf("%s [▶ %s]", displayTitle, tc)
					}

					durationAttr := ""
					if meta.DurationSeconds > 0 {
						durationAttr = fmt.Sprintf(` duration="%s"`, formatDLNADuration(meta.DurationSeconds))
					}

					resParts := make([]string, 0, 2)
					if tvStreamEnabled {
						tvProto := fmt.Sprintf("http-get:*:%s:%s", tvContentType, tvDLNAFeatures)
						// size неизвестен (транскод в реальном времени)
						tvRes := fmt.Sprintf(`&lt;res%s protocolInfo="%s"&gt;%s&lt;/res&gt;`, durationAttr, tvProto, tvURL)
						fileRes := fmt.Sprintf(`&lt;res size="%d"%s protocolInfo="%s"&gt;%s&lt;/res&gt;`, info.Size(), durationAttr, proto, fileURL)
						if tvStreamFirst {
							resParts = append(resParts, tvRes, fileRes)
						} else {
							resParts = append(resParts, fileRes, tvRes)
						}
					} else {
						resParts = append(resParts, fmt.Sprintf(`&lt;res size="%d"%s protocolInfo="%s"&gt;%s&lt;/res&gt;`, info.Size(), durationAttr, proto, fileURL))
					}

					item := fmt.Sprintf(`&lt;item id="vid-%s" parentID="%s" restricted="1"&gt;`+
						`&lt;dc:title&gt;%s&lt;/dc:title&gt;`+
						`&lt;upnp:class&gt;object.item.videoItem&lt;/upnp:class&gt;`+
						`%s`+
						`&lt;/item&gt;`, stableID, objID, title, strings.Join(resParts, ""))

					items = append(items, item)
					count++
				}
			}
			payload := strings.Join(items, "")
			setBrowseCache(relPath, payload, count)
			fmt.Fprintf(w, soapResponse, payload, count, count, currentBrowseUpdateID())
			return
		}
	}
}

func getBrowseCache(key string) (string, int, bool) {
	browseCacheMu.RLock()
	entry, ok := browseCache[key]
	browseCacheMu.RUnlock()
	if !ok {
		return "", 0, false
	}
	if time.Now().After(entry.expires) {
		browseCacheMu.Lock()
		delete(browseCache, key)
		browseCacheMu.Unlock()
		return "", 0, false
	}
	return entry.payload, entry.count, true
}

func setBrowseCache(key, payload string, count int) {
	browseCacheMu.Lock()
	browseCache[key] = browseCacheEntry{
		payload: payload,
		count:   count,
		expires: time.Now().Add(browseCacheTTL),
	}
	browseCacheMu.Unlock()
}

// ── Status & folder ───────────────────────────────────────────────────────────

type statusPayload struct {
	Name      string `json:"name"`
	MediaDir  string `json:"mediaDir,omitempty"`
	MediaName string `json:"mediaDirName"`
	Endpoint  string `json:"endpoint"`
	StartedAt string `json:"startedAt"`
}

func statusHandler(w http.ResponseWriter, r *http.Request, ip string) {
	mediaDir := getMediaDir()
	payload := statusPayload{
		Name:      friendlyName,
		MediaName: filepath.Base(mediaDir),
		Endpoint:  fmt.Sprintf("http://%s:%s", ip, serverPort),
		StartedAt: startedAt.Format(time.RFC3339),
	}
	if isLocalRequest(r) {
		payload.MediaDir = mediaDir
	}
	respondJSON(w, http.StatusOK, payload)
}

func handleFolderSelection(w http.ResponseWriter, r *http.Request) {
	if !remoteControlOK && !isLocalRequest(r) {
		respondJSON(w, http.StatusForbidden, map[string]string{
			"status":  "error",
			"message": "Доступ запрещён (только localhost). Запустите с --allow-remote-control если уверены в сети.",
		})
		return
	}

	var candidate string
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err == nil {
			candidate = r.FormValue("mediaDir")
		}
	} else {
		candidate = r.URL.Query().Get("dir")
	}
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"status":  "error",
			"message": "Введите путь к папке.",
		})
		return
	}
	info, err := os.Stat(candidate)
	if err != nil || !info.IsDir() {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"status":  "error",
			"message": fmt.Sprintf("Папка %s недоступна.", redactPath(candidate)),
		})
		return
	}

	if abs, err := filepath.Abs(candidate); err == nil {
		candidate = abs
	}
	setMediaDir(candidate)
	invalidateBrowseCache()
	warmupMetaCache(candidate)

	resp := map[string]string{
		"status":  "success",
		"message": fmt.Sprintf("Папка %s активна.", redactPath(candidate)),
	}
	if isLocalRequest(r) {
		resp["mediaDir"] = candidate
	}
	log.Printf("📁 Папка установлена: %s", redactPath(candidate))
	respondJSON(w, http.StatusOK, resp)
}

func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("Ошибка формата JSON: %v", err)
	}
}

// ── Video streaming ───────────────────────────────────────────────────────────

func serveVideo(w http.ResponseWriter, r *http.Request, filePath, requestedRelPath string) {
	reqID := atomic.AddUint64(&streamSeq, 1)
	active := atomic.AddInt64(&activeStreamRequests, 1)
	defer atomic.AddInt64(&activeStreamRequests, -1)

	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("❌ ОШИБКА: Файл не найден: %s", requestedRelPath)
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		http.Error(w, "Не удалось прочитать файл", http.StatusInternalServerError)
		return
	}

	ctype, dlnaProfile := detectContentType(filePath)
	meta, metaOK := getVideoMetaCached(filePath)
	if !metaOK || meta.DurationSeconds <= 0 {
		meta = getVideoMeta(filePath)
	}

	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("TransferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", dlnaProfile)
	w.Header().Set("ContentFeatures.dlna.org", dlnaProfile)
	setDLNATimeSeekHeaders(w, meta.DurationSeconds)
	w.Header().Set("Cache-Control", "no-transform")

	rangeHdr := r.Header.Get("Range")
	if rangeHdr == "" {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		log.Printf("▶️ СТРИМИНГ: %s (без Range)", info.Name())
		if streamDebugEnabled {
			extra := ""
			if streamDebugHeaders {
				extra = fmt.Sprintf(" hdr{transfer=%q getfeat=%q timeseek=%q conn=%q}",
					r.Header.Get("transferMode.dlna.org"),
					r.Header.Get("GetContentFeatures.dlna.org"),
					r.Header.Get("TimeSeekRange.dlna.org"),
					r.Header.Get("Connection"),
				)
			}
			log.Printf("DBG stream#%d active=%d %s %s ua=%q file=%q size=%d%s", reqID, active, r.Method, r.RemoteAddr, shortUA(r.UserAgent()), requestedRelPath, info.Size(), extra)
		}
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		streamFile(w, r, file, reqID, requestedRelPath, progressKeyFromRelPath(requestedRelPath), 0, info.Size()-1, info.Size())
		return
	}

	start, end, ok := parseRange(rangeHdr, info.Size())
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size()))
		if streamDebugEnabled {
			extra := ""
			if streamDebugHeaders {
				extra = fmt.Sprintf(" hdr{transfer=%q getfeat=%q timeseek=%q conn=%q}",
					r.Header.Get("transferMode.dlna.org"),
					r.Header.Get("GetContentFeatures.dlna.org"),
					r.Header.Get("TimeSeekRange.dlna.org"),
					r.Header.Get("Connection"),
				)
			}
			log.Printf("DBG stream#%d active=%d %s %s ua=%q file=%q bad_range=%q size=%d%s", reqID, active, r.Method, r.RemoteAddr, shortUA(r.UserAgent()), requestedRelPath, rangeHdr, info.Size(), extra)
		}
		http.Error(w, "Неверный диапазон", http.StatusRequestedRangeNotSatisfiable)
		return
	}

	length := end - start + 1
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, info.Size()))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusPartialContent)

	if _, err := file.Seek(start, io.SeekStart); err != nil {
		http.Error(w, "Не удалось перемотать", http.StatusInternalServerError)
		return
	}
	logOnce("⏩ RANGE: %s [%d-%d]", info.Name(), start, end)
	if streamDebugEnabled {
		extra := ""
		if streamDebugHeaders {
			extra = fmt.Sprintf(" hdr{transfer=%q getfeat=%q timeseek=%q conn=%q}",
				r.Header.Get("transferMode.dlna.org"),
				r.Header.Get("GetContentFeatures.dlna.org"),
				r.Header.Get("TimeSeekRange.dlna.org"),
				r.Header.Get("Connection"),
			)
		}
		log.Printf("DBG stream#%d active=%d %s %s ua=%q file=%q range=%d-%d len=%d total=%d%s", reqID, active, r.Method, r.RemoteAddr, shortUA(r.UserAgent()), requestedRelPath, start, end, length, info.Size(), extra)
	}
	if r.Method == http.MethodHead {
		return
	}
	streamFile(w, r, file, reqID, requestedRelPath, progressKeyFromRelPath(requestedRelPath), start, end, info.Size())
}

var streamBufSize = 4 * 1024 * 1024

var streamBufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, streamBufSize)
	},
}

var progressUpdateEvery = 3 * time.Second

func streamFile(w http.ResponseWriter, r *http.Request, file *os.File, reqID uint64, requestedRelPath, progressKey string, start, end, totalSize int64) {
	length := end - start + 1

	// Probe-соединения не пишем в прогресс:
	// маленький range (< 1MB) или конец файла
	isProbe := length < 1024*1024 || start > totalSize*9/10

	buf := streamBufPool.Get().([]byte)
	defer streamBufPool.Put(buf)
	remaining := length
	written := start
	flusher, _ := w.(http.Flusher)
	lastSaved := time.Now()
	started := time.Now()
	lastDbg := started
	endReason := "unknown"
	var endErr error
	var (
		chunkCount    int64
		minChunkMbps  = math.Inf(1)
		maxChunkMbps  float64
		lastChunkMbps float64
	)

	defer func() {
		if streamDebugEnabled {
			dur := time.Since(started)
			sent := written - start
			mbps := 0.0
			if dur > 0 {
				mbps = (float64(sent) * 8) / dur.Seconds() / 1e6
			}
			errStr := ""
			if endErr != nil {
				errStr = fmt.Sprintf(" err=%q", endErr.Error())
			}
			minStr := "n/a"
			if chunkCount > 0 && minChunkMbps != math.Inf(1) {
				minStr = fmt.Sprintf("%.2f", minChunkMbps)
			}
			log.Printf("DBG stream#%d done file=%q sent=%d dur=%s avg_mbps=%.2f reason=%s%s", reqID, requestedRelPath, sent, dur.Round(time.Millisecond), mbps, endReason, errStr)
			log.Printf("DBG stream#%d stats file=%q chunks=%d min_chunk_mbps=%s max_chunk_mbps=%.2f buf=%d", reqID, requestedRelPath, chunkCount, minStr, maxChunkMbps, len(buf))
		}
	}()

	for remaining > 0 {
		chunkSize := int64(len(buf))
		if written == start && chunkSize > 256*1024 {
			// Первый чанк делаем маленьким: многие ТВ/клиенты делают probe-запросы и
			// быстро закрывают соединение — не хотим зря читать 4MB с диска.
			chunkSize = 256 * 1024
		}
		if remaining < chunkSize {
			chunkSize = remaining
		}
		readStart := time.Now()
		n, err := file.Read(buf[:chunkSize])
		readDur := time.Since(readStart)
		if streamDebugEnabled && readDur > streamSlowRead {
			log.Printf("DBG stream#%d slow_read=%s file=%q off=%d want=%d got=%d", reqID, readDur.Round(time.Millisecond), requestedRelPath, written, chunkSize, n)
		}
		if n > 0 {
			writeStart := time.Now()
			_, wErr := w.Write(buf[:n])
			writeDur := time.Since(writeStart)
			chunkMbps := 0.0
			if writeDur > 0 {
				chunkMbps = (float64(n) * 8) / writeDur.Seconds() / 1e6
			}
			lastChunkMbps = chunkMbps
			chunkCount++
			if chunkMbps > 0 && chunkMbps < minChunkMbps {
				minChunkMbps = chunkMbps
			}
			if chunkMbps > maxChunkMbps {
				maxChunkMbps = chunkMbps
			}
			if streamDebugEnabled && writeDur > streamSlowWrite {
				curSent := written - start
				curDur := time.Since(started)
				curMbps := 0.0
				if curDur > 0 {
					curMbps = (float64(curSent) * 8) / curDur.Seconds() / 1e6
				}
				log.Printf("DBG stream#%d slow_write=%s file=%q off=%d n=%d rem=%d chunk_mbps=%.2f avg_mbps=%.2f", reqID, writeDur.Round(time.Millisecond), requestedRelPath, written, n, remaining, chunkMbps, curMbps)
			}
			if wErr != nil {
				if isClientClosed(wErr) {
					endReason = "client_closed"
				} else {
					endReason = "write_error"
				}
				endErr = wErr
				if !isClientClosed(wErr) {
					log.Printf("❌ Ошибка выдачи: %v", wErr)
				}
				if !isProbe {
					recordProgressBytes(progressKey, written, totalSize)
				}
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			written += int64(n)
			remaining -= int64(n)

			if !isProbe && time.Since(lastSaved) > progressUpdateEvery {
				recordProgressBytes(progressKey, written, totalSize)
				log.Printf("💾 %s → %.0f%%", filepath.Base(requestedRelPath), float64(written)/float64(totalSize)*100)
				lastSaved = time.Now()
			}

			if streamDebugEnabled && time.Since(lastDbg) >= streamDebugEvery {
				sent := written - start
				dur := time.Since(started)
				mbps := 0.0
				if dur > 0 {
					mbps = (float64(sent) * 8) / dur.Seconds() / 1e6
				}
				minNow := 0.0
				if minChunkMbps != math.Inf(1) {
					minNow = minChunkMbps
				}
				log.Printf("DBG stream#%d tick file=%q sent=%d rem=%d dur=%s avg_mbps=%.2f last_chunk_mbps=%.2f min_chunk_mbps=%.2f max_chunk_mbps=%.2f",
					reqID, requestedRelPath, sent, remaining, dur.Round(time.Millisecond), mbps, lastChunkMbps, minNow, maxChunkMbps)
				lastDbg = time.Now()
			}
		}
		if err != nil {
			if err != io.EOF && !isClientClosed(err) {
				endReason = "read_error"
				endErr = err
				log.Printf("❌ Ошибка чтения: %v", err)
				return
			}
			if err == io.EOF {
				endReason = "eof"
				endErr = err
			}
			return
		}

		select {
		case <-r.Context().Done():
			endReason = "client_cancel"
			endErr = r.Context().Err()
			if !isProbe {
				recordProgressBytes(progressKey, written, totalSize)
				log.Printf("💾 СТОП: %s → %.0f%%", filepath.Base(requestedRelPath), float64(written)/float64(totalSize)*100)
			}
			return
		default:
		}
	}

	endReason = "finished"
}

func isClientClosed(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "reset by peer") ||
		strings.Contains(s, "socket is not connected")
}

func shortUA(ua string) string {
	ua = strings.TrimSpace(ua)
	if ua == "" {
		return ""
	}
	const max = 120
	if len(ua) <= max {
		return ua
	}
	return ua[:max] + "…"
}

func detectContentType(path string) (string, string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mp4", ".m4v":
		return "video/mp4", "DLNA.ORG_PN=AVC_MP4_HP_HD_24;DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	case ".mkv":
		return "video/x-matroska", "DLNA.ORG_PN=MATROSKA;DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	case ".avi":
		return "video/x-msvideo", "DLNA.ORG_PN=AVI;DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	default:
		if m := mime.TypeByExtension(ext); m != "" {
			return m, "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
		}
		return "application/octet-stream", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	}
}

type flushWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (fw flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if n > 0 && fw.f != nil {
		fw.f.Flush()
	}
	return n, err
}

func serveTVStream(w http.ResponseWriter, r *http.Request, filePath, requestedRelPath string) {
	if !tvStreamEnabled {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		log.Printf("❌ ОШИБКА: Файл не найден: %s", requestedRelPath)
		http.NotFound(w, r)
		return
	}

	meta, metaOK := getVideoMetaCached(filePath)
	if !metaOK {
		// не задерживаем старт /tv ожиданием ffprobe
		warmVideoMetaAsync(filePath)
	}
	progressKey := progressKeyFromRelPath(requestedRelPath)

	w.Header().Set("Content-Type", tvContentType)
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("TransferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", tvDLNAFeatures)
	w.Header().Set("ContentFeatures.dlna.org", tvDLNAFeatures)
	w.Header().Set("Cache-Control", "no-transform")

	// Range для live-транскода не поддерживаем.
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Header.Get("Range") != "" {
		// Некоторые ТВ пытаются Range даже для live-ресурса — игнорируем и отдаём 200.
		logOnce("⚠️ TV stream: игнор Range для %s", requestedRelPath)
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		// Ускоряем старт (меньше probe/analysis на входе).
		"-analyzeduration", "200000",
		"-probesize", "65536",
		"-i", filePath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-sn",
		"-c:v", "libx264",
		"-preset", tvVideoPreset,
		"-tune", "zerolatency",
		"-g", "48",
		"-keyint_min", "48",
		"-sc_threshold", "0",
		"-bf", "0",
		"-pix_fmt", "yuv420p",
		"-crf", strconv.Itoa(tvVideoCRF),
		"-maxrate", fmt.Sprintf("%dM", tvVideoMaxrateMb),
		"-bufsize", fmt.Sprintf("%dM", tvVideoBufsizeMb),
		"-c:a", "aac",
		"-b:a", fmt.Sprintf("%dk", tvAudioKbps),
		"-ac", strconv.Itoa(tvAudioChannels),
		"-ar", "48000",
		"-f", "mpegts",
		"-mpegts_flags", "+resend_headers",
		"-flush_packets", "1",
		"-max_interleave_delta", "0",
		"-muxdelay", "0",
		"-muxpreload", "0",
		"pipe:1",
	}

	t0 := time.Now()
	cmd := exec.CommandContext(r.Context(), ffmpegExe, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		http.Error(w, "Не удалось запустить ffmpeg", http.StatusInternalServerError)
		return
	}
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		http.Error(w, "Не удалось запустить ffmpeg", http.StatusInternalServerError)
		return
	}
	if streamDebugEnabled {
		log.Printf("DBG tv_start file=%q ffmpeg_start=%s", requestedRelPath, time.Since(t0).Round(time.Millisecond))
	}

	go func() {
		if stderr == nil {
			return
		}
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			log.Printf("ffmpeg(tv) %s: %s", filepath.Base(requestedRelPath), s.Text())
		}
	}()

	log.Printf("📺 TV stream: %s (maxrate=%dM buf=%dM crf=%d aac=%dk ch=%d)", requestedRelPath, tvVideoMaxrateMb, tvVideoBufsizeMb, tvVideoCRF, tvAudioKbps, tvAudioChannels)

	flusher, _ := w.(http.Flusher)
	out := io.Writer(w)
	if flusher != nil {
		out = flushWriter{w: w, f: flusher}
	}

	// Обновление прогресса по времени (приблизительно).
	started := time.Now()
	lastSaved := time.Now()
	firstByteLogged := false
	copyBuf := make([]byte, 64*1024)
	for {
		n, rErr := stdout.Read(copyBuf)
		if n > 0 {
			if !firstByteLogged {
				firstByteLogged = true
				if streamDebugEnabled {
					log.Printf("DBG tv_first_byte file=%q after=%s", requestedRelPath, time.Since(t0).Round(time.Millisecond))
				}
			}
			if _, wErr := out.Write(copyBuf[:n]); wErr != nil {
				break
			}
			if time.Since(lastSaved) >= progressUpdateEvery {
				elapsed := time.Since(started).Seconds()
				recordProgressSeconds(progressKey, elapsed, meta.DurationSeconds)
				lastSaved = time.Now()
			}
		}
		if rErr != nil {
			break
		}
		select {
		case <-r.Context().Done():
			break
		default:
		}
	}
	// финальный прогресс (если успели начать)
	elapsed := time.Since(started).Seconds()
	recordProgressSeconds(progressKey, elapsed, meta.DurationSeconds)

	_ = cmd.Wait()
}

func parseRange(h string, size int64) (start, end int64, ok bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(h, prefix) {
		return 0, 0, false
	}
	rng := strings.TrimPrefix(h, prefix)
	if strings.Contains(rng, ",") {
		return 0, 0, false
	}
	parts := strings.Split(rng, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	if parts[0] == "" {
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true
	}
	var err error
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	if parts[1] == "" {
		end = size - 1
	} else {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true
}

// ── XML ───────────────────────────────────────────────────────────────────────

const deviceDescription = `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0" xmlns:dlna="urn:schemas-dlna-org:device-1-0">
	<specVersion><major>1</major><minor>0</minor></specVersion>
	<device>
		<deviceType>urn:schemas-upnp-org:device:MediaServer:1</deviceType>
		<friendlyName>%s</friendlyName>
		<manufacturer>%s</manufacturer>
		<modelName>%s</modelName>
		<UDN>uuid:%s</UDN>
		<dlna:X_DLNADOC xmlns:dlna="urn:schemas-dlna-org:device-1-0">DMS-1.50</dlna:X_DLNADOC>
		<serviceList>
			<service>
				<serviceType>urn:schemas-upnp-org:service:ContentDirectory:1</serviceType>
				<serviceId>urn:upnp-org:serviceId:ContentDirectory</serviceId>
				<SCPDURL>/desc.xml</SCPDURL>
				<controlURL>/ctl/ContentDirectory</controlURL>
				<eventSubURL>/evt/ContentDirectory</eventSubURL>
			</service>
		</serviceList>
	</device>
</root>`

const soapResponse = `<?xml version="1.0" encoding="utf-8"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
<s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>&lt;didl-lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/"&gt;%s&lt;/didl-lite&gt;</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>%d</UpdateID></u:BrowseResponse></s:Body></s:Envelope>`
