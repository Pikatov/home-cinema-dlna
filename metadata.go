package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func toolCandidates(name, envName string) []string {
	candidates := make([]string, 0, 6)
	if v := strings.TrimSpace(os.Getenv(envName)); v != "" {
		candidates = append(candidates, v)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, name),
			filepath.Join(exeDir, "..", "Resources", "bin", name),
		)
	}
	candidates = append(candidates,
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
		filepath.Join("/usr/bin", name),
		name,
	)
	return candidates
}

func ffprobeBin() string {
	for _, p := range toolCandidates("ffprobe", "HOMECINEMA_FFPROBE") {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "ffprobe"
}

var ffprobeExe = ffprobeBin()

func ffmpegBin() string {
	for _, p := range toolCandidates("ffmpeg", "HOMECINEMA_FFMPEG") {
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

	candidates := []string{bin}
	switch filepath.Base(bin) {
	case "ffmpeg":
		candidates = append(candidates, toolCandidates("ffmpeg", "HOMECINEMA_FFMPEG")...)
	case "ffprobe":
		candidates = append(candidates, toolCandidates("ffprobe", "HOMECINEMA_FFPROBE")...)
	}

	seen := make(map[string]bool, len(candidates))
	for _, raw := range candidates {
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true

		candidate := raw
		if strings.ContainsRune(raw, filepath.Separator) {
			if !isExecutable(raw) {
				continue
			}
		} else {
			p, err := exec.LookPath(raw)
			if err != nil {
				continue
			}
			candidate = p
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := exec.CommandContext(ctx, candidate, "-version").Run()
		cancel()
		if err != nil {
			log.Printf("⚠️ %s найден, но не запускается: %v", candidate, err)
			continue
		}

		return candidate, true
	}
	return "", false
}

func probeErrorString(err error, out []byte) string {
	if err == nil {
		return ""
	}
	msg := strings.TrimSpace(string(out))
	if msg == "" {
		msg = err.Error()
	} else {
		msg = fmt.Sprintf("%v: %s", err, msg)
	}
	return msg
}

type videoMeta struct {
	DurationSeconds float64
	// VideoCodec/AudioCodec/PixFmt заполняются только полным ffprobe (не
	// быстрым бинарным парсером Matroska) — см. probeFormatAndCodecs.
	VideoCodec string
	AudioCodec string
	PixFmt     string
	// CodecProbed отличает «длительность нашли быстрым парсером/ffprobe
	// с истёкшим таймаутом» (кодек ещё неизвестен) от «полный ffprobe
	// отработал» — без этого флага warmVideoMetaAsync считал бы запись
	// готовой сразу после матрёшечного fast-path и никогда не подтягивал
	// бы кодек в фоне.
	CodecProbed bool
	// LastProbedAt — момент последнего ffprobe для файла. Используется чтобы
	// не перепрашивать битые файлы (DurationSeconds==0) на каждом запросе —
	// см. metaNegativeTTL и warmVideoMetaAsync.
	LastProbedAt time.Time
}

// metaNegativeTTL — как долго не дёргать ffprobe повторно для файлов,
// у которых длительность не определилась (битый контейнер, отсутствие
// видеопотока). Положительные результаты живут «вечно» (до clearMetaCache).
const metaNegativeTTL = 5 * time.Minute

// codecProbeMinTimeout — минимальный бюджет времени, при котором мы вообще
// пытаемся довыполнить ffprobe за кодеком после быстрого бинарного парсера
// длительности Matroska. Короткие синхронные вызовы (/resume/ с 300 мс)
// не должны спотыкаться о запуск ffprobe — кодек в таком случае донабирается
// позже через warmVideoMetaAsync/warmupMetaCache.
const codecProbeMinTimeout = 1 * time.Second

var (
	metaCacheMu sync.RWMutex
	metaCache   = make(map[string]videoMeta)
)

var (
	metaWarmMu  sync.Mutex
	metaWarm    = make(map[string]bool)
	metaWarmSem = make(chan struct{}, 2)
)

var (
	warmupMu     sync.Mutex
	warmupCancel context.CancelFunc
	warmupGen    uint64
)

func getVideoMetaCached(filePath string) (videoMeta, bool) {
	metaCacheMu.RLock()
	m, ok := metaCache[filePath]
	metaCacheMu.RUnlock()
	return m, ok
}

// clearMetaCache drops all cached durations. Call when the media directory changes
// so stale entries from the old directory do not bleed into the new one.
func clearMetaCache() {
	metaCacheMu.Lock()
	metaCache = make(map[string]videoMeta)
	metaCacheMu.Unlock()
}

func warmVideoMetaAsync(filePath string) {
	if filePath == "" {
		return
	}
	if m, ok := getVideoMetaCached(filePath); ok {
		// Полностью готовый положительный кеш (длительность + кодек) — выходим.
		if m.DurationSeconds > 0 && m.CodecProbed {
			return
		}
		// Отрицательный кеш в пределах TTL — тоже выходим, чтобы битый файл
		// не дёргал ffprobe на каждом запросе.
		if m.DurationSeconds <= 0 && !m.LastProbedAt.IsZero() && time.Since(m.LastProbedAt) < metaNegativeTTL {
			return
		}
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
	return getVideoMetaWithTimeout(filePath, 20*time.Second)
}

func getVideoMetaWithTimeout(filePath string, timeout time.Duration) videoMeta {
	metaCacheMu.RLock()
	cached, hasCached := metaCache[filePath]
	metaCacheMu.RUnlock()

	if hasCached {
		// Полностью готовый положительный кеш — отдаём. Отрицательный — отдаём
		// в пределах TTL, чтобы битые файлы не запускали новый ffprobe на
		// каждом запросе.
		if cached.DurationSeconds > 0 && cached.CodecProbed {
			return cached
		}
		if cached.DurationSeconds <= 0 && !cached.LastProbedAt.IsZero() && time.Since(cached.LastProbedAt) < metaNegativeTTL {
			return cached
		}
	}

	if !hasCached {
		if m, ok := getContainerDuration(filePath); ok {
			metaCacheMu.Lock()
			metaCache[filePath] = m
			metaCacheMu.Unlock()
			cached = m
			hasCached = true
			// Кодек этот быстрый парсер не знает. Короткие синхронные вызовы
			// не должны ждать ffprobe ради него — он довыполнится позже через
			// warmVideoMetaAsync/warmupMetaCache.
			if timeout > 0 && timeout < codecProbeMinTimeout {
				return m
			}
		}
	}

	if ffprobeExe == "" {
		if hasCached {
			return cached
		}
		return videoMeta{LastProbedAt: time.Now()}
	}

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	probed, ok := probeFormatAndCodecs(ctx, filePath)
	if !ok {
		// ffprobe не отработал (таймаут/ошибка) — сохраняем то, что уже
		// знаем (например, длительность от быстрого парсера), и помечаем
		// момент попытки для отрицательного TTL.
		result := cached
		result.LastProbedAt = time.Now()
		metaCacheMu.Lock()
		metaCache[filePath] = result
		metaCacheMu.Unlock()
		return result
	}

	if probed.DurationSeconds <= 0 && cached.DurationSeconds > 0 {
		probed.DurationSeconds = cached.DurationSeconds
	}
	probed.CodecProbed = true
	probed.LastProbedAt = time.Now()
	metaCacheMu.Lock()
	metaCache[filePath] = probed
	metaCacheMu.Unlock()
	return probed
}

// ffprobeStreamInfo — то, что нам нужно из ffprobe -show_streams в JSON:
// тип и имя кодека, плюс pix_fmt для определения 10-бит видео.
type ffprobeStreamInfo struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	PixFmt    string `json:"pix_fmt"`
}

type ffprobeFormatInfo struct {
	Duration string `json:"duration"`
}

type ffprobeOutput struct {
	Streams []ffprobeStreamInfo `json:"streams"`
	Format  ffprobeFormatInfo   `json:"format"`
}

// probeFormatAndCodecs запускает один ffprobe-вызов и достаёт длительность
// вместе с кодеком первого видео- и аудиопотока. Кодек нужен, чтобы решать,
// сможет ли ТВ декодировать файл напрямую (см. shouldTranscodeForTVCompatibility) —
// одного расширения/имени файла недостаточно: многие рипы не помечают в
// имени ни HEVC, ни 10-бит, хотя именно это чаще всего мешает встроенному
// декодеру ТВ открыть такой MKV.
func probeFormatAndCodecs(ctx context.Context, filePath string) (videoMeta, bool) {
	out, err := exec.CommandContext(ctx, ffprobeExe,
		"-v", "error",
		"-show_entries", "format=duration:stream=codec_type,codec_name,pix_fmt",
		"-print_format", "json",
		filePath,
	).CombinedOutput()
	if err != nil {
		if streamDebugEnabled {
			log.Printf("DBG ffprobe failed file=%q err=%s", filePath, probeErrorString(err, out))
		}
		return videoMeta{}, false
	}

	var parsed ffprobeOutput
	if jsonErr := json.Unmarshal(out, &parsed); jsonErr != nil {
		if streamDebugEnabled {
			log.Printf("DBG ffprobe json parse failed file=%q err=%v", filePath, jsonErr)
		}
		return videoMeta{}, false
	}

	m := videoMeta{}
	if secs, convErr := strconv.ParseFloat(strings.TrimSpace(parsed.Format.Duration), 64); convErr == nil && secs > 0 {
		m.DurationSeconds = secs
	}
	for _, s := range parsed.Streams {
		switch s.CodecType {
		case "video":
			if m.VideoCodec == "" {
				m.VideoCodec = s.CodecName
				m.PixFmt = s.PixFmt
			}
		case "audio":
			if m.AudioCodec == "" {
				m.AudioCodec = s.CodecName
			}
		}
	}
	return m, true
}

func getContainerDuration(filePath string) (videoMeta, bool) {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".mkv", ".webm":
		return getMatroskaDuration(filePath)
	default:
		return videoMeta{}, false
	}
}

func getMatroskaDuration(filePath string) (videoMeta, bool) {
	f, err := os.Open(filePath)
	if err != nil {
		return videoMeta{}, false
	}
	defer f.Close()

	const maxHeader = 32 << 20
	buf := make([]byte, maxHeader)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if len(buf) == 0 {
		return videoMeta{}, false
	}

	scale := float64(1000000) // Matroska default TimecodeScale, nanoseconds.
	if i := bytes.Index(buf, []byte{0x2A, 0xD7, 0xB1}); i >= 0 {
		if value, ok := readEBMLUnsigned(buf[i+3:]); ok && value > 0 {
			scale = float64(value)
		}
	}

	if i := bytes.Index(buf, []byte{0x44, 0x89}); i >= 0 {
		if value, ok := readEBMLFloat(buf[i+2:]); ok && value > 0 {
			return videoMeta{
				DurationSeconds: value * scale / 1e9,
				LastProbedAt:    time.Now(),
			}, true
		}
	}
	return videoMeta{}, false
}

func readEBMLUnsigned(data []byte) (uint64, bool) {
	size, sizeLen, ok := readEBMLSize(data)
	if !ok || size == 0 || size > 8 || len(data) < sizeLen+int(size) {
		return 0, false
	}
	var value uint64
	for _, b := range data[sizeLen : sizeLen+int(size)] {
		value = (value << 8) | uint64(b)
	}
	return value, true
}

func readEBMLFloat(data []byte) (float64, bool) {
	size, sizeLen, ok := readEBMLSize(data)
	if !ok || len(data) < sizeLen+int(size) {
		return 0, false
	}
	value := data[sizeLen : sizeLen+int(size)]
	switch size {
	case 4:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(value))), true
	case 8:
		return math.Float64frombits(binary.BigEndian.Uint64(value)), true
	default:
		return 0, false
	}
}

