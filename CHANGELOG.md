# Changelog

## 1.4 — 2026-04-01
- Security: устранён directory traversal в `/video/`, `/tv/` и DLNA `Browse`.
- Fix: убрано задвоение расширения в названиях (например `.mkv.mkv`).
- Repo: добавлены GitHub Actions CI и Dependabot; исправлен `module` в `go.mod`.

## 1.3 — 2026-03-29
- Исправлено отображение прогресса/длительности на ТВ (DLNA TimeSeekRange / Content-Duration для `/video`).
- Исправлено неверное определение формата: для DLNA первым ресурсом отдаётся оригинальный файл (`/video`), а TV‑транскод (`/tv`) — альтернативой.
- Добавлен флаг `--tv-stream-first` для возврата старого порядка `<res>` при необходимости.

## 1.2 — 2026-03-26
- TV stream via ffmpeg + faster progress.
