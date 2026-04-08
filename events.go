package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type upnpEventSub struct {
	sid      string
	callback string
	expires  time.Time
	seq      uint32
}

var (
	eventSubsMu sync.Mutex
	eventSubs   = make(map[string]*upnpEventSub)
)

func newSID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return sprintf("uuid:%d", time.Now().UnixNano())
	}
	return "uuid:" + hex.EncodeToString(b[:])
}

func parseTimeout(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 30 * time.Minute
	}
	upper := strings.ToUpper(h)
	if strings.HasPrefix(upper, "SECOND-") {
		n, err := strconv.Atoi(strings.TrimPrefix(upper, "SECOND-"))
		if err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 30 * time.Minute
}

func notifyContentDirectory(updateID uint32) {
	now := time.Now()
	type target struct {
		sid      string
		callback string
		seq      uint32
	}

	var targets []target
	eventSubsMu.Lock()
	for sid, sub := range eventSubs {
		if now.After(sub.expires) {
			delete(eventSubs, sid)
			continue
		}
		targets = append(targets, target{sid: sid, callback: sub.callback, seq: sub.seq})
		sub.seq++
	}
	eventSubsMu.Unlock()

	if len(targets) == 0 {
		return
	}

	body := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+"\n"+
		`<e:propertyset xmlns:e="urn:schemas-upnp-org:event-1-0">`+"\n"+
		`  <e:property><SystemUpdateID>%d</SystemUpdateID></e:property>`+"\n"+
		`</e:propertyset>`, updateID)

	client := &http.Client{Timeout: 2 * time.Second}
	for _, t := range targets {
		req, err := http.NewRequest("NOTIFY", t.callback, strings.NewReader(body))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "text/xml; charset=\"utf-8\"")
		req.Header.Set("NT", "upnp:event")
		req.Header.Set("NTS", "upnp:propchange")
		req.Header.Set("SID", t.sid)
		req.Header.Set("SEQ", strconv.FormatUint(uint64(t.seq), 10))
		_, _ = client.Do(req)
	}
}

func handleEventContentDirectory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "SUBSCRIBE":
			sid := strings.TrimSpace(r.Header.Get("SID"))
			timeout := parseTimeout(r.Header.Get("TIMEOUT"))
			expires := time.Now().Add(timeout)

			if sid == "" {
				cb := r.Header.Get("CALLBACK")
				m := callbackRe.FindStringSubmatch(cb)
				if len(m) < 2 {
					http.Error(w, "Missing CALLBACK", http.StatusPreconditionFailed)
					return
				}
				sid = newSID()
				sub := &upnpEventSub{
					sid:      sid,
					callback: strings.TrimSpace(m[1]),
					expires:  expires,
				}
				eventSubsMu.Lock()
				eventSubs[sid] = sub
				eventSubsMu.Unlock()

				w.Header().Set("SID", sid)
				w.Header().Set("TIMEOUT", fmt.Sprintf("Second-%d", int(timeout.Seconds())))
				w.WriteHeader(http.StatusOK)
				go notifyContentDirectory(currentBrowseUpdateID())
				return
			}

			eventSubsMu.Lock()
			sub, ok := eventSubs[sid]
			if ok {
				sub.expires = expires
			}
			eventSubsMu.Unlock()
			if !ok {
				http.Error(w, "Unknown SID", http.StatusPreconditionFailed)
				return
			}

			w.Header().Set("SID", sid)
			w.Header().Set("TIMEOUT", fmt.Sprintf("Second-%d", int(timeout.Seconds())))
			w.WriteHeader(http.StatusOK)
			return

		case "UNSUBSCRIBE":
			sid := strings.TrimSpace(r.Header.Get("SID"))
			if sid == "" {
				http.Error(w, "Missing SID", http.StatusPreconditionFailed)
				return
			}
			eventSubsMu.Lock()
			delete(eventSubs, sid)
			eventSubsMu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