func readEBMLSize(data []byte) (uint64, int, bool) {
	if len(data) == 0 {
		return 0, 0, false
	}
	first := data[0]
	mask := byte(0x80)
	length := 1
	for length <= 8 && first&mask == 0 {
		mask >>= 1
		length++
	}
	if length > 8 || len(data) < length {
		return 0, 0, false
	}
	value := uint64(first &^ mask)
	for i := 1; i < length; i++ {
		value = (value << 8) | uint64(data[i])
	}
	return value, length, true
}

func stopWarmupMeta() {
	warmupMu.Lock()
	cancel := warmupCancel
	warmupCancel = nil
	warmupMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func warmupMetaCache(dir string) {
	if !warmupMetaEnabled {
		return
	}
	if ffprobeExe == "" {
		return
	}

	stopWarmupMeta()

	ctx, cancel := context.WithCancel(context.Background())
	warmupMu.Lock()
	warmupGen++
	gen := warmupGen
	warmupCancel = cancel
	warmupMu.Unlock()

	go func() {
		defer func() {
			warmupMu.Lock()
			if warmupGen == gen {
				warmupCancel = nil
			}
			warmupMu.Unlock()
		}()

		errStop := errors.New("warmup done")
		count := 0
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d == nil || d.IsDir() {
				return nil
			}
			if ctx.Err() != nil {
				return errStop
			}
			if !isVideoExt(filepath.Ext(path)) {
				return nil
			}
			for atomic.LoadInt64(&activeStreamRequests) > 0 {
				select {
				case <-ctx.Done():
					return errStop
				case <-streamIdleC:
					// a stream ended; re-check at top of loop
				case <-time.After(5 * time.Second):
					// safety fallback in case signal was missed
				}
			}
			getVideoMeta(path)
			count++
			if warmupMetaThrottle > 0 {
				select {
				case <-ctx.Done():
					return errStop
				case <-time.After(warmupMetaThrottle):
				}
			}
			if warmupMetaMaxFiles > 0 && count >= warmupMetaMaxFiles {
				return errStop
			}
			if streamDebugEnabled && count%50 == 0 {
				log.Printf("DBG warmup: %d файлов (в процессе) в %s", count, redactPath(dir))
			}
			return nil
		})
		if err != nil && !errors.Is(err, errStop) {
			log.Printf("⚠️ warmup ffprobe: %v", err)
		}
		if ctx.Err() == nil {
			log.Printf("✅ ffprobe кеш: %d файлов в %s", count, redactPath(dir))
		}
	}()
}

