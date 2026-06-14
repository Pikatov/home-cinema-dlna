package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// tvStreamSem ограничивает число параллельных ffmpeg-транскодов.
// Если ТВ при быстром seek успевает открыть несколько соединений до
// окончания предыдущего, лишние запросы ждут слот вместо того, чтобы
// душить CPU и диск конкурентным транскодом.
var tvStreamSem chan struct{}

// tvStreamHandle оборачивает context.CancelFunc, чтобы значения в sync.Map были
// comparable. Функции в Go не comparable (можно сравнить только с nil) — попытка
// sync.Map.CompareAndDelete с CancelFunc-значением паникует во время выполнения.
// Указатель на структуру comparable по identity, что нам и нужно: владелец
// удаляет только тот handle, который сам положил, не задевая чужой.
type tvStreamHandle struct {
	cancel context.CancelFunc
}

// tvFileStreams хранит handle активного ffmpeg-процесса для каждого
// (файл, клиент). Ключом служит filePath+remoteHost: новый запрос с того же
// клиента (seek/reconnect) отменяет его предыдущий процесс, но не задевает
// параллельных зрителей этого же файла (ТВ + проектор, ТВ + iPad-DLNA).
var tvFileStreams sync.Map // map[string]*tvStreamHandle

// directFileStreams хранит активный прямой playback-поток /video/ для каждого
// (файл, клиент). Многие DLNA-ТВ при старте MKV открывают несколько больших
// Range "почти с начала до конца"; если отдавать их параллельно, диск и Wi-Fi
// забиваются сразу. Новый playback-запрос от того же ТВ отменяет предыдущий,
// а короткие/концевые probe-запросы не трогают основной поток.
var directFileStreams sync.Map // map[string]*tvStreamHandle

func initTVStreamSem(max int) {
	if max < 1 {
		max = 1
	}
	tvStreamSem = make(chan struct{}, max)
}

// acquireTVSlot блокирует до получения слота. Возвращает false, если
// контекст клиента отменился раньше (ТВ ушёл — слот ему уже не нужен).
// Ждём не дольше короткого таймаута: если все слоты заняты дольше — отдаём 503,
// чтобы ТВ не висел в ожидании первого байта.
func acquireTVSlot(ctx context.Context) bool {
	if tvStreamSem == nil {
		return true
	}
	select {
	case tvStreamSem <- struct{}{}:
		return true
	default:
	}
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case tvStreamSem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	case <-timer.C:
		return false
	}
}

func releaseTVSlot() {
	if tvStreamSem == nil {
		return
	}
	// Без default: дисбаланс Acquire/Release блокирует здесь, что заметно
	// в дев-режиме. Тихое проглатывание поверх default: маскировало бы баги
	// пайплайна (Release без парного Acquire) → семафор уплывал бы вверх и
	// сервер пускал бы больше ffmpeg, чем разрешено.
	<-tvStreamSem
}

// tvStreamKey формирует ключ tvFileStreams: путь файла + хост клиента (без
// порта — порт у TCP-соединения меняется на каждый запрос). Невалидный
// remoteAddr трактуем как «неизвестный клиент» — все такие зрители делят
// один слот (хуже изоляции, но безопаснее, чем глобальный ключ).
func tvStreamKey(filePath, remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil || host == "" {
		host = remoteAddr
	}
	return filePath + "\x00" + host
}

// cancelPreviousTVStream немедленно убивает предыдущий ffmpeg-процесс для
// (файл, клиент). Вызывается перед семафором — к моменту ожидания слота
// старый процесс уже мёртв и быстро освобождает место.
func cancelPreviousTVStream(key string) {
	if prev, ok := tvFileStreams.LoadAndDelete(key); ok {
		prev.(*tvStreamHandle).cancel()
	}
}

func cancelPreviousDirectStream(key string) {
	if prev, ok := directFileStreams.LoadAndDelete(key); ok {
		prev.(*tvStreamHandle).cancel()
	}
}

func isProbeStreamRange(start, end, totalSize int64) bool {
	if totalSize <= 0 || end < start {
		return true
	}
	length := end - start + 1
	return length < 1024*1024 || start > totalSize*9/10
}

func shouldReplaceDirectPlaybackStream(start, end, totalSize int64) bool {
	if isProbeStreamRange(start, end, totalSize) {
		return false
	}
	// DLNA clients often open the same item as GET without Range, then
	// immediately as Range: bytes=0-... while probing the container. Both are
	// still startup reads, not a user seek. Cancelling the first one here makes
	// some TVs loop forever through open/probe/reopen.
	return start > 0
}

