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

func handleContentDirectory(ip string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodyStr := string(body)

		objIDMatch := objectIDRe.FindStringSubmatch(bodyStr)
		flagMatch := flagRe.FindStringSubmatch(bodyStr)

		objID := "0"
		if len(objIDMatch) > 1 {
			objID = objIDMatch[1]
		}
		flag := ""
		if len(flagMatch) > 1 {
			flag = flagMatch[1]
		}

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
			fileHash := crc32.ChecksumIEEE([]byte(childRelPath))
			stableID := strconv.FormatUint(uint64(fileHash), 10)

			parts := strings.Split(childRelPath, string(filepath.Separator))
			for i, p := range parts {
				parts[i] = url.PathEscape(p)
			}
			escapedRel := strings.Join(parts, "/")
			fileURL := fmt.Sprintf("http://%s:%s/video/%s", ip, serverPort, escapedRel)
			tvURL := fmt.Sprintf("http://%s:%s/tv/%s", ip, serverPort, escapedRel)

			title := displayTitle
			fullPath := filepath.Join(dir, childRelPath)
			mimeType, dlnaProfile := detectContentType(fullPath)
			proto := fmt.Sprintf("http-get:*:%s:%s", mimeType, dlnaProfile)
			meta, _ := getVideoMetaCached(fullPath)
			key := progressKeyFromRelPath(childRelPath)
			if entry, ok := getProgressEntry(key); ok && entry.Seconds <= 0 && entry.Position > 0 && entry.Size == info.Size() && meta.DurationSeconds <= 0 {
				if entry.DurationSeconds > 0 {
					meta.DurationSeconds = entry.DurationSeconds
				} else {
					warmVideoMetaAsync(fullPath)
				}
			}
			if tc := getProgressTimecode(childRelPath, info.Size(), meta.DurationSeconds); tc != "" {
				title = fmt.Sprintf("%s [▶ %s]", displayTitle, tc)
			}

			durationAttr := ""
			if meta.DurationSeconds > 0 {
				durationAttr = fmt.Sprintf(` duration="%s"`, escapeXMLAttr(formatDLNADuration(meta.DurationSeconds)))
			}

			resParts := make([]string, 0, 2)
			if tvStreamEnabled {
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
				if tvStreamFirst {
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