// isHEVCVideoCodec сообщает, декодирует ли ffprobe видеопоток как HEVC/H.265 —
// именно этот кодек чаще всего не тянут встроенные декодеры ТВ старше
// нескольких лет, даже когда рип не помечен как h265/hevc в имени файла.
func isHEVCVideoCodec(codec string) bool {
	codec = strings.ToLower(strings.TrimSpace(codec))
	return codec == "hevc" || codec == "h265"
}

// isHighBitDepthPixFmt сообщает про 10-бит (и выше) видео — ffprobe отдаёт
// это как часть pix_fmt (yuv420p10le, yuv422p10le, yuv420p12le и т.п.),
// а не как отдельное поле.
func isHighBitDepthPixFmt(pixFmt string) bool {
	pixFmt = strings.ToLower(strings.TrimSpace(pixFmt))
	if pixFmt == "" {
		return false
	}
	return strings.Contains(pixFmt, "p10") || strings.Contains(pixFmt, "p12") || strings.Contains(pixFmt, "p16")
}

// isTVIncompatibleAudioCodec — консервативный список аудиокодеков, которые
// декодер большинства DLNA-ТВ (при прямой отдаче файла, не HDMI-passthrough
// через AVR) не умеет проигрывать. Видео при этом может быть полностью
// совместимым — сам факт такого аудио уже достаточная причина форсировать
// TV-поток (ffmpeg перекодирует звук в AC3).
func isTVIncompatibleAudioCodec(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "dts", "dts_hd", "truehd", "mlp", "flac", "opus":
		return true
	default:
		return false
	}
}
