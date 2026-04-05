# Home Cinema — DLNA/UPnP media server for your home network

[Русская версия →](README.md)

A small DLNA/UPnP media server written in Go. It exposes a UPnP `MediaServer` + `ContentDirectory`, streams video over HTTP with `Range` support (seeking/resume), remembers watch progress, and can optionally provide a “TV stream” via `ffmpeg` to reduce stuttering on weak Wi‑Fi.

The repo also includes a macOS control app (SwiftUI): pick a folder, start/stop the server, open logs.

## When it’s useful
- Your TV/set‑top box supports DLNA, but SMB shares stutter.
- You want a simple “folder with movies” setup (no Plex/Jellyfin, no database).
- You want reliable seeking and “continue watching”.

## Features
- UPnP/DLNA: SSDP announcements, `desc.xml`, `ContentDirectory:Browse`, HTTP streaming with `Range`.
- Video: `.mp4`, `.mkv`, `.avi`.
- Browse caching (helps some TVs browse faster).
- Watch progress: stores position and shows a timecode in the file name (e.g. `Movie.mkv [▶ 12:34]`).
- Switch media library on the fly: `POST /set-media-dir` (localhost‑only by default).

## Quick start (server)
Requirement: Go `1.26+`.

```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

Then open the list of DLNA/media servers on your TV/set‑top box and choose **Home Cinema**.

## If playback stutters on Wi‑Fi
`--tv-stream` (enabled by default) adds an alternative stream via `ffmpeg` to smooth bitrate peaks.

- If `ffmpeg` is not installed, the server will automatically disable the TV stream and log a warning.
- For durations/timecodes, you’ll also want `ffprobe` (usually bundled with `ffmpeg`).

Example tuning:
```bash
./HomeCinemaServer --tv-stream=true --tv-maxrate-mbps 8 --tv-bufsize-mbps 16 --tv-crf 23
```

## Logs and progress files
Default:
- macOS: `~/Library/Application Support/HomeCinema/` (`server.log`, `progress.json`)

Override via:
- `HOMECINEMA_DATA_DIR`
- or `--data-dir "/path/to/folder"`

## Control and security
`/set-media-dir` is **localhost‑only** by default, so devices on your LAN cannot remotely point the server at arbitrary folders.

To allow remote control (not recommended unless you understand the risk):
```bash
./HomeCinemaServer --allow-remote-control
```

Useful requests:
```bash
# status (localhost returns full path; over network returns folder name only)
curl -s http://127.0.0.1:8080/ | jq

# switch media folder (localhost-only by default)
curl -s -X POST --data-urlencode mediaDir="$HOME/Movies" http://127.0.0.1:8080/set-media-dir | jq
```

## macOS control app (SwiftUI)
Build:
```bash
cd build/home-cinema/HomeCinemaControlSwift
./build.sh
```

The script builds a universal `.app` and bundles a fresh server binary inside. UI/start logs go to `/tmp/homecinema.log`.

## Build DMG (optional)
`build/home-cinema/make_dmg.sh` packs the `.app` into a DMG.

Dependency:
```bash
brew install create-dmg
```

Build:
```bash
./build/home-cinema/make_dmg.sh
```

## Handy flags
- `--media-dir` — media library folder.
- `--port` — HTTP port (default `8080`).
- `--tv-stream` — enable/disable TV stream (needs `ffmpeg`).
- `--tv-stream-first` — put TV stream first (many TVs pick the first `<res>`, but progress/duration might disappear).
- `--stream-buf-mb` — streaming buffer; try `1–2` if your network “hangs”.

Full list: `./HomeCinemaServer -h`.

## Project layout
- `main.go` — DLNA/UPnP server.
- `build/home-cinema/HomeCinemaControlSwift/` — SwiftUI control app.
- `build/home-cinema/make_dmg.sh` — DMG packaging.

## License & changes
- License: MIT (`LICENSE`)
- Changelog: `CHANGELOG.md`

