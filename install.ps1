# install.ps1 — fetch the klainmain binary for this Windows machine from the
# GitHub release assets and put it in a user-writable directory (TDD-00179).
#
#   irm https://raw.githubusercontent.com/memphisx/KlainMainLang/main/install.ps1 | iex
#
# Environment:
#   KLAINMAIN_VERSION      release to install, e.g. v0.64.0 (default: latest)
#   KLAINMAIN_INSTALL_DIR  where to put the binary (default: %LOCALAPPDATA%\Programs\klainmain)
#   KLAINMAIN_BASE_URL     alternate download base (testing; default: GitHub releases)
#
# No elevation: the binary lands under the user's profile and the folder is
# appended to the *user* PATH. The binary is the compiler only — it drives
# clang from the MSYS2 UCRT64 toolchain described in the README's Windows
# section, which is installed separately.
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

$repo = 'memphisx/KlainMainLang'
$version = if ($env:KLAINMAIN_VERSION) { $env:KLAINMAIN_VERSION } else { 'latest' }
$installDir = if ($env:KLAINMAIN_INSTALL_DIR) { $env:KLAINMAIN_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\klainmain' }

$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -ne 'AMD64') {
  throw "install.ps1: unsupported architecture '$arch' (only x64 Windows binaries are published today)"
}
$platform = 'windows-x64'

if ($env:KLAINMAIN_BASE_URL) {
  $base = $env:KLAINMAIN_BASE_URL
} elseif ($version -eq 'latest') {
  $base = "https://github.com/$repo/releases/latest/download"
} else {
  if (-not $version.StartsWith('v')) { $version = "v$version" }
  $base = "https://github.com/$repo/releases/download/$version"
}

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("klainmain-install-" + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
  Write-Host "klainmain: fetching checksums from $base ..."
  try {
    Invoke-WebRequest -UseBasicParsing -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')
  } catch {
    throw "install.ps1: no release found at $base (does the release exist, and is the version spelled like v0.64.0?)"
  }
  $lines = Get-Content (Join-Path $tmp 'checksums.txt')
  # a checksums line is "<sha256>  <name>"; a "*" before the name is sha256sum's binary-mode marker on some hosts
  $entry = $lines | Where-Object { $_ -match "^([0-9a-f]{64})\s+\*?(klainmain-v\S+-$platform\.exe)$" } | Select-Object -First 1
  if (-not $entry) {
    $assets = ($lines | ForEach-Object { (($_ -split '\s+')[1]).TrimStart('*') }) -join ', '
    throw "install.ps1: this release has no binary for $platform. A platform is left out of a release when its test lane did not pass for that version; it returns with the next release that is green there. Assets in this release: $assets"
  }
  $null = $entry -match "^([0-9a-f]{64})\s+\*?(\S+)$"
  $expected = $Matches[1]; $asset = $Matches[2]

  Write-Host "klainmain: downloading $asset ..."
  $file = Join-Path $tmp $asset
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile $file
  $actual = (Get-FileHash -Algorithm SHA256 $file).Hash.ToLower()
  if ($actual -ne $expected) { throw "install.ps1: checksum mismatch for $asset (expected $expected, got $actual)" }

  New-Item -ItemType Directory -Force -Path $installDir | Out-Null
  $target = Join-Path $installDir 'klainmain.exe'
  Copy-Item -Force $file $target
  Write-Host "klainmain: installed $(& $target --version) to $target"

  $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
  $onPath = ($userPath -split ';') -contains $installDir -or ($env:Path -split ';') -contains $installDir
  if (-not $onPath) {
    [Environment]::SetEnvironmentVariable('Path', (($userPath.TrimEnd(';')) + ';' + $installDir), 'User')
    $env:Path = "$env:Path;$installDir"
    Write-Host "note: added $installDir to your user PATH (new terminals pick it up; this one has it already)."
  }
  if (-not (Get-Command clang -ErrorAction SilentlyContinue)) {
    Write-Host "note: 'clang' is not on your PATH; klainmain needs LLVM's clang and the MSYS2 UCRT64 toolchain to build programs (see the README's Windows section)."
  }
} finally {
  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
