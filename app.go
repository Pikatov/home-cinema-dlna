package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	defaultServerPort = "8080"
	friendlyName      = "Home Cinema"
	manufacturerName  = "Home Cinema"
	modelName         = "HomeCinemaStreamer"
	appVersion        = "1.5"
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
)

var (
	serverPort      = defaultServerPort
	startedAt       = time.Now()
	defaultMediaDir = resolveDefaultMediaDir()
	currentMediaDir = defaultMediaDir
	defaultDataDir  = resolveDefaultDataDir()
	dataDir         = defaultDataDir
	logFilePath     = filepath.Join(dataDir, logFileName)
	progressFile    = filepath.Join(dataDir, progressFileName)
	remoteControlOK = false
)

var (
	mediaDirMu    sync.RWMutex
	browseCache   = make(map[string]browseCacheEntry)
	browseCacheMu sync.RWMutex
)

var (
	streamDebugEnabled bool
	streamDebugHeaders bool
	streamDebugEvery   = 15 * time.Second
	streamSlowRead     = 200 * time.Millisecond
	streamSlowWrite    = 200 * time.Millisecond
)

var (
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
	if *progressEveryFlag < 250*time.Millisecond {
		*progressEveryFlag = 250 * time.Millisecond
	}
	progressUpdateEvery = *progressEveryFlag

	tvStreamEnabled = *tvStreamFlag
	tvStreamFirst = *tvStreamFirstFlag
	tvVideoCRF = *tvCRFFlag
	tvVideoMaxrateMb = *tvMaxrateFlag
	tvVideoBufsizeMb = *tvBufsizeFlag
	tvVideoPreset = stringsTrimDefault(*tvPresetFlag, "veryfast")
	tvAudioKbps = *tvAudioKbpsFlag
	tvAudioChannels = *tvAudioChFlag

	if p, ok := resolveExec(ffprobeExe); ok {
		ffprobeExe = p
	} else {
		log.Printf("⚠️ ffprobe не найден. Длительность/таймкоды будут появляться только из сохранённого прогресса.")
	}
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

	progressStoreRef = newProgressStore(progressFile)

	initLogger()
	loadProgress()
	runProgressSaver()
	defer closeProgressStore()

	invalidateBrowseCache()
	warmupMetaCache(getMediaDir())

	ip := getLocalIP()
	serverAddr := fmt.Sprintf("http://%s:%s", ip, serverPort)

	go startSSDP(ip)
	go respondMSearch(ip)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		statusHandler(w, r, ip)
	})
	mux.HandleFunc("/set-media-dir", handleFolderSelection)
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		fmt.Fprintf(w, deviceDescription, friendlyName, manufacturerName, modelName, uuid)
	})
	mux.HandleFunc("/ctl/ContentDirectory", handleContentDirectory(ip))
	mux.HandleFunc("/evt/ContentDirectory", handleEventContentDirectory())
	mux.HandleFunc("/video/", func(w http.ResponseWriter, r *http.Request) {
		relRaw := stringsTrimPrefix(r.URL.Path, "/video/")
		relPath, ok := safeMediaRelPathFromURL(relRaw)
		if !ok || relPath == "" {
			http.NotFound(w, r)
			return
		}
		filePath, ok := safeJoinUnderBase(getMediaDir(), relPath)
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveVideo(w, r, filePath, relPath)
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		relRaw := stringsTrimPrefix(r.URL.Path, "/tv/")
		relPath, ok := safeMediaRelPathFromURL(relRaw)
		if !ok || relPath == "" {
			http.NotFound(w, r)
			return
		}
		filePath, ok := safeJoinUnderBase(getMediaDir(), relPath)
		if !ok {
			http.NotFound(w, r)
			return
		}
		serveTVStream(w, r, filePath, relPath)
	})

	server := &http.Server{
		Addr:              ":" + serverPort,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Printf("🛑 Получен сигнал завершения, сохраняю прогресс...")
		stopWarmupMeta()
		closeProgressStore()
		_ = server.Close()
	}()

	log.Printf("📡 СЕРВЕР ЗАПУЩЕН | %s | %s", friendlyName, serverAddr)
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func bumpBrowseUpdateID() uint32 {
	return atomic.AddUint32(&browseUpdateID, 1)
}

func currentBrowseUpdateID() uint32 {
	return atomic.LoadUint32(&browseUpdateID)
}
