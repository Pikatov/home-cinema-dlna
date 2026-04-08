package main

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type progressEntry struct {
	Position        int64     `json:"position,omitempty"`
	Size            int64     `json:"size,omitempty"`
	Seconds         float64   `json:"seconds,omitempty"`
	DurationSeconds float64   `json:"durationSeconds,omitempty"`
	Updated         time.Time `json:"updated"`
}

type progressStore struct {
	path      string
	mu        sync.RWMutex
	data      map[string]progressEntry
	saveCh    chan struct{}
	stopCh    chan struct{}
	doneCh    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
}

var progressStoreRef = newProgressStore(progressFile)

func newProgressStore(path string) *progressStore {
	return &progressStore{
		path:   path,
		data:   make(map[string]progressEntry),
		saveCh: make(chan struct{}, 1),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

func (ps *progressStore) Start() {
	ps.startOnce.Do(func() {
		go ps.run()
	})
}

func (ps *progressStore) Close() {
	ps.closeOnce.Do(func() {
		close(ps.stopCh)
		<-ps.doneCh
	})
}

func (ps *progressStore) Load() error {
	data, err := os.ReadFile(ps.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var loaded map[string]progressEntry
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	cutoff := time.Now().AddDate(0, 0, -7)
	cleaned := 0
	for k, v := range loaded {
		if v.Updated.Before(cutoff) {
			delete(loaded, k)
			cleaned++
		}
	}

	ps.mu.Lock()
	ps.data = loaded
	ps.mu.Unlock()

	if cleaned > 0 {
		log.Printf("🗑️ Очищено старых записей прогресса: %d", cleaned)
	}
	log.Printf("📖 Прогресс: %d файлов", len(loaded))
	return nil
}

func (ps *progressStore) SaveNow() error {
	ps.mu.RLock()
	snapshot := make(map[string]progressEntry, len(ps.data))
	for k, v := range ps.data {
		snapshot[k] = v
	}
	ps.mu.RUnlock()

	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return writeFileAtomic(ps.path, data, 0600)
}

func (ps *progressStore) RequestSave() {
	select {
	case ps.saveCh <- struct{}{}:
	default:
	}
}

func (ps *progressStore) run() {
	defer close(ps.doneCh)

	var (
		timer       *time.Timer
		timerC      <-chan time.Time
		lastWrite   time.Time
		pending     bool
		debounce    = 300 * time.Millisecond
		maxInterval = 5 * time.Second
	)

	stopTimer := func() {
		if timer == nil {
			return
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer = nil
		timerC = nil
	}

	for {
		select {
		case <-ps.saveCh:
			pending = true
			if !lastWrite.IsZero() && time.Since(lastWrite) >= maxInterval {
				if err := ps.SaveNow(); err != nil {
					log.Printf("⚠️ Не удалось сохранить прогресс: %v", err)
				} else {
					lastWrite = time.Now()
					pending = false
				}
				stopTimer()
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
				if err := ps.SaveNow(); err != nil {
					log.Printf("⚠️ Не удалось сохранить прогресс: %v", err)
				} else {
					lastWrite = time.Now()
					pending = false
				}
			}
			stopTimer()

		case <-ps.stopCh:
			stopTimer()
			if err := ps.SaveNow(); err != nil {
				log.Printf("⚠️ Финальное сохранение прогресса не удалось: %v", err)
			}
			return
		}
	}
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func (ps *progressStore) RecordBytes(key string, position, size int64, durationSeconds float64) {
	if key == "" || size <= 0 || position <= 0 {
		return
	}
	if position > size {
		position = size
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing := ps.data[key]
	if durationSeconds <= 0 {
		durationSeconds = existing.DurationSeconds
	}

	if shouldClearProgressByBytes(position, size, durationSeconds) {
		if _, ok := ps.data[key]; ok {
			delete(ps.data, key)
			ps.RequestSave()
			invalidateBrowseCache()
		}
		return
	}
	if !shouldPersistByteProgress(position, size, durationSeconds) {
		return
	}
	if shouldIgnoreByteRegression(existing, position, size, durationSeconds) {
		return
	}

	ps.data[key] = progressEntry{
		Position:        position,
		Size:            size,
		DurationSeconds: durationSeconds,
		Updated:         time.Now(),
	}
	ps.RequestSave()
	invalidateBrowseCache()
}

func (ps *progressStore) RecordSeconds(key string, seconds float64, durationSeconds float64) {
	if key == "" {
		return
	}

	ps.mu.Lock()
	defer ps.mu.Unlock()

	existing := ps.data[key]
	if durationSeconds <= 0 {
		durationSeconds = existing.DurationSeconds
	}

	if shouldClearProgressBySeconds(seconds, durationSeconds) {
		if _, ok := ps.data[key]; ok {
			delete(ps.data, key)
			ps.RequestSave()
			invalidateBrowseCache()
		}
		return
	}
	if !shouldPersistSecondsProgress(seconds, durationSeconds) {
		return
	}
	if shouldIgnoreSecondsRegression(existing, seconds) {
		return
	}

	ps.data[key] = progressEntry{
		Seconds:         seconds,
		DurationSeconds: durationSeconds,
		Updated:         time.Now(),
	}
	ps.RequestSave()
	invalidateBrowseCache()
}

func (ps *progressStore) GetEntry(key string) (progressEntry, bool) {
	ps.mu.RLock()
	entry, ok := ps.data[key]
	ps.mu.RUnlock()
	return entry, ok
}

func shouldPersistByteProgress(position, size int64, durationSeconds float64) bool {
	if size <= 0 || position <= 0 || position >= size {
		return false
	}
	if shouldClearProgressByBytes(position, size, durationSeconds) {
		return false
	}
	if durationSeconds > 0 {
		if durationSeconds*float64(position)/float64(size) < 10 {
			return false
		}
	}
	minBytes := int64(1 * 1024 * 1024)
	if durationSeconds <= 0 {
		minBytes = 4 * 1024 * 1024
	}
	if scaled := size / 1000; scaled > minBytes {
		minBytes = scaled
	}
	return position >= minBytes
}

func shouldClearProgressByBytes(position, size int64, durationSeconds float64) bool {
	if size <= 0 || position <= 0 {
		return false
	}
	if position >= size {
		return true
	}
	if durationSeconds > 0 {
		remaining := durationSeconds - (durationSeconds * float64(position) / float64(size))
		if remaining <= 30 {
			return true
		}
	}
	return position >= size-(size/50)
}

func shouldIgnoreByteRegression(existing progressEntry, candidatePos, size int64, durationSeconds float64) bool {
	if existing.Position <= 0 || existing.Size != size || candidatePos >= existing.Position {
		return false
	}

	allowedBacktrack := int64(8 * 1024 * 1024)
	if pct := size / 20; pct > allowedBacktrack {
		allowedBacktrack = pct
	}
	if durationSeconds > 0 {
		bytesFor120s := int64(float64(size) * 120 / durationSeconds)
		if bytesFor120s > 0 && bytesFor120s < allowedBacktrack {
			allowedBacktrack = bytesFor120s
		}
	}
	return existing.Position-candidatePos > allowedBacktrack
}

func shouldPersistSecondsProgress(seconds, durationSeconds float64) bool {
	if seconds < 3 {
		return false
	}
	return !shouldClearProgressBySeconds(seconds, durationSeconds)
}

func shouldClearProgressBySeconds(seconds, durationSeconds float64) bool {
	if durationSeconds <= 0 {
		return false
	}
	return seconds >= durationSeconds-5
}

func shouldIgnoreSecondsRegression(existing progressEntry, seconds float64) bool {
	if existing.Seconds <= 0 || seconds >= existing.Seconds {
		return false
	}
	return existing.Seconds-seconds > 120
}

func loadProgress() {
	if err := progressStoreRef.Load(); err != nil {
		log.Printf("⚠️ Не удалось загрузить прогресс: %v", err)
	}
}

func saveProgress() {
	if err := progressStoreRef.SaveNow(); err != nil {
		log.Printf("⚠️ Не удалось сохранить прогресс: %v", err)
	}
}

func requestProgressSave() {
	progressStoreRef.RequestSave()
}

func runProgressSaver() {
	progressStoreRef.Start()
}

func closeProgressStore() {
	if progressStoreRef != nil {
		progressStoreRef.Close()
	}
}

func recordProgressBytes(key string, position, size int64, durationSeconds float64) {
	progressStoreRef.RecordBytes(key, position, size, durationSeconds)
}

func recordProgressSeconds(key string, seconds float64, durationSeconds float64) {
	progressStoreRef.RecordSeconds(key, seconds, durationSeconds)
}

func getProgressEntry(key string) (progressEntry, bool) {
	return progressStoreRef.GetEntry(key)
}

func formatTimecode(pos, size int64, durationSecs float64) string {
	if durationSecs <= 0 || size <= 0 {
		return ""
	}
	secs := durationSecs * float64(pos) / float64(size)
	h := int(secs) / 3600
	m := (int(secs) % 3600) / 60
	s := int(secs) % 60
	if h > 0 {
		return sprintf("%d:%02d:%02d", h, m, s)
	}
	return sprintf("%d:%02d", m, s)
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
		return sprintf("%d:%02d:%02d", h, m, s)
	}
	return sprintf("%d:%02d", m, s)
}

func progressKeyFromRelPath(rel string) string {
	rel = stringsTrimDefault(rel, "")
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
		entry, ok = getProgressEntry(filepath.Base(key))
	}
	if !ok {
		return ""
	}

	if durationSecs <= 0 {
		durationSecs = entry.DurationSeconds
	}
	if entry.Seconds > 0 {
		return formatSecondsTimecode(entry.Seconds)
	}
	if entry.Position > 0 && entry.Size == size && durationSecs > 0 {
		return formatTimecode(entry.Position, size, durationSecs)
	}
	return ""
}
