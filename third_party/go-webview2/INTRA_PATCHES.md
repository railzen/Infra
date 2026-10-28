# Local patches

Vendored from github.com/wailsapp/go-webview2 v1.0.23 (MIT, see LICENSE).

- `pkg/edge/chromium.go` `errorCallback`: upstream calls `os.Exit(1)` on any
  WebView2 error. Intra only exits when the webview has not been created yet;
  later errors are logged so the app keeps running.
- Trimmed to what a windows/amd64 build with the pure-Go loader uses:
  `pkg/edge`, `pkg/combridge`, `internal/w32`, `webviewloader`. Removed
  `pkg/webview2`, the embedded WebView2Loader.dll files and their
  `native_webview2loader` build variant, the 386/arm64 files, tests and docs.
