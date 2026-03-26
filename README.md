# Home Cinema (DLNA/UPnP Media Server)

Минимальный DLNA/UPnP медиасервер на Go для macOS (и других ОС, где доступен Go). Поднимает UPnP MediaServer + ContentDirectory и раздаёт видеофайлы по HTTP с поддержкой `Range` (перемотка/продолжение просмотра).

В репозитории также есть простое приложение управления для macOS (SwiftUI), которое умеет выбрать папку, запускать/останавливать сервер и открывать логи.

## Возможности
- UPnP/DLNA: SSDP-анонс, `desc.xml`, `ContentDirectory:Browse`, HTTP streaming с `Range`.
- Поддержка видео: `.mp4`, `.mkv`, `.avi`.
- Кеширование результата Browse (ускоряет навигацию на некоторых ТВ).
- Прогресс просмотра: сервер сохраняет позицию и показывает таймкод в имени файла (например: `Movie.mkv [▶ 12:34]`).
- Смена медиатеки локально: `POST /set-media-dir` (по умолчанию доступно только с `localhost`).

## Требования
- Go 1.26+.
- (Опционально) `ffprobe` из FFmpeg — нужно для вычисления длительности (таймкод в названии, `duration` в DLNA). Без `ffprobe` сервер продолжит работать, но таймкоды/длительность могут не отображаться.
- (Опционально) `ffmpeg` — нужен для TV-потока (`/tv/`, флаг `--tv-stream`), который ограничивает пики битрейта и помогает убрать зависания на слабом Wi‑Fi. Без `ffmpeg` отключите `--tv-stream=false`.
- Для сборки UI: macOS 12+ и Xcode Command Line Tools (`xcrun`, `swiftc`, `sips`, `iconutil`).

## Быстрый старт (сервер из исходников)
```bash
go build -o HomeCinemaServer .
./HomeCinemaServer --media-dir "$HOME/Movies" --port 8080
```

После запуска откройте на ТВ/приставке список DLNA/медиасерверов и выберите **Home Cinema**.

Если воспроизведение тормозит по Wi‑Fi, включите TV-поток (включён по умолчанию) и подберите `--tv-maxrate-mbps`:
```bash
./HomeCinemaServer --tv-stream=true --tv-maxrate-mbps 8 --tv-bufsize-mbps 16 --tv-crf 23
```

## Где хранятся логи и прогресс
По умолчанию сервер пишет состояние в пользовательскую папку:
- macOS: `~/Library/Application Support/HomeCinema/` (файлы `server.log` и `progress.json`)

Можно переопределить:
- переменной окружения `HOMECINEMA_DATA_DIR`
- или флагом `--data-dir "/путь/к/папке"`

## Локальное управление и безопасность
Эндпоинт смены папки (`/set-media-dir`) по умолчанию доступен **только с localhost**. Это сделано, чтобы устройства в локальной сети не могли удалённо переключить сервер на произвольные каталоги.

Если вы понимаете риски и хотите разрешить удалённое управление, запустите сервер с флагом:
```bash
./HomeCinemaServer --allow-remote-control
```

Полезные запросы:
```bash
# статус (для localhost возвращает полный путь, для сети — только имя папки)
curl -s http://127.0.0.1:8080/ | jq

# сменить папку медиатеки (по умолчанию только localhost)
curl -s -X POST --data-urlencode mediaDir="$HOME/Movies" http://127.0.0.1:8080/set-media-dir | jq
```

## SwiftUI-приложение управления (macOS)
Сборка:
```bash
cd build/home-cinema/HomeCinemaControlSwift
./build.sh
```

Скрипт соберёт универсальный `.app` и вложит внутрь свежий бинарь сервера. Логи UI/запуска пишутся в `/tmp/homecinema.log` (кнопка **Logs** открывает этот файл).

## Конфигурация и приватность
- Путь к медиатеке передаётся флагом `--media-dir` или через UI; не коммитится.
- Логи и локальные пути добавлены в `.gitignore`.

## Сборка DMG (опционально)
Скрипт `build/home-cinema/make_dmg.sh` собирает красивый DMG (артефакт **не** коммитится).

Зависимость:
```bash
brew install create-dmg
```

Сборка:
```bash
./build/home-cinema/make_dmg.sh
```

## Структура проекта
- `main.go` — DLNA/UPnP сервер.
- `build/home-cinema/HomeCinemaControlSwift/` — SwiftUI UI + `build.sh`.
- `build/home-cinema/make_dmg.sh`, `build/home-cinema/dmg-background.png` — упаковка DMG.

## Лицензия
MIT — см. `LICENSE`.
