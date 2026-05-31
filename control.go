package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type statusPayload struct {
	Name              string `json:"name"`
	Version           string `json:"version"`
	MediaDir          string `json:"mediaDir,omitempty"`
	MediaName         string `json:"mediaDirName"`
	Endpoint          string `json:"endpoint"`
	StartedAt         string `json:"startedAt"`
	ProgressCount     int    `json:"progressCount"`
	ProgressUpdatedAt string `json:"progressUpdatedAt,omitempty"`
	ActiveStreams     int    `json:"activeStreams"`
}

func statusHandler(w http.ResponseWriter, r *http.Request, ip string) {
	mediaDir := getMediaDir()
	progress := getProgressSummary()
	payload := statusPayload{
		Name:          friendlyName,
		Version:       appVersion,
		MediaName:     filepath.Base(mediaDir),
		Endpoint:      fmt.Sprintf("http://%s:%s", ip, serverPort),
		StartedAt:     startedAt.Format(time.RFC3339),
		ProgressCount: progress.Count,
		ActiveStreams: int(activeStreamCount()),
	}
	if !progress.LastUpdated.IsZero() {
		payload.ProgressUpdatedAt = progress.LastUpdated.Format(time.RFC3339)
	}
	if isLocalRequest(r) {
		payload.MediaDir = mediaDir
	}
	respondJSON(w, http.StatusOK, payload)
}

// statsPayload — лёгкий ответ для UI-poll'а. Без редактируемых полей и без
// раскрытия абсолютных путей, чтобы безопасно отдавать без localhost-проверки.
type statsPayload struct {
	ActiveStreams int          `json:"activeStreams"`
	ProgressCount int          `json:"progressCount"`
	Version       string       `json:"version"`
	StartedAt     string       `json:"startedAt"`
	Sessions      []sessionDTO `json:"sessions"`
}

// sessionDTO — публичная версия activeSession для /stats: ElapsedSeconds
// вычисляется на лету как (Now − StartedAt) + SeekSeconds, чтобы UI получал
// готовый «текущий таймкод» без своего таймера.
type sessionDTO struct {
	ID              uint64  `json:"id"`
	Kind            string  `json:"kind"`
	Title           string  `json:"title"`
	Client          string  `json:"client"`
	Device          string  `json:"device"`
	StartedAt       string  `json:"startedAt"`
	ElapsedSeconds  float64 `json:"elapsedSeconds"`
	DurationSeconds float64 `json:"durationSeconds"`
}

func handleStats(w http.ResponseWriter, _ *http.Request) {
	raw := snapshotSessions()
	now := time.Now()
	dtos := make([]sessionDTO, 0, len(raw))
	for _, s := range raw {
		elapsed := s.SeekSeconds + now.Sub(s.StartedAt).Seconds()
		dtos = append(dtos, sessionDTO{
			ID:              s.ID,
			Kind:            s.Kind,
			Title:           s.Title,
			Client:          s.Client,
			Device:          s.Device,
			StartedAt:       s.StartedAt.Format(time.RFC3339),
			ElapsedSeconds:  elapsed,
			DurationSeconds: s.DurationSeconds,
		})
	}
	respondJSON(w, http.StatusOK, statsPayload{
		// Считаем по списку сессий (тут уже исключены probe-Range и HEAD).
		// activeStreamCount() считал бы и зондирующие запросы.
		ActiveStreams: len(dtos),
		ProgressCount: getProgressSummary().Count,
		Version:       appVersion,
		StartedAt:     startedAt.Format(time.RFC3339),
		Sessions:      dtos,
	})
}

func handleFolderSelection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"status":  "error",
			"message": "Используйте POST /set-media-dir.",
		})
		return
	}
	if !remoteControlOK && !isLocalRequest(r) {
		respondJSON(w, http.StatusForbidden, map[string]string{
			"status":  "error",
			"message": "Доступ запрещён (только localhost). Запустите с --allow-remote-control если уверены в сети.",
		})
		return
	}

	if err := r.ParseForm(); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"status":  "error",
			"message": "Не удалось прочитать форму запроса.",
		})
		return
	}

	candidate := strings.TrimSpace(r.FormValue("mediaDir"))
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
	clearMetaCache()
	invalidateBrowseCache()
	// Удаляем записи прогресса для файлов, которых нет в новой папке —
	// иначе DLNA-каталог покажет «фантомные» таймкоды.
	if pruned := pruneMissingProgress(candidate); pruned > 0 {
		log.Printf("🗑️ Прогресс: удалено %d записей для отсутствующих файлов", pruned)
	}
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

func handleResetProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"status":  "error",
			"message": "Используйте POST /reset-progress.",
		})
		return
	}
	if !remoteControlOK && !isLocalRequest(r) {
		respondJSON(w, http.StatusForbidden, map[string]string{
			"status":  "error",
			"message": "Доступ запрещён (только localhost).",
		})
		return
	}

	cleared := clearAllProgress()
	saveProgress()

	message := "Сохранённый прогресс не найден."
	if cleared > 0 {
		message = fmt.Sprintf("Сброшен прогресс для %d фильмов.", cleared)
	}
	log.Printf("🧹 Сброс прогресса: %d записей", cleared)

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": message,
		"cleared": cleared,
	})
}

func handleDeleteProgress(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"status":  "error",
			"message": "Используйте POST /delete-progress.",
		})
		return
	}
	if !remoteControlOK && !isLocalRequest(r) {
		respondJSON(w, http.StatusForbidden, map[string]string{
			"status":  "error",
			"message": "Доступ запрещён (только localhost).",
		})
		return
	}
	if err := r.ParseForm(); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"status":  "error",
			"message": "Не удалось прочитать форму запроса.",
		})
		return
	}

	key := strings.TrimSpace(r.FormValue("key"))
	if key == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{
			"status":  "error",
			"message": "Не указан ключ прогресса.",
		})
		return
	}

	if !deleteProgress(key) {
		respondJSON(w, http.StatusNotFound, map[string]interface{}{
			"status":  "error",
			"message": "Запись прогресса не найдена.",
			"cleared": 0,
		})
		return
	}
	saveProgress()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": "Прогресс файла удалён.",
		"cleared": 1,
	})
}
