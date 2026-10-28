module intra

go 1.25

require (
	github.com/wailsapp/go-webview2 v1.0.23
	golang.org/x/sys v0.36.0
)

// patched copy, see third_party/go-webview2/INTRA_PATCHES.md
replace github.com/wailsapp/go-webview2 => ./third_party/go-webview2