func withDirectPlaybackStream(r *http.Request, filePath string, start, end, totalSize int64) (*http.Request, func()) {
	if !shouldReplaceDirectPlaybackStream(start, end, totalSize) {
		return r, func() {}
	}
	streamKey := tvStreamKey(filePath, r.RemoteAddr)
	cancelPreviousDirectStream(streamKey)

	ctx, cancel := context.WithCancel(r.Context())
	handle := &tvStreamHandle{cancel: cancel}
	directFileStreams.Store(streamKey, handle)

	return r.Clone(ctx), func() {
		directFileStreams.CompareAndDelete(streamKey, handle)
		cancel()
	}
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
	w.Header().Set("X-Content-Duration", fmt.Sprintf("%.3f", durationSeconds))
	w.Header().Set("X-AvailableSeekRange", fmt.Sprintf("1 npt=00:00:00.000-%s", dur))
	w.Header().Set("TimeSeekRange.dlna.org", fmt.Sprintf("npt=00:00:00.000-%s/%s", dur, dur))
	w.Header().Set("X-Seek-Range", fmt.Sprintf("npt=0-%.0f", durationSeconds))
}

// streamBufSize — размер буфера для одного цикла Read→Write в streamFile.
// 1 MB подобран под Wi‑Fi BDP (см. --stream-buf-mb). На проводе разница
// между 1 и 4 MB неощутима, на Wi‑Fi 4 MB давал bursty-поток.
var streamBufSize = 1 * 1024 * 1024

var streamBufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, streamBufSize)
	},
}

var progressUpdateEvery = 3 * time.Second

// streamIdleC receives a token whenever activeStreamRequests drops to zero.
var streamIdleC = make(chan struct{}, 1)

// displayTitleFromRel убирает расширение из relative-path и возвращает
// basename — то, что UI показывает в Now Playing.
func displayTitleFromRel(rel string) string {
	if rel == "" {
		return ""
	}
	base := filepath.Base(rel)
	return trimVideoExtension(base)
}

// activeStreamCount возвращает число активных /video/ запросов (для /stats и UI).
// TV-стримы считаются через семафор tvStreamSem, не учитываются здесь.
func activeStreamCount() int64 {
	return atomic.LoadInt64(&activeStreamRequests)
}

func notifyStreamIdle() {
	if atomic.LoadInt64(&activeStreamRequests) == 0 {
		select {
		case streamIdleC <- struct{}{}:
		default:
		}
	}
}

func serveVideo(w http.ResponseWriter, r *http.Request, filePath, requestedRelPath string) {
	reqID := atomic.AddUint64(&streamSeq, 1)
	active := atomic.AddInt64(&activeStreamRequests, 1)
	defer func() {
		atomic.AddInt64(&activeStreamRequests, -1)
		notifyStreamIdle()
	}()

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
		// Пробуем прогресс (быстро, без I/O).
		if entry, ok := getProgressEntry(progressKeyFromRelPath(requestedRelPath)); ok && entry.DurationSeconds > 0 {
			meta.DurationSeconds = entry.DurationSeconds
		} else {
			// Синхронный ffprobe: заголовки Duration и TimeSeekRange.dlna.org
			// нужны ТВ до начала воспроизведения и для отображения «хх:хх - yy:yy»
			// после рестарта сервера. 300 мс не хватало 4K/8 ГБ файлам — ffprobe
			// читает контейнер до первого Cluster/moof, на медленном диске это
			// 0.5–1.5 с. Поднял до 2 с: воспроизведение ждёт максимум 2 с, ТВ
			// нормально терпят такую задержку (типичный HEAD→GET сам по себе
			// занимает у них 0.5–1 с).
			meta = getVideoMetaWithTimeout(filePath, 2*time.Second)
			if meta.DurationSeconds <= 0 {
				warmVideoMetaAsync(filePath)
			}
		}
	}

	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Accept-Ranges", "bytes")
	// HTTP-заголовки case-insensitive — Go нормализует имя, второй Set
	// просто перетирал первый. Оставляем canonical-форму.
	w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
	w.Header().Set("ContentFeatures.DLNA.ORG", dlnaProfile)
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
		rStream, doneStream := withDirectPlaybackStream(r, filePath, 0, info.Size()-1, info.Size())
		defer doneStream()
		sid := openSession("direct", displayTitleFromRel(requestedRelPath), requestedRelPath, r.RemoteAddr, r.UserAgent(), 0, meta.DurationSeconds)
		defer closeSession(sid)
		streamFile(w, rStream, file, reqID, requestedRelPath, progressKeyFromRelPath(requestedRelPath), 0, info.Size()-1, info.Size(), meta.DurationSeconds)
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
	if start > 0 && start < resumeProbeRangeBytes {
		start = 0
		end = info.Size() - 1
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
	rStream, doneStream := withDirectPlaybackStream(r, filePath, start, end, info.Size())
	defer doneStream()
	// Регистрируем сессию только для «жирных» Range — короткие probe-запросы
	// (типа bytes=0-1023) не должны мелькать в UI.
	var sid uint64
	if length > 1024*1024 {
		sid = openSession("direct", displayTitleFromRel(requestedRelPath), requestedRelPath, rStream.RemoteAddr, rStream.UserAgent(),
			float64(start)/float64(info.Size())*meta.DurationSeconds, meta.DurationSeconds)
		defer closeSession(sid)
	}
	streamFile(w, rStream, file, reqID, requestedRelPath, progressKeyFromRelPath(requestedRelPath), start, end, info.Size(), meta.DurationSeconds)
}

