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
	MediaDir          string `json:"mediaDir,omitempty"`
	MediaName         string `json:"mediaDirName"`
	Endpoint          string `json:"endpoint"`
	StartedAt         string `json:"startedAt"`
	ProgressCount     int    `json:"progressCount"`
	ProgressUpdatedAt string `json:"progressUpdatedAt,omitempty"`
}

func statusHandler(w http.ResponseWriter, r *http.Request, ip string) {
	mediaDir := getMediaDir()
	progress := getProgressSummary()
	payload := statusPayload{
		Name:          friendlyName,
		MediaName:     filepath.Base(mediaDir),
		Endpoint:      fmt.Sprintf("http://%s:%s", ip, serverPort),
		StartedAt:     startedAt.Format(time.RFC3339),
		ProgressCount: progress.Count,
	}
	if !progress.LastUpdated.IsZero() {
		payload.ProgressUpdatedAt = progress.LastUpdated.Format(time.RFC3339)
	}
	if isLocalRequest(r) {
		payload.MediaDir = mediaDir
	}
	respondJSON(w, http.StatusOK, payload)
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
