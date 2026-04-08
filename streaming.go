package main

import (
	"bufio"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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
	return sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
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
	w.Header().Set("TimeSeekRange.dlna.org", sprintf("npt=00:00:00.000-%s/%s", dur, dur))
	w.Header().Set("X-Seek-Range", sprintf("npt=0-%.0f", durationSeconds))
}

var streamBufSize = 4 * 1024 * 1024

var streamBufPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, streamBufSize)
	},
}

var progressUpdateEvery = 3 * time.Second

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
		meta = getVideoMetaWithTimeout(filePath, 750*time.Millisecond)
		if meta.DurationSeconds <= 0 {
			warmVideoMetaAsync(filePath)
		}
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
				extra = sprintf(" hdr{transfer=%q getfeat=%q timeseek=%q conn=%q}",
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
		streamFile(w, r, file, reqID, requestedRelPath, progressKeyFromRelPath(requestedRelPath), 0, info.Size()-1, info.Size(), meta.DurationSeconds)
		return
	}

	start, end, ok := parseRange(rangeHdr, info.Size())
	if !ok {
		w.Header().Set("Content-Range", sprintf("bytes */%d", info.Size()))
		if streamDebugEnabled {
			extra := ""
			if streamDebugHeaders {
				extra = sprintf(" hdr{transfer=%q getfeat=%q timeseek=%q conn=%q}",
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
	w.Header().Set("Content-Range", sprintf("bytes %d-%d/%d", start, end, info.Size()))
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
			extra = sprintf(" hdr{transfer=%q getfeat=%q timeseek=%q conn=%q}",
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
	streamFile(w, r, file, reqID, requestedRelPath, progressKeyFromRelPath(requestedRelPath), start, end, info.Size(), meta.DurationSeconds)
}

func streamFile(w http.ResponseWriter, r *http.Request, file *os.File, reqID uint64, requestedRelPath, progressKey string, start, end, totalSize int64, durationSeconds float64) {
	length := end - start + 1
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
		chunkCount   int64
		minChunkMbps = math.Inf(1)
		maxChunkMbps float64
	)

	defer func() {
		if !isProbe {
			recordProgressBytes(progressKey, written, totalSize, durationSeconds)
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
				errStr = sprintf(" err=%q", endErr.Error())
			}
			minStr := "n/a"
			if chunkCount > 0 && minChunkMbps != math.Inf(1) {
				minStr = sprintf("%.2f", minChunkMbps)
			}
			log.Printf("DBG stream#%d done file=%q sent=%d dur=%s avg_mbps=%.2f reason=%s%s", reqID, requestedRelPath, sent, dur.Round(time.Millisecond), mbps, endReason, errStr)
			log.Printf("DBG stream#%d stats file=%q chunks=%d min_chunk_mbps=%s max_chunk_mbps=%.2f buf=%d", reqID, requestedRelPath, chunkCount, minStr, maxChunkMbps, len(buf))
		}
	}()

	for remaining > 0 {
		chunkSize := int64(len(buf))
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
			log.Printf("DBG stream#%d slow_read=%s file=%q off=%d want=%d got=%d", reqID, readDur.Round(time.Millisecond), requestedRelPath, written, chunkSize, n)
		}

		if n > 0 {
			writeStart := time.Now()
			_, wErr := w.Write(buf[:n])
			writeDur := time.Since(writeStart)
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
			if streamDebugEnabled && writeDur > streamSlowWrite {
				curSent := written - start
				curDur := time.Since(started)
				curMbps := 0.0
				if curDur > 0 {
					curMbps = (float64(curSent) * 8) / curDur.Seconds() / 1e6
				}
				log.Printf("DBG stream#%d slow_write=%s file=%q off=%d n=%d rem=%d avg_mbps=%.2f", reqID, writeDur.Round(time.Millisecond), requestedRelPath, written, n, remaining, curMbps)
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
				return
			}
			if flusher != nil {
				flusher.Flush()
			}

			written += int64(n)
			remaining -= int64(n)

			if !isProbe && time.Since(lastSaved) > progressUpdateEvery {
				recordProgressBytes(progressKey, written, totalSize, durationSeconds)
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
		warmVideoMetaAsync(filePath)
	}
	progressKey := progressKeyFromRelPath(requestedRelPath)

	w.Header().Set("Content-Type", tvContentType)
	w.Header().Set("transferMode.dlna.org", "Streaming")
	w.Header().Set("TransferMode.dlna.org", "Streaming")
	w.Header().Set("contentFeatures.dlna.org", tvDLNAFeatures)
	w.Header().Set("ContentFeatures.dlna.org", tvDLNAFeatures)
	w.Header().Set("Cache-Control", "no-transform")

	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}

	if r.Header.Get("Range") != "" {
		logOnce("⚠️ TV stream: игнор Range для %s", requestedRelPath)
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
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
		"-maxrate", sprintf("%dM", tvVideoMaxrateMb),
		"-bufsize", sprintf("%dM", tvVideoBufsizeMb),
		"-c:a", "aac",
		"-b:a", sprintf("%dk", tvAudioKbps),
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

	started := time.Now()
	lastSaved := time.Now()
	firstByteLogged := false
	copyBuf := make([]byte, 64*1024)

loop:
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
			break loop
		default:
		}
	}

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
