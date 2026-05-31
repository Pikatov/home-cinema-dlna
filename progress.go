package main

import (
	"encoding/json"
	"fmt"
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

type progressSummary struct {
	Count       int
	LastUpdated time.Time
}

var progressStoreRef *progressStore

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

	// shouldPersistByteProgress сам фильтрует probe-запросы (минимум 1 MB
	// и 10 секунд по времени), поэтому отдельной P-8 защиты от перезаписи
	// Seconds-записи здесь не нужно — пробинг не пройдёт через persist-проверку.
	// Если же byte-progress всё-таки доходит сюда из реального /video/ -
	// пользователь играет файл «как есть», и новые байты должны выигрывать.

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

	newEntry := progressEntry{
		Position:        position,
		Size:            size,
		DurationSeconds: durationSeconds,
		Updated:         time.Now(),
	}
	if durationSeconds <= 0 && existing.Seconds > 0 {
		newEntry.Seconds = existing.Seconds
	}
	ps.data[key] = newEntry
	ps.RequestSave()
	maybeInvalidateBrowseCache(existing, newEntry, durationSeconds)
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

	newEntry := progressEntry{
		Seconds:         seconds,
		DurationSeconds: durationSeconds,
		Updated:         time.Now(),
	}
	if existing.Position > 0 && existing.Size > 0 {
		newEntry.Position = existing.Position
		newEntry.Size = existing.Size
	}
	ps.data[key] = newEntry
	ps.RequestSave()
	maybeInvalidateBrowseCache(existing, newEntry, durationSeconds)
}

func (ps *progressStore) GetEntry(key string) (progressEntry, bool) {
	ps.mu.RLock()
	entry, ok := ps.data[key]
	ps.mu.RUnlock()
	return entry, ok
}

func (ps *progressStore) Summary() progressSummary {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	summary := progressSummary{
		Count: len(ps.data),
	}
	for _, entry := range ps.data {
		if entry.Updated.After(summary.LastUpdated) {
			summary.LastUpdated = entry.Updated
		}
	}
	return summary
}

func (ps *progressStore) ClearAll() int {
	ps.mu.Lock()
	removed := len(ps.data)
	if removed > 0 {
		ps.data = make(map[string]progressEntry)
	}
	ps.mu.Unlock()

	if removed > 0 {
		ps.RequestSave()
		invalidateBrowseCache()
	}
	return removed
}

// PruneMissingFiles удаляет записи прогресса для файлов, которых нет под
// текущим mediaDir. Вызывается при смене папки и периодически — иначе
// устаревшие записи живут до Load()-cutoff (7 дней) и засоряют DLNA-каталог
// «фантомными» таймкодами.
func (ps *progressStore) PruneMissingFiles(mediaDir string) int {
	if mediaDir == "" {
		return 0
	}
	ps.mu.Lock()
	candidates := make([]string, 0, len(ps.data))
	for k := range ps.data {
		candidates = append(candidates, k)
	}
	ps.mu.Unlock()

	// Stat без mu, чтобы не блокировать стримы во время дискового I/O.
	missing := make([]string, 0)
	for _, k := range candidates {
		fullPath, ok := safeJoinUnderBase(mediaDir, k)
		if !ok {
			missing = append(missing, k)
			continue
		}
		if _, err := os.Stat(fullPath); err != nil {
			missing = append(missing, k)
		}
	}

	if len(missing) == 0 {
		return 0
	}

	ps.mu.Lock()
	removed := 0
	for _, k := range missing {
		if _, ok := ps.data[k]; ok {
			delete(ps.data, k)
			removed++
		}
	}
	ps.mu.Unlock()

	if removed > 0 {
		ps.RequestSave()
		invalidateBrowseCache()
	}
	return removed
}

func (ps *progressStore) Delete(key string) bool {
	if key == "" {
		return false
	}

	ps.mu.Lock()
	_, ok := ps.data[key]
	if ok {
		delete(ps.data, key)
	}
	ps.mu.Unlock()

	if ok {
		ps.RequestSave()
		invalidateBrowseCache()
	}
	return ok
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

func getProgressSummary() progressSummary {
	if progressStoreRef == nil {
		return progressSummary{}
	}
	return progressStoreRef.Summary()
}

func clearAllProgress() int {
	if progressStoreRef == nil {
		return 0
	}
	return progressStoreRef.ClearAll()
}

func deleteProgress(key string) bool {
	if progressStoreRef == nil {
		return false
	}
	return progressStoreRef.Delete(key)
}

func pruneMissingProgress(mediaDir string) int {
	if progressStoreRef == nil {
		return 0
	}
	return progressStoreRef.PruneMissingFiles(mediaDir)
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

// maybeInvalidateBrowseCache инвалидирует browse-cache только если у элемента
// поменялся ОТОБРАЖАЕМЫЙ таймкод (формат `h:mm:ss`, разрешение 1 с). Это
// устраняет тысячи лишних cache-bust'ов во время стрима: прогресс пишется
// раз в 250 мс – 3 с, но человек видит ту же строку «1:23:45», пока
// фактическая секунда не сменилась.
//
// При смене displayed-timecode перерендерить каталог нужно при следующем Browse,
// но нельзя слать ContentDirectory NOTIFY на каждый такой тик: часть ТВ на любое
// событие каталога заново опрашивает папку прямо во время playback, что выглядит
// как регулярный фриз примерно раз в 5 секунд.
func maybeInvalidateBrowseCache(old, new progressEntry, durationSecs float64) {
	if displayedTimecodeSeconds(old, durationSecs) == displayedTimecodeSeconds(new, durationSecs) {
		return
	}
	invalidateBrowseCacheQuiet()
}

// displayedTimecodeSeconds возвращает целочисленную секунду, в которую
// округляется отображаемый в DLNA-каталоге таймкод. Используется только для
// сравнения «нужно ли инвалидировать кеш каталога».
func displayedTimecodeSeconds(e progressEntry, durationSecs float64) int64 {
	if e.Seconds > 0 {
		return int64(e.Seconds)
	}
	if e.Position > 0 && e.Size > 0 && durationSecs > 0 {
		return int64(durationSecs * float64(e.Position) / float64(e.Size))
	}
	return 0
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
