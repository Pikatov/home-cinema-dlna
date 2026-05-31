# Home Cinema v1.8 (2026-05-31)

Home Cinema 1.8 focuses on two things: making TV playback start more reliably, and making the macOS control app easier to read while something is playing.

## What you will notice first
- **A redesigned macOS app.** The control window is calmer and more useful, with an `ON AIR` block, live stream rows, clearer server state, and a cleaner saved-progress list.
- **Now playing is visible.** The app shows active sessions with title, device, stream type (`DIRECT`, `TRANSCODE`, `RESUME`), and current timecode.
- **Progress reset has undo.** After clearing saved progress, the app gives you a 5-second undo banner.
- **Heavy MKVs start on more TVs.** For large files and HEVC/HDR MKVs, the server can move the compatible ffmpeg TV stream ahead of the original file.

## Playback and DLNA
- TV streams now advertise duration more completely: `Content-Duration`, `TimeSeekRange.dlna.org`, `X-Content-Duration`, and `X-AvailableSeekRange`.
- The TV stream now advertises DLNA time seek support (`DLNA.ORG_OP=10`) and understands incoming `TimeSeekRange.dlna.org` requests.
- ffmpeg transcodes are limited (`--max-tv-streams`, default 1). When the TV reconnects or seeks, the old ffmpeg process for that same client is cancelled.
- The TV stream now uses AC3 audio and a larger default VBV buffer (`40 Mbps`), which is friendlier to TVs and high-motion scenes.
- Honest limitation: some TVs may still display the ffmpeg/MPEG-TS stream as `mpg` or show the end time as `0:00`. The server sends duration and time-seek metadata, but the final display is up to the DLNA client.

## Watch progress
- Progress is saved in bytes and in seconds when possible.
- Early TV probe requests are less likely to overwrite good progress.
- Progress entries for deleted files are cleaned up on startup, on media-folder change, and then periodically once an hour.
- After a restart, the server can recover duration from saved progress while the `ffprobe` cache is still warming up.

## Server
- Added `/stats`, a lightweight endpoint used by the UI for live stream state.
- `/video/` remains direct file streaming with HTTP `Range`.
- `/tv/` serves a compatible ffmpeg stream for TVs that struggle with the original file.
- `/resume/` serves a virtual tail of the original file from the saved position with proper `Content-Range`, `Content-Length`, and DLNA headers.
- SSDP shutdown is cleaner: the server sends `ssdp:byebye` before exiting so TVs drop the old server entry sooner.

## Requirements
- macOS app: **macOS 14 Sonoma or newer**.
- Server: Go `1.26+` when building from source.
- `ffprobe` is recommended for duration and timecodes.
- `ffmpeg` is required for the TV stream; if it is missing, the server disables that mode and continues with direct file streaming.

## Updating
- User data format did not change.
- Replace the `.app` or the standalone server binary.
- After updating, reopen Home Cinema on the TV so the TV drops any stale DLNA cache.

## Assets
- `Home-Cinema-1.8-macOS-app.zip` — ready-to-use macOS app bundle with the server inside.
- `Home-Cinema-1.8-macOS-server.zip` — standalone universal server binary for terminal or custom workflows.
