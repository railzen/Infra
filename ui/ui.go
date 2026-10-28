// Package ui holds the embedded front-end used by both the title bar/pages
// webview and the favorites dropdown.
package ui

import _ "embed"

//go:embed index.html
var HTML string
