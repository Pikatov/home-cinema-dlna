package main

import (
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"github.com/koron/go-ssdp"
)

func startSSDP(ip string) {
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

	for i := 0; i < burstAliveCount; i++ {
		for _, ad := range ads {
			if err := ad.Alive(); err != nil {
				log.Printf("Ошибка SSDP burst: %v", err)
			}
		}
		time.Sleep(400 * time.Millisecond)
	}

	fastAnnounce := time.NewTicker(5 * time.Second)
	time.AfterFunc(1*time.Minute, func() { fastAnnounce.Stop() })
	fastTicker := time.NewTicker(15 * time.Second)
	time.AfterFunc(2*time.Minute, func() { fastTicker.Stop() })
	slowTicker := time.NewTicker(60 * time.Second)
	defer slowTicker.Stop()
	defer fastTicker.Stop()

	for {
		select {
		case <-fastAnnounce.C:
			for _, ad := range ads {
				_ = ad.Alive()
			}
		case <-fastTicker.C:
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

func respondMSearch(ip string) {
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

	buf := make([]byte, 2048)
	location := fmt.Sprintf("http://%s:%s/desc.xml", ip, serverPort)

	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("SSDP read error: %v", err)
			continue
		}
		data := strings.ToUpper(string(buf[:n]))
		if !strings.Contains(data, "M-SEARCH") || !strings.Contains(data, "ST:") {
			continue
		}

		st := "urn:schemas-upnp-org:device:MediaServer:1"
		if strings.Contains(data, "ST: SSDP:ALL") {
			st = "ssdp:all"
		} else if strings.Contains(data, "CONTENTDIRECTORY") {
			st = "urn:schemas-upnp-org:service:ContentDirectory:1"
		}

		res := fmt.Sprintf("HTTP/1.1 200 OK\r\n"+
			"CACHE-CONTROL: max-age=1800\r\n"+
			"DATE: %s\r\n"+
			"EXT:\r\n"+
			"LOCATION: %s\r\n"+
			"SERVER: MacOS/13.0 UPnP/1.0 DLNADOC/1.50 HomeCinema/%s\r\n"+
			"ST: %s\r\n"+
			"USN: uuid:%s::%s\r\n"+
			"\r\n", time.Now().UTC().Format(time.RFC1123), location, appVersion, st, uuid, st)

		if _, err := conn.WriteToUDP([]byte(res), src); err != nil {
			log.Printf("SSDP write error: %v", err)
		} else {
			log.Printf("📣 M-SEARCH ответ: %s -> %s", st, src)
		}
	}
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
