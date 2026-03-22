package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"hash/crc32"
	"io"
	"log"
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
	"time"

	"github.com/koron/go-ssdp"
)

const (
	defaultServerPort = "8080"
	friendlyName      = "Home Cinema"
	manufacturerName  = "Home Cinema"
	modelName         = "HomeCinemaStreamer"
	uuid              = "673f-431d-90b6-homecinema-001"
	logFileName       = "server.log"
	browseCacheTTL    = 5 * time.Second
	burstAliveCount   = 5
	progressFileName  = "progress.json"
)

var (
	objectIDRe = regexp.MustCompile(`<ObjectID>(.*?)</ObjectID>`)
	flagRe     = regexp.MustCompile(`<BrowseFlag>(.*?)</BrowseFlag>`)

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

type browseCacheEntry struct {
	payload string
	count   int
	expires time.Time
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

type videoMeta struct {
	DurationSeconds float64
}

var (
	metaCacheMu sync.RWMutex
	metaCache   = make(map[string]videoMeta)
)

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
	go func() {
		count := 0
		_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".mp4" || ext == ".mkv" || ext == ".avi" {
				getVideoMeta(path)
				count++
			}
			return nil
		})
		log.Printf("✅ ffprobe кеш: %d файлов в %s", count, redactPath(dir))
	}()
}

// ── Прогресс просмотра ────────────────────────────────────────────────────────

type progressEntry struct {
	Position int64     `json:"position"`
	Size     int64     `json:"size"`
	Updated  time.Time `json:"updated"`
}

var (
	progressMu   sync.RWMutex
	progressData = make(map[string]progressEntry)
)

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
	data, _ := json.MarshalIndent(progressData, "", "  ")
	progressMu.RUnlock()
	_ = os.WriteFile(progressFile, data, 0600)
}

func recordProgress(filename string, position, size int64) {
	// Не сохраняем первые и последние 2% — это probe-запросы
	if size == 0 || position < size/50 || position > size*49/50 {
		return
	}
	progressMu.Lock()
	progressData[filename] = progressEntry{Position: position, Size: size, Updated: time.Now()}
	progressMu.Unlock()
	saveProgress()
	// Сбрасываем browse-кеш чтобы таймкод обновился при следующем открытии папки
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
}

// ── Main ──────────────────────────────────────────────────────────────────────

