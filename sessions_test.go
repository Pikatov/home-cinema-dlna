package main

import "testing"

func TestFriendlyDeviceName(t *testing.T) {
	cases := []struct {
		name string
		ua   string
		want string
	}{
		{"empty", "", "Unknown device"},
		{"samsung sec_hhp", "SEC_HHP_[TV]UN65MU8000/1.0 DLNADOC/1.50", "Samsung TV"},
		{"samsung explicit", "Samsung Allshare", "Samsung TV"},
		{"lg webos", "Mozilla/5.0 (Web0S; Linux/SmartTV) LG NetCast", "LG TV"},
		{"sony bravia", "BRAVIA-2017", "Sony Bravia"},
		{"philips nettv", "NETTV/4.6.0.1", "Philips TV"},
		{"hisense", "Hisense MediaPlayer", "Hisense TV"},
		{"roku", "Roku/DVP-9.10", "Roku"},
		{"apple tv", "AppleTV/12.4", "Apple TV"},
		{"chromecast", "CrKey/1.54 Chromecast", "Chromecast"},
		{"nvidia shield", "Shield Android TV", "NVIDIA Shield"},
		{"fire tv", "AFTM Build/KOT49H Fire TV", "Fire TV"},
		{"kodi", "Kodi/20.0 (X11; Linux)", "Kodi"},
		{"vlc", "VLC/3.0.18 LibVLC/3.0.18", "VLC"},
		{"infuse", "Infuse Pro/7.5.5", "Infuse"},
		{"iphone", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0)", "iPhone"},
		{"ipad", "Mozilla/5.0 (iPad; CPU OS 17_0)", "iPad"},
		{"android", "Mozilla/5.0 (Linux; Android 13)", "Android"},
		{"mac", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0)", "Mac"},
		{"windows", "Mozilla/5.0 (Windows NT 10.0)", "Windows"},
		{"upnp generic", "Generic UPnP/1.0 PlatinumLib/2.0.0.0", "DLNA client"},
		{"unknown short", "x", "Unknown device"},
		{"unknown fallback", "Foobar/1.2.3", "Foobar"},
		{"truncation", "ReallyLongClientNameWithoutSlashesYesIndeed", "ReallyLongClientNameWithou"[:24]},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := friendlyDeviceName(c.ua)
			if got != c.want {
				t.Fatalf("friendlyDeviceName(%q) = %q, want %q", c.ua, got, c.want)
			}
		})
	}
}

func TestClientHost(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"192.168.1.10:54321", "192.168.1.10"},
		{"[fe80::1]:54321", "fe80::1"},
		{"192.168.1.10", "192.168.1.10"},
		{"hostname-without-port", "hostname-without-port"},
		{"weird:thing:not-port", "weird:thing:not-port"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got := clientHost(c.in)
			if got != c.want {
				t.Fatalf("clientHost(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
