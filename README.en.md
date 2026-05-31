# Home Cinema

[Русская версия →](README.md)

Home Cinema is a small Go-based DLNA/UPnP server with a companion macOS control app. If you just want to point your TV at a folder of movies without bringing in Plex, Jellyfin, a database, or a full media stack, this project is built for that workflow.

The current release prepared in this repository is **1.8**.

macOS app screenshots:

<img width="408" height="276" alt="Light" src="https://github.com/user-attachments/assets/a4c35f90-6df3-4cd9-a1d8-ff930cf9da99" />
<img width="408" height="276" alt="Dark" src="https://github.com/user-attachments/assets/4959e841-9816-411e-811e-27e18015e5cc" />

## What the project does well
- Exposes a DLNA/UPnP `MediaServer` and `ContentDirectory`.
- Streams video over HTTP with `Range`, so seek and resume behave the way DLNA clients expect.
- Stores watch progress on disk and survives client or server restarts.
- Can resume from a saved position for the TV stream and `/resume/` flow.
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
`--tv-stream` is enabled by default. The server can add an `ffmpeg`-based alternative stream. For heavy files and some HEVC/HDR MKVs, that TV stream is automatically moved first because many TVs cannot start those MKVs directly.

- If `ffmpeg` is missing, the server disables that extra stream automatically and logs a warning.
- `ffprobe` is recommended for duration and timecode handling.
- Some TVs still display that compatible stream as `mpg` or show the end time as `0:00`. The server sends DLNA duration and time-seek headers, but the final display depends on the TV.

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

The server cleans up stale progress entries for files that no longer exist in the current media library (on folder change and hourly), so deleted movies stop reappearing in the UI.

Saved progress is used in movie titles (`[▶ 12:34 - 1:45:00]`), in the macOS app, and in the TV stream when the server can start ffmpeg from a known position. Some DLNA clients may still handle seeking and full-duration display differently from a local player.

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

The build script creates a universal `Home Cinema.app`, bundles a fresh server binary, and wires in the helper scripts used to start and stop the service. In `1.8`, the window shows `ON AIR`, active streams, saved progress, and an undo banner after clearing progress. Status polling pauses when the window is inactive.

Minimum requirement: **macOS 14 (Sonoma)**. For older systems use the standalone server binary.

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
- DLNA duration/time-seek headers
- active `/stats` sessions
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
- Russian release notes for `1.8` — [releases/v1.8.md](releases/v1.8.md)
- Release notes for `1.8` — [releases/v1.8.en.md](releases/v1.8.en.md)
- Release notes for `1.7` — [releases/v1.7.en.md](releases/v1.7.en.md)
- Russian release notes for `1.7` — [releases/v1.7.md](releases/v1.7.md)
- Security policy — [SECURITY.md](SECURITY.md)
- License — [LICENSE](LICENSE)
