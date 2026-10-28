param([string]$Version = "1.0.0")
Set-Location $PSScriptRoot
function Check($step) { if ($LASTEXITCODE -ne 0) { throw "$step failed" } }

# icon + manifest + version resource -> rsrc_windows_amd64.syso
Push-Location tools/genicon
go run . -root ../.. -version $Version; Check "genicon"
Pop-Location

# -unsafeptr=false: the Win32 message handlers intentionally turn lParam into pointers
go vet -unsafeptr=false ./...; Check "go vet"
go test ./internal/...; Check "go test"

# v0.15.0 supports Go 1.25; pin the tool instead of using a moving latest.
$garbleVersion = "v0.15.0"
$previousGoBin = $env:GOBIN
try {
    $env:GOBIN = Join-Path $PSScriptRoot "build/tools"
    New-Item -ItemType Directory -Force $env:GOBIN | Out-Null
    go install "mvdan.cc/garble@$garbleVersion"; Check "install garble"
    $garble = Join-Path $env:GOBIN "garble.exe"
} finally {
    $env:GOBIN = $previousGoBin
}

New-Item -ItemType Directory -Force release | Out-Null
$env:CGO_ENABLED = "0"
# Standard identifier obfuscation only: no -literals or -tiny.
& $garble build -trimpath -ldflags "-H windowsgui -s -w -X main.version=$Version" -o release/Intra.exe .; Check "garble build"
Get-Item release/Intra.exe | Select-Object Name, Length
