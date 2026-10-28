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

New-Item -ItemType Directory -Force release | Out-Null
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-H windowsgui -s -w -X main.version=$Version" -o release/Intra.exe .; Check "go build"
Get-Item release/Intra.exe | Select-Object Name, Length