func streamFile(w http.ResponseWriter, r *http.Request, file *os.File, reqID uint64, requestedRelPath, progressKey string, start, end, totalSize int64, durationSeconds float64) {
	length := end - start + 1
	// isProbe ловит ДВА типа запросов:
	//   - короткие закрытые диапазоны (TV читает несколько KB метаданных);
	//   - запросы у самого конца файла (Cues/SeekHead в MKV, moov/mfra в MP4 с
	//     -movflags faststart=off).
	// На probe-стримах не пишем прогресс и не пре-фетчим (фактическое чтение всё
	// равно идёт по chunkSize, но мы не делаем reader-goroutine впрок) — это
	// важно для тяжёлых MKV: TV открывает 6–8 параллельных Range-запросов
	// «от любого байта до конца файла», и каждый из них имеет длину ≈ размер
	// файла, что не probe по length. Поэтому ещё мы помечаем как probe Range,
	// который заканчивается ровно концом файла И начинается после самых первых
	// байт — реальный playback стартует с 0 или с одной из небольших позиций
	// у начала, а не с произвольной точки в середине.
	isProbe := isProbeStreamRange(start, end, totalSize)

	buf := streamBufPool.Get().([]byte)
	defer streamBufPool.Put(buf)
	remaining := length
	written := start
	flusher, _ := w.(http.Flusher)
	started := time.Now()
	lastSaved := started
	var lastDbg time.Time
	if streamDebugEnabled {
		lastDbg = started
	}
	endReason := "unknown"
	var endErr error
	// Метрики используются только под --debug-stream, чтобы не дёргать
	// time.Now() и float-арифметику на каждом chunk-е в hot path.
	var (
		chunkCount   int64
		minChunkMbps = math.Inf(1)
		maxChunkMbps float64
	)

	defer func() {
		if !isProbe {
			recordProgressBytes(progressKey, written, totalSize, durationSeconds)
			if durationSeconds <= 0 && start < 1024*1024 {
				recordProgressSeconds(progressKey, time.Since(started).Seconds(), 0)
			}
		}
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
			log.Printf("DBG stream#%d done file=%q sent=%d dur=%s avg_mbps=%.2f reason=%s%s",
				reqID, requestedRelPath, sent, dur.Round(time.Millisecond), mbps, endReason, errStr)
			log.Printf("DBG stream#%d stats file=%q chunks=%d min_chunk_mbps=%s max_chunk_mbps=%.2f buf=%d",
				reqID, requestedRelPath, chunkCount, minStr, maxChunkMbps, len(buf))
		}
	}()

	for remaining > 0 {
		select {
		case <-r.Context().Done():
			endReason = "client_cancel"
			endErr = r.Context().Err()
			return
		default:
		}

		chunkSize := int64(len(buf))
		// Первый chunk делаем маленьким (256 KB) — TV получает первые
		// байты быстрее, декодер стартует, time-to-first-frame падает.
		if written == start && chunkSize > 256*1024 {
			chunkSize = 256 * 1024
		}
		if remaining < chunkSize {
			chunkSize = remaining
		}

		readStart := time.Now()
		n, err := file.Read(buf[:chunkSize])
		readDur := time.Since(readStart)
		if streamDebugEnabled && readDur > streamSlowRead {
			log.Printf("DBG stream#%d slow_read=%s file=%q off=%d want=%d got=%d",
				reqID, readDur.Round(time.Millisecond), requestedRelPath, written, chunkSize, n)
		}

		if n > 0 {
			writeStart := time.Now()
			_, wErr := w.Write(buf[:n])
			writeDur := time.Since(writeStart)
			if streamDebugEnabled {
				chunkCount++
				if writeDur > 0 {
					chunkMbps := (float64(n) * 8) / writeDur.Seconds() / 1e6
					if chunkMbps > 0 && chunkMbps < minChunkMbps {
						minChunkMbps = chunkMbps
					}
					if chunkMbps > maxChunkMbps {
						maxChunkMbps = chunkMbps
					}
				}
				if writeDur > streamSlowWrite {
					curSent := written - start + int64(n)
					curDur := time.Since(started)
					curMbps := 0.0
					if curDur > 0 {
						curMbps = (float64(curSent) * 8) / curDur.Seconds() / 1e6
					}
					log.Printf("DBG stream#%d slow_write=%s file=%q off=%d n=%d rem=%d avg_mbps=%.2f",
						reqID, writeDur.Round(time.Millisecond), requestedRelPath, written, n, remaining, curMbps)
				}
			}

			if wErr != nil {
				if isClientClosed(wErr) {
					endReason = "client_closed"
				} else {
					endReason = "write_error"
					log.Printf("❌ Ошибка выдачи: %v", wErr)
				}
				endErr = wErr
				return
			}

			// Flush после каждой записи: важно для DLNA-ТВ — без него ядерный
			// TCP-буфер задерживает первые байты декодера и Samsung/LG/Sony
			// либо стартуют через 30–60 с buffering, либо обрывают и пере-
			// открывают стрим (видно в логах как несколько «▶️ СТРИМИНГ»
			// подряд). Версия 1.8 пыталась отказаться от per-write Flush ради
			// throughput, но на практике throughput не страдает (1 MB write
			// ≫ bufio-буфера, идёт напрямую в TCP), а вот совместимость ТВ
			// рушится.
			if flusher != nil {
				flusher.Flush()
			}

			written += int64(n)
			remaining -= int64(n)

			if !isProbe && time.Since(lastSaved) > progressUpdateEvery {
				recordProgressBytes(progressKey, written, totalSize, durationSeconds)
				if durationSeconds <= 0 && start < 1024*1024 {
					recordProgressSeconds(progressKey, time.Since(started).Seconds(), 0)
				}
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
				log.Printf("DBG stream#%d tick file=%q sent=%d rem=%d dur=%s avg_mbps=%.2f min_chunk_mbps=%.2f max_chunk_mbps=%.2f",
					reqID, requestedRelPath, sent, remaining, dur.Round(time.Millisecond), mbps, minNow, maxChunkMbps)
				lastDbg = time.Now()
			}
		}

		if err != nil {
			switch {
			case err == io.EOF:
				endReason = "eof"
			case isClientClosed(err):
				endReason = "client_closed"
				endErr = err
			default:
				endReason = "read_error"
				endErr = err
				log.Printf("❌ Ошибка чтения: %v", err)
			}
			return
		}
	}

	endReason = "finished"
}

