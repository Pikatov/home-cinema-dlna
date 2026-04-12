# Home Cinema v1.6 (2026-04-12)

## Highlights
- **macOS app:** the interface is now much calmer and more compact. Extra sections and repeated state labels were removed, the main actions are clearer, and the window now feels like a focused control panel instead of an overloaded dashboard.
- **Progress management:** `Refresh` was renamed to `Reset Progress`, the app now shows a list of files with saved progress, and each item can be removed individually right from the UI.
- **Resume playback:** when a movie already has saved progress, the server now does a better job of starting playback from that exact point.
- **Cleanup:** if a movie has already been removed from the media folder, the app no longer keeps showing an outdated progress entry for it.

## App changes
- Saved-progress items now use stable sorting, so the list no longer jumps around during refreshes.
- The inline remove button next to progress entries now matches the softer visual style of the window.
- Resetting progress works both while the server is running and while it is offline, as long as local state already exists on disk.

## Resume playback limitation on TVs
- Starting a movie from its saved position comes with a tradeoff on some DLNA clients.
- After that kind of start, seeking may stop working correctly.
- The same clients may also stop showing the remaining playback time.
- This is a known limitation of the current approach and an intentional tradeoff in favor of more reliable resume behavior on TVs.

## Repository and release prep
- `README.md` and `README.en.md` were updated to match the current workflow and feature set.
- `CHANGELOG.md` was refreshed for version `1.6`.
- `.gitignore` was expanded to cover local SwiftPM caches, build artifacts, `.app` bundles, binaries, and pid files.

## Updating
- To update, replace the server binary or rebuild the `.app` from the current repository state.
- The user data format did not change in this release.
- If `progress.json` still contains entries for movies that were already removed, version `1.6` will clean them up automatically during the next state refresh.

## Assets
- `Home-Cinema-1.6-macOS-app.zip` — the ready-to-use macOS app bundle with the control UI and bundled server inside.
- `Home-Cinema-1.6-macOS-server.zip` — the standalone universal server binary for terminal or custom workflow use.
