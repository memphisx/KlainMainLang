# Run a command with every throwaway write of the test / conformance harness on
# a RAM drive, then remove the drive. Windows counterpart of run.sh.
#
#   tools\ramdisk\run.ps1 -SizeGB 12 -Run "go test ./... -timeout 80m"
#
# The command is one string (run through cmd /c): PowerShell would otherwise
# try to bind the command's own -flags as parameters of this script.
#
# Windows has no built-in RAM drive; this drives OSFMount's command line
# (`winget install PassMark.OSFMount`), which needs an elevated shell to create
# or remove a drive. If the drive letter already holds a volume it is reused
# and left in place — so creating it once per boot from an elevated prompt
#   & "$env:ProgramFiles\OSFMount\OSFMount.com" -a -t vm -s 12G -m R: -o format:ntfs:KMLScratch
# lets every later run stay unelevated.
param(
    [Parameter(Mandatory = $true)][string]$Run,
    [int]$SizeGB = 8,
    [string]$Letter = "R"
)
$ErrorActionPreference = "Stop"

$osf = Join-Path $env:ProgramFiles "OSFMount\OSFMount.com"
$drive = "${Letter}:"
$created = $false
if (-not (Test-Path "$drive\")) {
    if (-not (Test-Path $osf)) { throw "OSFMount not found at $osf (winget install PassMark.OSFMount)" }
    & $osf -a -t vm -s "${SizeGB}G" -m $drive -o "format:ntfs:KMLScratch" | Out-Null
    for ($i = 0; $i -lt 50 -and -not (Test-Path "$drive\"); $i++) { Start-Sleep -Milliseconds 200 }
    if (-not (Test-Path "$drive\")) { throw "RAM drive $drive did not appear (elevated shell?)" }
    $created = $true
}

$root = "$drive\kml-scratch"
New-Item -ItemType Directory -Force $root | Out-Null
$env:KML_SCRATCH = $root
$env:GOTMPDIR = $root
[Console]::Error.WriteLine("ramdisk: $root")
$code = 1
try {
    & cmd.exe /c $Run
    $code = $LASTEXITCODE
}
finally {
    if ($created) { & $osf -d -m $drive | Out-Null }
}
exit $code
