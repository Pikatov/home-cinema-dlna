# Home Cinema DLNA Server

Минимальный DLNA/UPnP медиасервер на Go с локальным управлением для macOS. В комплекте: CLI/HTTP сервер и две утилиты управления (AppleScript диалог и SwiftUI-приложение).

## Возможности
- SSDP-анонс, ContentDirectory и раздача видеофайлов с поддержкой Range.
- Быстрый кеширование дерева папок (DLNA browse cache).
- Смена медиатеки через HTTP `POST /set-media-dir` или через UI.
- macOS control UI: AppleScript диалог (`HomeCinemaControl.applescript`) и SwiftUI-приложение (`HomeCinemaControlSwift`).

## Требования
- Go 1.21+ (go.mod указывает 1.26.1).
- macOS 12+ для UI сборки; нужны Xcode CLI tools (`xcrun`, `swiftc`, `sips`, `iconutil`).

## Быстрый старт (сервер)
```bash
go build -o build/home-cinema/HomeCinemaServer ./main.go
./build/home-cinema/HomeCinemaServer --media-dir "/path/to/movies" --port 8080
```
Сервер логирует в `server.log` рядом с бинарём (игнорируется в .gitignore).

## Сборка SwiftUI-контроллера
```bash
cd build/home-cinema/HomeCinemaControlSwift
./build.sh
```
Скрипт собирает универсальный `.app` и упаковывает свежий сервер в бандл. Артефакты бандла и бинарников не хранятся в git (.gitignore).

## Структура
- `main.go` — DLNA сервер.
- `build/home-cinema/HomeCinemaControl.applescript` — диалог запуска/остановки.
- `build/home-cinema/HomeCinemaControlSwift/` — SwiftUI-приложение и билд-скрипт.
- `build/home-cinema/README.txt` — краткая справка по UI.

## Конфигурация и приватность
- Путь к медиатеке передаётся флагом `--media-dir` или через UI; не коммитится.
- Логи и локальные пути добавлены в `.gitignore`.
- Перед публикацией очистите каталоги `build/home-cinema/*.log`, `media_dir.txt`, `.app` и бинарники (уже исключены).

## Лицензия
MIT — см. файл `LICENSE`.
