# Changelog

## 1.5 — 2026-04-08
- Refactor: сервер разбит на отдельные Go-файлы по зонам ответственности (`streaming`, `progress`, `browse`, `metadata`, `events`, `ssdp`, `control`, `xml`, `helpers`).
- Progress: сохранение прогресса сделано атомарным и устойчивым к рестартам клиента; байтовый прогресс теперь хранит длительность, есть финальный flush при остановке сервера и защита от затирания прогресса ранними probe-запросами.
- Streaming: снижены риски фризов за счёт ограничения ожидания `ffprobe`, pause-aware warmup метаданных во время активного стрима, более безопасного завершения и снижения частоты UPnP-обновлений каталога.
- DLNA/XML: исправлено полноценное XML-экранирование DIDL-Lite для имён, URL и атрибутов.
- Tests: добавлены локальные unit-тесты для путей, `Range`, XML и прогресса.
- macOS app: приложение переименовано в Home Cinema, обновлён SwiftUI-интерфейс со светлой и тёмной темой и glass-стилем.
- Build workflow: добавлены новые скрипты `build/home-cinema/build_app.sh`, `build/home-cinema/run_control_app.sh` и helper-скрипты запуска сервера внутри `.app`.
- Cleanup: удалены legacy DMG- и AppleScript-артефакты, README переписаны под новый workflow без упаковки DMG.

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