// isClientClosed reports whether err is a normal client-disconnect error
// (broken pipe, connection reset, etc.).
func isClientClosed(err error) bool {
	if err == nil {
		return false
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var syscallErr *os.SyscallError
		if errors.As(opErr.Err, &syscallErr) {
			switch syscallErr.Err {
			case syscall.EPIPE, syscall.ECONNRESET, syscall.ENOTCONN:
				return true
			}
		}
	}
	// Fallback for edge cases (TLS, HTTP/2, platform variants).
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
	case ".mov":
		return "video/quicktime", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	default:
		if m := mime.TypeByExtension(ext); m != "" {
			return m, "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
		}
		return "application/octet-stream", "DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000"
	}
}

// pipeCopyBufSize — размер буфера для копирования из ffmpeg stdout в сеть.
// 64 KiB давал по 30–60 read/write-syscall'ов в секунду на 4K-потоке: лишние
// переключения контекста и куча мелких TCP-пакетов. 256 KiB укладывается в
// типичный TCP send buffer и режет syscall-нагрузку в ~4 раза.
const pipeCopyBufSize = 256 * 1024

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
	progressKey := progressKeyFromRelPath(requestedRelPath)
	meta, metaOK := getVideoMetaCached(filePath)
	if !metaOK || meta.DurationSeconds <= 0 {
		if entry, ok := getProgressEntry(progressKey); ok && entry.DurationSeconds > 0 {
			meta.DurationSeconds = entry.DurationSeconds
		} else {
			meta = getVideoMetaWithTimeout(filePath, 2*time.Second)
			if meta.DurationSeconds <= 0 {
				warmVideoMetaAsync(filePath)
			}
		}
	}

	w.Header().Set("Content-Type", tvContentType)
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
	w.Header().Set("ContentFeatures.DLNA.ORG", tvDLNAFeatures)
	w.Header().Set("Cache-Control", "no-transform")

	// Determine seek position. Byte Range on transcoded MPEG-TS is not meaningful:
	// TVs that honor DLNA time-seek send TimeSeekRange.dlna.org instead.
	var seekSecs float64
	if requestedSeek, ok := parseTimeSeekRangeStart(r.Header.Get("TimeSeekRange.dlna.org"), meta.DurationSeconds); ok {
		seekSecs = requestedSeek
		log.Printf("⏩ TV TIME SEEK: %s → %.0f сек", requestedRelPath, seekSecs)
	} else {
		// Initial start (no Range, or bytes=0-): check for saved resume position.
		if entry, ok := getProgressEntry(progressKey); ok {
			if entry.Seconds > 0 {
				seekSecs = entry.Seconds
			} else if entry.Position > 0 && entry.Size > 0 && entry.DurationSeconds > 0 {
				if fi2, err2 := os.Stat(filePath); err2 == nil && fi2.Size() == entry.Size {
					seekSecs = entry.DurationSeconds * float64(entry.Position) / float64(entry.Size)
				}
			}
			if seekSecs > 0 {
				log.Printf("▶️ TV RESUME: %s от %.0f сек", requestedRelPath, seekSecs)
			}
		}
	}

	setDLNATimeSeekHeadersWithOffset(w, meta.DurationSeconds, seekSecs)

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Немедленно отменяем предыдущий ffmpeg того же клиента — он освободит слот
	// семафора до того, как мы начнём ждать. Без этого seek ждал бы пока старый
	// процесс сам заметит EPIPE (1–5 с) → видимый фриз на ТВ.
	streamKey := tvStreamKey(filePath, r.RemoteAddr)
	cancelPreviousTVStream(streamKey)

	// Семафор: ограничиваем число одновременных ffmpeg-транскодов.
	if !acquireTVSlot(r.Context()) {
		http.Error(w, "TV stream busy", http.StatusServiceUnavailable)
		return
	}
	defer releaseTVSlot()

	sid := openSession("tv", displayTitleFromRel(requestedRelPath), requestedRelPath, r.RemoteAddr, r.UserAgent(), seekSecs, meta.DurationSeconds)
	defer closeSession(sid)

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-analyzeduration", "200000",
		"-probesize", "65536",
	}
	if seekSecs > 0 {
		// -ss before -i = быстрый input-seek по keyframe.
		args = append(args, "-ss", fmt.Sprintf("%.3f", seekSecs))
	}
	outputArgs := []string{
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
		"-c:a", "ac3",
		"-b:a", fmt.Sprintf("%dk", tvAudioKbps),
		"-ac", strconv.Itoa(tvAudioChannels),
		"-ar", "48000",
		"-f", "mpegts",
		"-mpegts_flags", "+resend_headers",
		"-flush_packets", "1",
		"-muxdelay", "0",
		"-muxpreload", "0",
	}
	outputArgs = append(outputArgs, "pipe:1")
	args = append(args, outputArgs...)

	// Собственный context — чтобы гарантировано убить ffmpeg при отключении ТВ,
	// не дожидаясь пока он сам заметит EPIPE на stdout.
	cmdCtx, cancelCmd := context.WithCancel(r.Context())
	defer cancelCmd()
	handle := &tvStreamHandle{cancel: cancelCmd}

	t0 := time.Now()
	cmd := exec.CommandContext(cmdCtx, ffmpegExe, args...)
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
	// Публикуем handle ТОЛЬКО после успешного Start: до этого нечего отменять,
	// а параллельный запрос на тот же ключ мог бы «отменить» ещё не запущенный
	// процесс — безопасно, но грязно.
	tvFileStreams.Store(streamKey, handle)
	defer tvFileStreams.CompareAndDelete(streamKey, handle)
	if streamDebugEnabled {
		log.Printf("DBG tv_start file=%q ffmpeg_start=%s", requestedRelPath, time.Since(t0).Round(time.Millisecond))
	}

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		if stderr == nil {
			return
		}
		s := bufio.NewScanner(stderr)
		for s.Scan() {
			log.Printf("ffmpeg(tv) %s: %s", filepath.Base(requestedRelPath), s.Text())
		}
	}()

	log.Printf("📺 TV stream: %s (maxrate=%dM buf=%dM crf=%d ac3=%dk ch=%d)", requestedRelPath, tvVideoMaxrateMb, tvVideoBufsizeMb, tvVideoCRF, tvAudioKbps, tvAudioChannels)

	flusher, _ := w.(http.Flusher)

	started := time.Now()
	lastSaved := time.Now()
	firstByteLogged := false
	firstWrite := true
	copyBuf := make([]byte, pipeCopyBufSize)

	for {
		select {
		case <-r.Context().Done():
			// Клиент отвалился — выходим, defer убьёт ffmpeg.
			goto done
		default:
		}

		n, rErr := stdout.Read(copyBuf)
		if n > 0 {
			if !firstByteLogged {
				firstByteLogged = true
				if streamDebugEnabled {
					log.Printf("DBG tv_first_byte file=%q after=%s", requestedRelPath, time.Since(t0).Round(time.Millisecond))
				}
			}
			if _, wErr := w.Write(copyBuf[:n]); wErr != nil {
				goto done
			}
			// Priming Flush только на первой записи: ТВ получает первые байты
			// без задержки и стартует декодер. Дальше ядро/TCP сами решают,
			// когда отправлять — периодический Flush на каждый 64–256 KiB
			// убивал throughput по Wi-Fi (мелкие нервные пакеты).
			if firstWrite && flusher != nil {
				flusher.Flush()
				firstWrite = false
			}
			if time.Since(lastSaved) >= progressUpdateEvery {
				elapsed := seekSecs + time.Since(started).Seconds()
				recordProgressSeconds(progressKey, elapsed, meta.DurationSeconds)
				lastSaved = time.Now()
			}
		}
		if rErr != nil {
			break
		}
	}

