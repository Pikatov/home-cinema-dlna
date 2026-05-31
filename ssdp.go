package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/koron/go-ssdp"
)

func startSSDP(ctx context.Context, wg *sync.WaitGroup, ip string) {
	defer wg.Done()
	location := fmt.Sprintf("http://%s:%s/desc.xml", ip, serverPort)
	adverts := []struct {
		st  string
		usn string
	}{
		{"urn:schemas-upnp-org:device:MediaServer:1", "uuid:" + uuid + "::urn:schemas-upnp-org:device:MediaServer:1"},
		{"upnp:rootdevice", "uuid:" + uuid},
		{"urn:schemas-upnp-org:service:ContentDirectory:1", "uuid:" + uuid + "::urn:schemas-upnp-org:service:ContentDirectory:1"},
	}

	var ads []*ssdp.Advertiser
	for _, a := range adverts {
		ad, err := ssdp.Advertise(a.st, a.usn, location, manufacturerName, 1800, ssdp.AdvertiseHost(), ssdp.TTL(4))
		if err != nil {
			log.Printf("Ошибка SSDP (%s): %v", a.st, err)
			continue
		}
		ads = append(ads, ad)
	}
	if len(ads) == 0 {
		log.Printf("Ошибка SSDP: ни одно объявление не запущено")
		return
	}

	defer func() {
		for _, ad := range ads {
			_ = ad.Bye()
		}
	}()

	for i := 0; i < burstAliveCount; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}
		for _, ad := range ads {
			if err := ad.Alive(); err != nil {
				log.Printf("Ошибка SSDP burst: %v", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(400 * time.Millisecond):
		}
	}

	// Каскад «горячий → тёплый → медленный» announce. Раньше каждое окно
	// открывалось через time.AfterFunc — оно жило вне ctx и могло сработать
	// после shutdown. Теперь интервал «переключается» внутри select на основе
	// прошедшего времени от старта.
	fastAnnounce := time.NewTicker(5 * time.Second)
	fastTicker := time.NewTicker(15 * time.Second)
	slowTicker := time.NewTicker(60 * time.Second)
	defer fastAnnounce.Stop()
	defer fastTicker.Stop()
	defer slowTicker.Stop()

	startedAt := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-fastAnnounce.C:
			if time.Since(startedAt) > time.Minute {
				continue
			}
			for _, ad := range ads {
				_ = ad.Alive()
			}
		case <-fastTicker.C:
			if time.Since(startedAt) > 2*time.Minute {
				continue
			}
			for _, ad := range ads {
				_ = ad.Alive()
			}
		case <-slowTicker.C:
			for _, ad := range ads {
				if err := ad.Alive(); err != nil {
					log.Printf("Ошибка SSDP: не удалось обновить анонс: %v", err)
				}
			}
		}
	}
}

// knownSTs — service types, на которые сервер отвечает по UPnP spec.
var knownSTs = []string{
	"ssdp:all",
	"upnp:rootdevice",
	"urn:schemas-upnp-org:device:mediaserver:1",
	"urn:schemas-upnp-org:service:contentdirectory:1",
}

func respondMSearch(ctx context.Context, wg *sync.WaitGroup, ip string) {
	defer wg.Done()
	addr, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		log.Printf("SSDP resolve error: %v", err)
		return
	}
	iface := primaryInterface()
	conn, err := net.ListenMulticastUDP("udp4", iface, addr)
	if err != nil {
		conn, err = net.ListenMulticastUDP("udp4", nil, addr)
	}
	if err != nil {
		log.Printf("SSDP listen error: %v", err)
		return
	}
	if err := conn.SetReadBuffer(64 * 1024); err != nil {
		log.Printf("SSDP set buffer error: %v", err)
	}

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 2048)
	location := fmt.Sprintf("http://%s:%s/desc.xml", ip, serverPort)

	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("SSDP read error: %v", err)
			continue
		}
		data := strings.ToUpper(string(buf[:n]))
		if !strings.Contains(data, "M-SEARCH") || !strings.Contains(data, "ST:") {
			continue
		}

		// Извлекаем значение ST из запроса.
		st := extractST(data)
		if st == "" {
			continue
		}

		// Отвечаем только на известные нам типы (UPnP spec §1.3.2).
		matched := false
		for _, known := range knownSTs {
			if strings.Contains(st, known) {
				matched = true
				break
			}
		}
		// Также отвечаем на поиск нашего конкретного UUID.
		if !matched && strings.Contains(st, strings.ToUpper(uuid)) {
			matched = true
		}
		if !matched {
			continue
		}

		// Нормализуем ST для ответа: если ssdp:all — отвечаем как MediaServer.
		responseST := "urn:schemas-upnp-org:device:MediaServer:1"
		if strings.Contains(st, "SSDP:ALL") {
			responseST = "ssdp:all"
		} else if strings.Contains(st, "CONTENTDIRECTORY") {
			responseST = "urn:schemas-upnp-org:service:ContentDirectory:1"
		} else if strings.Contains(st, "ROOTDEVICE") {
			responseST = "upnp:rootdevice"
		}

		res := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
			"CACHE-CONTROL: max-age=1800\r\n"+
			"DATE: %s\r\n"+
			"EXT:\r\n"+
			"LOCATION: %s\r\n"+
			"SERVER: MacOS/13.0 UPnP/1.0 DLNADOC/1.50 HomeCinema/%s\r\n"+
			"ST: %s\r\n"+
			"USN: uuid:%s::%s\r\n"+
			"\r\n", time.Now().UTC().Format(time.RFC1123), location, appVersion, responseST, uuid, responseST)

		// Короткий deadline на write: на нестабильной сети WriteToUDP может
		// зависнуть, а каждый зависший вызов блокирует приём следующих
		// M-SEARCH-ов из общего сокета.
		_ = conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
		if _, err := conn.WriteToUDP([]byte(res), src); err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("SSDP write error: %v", err)
		} else {
			log.Printf("📣 M-SEARCH ответ: %s -> %s", responseST, src)
		}
		_ = conn.SetWriteDeadline(time.Time{})
	}
}

// extractST извлекает значение заголовка ST из M-SEARCH запроса (в верхнем регистре).
func extractST(data string) string {
	for _, line := range strings.Split(data, "\r\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ST:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "ST:"))
		}
	}
	return ""
}

func primaryInterface() *net.Interface {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, iface := range ifaces {
		if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && ipnet.IP.To4() != nil {
				return &iface
			}
		}
	}
	return nil
}
