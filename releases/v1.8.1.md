# Home Cinema v1.8.1 (2026-06-14)

Home Cinema 1.8.1 — hotfix для запуска фильмов на ТВ после релиза 1.8.

## Что исправлено
- Приложение для macOS теперь вкладывает `ffmpeg` и `ffprobe` внутрь `Home Cinema.app` вместе с нужными динамическими библиотеками. На Apple Silicon Mac TV-поток больше не требует отдельной установки Homebrew/ffmpeg.
- Сервер сначала ищет встроенные `ffmpeg`/`ffprobe`, затем системные версии. Если найденный бинарник не запускается, сервер пробует следующий вариант.
- Для крупных `.mkv`/`.webm` сервер ставит совместимый TV-поток первым даже тогда, когда длительность ещё не успела прогреться через `ffprobe`.
- Прямой `/video/` больше не отменяет стартовый поток, когда DLNA-клиент сразу после запроса без `Range` открывает `Range` с нулевого байта.

## Важно
- Встроенный `ffmpeg` в этом app build взят из Apple Silicon Homebrew. На Intel Mac приложение продолжит искать системный `/usr/local/bin/ffmpeg`, если встроенный бинарник не подходит.
- Standalone server zip не включает `ffmpeg`; для TV-потока в standalone-режиме нужен системный `ffmpeg`.

## Assets
- `Home-Cinema-1.8.1-macOS-app.zip` — готовое macOS-приложение `Home Cinema.app` со встроенным сервером, `ffmpeg`, `ffprobe` и библиотеками для TV-потока.
- `Home-Cinema-1.8.1-macOS-server.zip` — standalone universal server binary для запуска без GUI.