func main() {
	mediaDirFlag := flag.String("media-dir", defaultMediaDir, "Путь к медиатеке")
	portFlag := flag.String("port", defaultServerPort, "HTTP порт сервера")
	dataDirFlag := flag.String("data-dir", defaultDataDir, "Папка для логов/прогресса (HOMECINEMA_DATA_DIR)")
	allowRemoteControlFlag := flag.Bool("allow-remote-control", false, "Разрешить менять папку медиатеки не только с localhost (НЕ рекомендуется)")
	flag.Parse()

	serverPort = *portFlag
	setMediaDir(*mediaDirFlag)
	setDataDir(*dataDirFlag)
	remoteControlOK = *allowRemoteControlFlag

	initLogger()
	loadProgress()
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
	http.HandleFunc("/video/", func(w http.ResponseWriter, r *http.Request) {
		relPath := strings.TrimPrefix(r.URL.Path, "/video/")
		filePath := filepath.Join(getMediaDir(), relPath)
		serveVideo(w, r, filePath, relPath)
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
			"SERVER: MacOS/13.0 UPnP/1.0 DLNADOC/1.50 HomeCinema/1.0\r\n"+
			"ST: %s\r\n"+
			"USN: uuid:%s::%s\r\n"+
			"\r\n", time.Now().UTC().Format(time.RFC1123), location, st, uuid, st)

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
		w.Header().Set("Server", "Linux/2.6 UPnP/1.0 DLNADOC/1.50 HomeCinema/1.0")

		if flag == "BrowseMetadata" {
			logOnce("⚙️ МЕТАДАННЫЕ: ID=%s", objID)
			meta := fmt.Sprintf(`&lt;container id="%s" parentID="-1" restricted="1"&gt;&lt;dc:title&gt;Folder&lt;/dc:title&gt;&lt;upnp:class&gt;object.container.storageFolder&lt;/upnp:class&gt;&lt;/container&gt;`, objID)
			fmt.Fprintf(w, soapResponse, meta, 1, 1)
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
				fmt.Fprintf(w, soapResponse, payload, count, count)
				return
			}

			dir := getMediaDir()
			logOnce("📂 ПАПКА: /%s", relPath)

			files, err := os.ReadDir(filepath.Join(dir, relPath))
			if err != nil {
				log.Printf("❌ ОШИБКА ЧТЕНИЯ ПАПКИ: %v", err)
				fmt.Fprintf(w, soapResponse, "", 0, 0)
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
					fileURL := fmt.Sprintf("http://%s:%s/video/%s", ip, serverPort, strings.Join(parts, "/"))

					proto := "http-get:*:video/mp4:DLNA.ORG_PN=AVC_MP4_HP_HD_24;DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"

					// Добавляем таймкод в название если есть прогресс
					title := displayTitle
					fullPath := filepath.Join(dir, childRelPath)
					if pos := getProgress(f.Name(), info.Size()); pos > 0 {
						meta := getVideoMeta(fullPath)
						if tc := formatTimecode(pos, info.Size(), meta.DurationSeconds); tc != "" {
							title = fmt.Sprintf("%s [▶ %s]", displayTitle, tc)
						}
					}

					item := fmt.Sprintf(`&lt;item id="vid-%s" parentID="%s" restricted="1"&gt;`+
						`&lt;dc:title&gt;%s&lt;/dc:title&gt;`+
						`&lt;upnp:class&gt;object.item.videoItem&lt;/upnp:class&gt;`+
						`&lt;res size="%d" protocolInfo="%s"&gt;%s&lt;/res&gt;`+
						`&lt;/item&gt;`, stableID, objID, title, info.Size(), proto, fileURL)

					items = append(items, item)
					count++
				}
			}
			payload := strings.Join(items, "")
			setBrowseCache(relPath, payload, count)
			fmt.Fprintf(w, soapResponse, payload, count, count)
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

	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", dlnaProfile)
	w.Header().Set("Cache-Control", "no-transform")

	rangeHdr := r.Header.Get("Range")
	if rangeHdr == "" {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
		log.Printf("▶️ СТРИМИНГ: %s (без Range)", info.Name())
		streamFile(w, r, file, 0, info.Size()-1, info.Size())
		return
	}

	start, end, ok := parseRange(rangeHdr, info.Size())
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", info.Size()))
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
	streamFile(w, r, file, start, end, info.Size())
}

func streamFile(w http.ResponseWriter, r *http.Request, file *os.File, start, end, totalSize int64) {
	filename := filepath.Base(file.Name())
	length := end - start + 1

	// Probe-соединения не пишем в прогресс:
	// маленький range (< 1MB) или конец файла
	isProbe := length < 1024*1024 || start > totalSize*9/10

	const bufSize = 4 * 1024 * 1024
	buf := make([]byte, bufSize)
	remaining := length
	written := start
	flusher, _ := w.(http.Flusher)
	lastSaved := time.Now()

	for remaining > 0 {
		chunkSize := int64(bufSize)
		if remaining < chunkSize {
			chunkSize = remaining
		}
		n, err := file.Read(buf[:chunkSize])
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				if !isClientClosed(wErr) {
					log.Printf("❌ Ошибка выдачи: %v", wErr)
				}
				if !isProbe {
					recordProgress(filename, written, totalSize)
				}
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
			written += int64(n)
			remaining -= int64(n)

			if !isProbe && time.Since(lastSaved) > 10*time.Second {
				recordProgress(filename, written, totalSize)
				log.Printf("💾 %s → %.0f%%", filename, float64(written)/float64(totalSize)*100)
				lastSaved = time.Now()
			}
		}
		if err != nil {
			if err != io.EOF && !isClientClosed(err) {
				log.Printf("❌ Ошибка чтения: %v", err)
			}
			return
		}

		select {
		case <-r.Context().Done():
			if !isProbe {
				recordProgress(filename, written, totalSize)
				log.Printf("💾 СТОП: %s → %.0f%%", filename, float64(written)/float64(totalSize)*100)
			}
			return
		default:
		}
	}
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
<s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>&lt;didl-lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/"&gt;%s&lt;/didl-lite&gt;</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>1</UpdateID></u:BrowseResponse></s:Body></s:Envelope>`
