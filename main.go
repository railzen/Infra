//go:build windows

// Intra opens websites as standalone desktop apps, with per-site DNS records
// and a certificate trust list that ignores the Windows certificate store.
package main

import "intra/internal/app"

var version = "1.0.0"

func main() { app.Run(version) }
