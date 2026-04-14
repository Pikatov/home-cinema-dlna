# Home Cinema

[Русская версия →](README.md)

Home Cinema is a small Go-based DLNA/UPnP server with a companion macOS control app. If you just want to point your TV at a folder of movies without bringing in Plex, Jellyfin, a database, or a full media stack, this project is built for that workflow.

The current release prepared in this repository is **1.7**.

MacOS App Screenshots:

<img width="408" height="276" alt="Light" src="https://github.com/user-attachments/assets/a4c35f90-6df3-4cd9-a1d8-ff930cf9da99" />
<img width="408" height="276" alt="Dark" src="https://github.com/user-attachments/assets/4959e841-9816-411e-811e-27e18015e5cc" />

## What the project does well
- Exposes a DLNA/UPnP `MediaServer` and `ContentDirectory`.
- Streams video over HTTP with `Range`, so seek and resume behave the way DLNA clients expect.
- Stores watch progress on disk and survives client or server restarts.
- Can start a movie directly from the saved watch position when progress already exists.
- Lets you inspect, reset, and remove individual progress entries from the macOS app.
- Can generate an alternative TV stream through `ffmpeg` when direct playback over Wi‑Fi is shaky.
- Provides local control endpoints for changing the media folder and managing saved progress.

## When it makes sense
- Your TV or set-top box supports DLNA, but SMB playback is unreliable.
- You want something much lighter than a full media platform.
- You care about “resume where I left off” more than posters, indexing, and library metadata.

## Quick start
Requirement: Go `1.26+`.

```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

Then pick **Home Cinema** from the DLNA source list on your TV or set-top box.

## If playback stutters on Wi‑Fi
`--tv-stream` is enabled by default. The server can add an `ffmpeg`-based alternative stream, and for heavier files it can even move that stream higher in the resource order so clients are more likely to choose the smoother option.

- If `ffmpeg` is missing, the server disables that extra stream automatically and logs a warning.
- `ffprobe` is recommended for duration and timecode handling.

Example tuning:

```bash
./HomeCinemaServer \
  --tv-stream=true \
  --tv-auto-first=true \
  --tv-auto-first-mbps 18 \
  --tv-maxrate-mbps 8 \
  --tv-bufsize-mbps 16 \
  --tv-crf 23
```

## Logs and watch progress
By default the server stores its state in:

- macOS: `~/Library/Application Support/HomeCinema/`

The main files are:

- `server.log`
- `progress.json`

You can override the location with `HOMECINEMA_DATA_DIR` or `--data-dir`.

Starting with `1.7`, the app also cleans up stale progress entries for files that no longer exist in the current media library, so deleted movies stop reappearing in the UI.

When a movie already has saved progress, the server may prefer the resource that lets the TV start directly from that point. That makes "resume playback" much more reliable, but it comes with a clear limitation: after such a start, many DLNA clients can no longer seek properly and may stop showing the remaining playback time. At the moment this is an intentional tradeoff in favor of dependable resume behavior on TVs.

## Control and security
The control endpoints are localhost-only by default:

- `POST /set-media-dir`
- `POST /reset-progress`
- `POST /delete-progress`

If you intentionally want remote control:

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

Launch it:

```bash
./build/home-cinema/run_control_app.sh
```

The build script creates a universal `Home Cinema.app`, bundles a fresh server binary, and wires in the helper scripts used to start and stop the service. In `1.7`, the UI moves further toward a polished glass-style control deck with an `Auto/Light/Dark` theme switcher, softer background motion, more tactile action buttons, and a tighter saved-progress panel.

## Local pre-release check
The repository includes a small pre-publish verification script:

```bash
./scripts/prepublish_check.sh
```

It:

- scans for obvious sensitive patterns
- runs `go test ./...`
- prints untracked files and the current `git status`

## Tests
The repository has unit tests around the core server behavior:

- XML escaping
- safe path normalization and joining
- `Range` parsing
- watch progress persistence

Run them manually with:

```bash
go test ./...
```

## Project layout
- `app.go`, `browse.go`, `streaming.go`, `progress.go`, `metadata.go`, and related files — server-side logic.
- `build/home-cinema/HomeCinemaControlSwift/` — macOS app bundle resources and build scripts.
- `build/home-cinema/HomeCinemaControlSwift/Sources/HomeCinemaControlSwift/` — current SwiftUI sources for the control app.
- `build/home-cinema/build_app.sh` — `.app` build entrypoint.
- `build/home-cinema/run_control_app.sh` — app launcher script.
- `scripts/prepublish_check.sh` — local verification before publishing.

## Docs
- Change history — [CHANGELOG.md](CHANGELOG.md)
- Release notes for `1.7` — [releases/v1.7.en.md](releases/v1.7.en.md)
- Russian release notes for `1.7` — [releases/v1.7.md](releases/v1.7.md)
- Security policy — [SECURITY.md](SECURITY.md)
- License — [LICENSE](LICENSE)
