package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// activeSession — публичная запись об одном текущем стриме, безопасная
// для сериализации в /stats. Сессии — это «что играет прямо сейчас»: один
// item на каждый открытый /video/, /resume/ или /tv/ запрос. Когда handler
// выходит, сессия закрывается.
type activeSession struct {
	ID              uint64    `json:"id"`
	Kind            string    `json:"kind"`            // "direct" | "resume" | "tv"
	Title           string    `json:"title"`           // отображаемое имя (basename без расширения)
	RelPath         string    `json:"relPath"`         // путь в медиатеке
	Client          string    `json:"client"`          // remote host без порта
	UserAgent       string    `json:"userAgent"`       // сырой User-Agent клиента
	Device          string    `json:"device"`          // friendly device name из UA
	StartedAt       time.Time `json:"startedAt"`
	SeekSeconds     float64   `json:"seekSeconds"`     // позиция, с которой стартовали
	DurationSeconds float64   `json:"durationSeconds"` // длительность файла (0 если неизвестна)
}

var (
	sessionsMu sync.RWMutex
	sessions   = make(map[uint64]*activeSession)
	sessionSeq uint64
)

// openSession регистрирует новый стрим и возвращает его id. closeSession(id)
// обязан быть вызван (через defer) на выход из handler-а.
func openSession(kind, title, relPath, client, userAgent string, seekSecs, durationSecs float64) uint64 {
	id := atomic.AddUint64(&sessionSeq, 1)
	s := &activeSession{
		ID:              id,
		Kind:            kind,
		Title:           title,
		RelPath:         relPath,
		Client:          clientHost(client),
		UserAgent:       userAgent,
		Device:          friendlyDeviceName(userAgent),
		StartedAt:       time.Now(),
		SeekSeconds:     seekSecs,
		DurationSeconds: durationSecs,
	}
	sessionsMu.Lock()
	sessions[id] = s
	sessionsMu.Unlock()
	return id
}

// friendlyDeviceName пытается выудить человекочитаемое имя устройства из
// User-Agent. DLNA-клиенты идентифицируют себя крайне нестандартно — это
// эвристика, не парсер. Порядок проверок важен (от более специфичных
// к более общим), иначе «AppleTV» съест более широкий «Apple».
func friendlyDeviceName(ua string) string {
	if strings.TrimSpace(ua) == "" {
		return "Unknown device"
	}
	low := strings.ToLower(ua)

	// TV-производители (DLNA-устройства)
	switch {
	case strings.Contains(low, "samsung") || strings.Contains(low, "sec_hhp") || strings.Contains(low, "secdlna"):
		return "Samsung TV"
	case strings.Contains(low, "lg") && (strings.Contains(low, "webos") || strings.Contains(low, "smart-tv") || strings.Contains(low, "smarttv")):
		return "LG TV"
	case strings.Contains(low, "webos"):
		return "LG TV"
	case strings.Contains(low, "bravia") || (strings.Contains(low, "sony") && strings.Contains(low, "tv")):
		return "Sony Bravia"
	case strings.Contains(low, "philipstv") || strings.Contains(low, "nettv"):
		return "Philips TV"
	case strings.Contains(low, "hisense"):
		return "Hisense TV"
	case strings.Contains(low, "panasonic"):
		return "Panasonic TV"
	case strings.Contains(low, "sharp") && strings.Contains(low, "aquos"):
		return "Sharp Aquos"
	case strings.Contains(low, "roku"):
		return "Roku"
	case strings.Contains(low, "appletv") || strings.Contains(low, "apple tv"):
		return "Apple TV"
	case strings.Contains(low, "chromecast"):
		return "Chromecast"
	case strings.Contains(low, "shield"):
		return "NVIDIA Shield"
	case strings.Contains(low, "fire") && strings.Contains(low, "tv"):
		return "Fire TV"
	case strings.Contains(low, "xbox"):
		return "Xbox"
	case strings.Contains(low, "playstation") || strings.Contains(low, "ps4") || strings.Contains(low, "ps5"):
		return "PlayStation"
	case strings.Contains(low, "kodi") || strings.Contains(low, "xbmc"):
		return "Kodi"
	case strings.Contains(low, "vlc"):
		return "VLC"
	case strings.Contains(low, "infuse"):
		return "Infuse"
	case strings.Contains(low, "mxplayer"):
		return "MX Player"

	// Платформы (мобильные клиенты / десктоп)
	case strings.Contains(low, "iphone"):
		return "iPhone"
	case strings.Contains(low, "ipad"):
		return "iPad"
	case strings.Contains(low, "android"):
		return "Android"
	case strings.Contains(low, "windows"):
		return "Windows"
	case strings.Contains(low, "macintosh") || strings.Contains(low, "mac os"):
		return "Mac"
	case strings.Contains(low, "linux"):
		return "Linux"

	// UPnP-фреймворки общего назначения
	case strings.Contains(low, "upnp") || strings.Contains(low, "dlna"):
		return "DLNA client"
	}

	// Fallback: первое «слово» из UA без слешей и спецсимволов.
	for _, token := range strings.FieldsFunc(ua, func(r rune) bool {
		return r == ' ' || r == '/' || r == ';' || r == ','
	}) {
		token = strings.TrimSpace(token)
		if len(token) >= 3 {
			if len(token) > 24 {
				return token[:24]
			}
			return token
		}
	}
	return "Unknown device"
}

// closeSession удаляет запись об активном стриме. Безопасно вызывать с id=0
// (no-op) — это упрощает defer в handler-ах, которые ещё не получили id.
func closeSession(id uint64) {
	if id == 0 {
		return
	}
	sessionsMu.Lock()
	delete(sessions, id)
	sessionsMu.Unlock()
}

// snapshotSessions возвращает копию активных стримов для /stats. Порядок:
// от свежих к старым, чтобы UI показывал то, что только что запустилось,
// первым.
func snapshotSessions() []activeSession {
	sessionsMu.RLock()
	out := make([]activeSession, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, *s)
	}
	sessionsMu.RUnlock()
	// Сортировка by StartedAt desc.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].StartedAt.After(out[j-1].StartedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// clientHost обрезает порт у "host:port" / "[ipv6]:port". Если без порта —
// возвращает как есть. Дублирует логику из tvStreamKey, но локально, чтобы
// sessions.go не зависел от streaming.go.
func clientHost(remoteAddr string) string {
	if remoteAddr == "" {
		return ""
	}
	// Простой парсер без net.SplitHostPort: ":" слева ищется с конца.
	// IPv6 в [host]:port попадает в случай "]:".
	for i := len(remoteAddr) - 1; i >= 0; i-- {
		c := remoteAddr[i]
		if c == ':' {
			host := remoteAddr[:i]
			if len(host) > 1 && host[0] == '[' && host[len(host)-1] == ']' {
				return host[1 : len(host)-1]
			}
			return host
		}
		if c < '0' || c > '9' {
			// Не порт — остальная часть содержит non-digit, значит ":" не от порта.
			return remoteAddr
		}
	}
	return remoteAddr
}
