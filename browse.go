package main

import (
	"encoding/base64"
	"fmt"
	"hash/crc32"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func averageBitrateMbps(size int64, durationSeconds float64) float64 {
	if size <= 0 || durationSeconds <= 0 {
		return 0
	}
	return (float64(size) * 8) / durationSeconds / 1e6
}

func shouldPreferTVResource(path string, size int64, durationSeconds float64) bool {
	if !tvStreamEnabled {
		return false
	}
	if tvStreamFirst {
		return true
	}
	if !tvAutoFirst {
		return false
	}
	if shouldTranscodeForTVCompatibility(path) {
		return true
	}
	if durationSeconds <= 0 {
		return shouldPreferTVResourceWithoutDuration(path, size)
	}
	return averageBitrateMbps(size, durationSeconds) >= float64(tvAutoFirstMbps)
}

func shouldPreferTVResourceWithoutDuration(path string, size int64) bool {
	if size < 512*1024*1024 {
		return false
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mkv", ".webm":
		return true
	default:
		return false
	}
}

func shouldTranscodeForTVCompatibility(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	if ext != ".mkv" && ext != ".webm" {
		return false
	}
	name := strings.ToLower(filepath.Base(path))
	return strings.Contains(name, "h265") ||
		strings.Contains(name, "h.265") ||
		strings.Contains(name, "h-265") ||
		strings.Contains(name, "h_265") ||
		strings.Contains(name, "hevc") ||
		strings.Contains(name, "x265") ||
		strings.Contains(name, "10bit") ||
		strings.Contains(name, "10-bit") ||
		strings.Contains(name, "10 bit") ||
		strings.Contains(name, "main10") ||
		strings.Contains(name, "hi10p") ||
		strings.Contains(name, "hdr")
}

func handleContentDirectory(ip string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("⚠️ ContentDirectory: ошибка чтения тела запроса: %v", err)
			fmt.Fprintf(w, soapResponse, "", 0, 0, currentBrowseUpdateID())
			return
		}
		bodyStr := string(body)

		// SOAP-тело — короткая, фиксированная структура. strings.Index в 2-3
		// раза быстрее regexp на типичном Browse-запросе (~500 байт), и
		// каждый TV шлёт 2-3 таких запроса на каждое открытие папки.
		objID := extractXMLTag(bodyStr, "ObjectID")
		if objID == "" {
			objID = "0"
		}
		flag := extractXMLTag(bodyStr, "BrowseFlag")

		w.Header().Set("Content-Type", "text/xml; charset=utf-8")
		w.Header().Set("Server", fmt.Sprintf("Linux/2.6 UPnP/1.0 DLNADOC/1.50 HomeCinema/%s", appVersion))

		if flag == "BrowseMetadata" {
			logOnce("⚙️ МЕТАДАННЫЕ: ID=%s", objID)
			meta := fmt.Sprintf(`&lt;container id="%s" parentID="-1" restricted="1"&gt;&lt;dc:title&gt;Folder&lt;/dc:title&gt;&lt;upnp:class&gt;object.container.storageFolder&lt;/upnp:class&gt;&lt;/container&gt;`,
				escapeXMLAttr(objID))
			fmt.Fprintf(w, soapResponse, meta, 1, 1, currentBrowseUpdateID())
			return
		}

		if flag != "BrowseDirectChildren" {
			fmt.Fprintf(w, soapResponse, "", 0, 0, currentBrowseUpdateID())
			return
		}

		relPath := ""
		if objID != "0" && !strings.HasPrefix(objID, "vid-") {
			b, err := base64.RawURLEncoding.DecodeString(objID)
			if err == nil {
				relPath = string(b)
			}
		}
		var ok bool
		relPath, ok = safeMediaRelPath(relPath)
		if !ok {
			log.Printf("⚠️ Небезопасный ObjectID path: %q", objID)
			fmt.Fprintf(w, soapResponse, "", 0, 0, currentBrowseUpdateID())
			return
		}

		if payload, count, ok := getBrowseCache(relPath); ok {
			logOnce("⚡️ КЕШ: /%s (%d)", relPath, count)
			fmt.Fprintf(w, soapResponse, payload, count, count, currentBrowseUpdateID())
			return
		}

		dir := getMediaDir()
		logOnce("📂 ПАПКА: /%s", relPath)

		dirPath, ok := safeJoinUnderBase(dir, relPath)
		if !ok {
			log.Printf("⚠️ Небезопасный browse path: %q", relPath)
			fmt.Fprintf(w, soapResponse, "", 0, 0, currentBrowseUpdateID())
			return
		}

		files, err := os.ReadDir(dirPath)
		if err != nil {
			log.Printf("❌ ОШИБКА ЧТЕНИЯ ПАПКИ: %v", err)
			fmt.Fprintf(w, soapResponse, "", 0, 0, currentBrowseUpdateID())
			return
		}

		var items []string
		count := 0

		for _, f := range files {
			if strings.HasPrefix(f.Name(), ".") {
				continue
			}

			name := f.Name()
			childRelPath := filepath.Join(relPath, name)

			if f.IsDir() {
				childID := base64.RawURLEncoding.EncodeToString([]byte(childRelPath))
				item := fmt.Sprintf(`&lt;container id="%s" parentID="%s" restricted="1"&gt;&lt;dc:title&gt;%s&lt;/dc:title&gt;&lt;upnp:class&gt;object.container.storageFolder&lt;/upnp:class&gt;&lt;/container&gt;`,
					escapeXMLAttr(childID),
					escapeXMLAttr(objID),
					escapeXMLText(name),
				)
				items = append(items, item)
				count++
				continue
			}

			if !isVideoExt(filepath.Ext(name)) {
				continue
			}

			displayTitle := trimVideoExtension(name)
			info, err := f.Info()
			if err != nil {
				continue
			}
			parts := strings.Split(childRelPath, string(filepath.Separator))
			for i, p := range parts {
				parts[i] = url.PathEscape(p)
			}
			escapedRel := strings.Join(parts, "/")
			fileURL := fmt.Sprintf("http://%s:%s/video/%s", ip, serverPort, escapedRel)
			tvURL := fmt.Sprintf("http://%s:%s/tv/%s.ts", ip, serverPort, escapedRel)
			resumeURL := fmt.Sprintf("http://%s:%s/resume/%s", ip, serverPort, escapedRel)

			title := displayTitle
			fullPath := filepath.Join(dir, childRelPath)
			mimeType, dlnaProfile := detectContentType(fullPath)
			proto := fmt.Sprintf("http-get:*:%s:%s", mimeType, dlnaProfile)
			meta, _ := getVideoMetaCached(fullPath)
			key := progressKeyFromRelPath(childRelPath)
			entry, hasProgressEntry := getProgressEntry(key)
			// Если кеш ffprobe ещё холодный (рестарт сервера, новая папка),
			// длительность достаём из сохранённого прогресса: и для byte-based,
			// и для seconds-based записей. Без этого после рестарта в DLNA-
			// заголовке исчезала «правая часть» таймкода ([▶ хх:хх] вместо
			// [▶ хх:хх - yy:yy]) — самое заметное место, так как seconds-based
			// прогресс пишут `/tv/` и `/resume/`.
			if meta.DurationSeconds <= 0 {
				if hasProgressEntry && entry.DurationSeconds > 0 {
					meta.DurationSeconds = entry.DurationSeconds
				} else if hasProgressEntry {
					meta = getVideoMetaWithTimeout(fullPath, 300*time.Millisecond)
					if meta.DurationSeconds <= 0 {
						warmVideoMetaAsync(fullPath)
					}
				}
			}
			tc := getProgressTimecode(childRelPath, info.Size(), meta.DurationSeconds)
			if tc != "" {
				if dur := formatSecondsTimecode(meta.DurationSeconds); dur != "" {
					title = fmt.Sprintf("%s [▶ %s - %s]", displayTitle, tc, dur)
				} else {
					title = fmt.Sprintf("%s [▶ %s]", displayTitle, tc)
				}
			}

			durationAttr := ""
			if meta.DurationSeconds > 0 {
				durationAttr = fmt.Sprintf(` duration="%s"`, escapeXMLAttr(formatDLNADuration(meta.DurationSeconds)))
			}

			// /resume/ снова использует обычный byte Range поверх /video/: это
			// менее красиво, чем ffmpeg-remux, зато на DLNA-ТВ стабильнее и не
			// зависит от контейнера. Для seconds-based прогресса нужна длительность,
			// для byte-based достаточно совпадения размера файла.
			hasResume := false
			preferTVResource := !hasResume && tvStreamEnabled && shouldPreferTVResource(fullPath, info.Size(), meta.DurationSeconds)

			// ID элемента: путь файла + флаг наличия прогресса. ТВ часто кешируют
			// DLNA-каталог и при повторном открытии берут URL из своего кеша —
			// если ID не поменялся, нашу подмену <res> на /resume/ они не подхватят.
			// Меняя суффикс ID при появлении/исчезновении прогресса или изменении
			// порядка <res>, мы заставляем ТВ считать это "новым" объектом и
			// заново запросить URL.
			idSeed := childRelPath
			if hasResume {
				idSeed = childRelPath + "\x00r"
			} else if preferTVResource {
				idSeed = childRelPath + "\x00tv"
			}
			fileHash := crc32.ChecksumIEEE([]byte(idSeed))
			stableID := strconv.FormatUint(uint64(fileHash), 10)

			resParts := make([]string, 0, 3)
			if hasResume {
				// /resume/ — первый ресурс: ТВ обычно выбирает первый.
				// protocolInfo берём от исходного файла, потому что handler внутри
				// синтезирует byte Range и отдаёт тот же контейнер через /video/.
				resumeRes := fmt.Sprintf(`&lt;res%s protocolInfo="%s"&gt;%s&lt;/res&gt;`,
					durationAttr,
					escapeXMLAttr(proto),
					escapeXMLText(resumeURL),
				)
				// /video/ — запасной ресурс со size: ТВ, не принимающие <res> без size
				// для первого варианта, хотя бы запустят фильм с начала.
				fileRes := fmt.Sprintf(`&lt;res size="%d"%s protocolInfo="%s"&gt;%s&lt;/res&gt;`,
					info.Size(),
					durationAttr,
					escapeXMLAttr(proto),
					escapeXMLText(fileURL),
				)
				resParts = append(resParts, resumeRes, fileRes)
			} else if tvStreamEnabled {
				tvProto := fmt.Sprintf("http-get:*:%s:%s", tvContentType, tvDLNAFeatures)
				tvRes := fmt.Sprintf(`&lt;res%s protocolInfo="%s"&gt;%s&lt;/res&gt;`,
					durationAttr,
					escapeXMLAttr(tvProto),
					escapeXMLText(tvURL),
				)
				fileRes := fmt.Sprintf(`&lt;res size="%d"%s protocolInfo="%s"&gt;%s&lt;/res&gt;`,
					info.Size(),
					durationAttr,
					escapeXMLAttr(proto),
					escapeXMLText(fileURL),
				)
				// TV-first only when explicitly forced or the file is too heavy for direct Wi‑Fi.
				if preferTVResource {
					resParts = append(resParts, tvRes, fileRes)
				} else {
					resParts = append(resParts, fileRes, tvRes)
				}
			} else {
				resParts = append(resParts, fmt.Sprintf(`&lt;res size="%d"%s protocolInfo="%s"&gt;%s&lt;/res&gt;`,
					info.Size(),
					durationAttr,
					escapeXMLAttr(proto),
					escapeXMLText(fileURL),
				))
			}

			item := fmt.Sprintf(`&lt;item id="vid-%s" parentID="%s" restricted="1"&gt;`+
				`&lt;dc:title&gt;%s&lt;/dc:title&gt;`+
				`&lt;upnp:class&gt;object.item.videoItem&lt;/upnp:class&gt;`+
				`%s`+
				`&lt;/item&gt;`,
				escapeXMLAttr(stableID),
				escapeXMLAttr(objID),
				escapeXMLText(title),
				strings.Join(resParts, ""),
			)

			items = append(items, item)
			count++
		}
		payload := strings.Join(items, "")
		setBrowseCache(relPath, payload, count)
		fmt.Fprintf(w, soapResponse, payload, count, count, currentBrowseUpdateID())
	}
}

func trimVideoExtension(name string) string {
	displayTitle := name
	for {
		ext := filepath.Ext(displayTitle)
		if !isVideoExt(ext) {
			return displayTitle
		}
		displayTitle = strings.TrimSuffix(displayTitle, ext)
	}
}

func getBrowseCache(key string) (string, int, bool) {
	browseCacheMu.RLock()
	entry, ok := browseCache[key]
	browseCacheMu.RUnlock()
	if !ok {
		return "", 0, false
	}
	if time.Now().After(entry.expires) {
		browseCacheMu.Lock()
		delete(browseCache, key)
		browseCacheMu.Unlock()
		return "", 0, false
	}
	return entry.payload, entry.count, true
}

func setBrowseCache(key, payload string, count int) {
	browseCacheMu.Lock()
	browseCache[key] = browseCacheEntry{
		payload: payload,
		count:   count,
		expires: time.Now().Add(browseCacheTTL),
	}
	browseCacheMu.Unlock()
}
