package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
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
	streamBufMBFlag := flag.Int("stream-buf-mb", 1, "Размер буфера выдачи (МБ). 1 MB подобран под BDP Wi‑Fi (200 Mbps × 30 ms ≈ 750 KB) — большие значения дают bursty-поток и подвисания на 4K по Wi‑Fi.")
	warmupMetaFlag := flag.Bool("warmup-meta", true, "Прогреть кеш длительности (ffprobe) при старте/смене папки (может грузить диск/CPU)")
	warmupMetaThrottleFlag := flag.Duration("warmup-meta-throttle", 0, "Пауза между ffprobe вызовами при прогреве (например 150ms)")
	warmupMetaMaxFlag := flag.Int("warmup-meta-max", 0, "Максимум файлов для прогрева (0 = все)")
	tvStreamFlag := flag.Bool("tv-stream", true, "Добавить TV-версию потока (ffmpeg) как альтернативный <res> (уменьшает тормоза по Wi‑Fi)")
	tvStreamFirstFlag := flag.Bool("tv-stream-first", false, "Ставить TV-поток (ffmpeg) первым <res> (ТВ чаще выбирает первый ресурс, но может пропасть прогресс/длительность)")
	tvAutoFirstFlag := flag.Bool("tv-auto-first", true, "Автоматически ставить TV-поток первым для тяжёлых файлов по рассчитанному битрейту (может показываться как mpg на части ТВ)")
	tvAutoFirstMbpsFlag := flag.Int("tv-auto-first-mbps", 8, "Порог среднего битрейта файла (Mbps), после которого TV-поток ставится первым")
	tvCRFFlag := flag.Int("tv-crf", 22, "CRF для TV-потока (выше = меньше битрейт/качество)")
	tvMaxrateFlag := flag.Int("tv-maxrate-mbps", 10, "Максимальный видеобитрейт TV-потока (Mbps)")
	tvBufsizeFlag := flag.Int("tv-bufsize-mbps", 40, "VBV bufsize для TV-потока (Mbps, рекомендуется 4×maxrate)")
	tvPresetFlag := flag.String("tv-preset", "veryfast", "Preset для TV-потока (ffmpeg x264)")
	tvAudioKbpsFlag := flag.Int("tv-audio-kbps", 384, "Аудиобитрейт TV-потока (kbps, AC3)")
	tvAudioChFlag := flag.Int("tv-audio-ch", 6, "Аудиоканалы TV-потока")
	maxTVStreamsFlag := flag.Int("max-tv-streams", 1, "Максимум одновременных ffmpeg TV-транскодов (>=1)")
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
	tvAutoFirst = *tvAutoFirstFlag
	tvAutoFirstMbps = *tvAutoFirstMbpsFlag
	tvVideoCRF = *tvCRFFlag
	tvVideoMaxrateMb = *tvMaxrateFlag
	tvVideoBufsizeMb = *tvBufsizeFlag
	tvVideoPreset = stringsTrimDefault(*tvPresetFlag, "veryfast")
	tvAudioKbps = *tvAudioKbpsFlag
	tvAudioChannels = *tvAudioChFlag

	if p, ok := resolveExec(ffprobeExe); ok {
		ffprobeExe = p
	} else {
		ffprobeExe = ""
		log.Printf("⚠️ ffprobe не найден. Длительность/таймкоды будут появляться только из сохранённого прогресса.")
	}
	if tvStreamEnabled {
		if p, ok := resolveExec(ffmpegExe); ok {
			ffmpegExe = p
		} else {
			ffmpegExe = ""
			log.Printf("⚠️ ffmpeg не найден. Отключаю --tv-stream (можно включить после установки ffmpeg).")
			tvStreamEnabled = false
		}
	}

	if tvVideoMaxrateMb < 2 {
		tvVideoMaxrateMb = 2
	}
	if tvAutoFirstMbps < 1 {
		tvAutoFirstMbps = 1
	}
	// VBV bufsize по умолчанию = 4 × maxrate. С узким bufsize ffmpeg слишком
	// агрессивно режет битрейт на сценах с большим движением → плеер ждёт
	// I-кадр → фриз на 1-2 секунды. 4× — стандартная рекомендация x264 для VOD.
	if tvVideoBufsizeMb < tvVideoMaxrateMb*2 {
		tvVideoBufsizeMb = tvVideoMaxrateMb * 4
	}
	initTVStreamSem(*maxTVStreamsFlag)
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
	uuid = loadOrCreateUUID(dataDir)
	log.Printf("🆔 UUID сервера: %s", uuid)
	loadProgress()
	runProgressSaver()
	defer closeProgressStore()

	invalidateBrowseCache()
	warmupMetaCache(getMediaDir())

	// При старте подчищаем «фантомные» записи прогресса для файлов, удалённых
	// между предыдущим и текущим запуском. Дальше — раз в час.
	if pruned := pruneMissingProgress(getMediaDir()); pruned > 0 {
		log.Printf("🗑️ Прогресс: при старте удалено %d записей для отсутствующих файлов", pruned)
	}
	progressPruneCtx, progressPruneCancel := context.WithCancel(context.Background())
	defer progressPruneCancel()
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-progressPruneCtx.Done():
				return
			case <-t.C:
				if pruned := pruneMissingProgress(getMediaDir()); pruned > 0 {
					log.Printf("🗑️ Прогресс: удалено %d записей для отсутствующих файлов", pruned)
				}
			}
		}
	}()

	ip := getLocalIP()
	serverAddr := fmt.Sprintf("http://%s:%s", ip, serverPort)

	ssdpCtx, ssdpCancel := context.WithCancel(context.Background())
	defer ssdpCancel()
	var ssdpWG sync.WaitGroup
	ssdpWG.Add(2)
	go startSSDP(ssdpCtx, &ssdpWG, ip)
	go respondMSearch(ssdpCtx, &ssdpWG, ip)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		statusHandler(w, r, ip)
	})
	mux.HandleFunc("/stats", handleStats)
	mux.HandleFunc("/set-media-dir", handleFolderSelection)
	mux.HandleFunc("/reset-progress", handleResetProgress)
	mux.HandleFunc("/delete-progress", handleDeleteProgress)
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		fmt.Fprintf(w, deviceDescription, friendlyName, manufacturerName, modelName, uuid)
	})
	mux.HandleFunc("/ctl/ContentDirectory", handleContentDirectory(ip))
	mux.HandleFunc("/evt/ContentDirectory", handleEventContentDirectory())
	mux.HandleFunc("/video/", func(w http.ResponseWriter, r *http.Request) {
		relRaw := strings.TrimPrefix(r.URL.Path, "/video/")
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
		// /video/ — всегда прямая отдача с Range. Resume-логика живёт в /resume/,
		// потому что байтовый seek в MP4/MKV не даёт ТВ заголовки контейнера и
		// показывает 0:00 без длительности.
		serveVideo(w, r, filePath, relPath)
	})
	mux.HandleFunc("/tv/", func(w http.ResponseWriter, r *http.Request) {
		relRaw := strings.TrimPrefix(r.URL.Path, "/tv/")
		if strings.HasSuffix(relRaw, ".ts") {
			relRaw = strings.TrimSuffix(relRaw, ".ts")
		} else if strings.HasSuffix(relRaw, ".mp4") {
			relRaw = strings.TrimSuffix(relRaw, ".mp4")
		}
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
	mux.HandleFunc("/resume/", func(w http.ResponseWriter, r *http.Request) {
		relRaw := strings.TrimPrefix(r.URL.Path, "/resume/")
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
		serveResume(w, r, filePath, relPath)
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
		// Отменяем SSDP-контекст и ждём, пока обе горутины пошлют ssdp:byebye
		// и закроют сокеты. Без явного ожидания shutdown мог завершиться
		// раньше отправки byebye → ТВ продолжал считать сервер живым ещё минуту.
		ssdpCancel()
		ssdpDone := make(chan struct{})
		go func() {
			ssdpWG.Wait()
			close(ssdpDone)
		}()
		select {
		case <-ssdpDone:
		case <-time.After(2 * time.Second):
			log.Printf("⚠️ SSDP shutdown: таймаут ожидания byebye")
		}
		closeProgressStore()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		_ = server.Shutdown(shutCtx)
	}()

	log.Printf("📡 СЕРВЕР ЗАПУЩЕН | %s | %s", friendlyName, serverAddr)
	err := server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
