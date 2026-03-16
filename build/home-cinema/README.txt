Home Cinema DLNA server (macOS)
================================

Files
- HomeCinemaServer — compiled Go binary (arm64).
- HomeCinemaControl.applescript — control dialog: start/stop, choose media folder, open logs.
- media_dir.txt — remembered media folder (created after first launch).
- server.log — runtime log in this folder.

Build the control app
1) From the repo root run:
   /usr/bin/osacompile -o build/home-cinema/HomeCinemaControl.app build/home-cinema/HomeCinemaControl.applescript
2) The app bundle will appear at build/home-cinema/HomeCinemaControl.app.

Run
- Double-click HomeCinemaControl.app (or open the .applescript in Script Editor). The dialog stays open, shows colored status, lets you pick a media folder via macOS picker, and updates the running server without restarting.
- Manual start without the UI:
   cd build/home-cinema && ./HomeCinemaServer --media-dir "/path/to/movies"

Notes
- The control script launches the server in a detached `screen` session named “homecinema”.
- Folder changes are sent to the running server through http://127.0.0.1:8080/set-media-dir.