done:
	elapsed := seekSecs + time.Since(started).Seconds()
	recordProgressSeconds(progressKey, elapsed, meta.DurationSeconds)
	// Гарантировано отменяем процесс и ждём окончания, чтобы не оставить
	// зомби-ffmpeg, который ещё держит CPU/диск.
	cancelCmd()
	<-stderrDone
	_ = cmd.Wait()
}

// serveResume отдаёт виртуальный файл "от сохранённой позиции до конца".
// HTTP Range в /resume/ трактуется относительно этой виртуальной длины, а
// физическое чтение идёт из исходного файла со смещением startPos.
func serveResume(w http.ResponseWriter, r *http.Request, filePath, requestedRelPath string) {
	key := progressKeyFromRelPath(requestedRelPath)
	entry, ok := getProgressEntry(key)
	if !ok {
		serveVideo(w, r, filePath, requestedRelPath)
		return
	}

	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		log.Printf("❌ ОШИБКА: Файл не найден: %s", requestedRelPath)
		http.NotFound(w, r)
		return
	}
	fileSize := info.Size()

	meta, _ := getVideoMetaCached(filePath)
	if meta.DurationSeconds <= 0 && entry.DurationSeconds > 0 {
		meta.DurationSeconds = entry.DurationSeconds
	}
	if meta.DurationSeconds <= 0 {
		meta = getVideoMetaWithTimeout(filePath, 300*time.Millisecond)
		if meta.DurationSeconds <= 0 {
			warmVideoMetaAsync(filePath)
		}
	}

	startPos := resumeStartByte(entry, fileSize, meta.DurationSeconds)
	if startPos <= 0 {
		serveVideo(w, r, filePath, requestedRelPath)
		return
	}

	virtualSize := fileSize - startPos
	if virtualSize <= 0 {
		serveVideo(w, r, filePath, requestedRelPath)
		return
	}

	relStart, relEnd := int64(0), virtualSize-1
	if rangeHdr := r.Header.Get("Range"); rangeHdr != "" {
		var ok bool
		relStart, relEnd, ok = parseRange(rangeHdr, virtualSize)
		if !ok {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", virtualSize))
			http.Error(w, "Неверный диапазон", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if relStart > 0 && relStart < resumeProbeRangeBytes {
			relStart = 0
			relEnd = virtualSize - 1
		}
	}

	absStart := startPos + relStart
	absEnd := startPos + relEnd
	length := relEnd - relStart + 1

	ctype, dlnaProfile := detectContentType(filePath)
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("TransferMode.DLNA.ORG", "Streaming")
	w.Header().Set("ContentFeatures.DLNA.ORG", dlnaProfile)
	w.Header().Set("Cache-Control", "no-transform")
	setDLNATimeSeekHeaders(w, meta.DurationSeconds)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", relStart, relEnd, virtualSize))
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	w.WriteHeader(http.StatusPartialContent)

	if r.Method == http.MethodHead {
		return
	}

	file, err := os.Open(filePath)
	if err != nil {
		log.Printf("❌ ОШИБКА: Файл не найден: %s", requestedRelPath)
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	if _, err := file.Seek(absStart, io.SeekStart); err != nil {
		http.Error(w, "Не удалось перемотать", http.StatusInternalServerError)
		return
	}

	log.Printf("▶️ RESUME: %s от байта %d", requestedRelPath, startPos)
	rStream, doneStream := withDirectPlaybackStream(r, filePath, absStart, absEnd, fileSize)
	defer doneStream()
	sid := openSession("resume", displayTitleFromRel(requestedRelPath), requestedRelPath, r.RemoteAddr, r.UserAgent(),
		float64(absStart)/float64(fileSize)*meta.DurationSeconds, meta.DurationSeconds)
	defer closeSession(sid)
	streamFile(w, rStream, file, atomic.AddUint64(&streamSeq, 1), requestedRelPath, key, absStart, absEnd, fileSize, meta.DurationSeconds)
}

// resumeRemuxContainer подбирает выходной контейнер для /resume/ ремукса по
// расширению исходного файла. Возвращает ffmpeg -f, Content-Type, DLNA features
// и дополнительные ffmpeg-аргументы. Если контейнер не поддерживает
// streaming-friendly remux в свой же формат — возвращает ok=false, и /resume/
// должен сделать fallback на прямой стрим.
//
// Сохранение контейнера критично для DLNA: ТВ ассоциирует <res> с конкретным
// типом и при подмене на MPEG-TS либо подставляет .mpg в имя файла, либо вовсе
// отказывается открыть. Соответствие protocolInfo в browse-ответе и Content-Type
// в /resume/ обязательно.
func resumeRemuxContainer(srcPath string) (ffmpegFmt, contentType, dlnaFeatures string, extraArgs []string, ok bool) {
	ext := strings.ToLower(filepath.Ext(srcPath))
	switch ext {
	case ".mp4", ".m4v":
		// Fragmented MP4: streaming-friendly, можно стартовать с произвольной
		// точки. empty_moov + default_base_moof + frag_keyframe — стандартный
		// набор для DLNA-стримов. frag_duration=1s даёт быстрый старт playback —
		// ТВ получает первый закрытый фрагмент в районе 1 секунды.
		return "mp4", "video/mp4",
			"DLNA.ORG_PN=AVC_MP4_HP_HD_24;DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000",
			[]string{"-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-frag_duration", "1000000"},
			true
	case ".mov":
		return "mp4", "video/quicktime",
			"DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000",
			[]string{"-movflags", "+frag_keyframe+empty_moov+default_base_moof", "-frag_duration", "1000000"},
			true
	case ".mkv":
		return "matroska", "video/x-matroska",
			"DLNA.ORG_PN=MATROSKA;DLNA.ORG_OP=01;DLNA.ORG_CI=0;DLNA.ORG_FLAGS=01700000000000000000000000000000",
			nil,
			true
	}
	// AVI и прочие — нет надёжного streaming remux в исходный формат.
	return "", "", "", nil, false
}

// resumeSeekSeconds выводит позицию возобновления в секундах из сохранённой
// записи прогресса. Предпочитает entry.Seconds (точно), иначе byte→time
// пересчёт с проверкой совпадения размера файла.
func resumeSeekSeconds(entry progressEntry, fileSize int64, durationSecs float64) float64 {
	if entry.Seconds > 0 {
		return entry.Seconds
	}
	if entry.Position > 0 && entry.Size > 0 && entry.Size == fileSize && durationSecs > 0 {
		return durationSecs * float64(entry.Position) / float64(entry.Size)
	}
	return 0
}

func canResumeFromEntry(entry progressEntry, fileSize int64, durationSecs float64) bool {
	return resumeStartByte(entry, fileSize, durationSecs) > 0
}

func resumeStartByte(entry progressEntry, fileSize int64, durationSecs float64) int64 {
	if fileSize <= 0 {
		return 0
	}
	if entry.Position > 0 && entry.Size > 0 && entry.Size == fileSize {
		if entry.Position >= fileSize {
			return 0
		}
		return entry.Position
	}
	if entry.Seconds > 0 && durationSecs > 0 {
		start := int64(float64(fileSize) * entry.Seconds / durationSecs)
		if start <= 0 || start >= fileSize {
			return 0
		}
		return start
	}
	return 0
}

// resumeSeekFromRange интерпретирует Range-заголовок как «seek с шкалы плеера»
// и возвращает новую позицию в секундах. Возвращает ok=false для случаев,
// которые НЕ являются реальным seek:
//   - пустой Range или bytes=0- (стартовый/initial fetch);
//   - закрытый узкий диапазон bytes=A-B с (B-A) < 1 MiB — типичный пробинг
//     контейнера от ТВ, не запрос «играть отсюда»;
//   - кандидат отличается от curSeekSecs на меньше чем resumeSeekMinDelta
//     (по модулю) — слишком близко к текущей точке, скорее всего повторный
//     fetch того же контента.
//
// curSeekSecs — позиция, с которой ffmpeg сейчас отдаёт поток. И вперёд,
// и назад дельта в модуле считается seek-ом: пользователь имеет право
// перематывать назад. Только idle re-probe рядом с текущей точкой
// отфильтровывается.
const (
	resumeProbeRangeBytes = 1 << 20 // 1 MiB
	resumeSeekMinDelta    = 3.0     // секунд (по модулю)
)

func resumeSeekFromRange(rangeHdr string, fileSize int64, durationSecs, curSeekSecs float64) (float64, bool) {
	if rangeHdr == "" || rangeHdr == "bytes=0-" || !strings.HasPrefix(rangeHdr, "bytes=") {
		return 0, false
	}
	if fileSize <= 0 || durationSecs <= 0 {
		return 0, false
	}
	startStr, endStr, _ := strings.Cut(strings.TrimPrefix(rangeHdr, "bytes="), "-")
	startByte, perr := strconv.ParseInt(startStr, 10, 64)
	if perr != nil || startByte <= 0 {
		return 0, false
	}
	// Закрытый узкий диапазон → это probe, а не seek.
	if endStr != "" {
		if endByte, eErr := strconv.ParseInt(endStr, 10, 64); eErr == nil && endByte >= startByte {
			if endByte-startByte+1 < resumeProbeRangeBytes {
				return 0, false
			}
		}
	}
	candidate := durationSecs * float64(startByte) / float64(fileSize)
	// Дельта по модулю — позволяем seek и вперёд, и назад.
	delta := candidate - curSeekSecs
	if delta < 0 {
		delta = -delta
	}
	if delta < resumeSeekMinDelta {
		return 0, false
	}
	return candidate, true
}

// remainingDurationSeconds возвращает «обрезанную» длительность для resume-стрима:
// сколько остаётся проиграть от seekSecs до конца файла. Так как ffmpeg ремукс
// без -copyts сбрасывает PTS к нулю, ТВ видит самостоятельный файл этой длительности.
// Возврат 0 — значит длительность неизвестна (тогда заголовок Content-Duration не ставим).
func remainingDurationSeconds(durationSecs, seekSecs float64) float64 {
	if durationSecs <= 0 {
		return 0
	}
	remaining := durationSecs - seekSecs
	if remaining < 0 {
		return 0
	}
	return remaining
}

// setDLNATimeSeekHeadersWithOffset проставляет TimeSeekRange.dlna.org c учётом
// позиции возобновления, чтобы ТВ показывал текущее время = seekSecs и полную
// длительность фильма.
func setDLNATimeSeekHeadersWithOffset(w http.ResponseWriter, durationSeconds, seekSeconds float64) {
	if durationSeconds <= 0 {
		return
	}
	dur := formatDLNADuration(durationSeconds)
	if dur == "" {
		return
	}
	if seekSeconds < 0 {
		seekSeconds = 0
	}
	if seekSeconds > durationSeconds {
		seekSeconds = durationSeconds
	}
	start := formatDLNADuration(seekSeconds)
	if start == "" {
		start = "00:00:00.000"
	}
	w.Header().Set("Content-Duration", dur)
	w.Header().Set("X-Content-Duration", fmt.Sprintf("%.3f", durationSeconds))
	w.Header().Set("X-AvailableSeekRange", fmt.Sprintf("1 npt=00:00:00.000-%s", dur))
	w.Header().Set("TimeSeekRange.dlna.org", fmt.Sprintf("npt=%s-%s/%s", start, dur, dur))
	w.Header().Set("X-Seek-Range", fmt.Sprintf("npt=%.0f-%.0f", seekSeconds, durationSeconds))
}

func parseTimeSeekRangeStart(header string, durationSeconds float64) (float64, bool) {
	header = strings.TrimSpace(header)
	if header == "" || durationSeconds <= 0 || !strings.HasPrefix(strings.ToLower(header), "npt=") {
		return 0, false
	}
	spec := strings.TrimSpace(header[len("npt="):])
	startToken, _, _ := strings.Cut(spec, "-")
	startToken, _, _ = strings.Cut(startToken, "/")
	seconds, ok := parseNPTSeconds(strings.TrimSpace(startToken))
	if !ok {
		return 0, false
	}
	if seconds < 0 {
		seconds = 0
	}
	if seconds > durationSeconds {
		seconds = durationSeconds
	}
	return seconds, true
}

func parseNPTSeconds(token string) (float64, bool) {
	if token == "" || strings.EqualFold(token, "now") {
		return 0, false
	}
	if !strings.Contains(token, ":") {
		seconds, err := strconv.ParseFloat(token, 64)
		return seconds, err == nil
	}
	parts := strings.Split(token, ":")
	if len(parts) != 3 {
		return 0, false
	}
	hours, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, false
	}
	minutes, err := strconv.ParseFloat(parts[1], 64)
	if err != nil {
		return 0, false
	}
	seconds, err := strconv.ParseFloat(parts[2], 64)
	if err != nil {
		return 0, false
	}
	return hours*3600 + minutes*60 + seconds, true
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
