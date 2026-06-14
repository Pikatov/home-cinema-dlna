# Home Cinema v1.8.1 (2026-06-14)

Home Cinema 1.8.1 is a playback hotfix for the 1.8 release.

## Fixed
- The macOS app now bundles `ffmpeg` and `ffprobe` inside `Home Cinema.app`, together with the dynamic libraries they need. On Apple Silicon Macs, the TV stream no longer requires a separate Homebrew/ffmpeg install.
- The server tries bundled `ffmpeg`/`ffprobe` first, then falls back to system binaries. If a discovered binary cannot run, the server tries the next candidate.
- Large `.mkv`/`.webm` files now prefer the compatible TV stream even while duration metadata is still warming up through `ffprobe`.
- Direct `/video/` playback no longer cancels the startup stream when a DLNA client opens a zero-based `Range` request immediately after a no-`Range` request.

## Notes
- The bundled `ffmpeg` in this app build comes from Apple Silicon Homebrew. On Intel Macs, the app will keep looking for a system `/usr/local/bin/ffmpeg` if the bundled binary is not compatible.
- The standalone server zip does not include `ffmpeg`; standalone TV streaming still needs a system `ffmpeg`.

## Assets
- `Home-Cinema-1.8.1-macOS-app.zip` — ready-to-use macOS app bundle with the server, `ffmpeg`, `ffprobe`, and TV-stream libraries included.
- `Home-Cinema-1.8.1-macOS-server.zip` — standalone universal server binary for terminal or custom workflows.
