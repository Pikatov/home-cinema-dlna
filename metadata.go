package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

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
	return getVideoMetaWithTimeout(filePath, 20*time.Second)
}

func getVideoMetaWithTimeout(filePath string, timeout time.Duration) videoMeta {
	metaCacheMu.RLock()
	if m, ok := metaCache[filePath]; ok {
		metaCacheMu.RUnlock()
		return m
	}
	metaCacheMu.RUnlock()

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

	var m videoMeta
	out, err := exec.CommandContext(ctx, ffprobeExe,
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
				case <-time.After(500 * time.Millisecond):
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
