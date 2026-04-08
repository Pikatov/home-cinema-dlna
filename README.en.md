# Home Cinema — DLNA/UPnP media server for your home network

[Русская версия →](README.md)

Home Cinema is a lightweight DLNA/UPnP media server written in Go. It exposes `MediaServer` and `ContentDirectory`, streams video over HTTP with `Range`, stores watch progress on disk, and can provide an alternative TV stream via `ffmpeg` to reduce stuttering on weak Wi‑Fi.

The repository also includes the Home Cinema SwiftUI macOS app: choose a media folder, start or stop the server, open logs, and switch between light and dark themes with a glass-inspired UI.

<img width="408" height="276" alt="Light" src="https://github.com/user-attachments/assets/be67e61d-b829-4381-9376-9aa1db8acaf9" /><img width="408" height="276" alt="Dark" src="https://github.com/user-attachments/assets/08360764-2e21-460d-94dd-aacd629a8afc" />

## When it’s useful
- Your TV or set-top box supports DLNA, but SMB playback is unreliable.
- You want a simple server without Plex/Jellyfin or a database.
- You want seek and resume to survive client restarts.

## Features
- DLNA/UPnP: SSDP, `desc.xml`, `ContentDirectory:Browse`, HTTP streaming with `Range`.
- Video: `.mp4`, `.mkv`, `.avi`, `.m4v`.
- On-disk watch progress that survives client restarts.
- Optional `ffmpeg` TV stream to smooth bitrate spikes on Wi‑Fi.
- Browse caching and safe media folder switching via `POST /set-media-dir`.
- macOS app with light and dark themes.

## Quick start
Requirement: Go `1.26+`.

```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

Then select **Home Cinema** from the DLNA source list on your TV or set-top box.

## If playback stutters on Wi‑Fi
`--tv-stream` is enabled by default and adds an alternative stream through `ffmpeg`.

- If `ffmpeg` is missing, the server disables the TV stream automatically and logs a warning.
- `ffprobe` is recommended for durations and timecodes.

Example tuning:

```bash
./HomeCinemaServer --tv-stream=true --tv-maxrate-mbps 8 --tv-bufsize-mbps 16 --tv-crf 23
```

## Logs and progress
By default the server stores data in:

- macOS: `~/Library/Application Support/HomeCinema/`

This directory contains:

- `server.log`
- `progress.json`

You can override it with `HOMECINEMA_DATA_DIR` or `--data-dir`.

## Control and security
`/set-media-dir` accepts `POST` only and is localhost-only by default.

To allow remote control explicitly:

```bash
./HomeCinemaServer --allow-remote-control
```

Useful requests:

```bash
# status
curl -s http://127.0.0.1:8080/

# change the media folder
curl -s -X POST --data-urlencode mediaDir="$HOME/Movies" http://127.0.0.1:8080/set-media-dir
```

## macOS app
Build the `.app`:

```bash
./build/home-cinema/build_app.sh
```

Launch the app:

```bash
./build/home-cinema/run_control_app.sh
```

The app builds a universal `.app`, bundles a fresh server binary, and uses helper scripts to start and stop the server. When started from the app, the server still stores logs and progress in `~/Library/Application Support/HomeCinema/`.

## Local pre-publish check
The repository includes a pre-publish verification script:

```bash
./scripts/prepublish_check.sh
```

It does three things:

- scans for obvious sensitive patterns
- runs `go test ./...`
- prints untracked files and current `git status`

## Handy flags
- `--media-dir` — media library folder.
- `--port` — HTTP port.
- `--tv-stream` — enable or disable the TV stream.
- `--tv-stream-first` — put the TV stream first in DLNA resources.
- `--stream-buf-mb` — streaming buffer size.
- `--warmup-meta` and `--warmup-meta-throttle` — metadata warmup via `ffprobe`.

Full list:

```bash
./HomeCinemaServer -h
```

## Project layout
- `app.go`, `browse.go`, `streaming.go`, `progress.go`, `metadata.go` and related files — server logic.
- `build/home-cinema/HomeCinemaControlSwift/` — SwiftUI macOS app Home Cinema.
- `build/home-cinema/build_app.sh` — `.app` build script.
- `build/home-cinema/run_control_app.sh` — `.app` launcher script.
- `scripts/prepublish_check.sh` — local pre-publish verification.

## License and changes
- MIT — [LICENSE](LICENSE)
- Change history — [CHANGELOG.md](CHANGELOG.md)
