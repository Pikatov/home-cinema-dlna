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
	Name      string `json:"name"`
	MediaDir  string `json:"mediaDir,omitempty"`
	MediaName string `json:"mediaDirName"`
	Endpoint  string `json:"endpoint"`
	StartedAt string `json:"startedAt"`
}

func statusHandler(w http.ResponseWriter, r *http.Request, ip string) {
	mediaDir := getMediaDir()
	payload := statusPayload{
		Name:      friendlyName,
		MediaName: filepath.Base(mediaDir),
		Endpoint:  fmt.Sprintf("http://%s:%s", ip, serverPort),
		StartedAt: startedAt.Format(time.RFC3339),
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
