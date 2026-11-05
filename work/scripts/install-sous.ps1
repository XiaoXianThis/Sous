# Purpose: install or update the sous client on Windows from GitHub Releases.
# Usage:   irm https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.ps1 | iex
#          pair right away: & ([scriptblock]::Create((irm <url>))) pair https://agent.wxian.cc 123456 --name laptop
# Env:     SOUS_VERSION      tag to install, such as v0.1.1 (default: latest)
#          SOUS_INSTALL_DIR  where to put sous.exe (default: where `sous` already is, else %LOCALAPPDATA%\sous\bin)
# Needs:   PowerShell 5.1 or later.
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$SousArgs)
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$repo = 'XiaoXianThis/Sous'

$arch = switch ($env:PROCESSOR_ARCHITECTURE) { 'ARM64' { 'arm64' } 'AMD64' { 'amd64' } default { throw "unsupported architecture $env:PROCESSOR_ARCHITECTURE" } }

$tag = $env:SOUS_VERSION
if (-not $tag) {
  $resp = Invoke-WebRequest -Uri "https://github.com/$repo/releases/latest" -MaximumRedirection 0 -ErrorAction SilentlyContinue -UseBasicParsing
  $loc = [string]$resp.Headers['Location']
  if (-not $loc) { throw 'cannot find the latest release' }
  $tag = $loc.Split('/')[-1]
}
if (-not $tag.StartsWith('v')) { $tag = "v$tag" }
$ver = $tag.Substring(1)

$existing = Get-Command sous -ErrorAction SilentlyContinue
if ($env:SOUS_INSTALL_DIR) { $dir = $env:SOUS_INSTALL_DIR }
elseif ($existing) { $dir = Split-Path $existing.Source }
else { $dir = Join-Path $env:LOCALAPPDATA 'sous\bin' }
New-Item -ItemType Directory -Force -Path $dir | Out-Null
$exe = Join-Path $dir 'sous.exe'

if ($existing -and ((& $existing.Source version) -eq $tag)) {
  Write-Host "sous $tag is already installed at $($existing.Source)"
} else {
  $name = "sous_${ver}_windows_${arch}.zip"
  $base = "https://github.com/$repo/releases/download/$tag"
  $tmp = Join-Path ([IO.Path]::GetTempPath()) ("sous-" + [guid]::NewGuid())
  New-Item -ItemType Directory -Path $tmp | Out-Null
  try {
    Write-Host "downloading sous $tag (windows/$arch)..."
    Invoke-WebRequest -Uri "$base/$name" -OutFile "$tmp\$name" -UseBasicParsing
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile "$tmp\checksums.txt" -UseBasicParsing
    $line = Get-Content "$tmp\checksums.txt" | Where-Object { $_ -match " $([regex]::Escape($name))$" } | Select-Object -First 1
    if (-not $line) { throw "$name is not in checksums.txt" }
    $want = ($line -split '\s+')[0]
    if ((Get-FileHash "$tmp\$name" -Algorithm SHA256).Hash -ne $want.ToUpper()) { throw "checksum mismatch for $name; not installing" }
    Expand-Archive -Path "$tmp\$name" -DestinationPath $tmp -Force
    # A running exe can be renamed but not overwritten.
    if (Test-Path $exe) { Move-Item -Force $exe "$exe.old" }
    Copy-Item "$tmp\sous.exe" $exe
    Write-Host "installed sous $tag to $exe"
  } finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  }
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $dir) {
  [Environment]::SetEnvironmentVariable('Path', "$userPath;$dir", 'User')
  $env:Path = "$env:Path;$dir"
  Write-Host "added $dir to your PATH (new terminals pick it up)"
}

if ($SousArgs.Count -gt 0) { & $exe @SousArgs }
else { Write-Host 'next: sous pair <hub-url> <code>   (the Machines panel on the web shows the command)' }
