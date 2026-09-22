# Self-test for install.ps1 on a Windows runner.
#
# This is the Windows half of scripts/install-selftest.sh, and it exists
# separately rather than inside the workflow for one reason: a Windows test that
# quietly does not run looks exactly like a Windows test that passed. Keeping the
# checks in a file that the job must invoke, and which fails loudly when it cannot
# run, is the difference between the two.
#
# It runs the real install.ps1 against archives built from the current commit and
# served over loopback HTTP, and it tries to falsify the installer rather than
# confirm it. The checks that matter:
#
#   * A tampered checksums.txt must be refused: non-zero exit, the mismatch named,
#     and nothing installed. An installer that silently skips verification is
#     worse than no installer at all, because it advertises a guarantee it does
#     not provide, and this is the only case that proves the guarantee is real.
#   * An asset missing from checksums.txt must be refused, not installed anyway.
#   * A version whose archive does not exist must fail rather than fall back to
#     another one.
#   * The installed exe must be byte for byte the one in the archive just built,
#     so "installed successfully" cannot mean "installed something else".
#   * Re-running must leave exactly one version directory. The installer edits the
#     user PATH, and an upgrade that appends instead of replacing grows it without
#     bound.
#
# Two things about running the real installer that are deliberate:
#
#   1. Every invocation is a child process (`powershell.exe -File install.ps1`),
#      because that is how a user runs it and because install.ps1's failures are
#      terminating errors whose exit status is only visible from outside. Windows
#      PowerShell 5.1 is used for the first run: it is the version most Windows
#      machines still ship, so it is the one worth being wrong about.
#   2. install.ps1 writes the install directory into the *persisted* user PATH.
#      That is its documented behaviour and the test must not work around it, so
#      the test installs into a temp directory and removes the entry it added
#      again on exit. On an ephemeral runner the leak would not matter; on a
#      self-hosted one it would accumulate a dead PATH entry per run.
#
# Usage: pwsh -File scripts/install-selftest.ps1 [-Dist dist] [-Installer install.ps1] [-Version VERSION]
#
# Requires: Windows, the Go toolchain (to build scripts/servedist), and a dist/
# built by `make release-snapshot`.

[CmdletBinding()]
param(
    [string]$Dist = 'dist',
    [string]$Installer = 'install.ps1',
    [string]$Version = ''
)

# No Set-StrictMode: install.ps1 is exercised as a child process rather than dot
# sourced, but strict mode would still be a second, invisible difference between
# this test's environment and a real install.
$ErrorActionPreference = 'Stop'

$Go = if ($env:GO) { $env:GO } else { 'go' }

# Failures are counted rather than thrown: the exit status is what CI reads, and a
# run that stops at the first failure hides the other four -- a list is a more
# useful report than a queue.
$script:Failures = 0
function Pass($Message) { Write-Host "   ok   $Message" }
function Fail($Message) { Write-Host "   FAIL $Message" -ForegroundColor Red; $script:Failures++ }

# Printed rather than passed over in silence: a check that cannot run here is a
# real hole in the coverage, and the CI log is where it has to be visible.
function Skip($Message) { Write-Host "   skip $Message" -ForegroundColor Yellow }

function Show($Command) { Write-Host "`$ $Command" }

function Get-Sha256($Path) { (Get-FileHash -Algorithm SHA256 -LiteralPath $Path).Hash }

$tmpRoot = Join-Path ([System.IO.Path]::GetTempPath()) ('sael-selftest-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $tmpRoot | Out-Null

$server = $null
function Cleanup {
    if ($server -and -not $server.HasExited) {
        Stop-Process -Id $server.Id -Force -ErrorAction SilentlyContinue
    }
    # The installer prepends the temp version directory to the persisted user
    # PATH; take it back out, so a self-hosted runner does not accumulate a dead
    # entry pointing into a temp directory that no longer exists.
    try {
        $current = [Environment]::GetEnvironmentVariable('Path', 'User')
        if ($current) {
            $kept = @($current -split ';' | Where-Object {
                $_ -and -not $_.TrimEnd('\').StartsWith($tmpRoot.TrimEnd('\'), [StringComparison]::OrdinalIgnoreCase)
            })
            $updated = $kept -join ';'
            if ($updated -ne $current) { [Environment]::SetEnvironmentVariable('Path', $updated, 'User') }
        }
    } catch {
        Write-Host "could not restore the user PATH: $($_.Exception.Message)" -ForegroundColor Yellow
    }
    Remove-Item -LiteralPath $tmpRoot -Recurse -Force -ErrorAction SilentlyContinue
}

if ($env:OS -ne 'Windows_NT') {
    # Running this on Linux or macOS would silently skip every Windows check,
    # which is the outcome this file exists to make impossible.
    throw "this self-test drives install.ps1 and only runs on Windows"
}

$DistPath = (Resolve-Path -LiteralPath $Dist).Path
if (-not (Test-Path -LiteralPath $Installer -PathType Leaf)) { throw "no installer at $Installer" }
$InstallerPath = (Resolve-Path -LiteralPath $Installer).Path

try {

# --- syntax first -------------------------------------------------------------
#
# install.ps1 is only ever run on Windows, so a parse error in it survives every
# other check in this repository. Parsing it here is free and names the line.
Write-Host "`ninstall.ps1 self-test"
$tokens = $null
$parseErrors = $null
[System.Management.Automation.Language.Parser]::ParseFile($InstallerPath, [ref]$tokens, [ref]$parseErrors) | Out-Null
if ($parseErrors -and $parseErrors.Count -gt 0) {
    foreach ($e in $parseErrors) { Write-Host "   $($e.Extent.StartLineNumber): $($e.Message)" -ForegroundColor Red }
    throw "install.ps1 does not parse"
}
Pass "install.ps1 parses"

# The Windows archive and nothing else: this fixes the architecture under test to
# the one the runner is, which is the one the installer would pick.
if (-not $Version) {
    $candidate = Get-ChildItem -LiteralPath $DistPath -Filter 'sael_*_windows_amd64.zip' | Select-Object -First 1
    if (-not $candidate) { throw "no sael_*_windows_amd64.zip in $DistPath; run 'make release-snapshot'" }
    $name = $candidate.Name
    $Version = $name.Substring(5, $name.Length - 5 - '_windows_amd64.zip'.Length)
}
$Asset = "sael_${Version}_windows_amd64.zip"
$Archive = Join-Path $DistPath $Asset
if (-not (Test-Path -LiteralPath $Archive)) { throw "no $Asset in $DistPath" }

Write-Host "  installer: $InstallerPath"
Write-Host "  dist:      $DistPath"
Write-Host "  asset:     $Asset"
Write-Host "  platform:  windows/amd64`n"

# --- the mirror ---------------------------------------------------------------
#
# Three copies of dist/, each served from its own path, so one long-lived server
# answers every case: ok/ is the honest release, tampered/ has one hex digit of
# the archive's checksum flipped, missing/ omits the archive's line entirely.
$mirror = Join-Path $tmpRoot 'mirror'
New-Item -ItemType Directory -Force -Path $mirror | Out-Null
Copy-Item -LiteralPath $DistPath -Destination (Join-Path $mirror 'ok') -Recurse
Copy-Item -LiteralPath (Join-Path $mirror 'ok') -Destination (Join-Path $mirror 'tampered') -Recurse
Copy-Item -LiteralPath (Join-Path $mirror 'ok') -Destination (Join-Path $mirror 'missing') -Recurse

function Get-ChecksumFor($Path, $AssetName) {
    foreach ($line in (Get-Content -LiteralPath $Path)) {
        # goreleaser writes "<hash>  <name>", and the name carries a leading '*'
        # when the hash was produced in binary mode.
        $parts = $line -split '\s+'
        if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $AssetName) { return $parts[0] }
    }
    return ''
}

function Set-TamperedChecksums($Source, $Destination, $AssetName) {
    $done = $false
    $out = foreach ($line in (Get-Content -LiteralPath $Source)) {
        $parts = $line -split '\s+'
        if (-not $done -and $parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $AssetName) {
            # "0" becomes "1" and anything else becomes "0": the replacement is
            # always different from what was there.
            $hash = $parts[0]
            $first = '0'
            if ($hash.Substring(0, 1) -eq '0') { $first = '1' }
            $line = $first + $hash.Substring(1) + '  ' + $parts[1]
            $done = $true
        }
        $line
    }
    Set-Content -LiteralPath $Destination -Value $out -Encoding Ascii
}

function Remove-ChecksumEntry($Source, $Destination, $AssetName) {
    $out = foreach ($line in (Get-Content -LiteralPath $Source)) {
        $parts = $line -split '\s+'
        if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $AssetName) { continue }
        $line
    }
    Set-Content -LiteralPath $Destination -Value $out -Encoding Ascii
}

$okChecksums = Join-Path $mirror 'ok\checksums.txt'
$origHash = Get-ChecksumFor $okChecksums $Asset
Set-TamperedChecksums $okChecksums (Join-Path $mirror 'tampered\checksums.txt') $Asset
Remove-ChecksumEntry $okChecksums (Join-Path $mirror 'missing\checksums.txt') $Asset
$tamperedHash = Get-ChecksumFor (Join-Path $mirror 'tampered\checksums.txt') $Asset

# Test the test. If the mutation quietly did nothing -- the asset is renamed, the
# file format changed, the loop dropped a line -- then the negative cases below
# assert against a file that is not actually corrupt, and a passing run would mean
# nothing at all.
Write-Host 'mirror sanity'
if (-not $origHash) {
    Fail "$Asset is not listed in dist\checksums.txt, so the mirror cannot be built"
} elseif ($origHash.Length -ne 64) {
    Fail "the checksum for $Asset is $($origHash.Length) characters, not 64"
} elseif ($tamperedHash -eq $origHash) {
    Fail "tampering did not change the checksum for $Asset"
} else {
    Pass "tampered checksum is $origHash -> $tamperedHash"
}
if (Get-ChecksumFor (Join-Path $mirror 'missing\checksums.txt') $Asset) {
    Fail "$Asset is still listed in the mirror's missing\checksums.txt"
} else {
    Pass "$Asset is absent from the mirror's missing\checksums.txt"
}
if ((Get-Item -LiteralPath (Join-Path $mirror "ok\$Asset")).Length -eq 0) { Fail "$Asset is empty in the mirror" }
Write-Host ''

# --- serve the mirror ---------------------------------------------------------
#
# The server is built rather than run through `go run`, so the process this
# script stops is the one holding the port. `go run` leaves the child behind, and
# a stray listener is a failure that surfaces as a port conflict in whatever runs
# next.
$serverExe = Join-Path $tmpRoot 'servedir.exe'
Show "$Go build -o $serverExe scripts/servedist/main.go"
& $Go build -o $serverExe 'scripts/servedist/main.go'
if ($LASTEXITCODE -ne 0) { throw "could not build the test file server" }

# Port 0 lets the kernel pick, and the port is read back from the server's own
# readiness line rather than from a constant here: a fixed port turns a leftover
# server into a failure that has nothing to do with the installer.
$serverLog = Join-Path $tmpRoot 'server.log'
$serverErr = Join-Path $tmpRoot 'server.err'
Show "$serverExe $mirror 0"
# One pre-quoted argument string rather than an array: Start-Process joins an
# array with spaces and does not quote the elements, so a temp directory whose
# path contains a space (a self-hosted runner with a "First Last" profile, which
# is exactly the machine install.ps1's own comments are written for) would be
# passed to the server as two arguments and it would serve the wrong directory.
$server = Start-Process -FilePath $serverExe -ArgumentList "`"$mirror`" 0" `
    -RedirectStandardOutput $serverLog -RedirectStandardError $serverErr -NoNewWindow -PassThru

$Base = ''
for ($i = 0; $i -lt 100; $i++) {
    if (Test-Path -LiteralPath $serverLog) {
        $match = Select-String -LiteralPath $serverLog -Pattern '^listening on (\S+)$' | Select-Object -First 1
        if ($match) { $Base = $match.Matches[0].Groups[1].Value; break }
    }
    if ($server.HasExited) {
        throw "servedir exited before listening: $(Get-Content -LiteralPath $serverErr -Raw -ErrorAction SilentlyContinue)"
    }
    Start-Sleep -Milliseconds 100
}
if (-not $Base) {
    throw "servedir never reported a listening address: $(Get-Content -LiteralPath $serverLog -Raw -ErrorAction SilentlyContinue)"
}
Write-Host "serving $mirror at $Base`n"

# --- invoking the real installer ----------------------------------------------
#
# Environment variables, not parameters: SAEL_RELEASE_BASE_URL is the documented
# testing override, and a test that reaches into the script differently from how
# it is used in production is testing a different script.
function Invoke-Installer {
    param(
        [string]$InstallDir,
        [string]$BaseUrl,
        [string]$HostExe,
        [string[]]$InstallerArgs
    )
    $env:SAEL_RELEASE_BASE_URL = $BaseUrl
    $env:SAEL_INSTALL_DIR = $InstallDir
    $env:SAEL_HOME = Join-Path $tmpRoot 'home'

    $argv = @('-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', $InstallerPath, '-SkipSetup') + $InstallerArgs
    $text = & $HostExe @argv 2>&1 | Out-String
    return @{ Output = $text; Code = $LASTEXITCODE }
}

function Get-ExeVersion($Exe) {
    $text = & $Exe --version 2>&1 | Out-String
    return @{ Output = $text.Trim(); Code = $LASTEXITCODE }
}

function Assert-NothingInstalled($Dir) {
    $found = @(Get-ChildItem -LiteralPath $Dir -Recurse -Filter 'sael.exe' -ErrorAction SilentlyContinue)
    if ($found.Count -gt 0) {
        Fail "an unverified install left a binary behind: $($found[0].FullName)"
    } else {
        Pass "nothing installed under $Dir"
    }
}

# Windows PowerShell 5.1 is what most Windows machines have, so it is the host the
# happy path runs under. pwsh is used for the second run below, which proves the
# installer is not accidentally dependent on one of them. If pwsh is not present
# the second run still happens, under 5.1: a missing second host is worse than no
# second run, but not worth failing the whole test over.
$WinPS = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
if (-not (Test-Path -LiteralPath $WinPS)) { throw "no Windows PowerShell 5.1 at $WinPS" }
$Pwsh = (Get-Command pwsh -ErrorAction SilentlyContinue).Source
if (-not $Pwsh) { $Pwsh = $WinPS }

# --- 1. the happy path --------------------------------------------------------
Write-Host '=== happy path'
$happyDir = Join-Path $tmpRoot 'install-happy'
Show "SAEL_RELEASE_BASE_URL=$Base/ok SAEL_INSTALL_DIR=$happyDir $WinPS -File install.ps1 -Version $Version -SkipSetup"
$r = Invoke-Installer -InstallDir $happyDir -BaseUrl "$Base/ok" -HostExe $WinPS -InstallerArgs @('-Version', $Version)
if ($r.Code -eq 0) { Pass 'installer exited 0' } else { Fail "installer exited $($r.Code)"; Write-Host $r.Output }
if ($r.Output -match 'SHA-256 verified') {
    # Checked explicitly because a run that verified and a run that skipped are
    # identical except for this line, and the next install could otherwise pass
    # while having downloaded unverified bytes.
    Pass 'the log shows the archive was verified'
} else {
    Fail 'the log does not show SHA-256 verification'
}

$exe = Join-Path $happyDir "$Version\sael.exe"
if (Test-Path -LiteralPath $exe) { Pass "installed $exe" } else { Fail "no executable at $exe" }

$v = Get-ExeVersion $exe
if ($v.Code -eq 0 -and $v.Output -eq "sael $Version") {
    Pass "sael --version prints '$($v.Output)'"
} else {
    Fail "sael --version printed '$($v.Output)' (status $($v.Code)), wanted 'sael $Version'"
}
# The version string is injected by goreleaser's ldflags; a build that drifted
# reports "devel" and still exits 0, so it is named rather than left to the
# equality check above to imply.
if ($v.Output -match 'devel') { Fail 'the installed binary reports a version of devel: the ldflags injection did not reach it' }

# Stronger than the version string: the bytes on disk are the bytes in the archive
# just built. A stale binary that happens to report the same version passes the
# check above and fails this one.
$extractCheck = Join-Path $tmpRoot 'extract-check'
Expand-Archive -LiteralPath $Archive -DestinationPath $extractCheck -Force
if ((Get-Sha256 (Join-Path $extractCheck 'sael.exe')) -eq (Get-Sha256 $exe)) {
    Pass "the installed exe is byte for byte the one in $Asset"
} else {
    Fail "the installed exe differs from the sael.exe inside $Asset"
}

# Asked to install into a temp directory, the installer must not also touch the
# place it would have used by default. This is the Windows counterpart of the
# assertion in scripts/install-selftest.sh that a relocated install leaves
# $HOME/.local/bin alone: %LOCALAPPDATA%\sael is the real location a user's
# existing sael lives in, and a test that only looked under the temp directory
# would pass on an installer that wrote to both.
#
# There is deliberately no -InstallDir-versus-SAEL_INSTALL_DIR case here, the way
# there is a --install-dir case for install.sh. install.ps1 has one install
# location and no separate symlink directory -- both the flag and the environment
# variable bind the same $InstallDir parameter (line 47), and Add-ToUserPath gets
# the per-version directory under it -- so the two forms cannot diverge. The bug
# the bash case exists for has no shape here.
$defaultRoot = Join-Path $env:LOCALAPPDATA 'sael'
if (Test-Path -LiteralPath $defaultRoot) {
    Fail "the installer created its default location anyway: $defaultRoot"
} else {
    Pass "the default location was not created: $defaultRoot"
}
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath -and $userPath -like "*$defaultRoot*") {
    Fail "the user PATH was given an entry under the default location: $defaultRoot"
} else {
    Pass 'the user PATH was not given an entry under the default location'
}
Write-Host ''

# --- 2. idempotency, and a tag pasted with its module prefix ------------------
#
# The second run passes the tag as it appears on the releases page
# ("cli/v0.0.1-rc1"). A user who pastes the tag off that page must not get a 404
# for an asset name they cannot see is wrong, so the prefix is stripped by the
# installer and the version directory is the bare number either way.
Write-Host '=== re-running the installer, with the tag as published'
$tagged = "cli/v$Version"
Show "SAEL_RELEASE_BASE_URL=$Base/ok SAEL_INSTALL_DIR=$happyDir $Pwsh -File install.ps1 -Version $tagged -SkipSetup   (second run)"
$r = Invoke-Installer -InstallDir $happyDir -BaseUrl "$Base/ok" -HostExe $Pwsh -InstallerArgs @('-Version', $tagged)
if ($r.Code -eq 0) { Pass 'the second run exited 0' } else { Fail "the second run exited $($r.Code)"; Write-Host $r.Output }
$versionDirs = @(Get-ChildItem -LiteralPath $happyDir -Directory -ErrorAction SilentlyContinue)
if ($versionDirs.Count -eq 1 -and $versionDirs[0].Name -eq $Version) {
    Pass "exactly one version directory: $($versionDirs[0].Name)"
} else {
    Fail "$($versionDirs.Count) version directories under ${happyDir}: $($versionDirs.Name -join ' ')"
}
$v = Get-ExeVersion $exe
if ($v.Output -eq "sael $Version") { Pass "sael --version still prints '$($v.Output)'" } else { Fail "sael --version printed '$($v.Output)' after the second run" }
Write-Host ''

# --- 3. tampered checksums.txt is refused -------------------------------------
Write-Host '=== tampered checksums.txt (the one that matters)'
$tamperedDir = Join-Path $tmpRoot 'install-tampered'
Show "SAEL_RELEASE_BASE_URL=$Base/tampered SAEL_INSTALL_DIR=$tamperedDir $WinPS -File install.ps1 -Version $Version -SkipSetup"
$r = Invoke-Installer -InstallDir $tamperedDir -BaseUrl "$Base/tampered" -HostExe $WinPS -InstallerArgs @('-Version', $Version)
if ($r.Code -ne 0) { Pass "the installer refused a tampered archive (exit $($r.Code))" } else { Fail 'the installer exited 0 on a tampered checksums.txt' }
if ($r.Output -match 'Checksum mismatch') {
    Pass "the refusal names the reason: $(($r.Output -split "`n" | Where-Object { $_ -match 'Checksum mismatch' } | Select-Object -First 1).Trim())"
} else {
    Fail 'the installer failed without reporting a checksum mismatch'
    Write-Host $r.Output
}
Assert-NothingInstalled $tamperedDir
Write-Host ''

# --- 3b. the control for 3 ----------------------------------------------------
#
# Without this, case 3 proves only that *something* went wrong, and a broken zip
# or a truncated download would pass it. Installing the same mirror with -NoVerify
# must succeed: same archives, same manifest, only the comparison skipped.
# Together the two cases isolate the rejection to the checksum step rather than to
# anything else about the mirror.
Write-Host '=== the control: the same mirror with -NoVerify'
$controlDir = Join-Path $tmpRoot 'install-control'
Show "SAEL_RELEASE_BASE_URL=$Base/tampered SAEL_INSTALL_DIR=$controlDir $WinPS -File install.ps1 -Version $Version -SkipSetup -NoVerify"
$r = Invoke-Installer -InstallDir $controlDir -BaseUrl "$Base/tampered" -HostExe $WinPS -InstallerArgs @('-Version', $Version, '-NoVerify')
if ($r.Code -eq 0) {
    Pass 'the archive installs when verification is switched off (exit 0)'
} else {
    Fail "the mirror does not install even with -NoVerify (exit $($r.Code)), so case 3 is not about the checksum"
    Write-Host $r.Output
}
if ($r.Output -match 'NoVerify') { Pass 'the skipped check is announced in the log' } else { Fail '-NoVerify installed without warning that nothing was verified' }
Write-Host ''

# --- 4. an asset with no checksum entry is refused ----------------------------
Write-Host '=== asset missing from checksums.txt'
$missingDir = Join-Path $tmpRoot 'install-missing'
Show "SAEL_RELEASE_BASE_URL=$Base/missing SAEL_INSTALL_DIR=$missingDir $WinPS -File install.ps1 -Version $Version -SkipSetup"
$r = Invoke-Installer -InstallDir $missingDir -BaseUrl "$Base/missing" -HostExe $WinPS -InstallerArgs @('-Version', $Version)
if ($r.Code -ne 0) { Pass "the installer refused an unlisted archive (exit $($r.Code))" } else { Fail 'the installer exited 0 for an archive absent from checksums.txt' }
if ($r.Output -match 'not listed in checksums.txt') { Pass 'the refusal names the reason' } else { Fail 'the installer failed without saying the asset is unlisted'; Write-Host $r.Output }
Assert-NothingInstalled $missingDir
Write-Host ''

# --- 5. an archive that does not exist fails ---------------------------------
Write-Host '=== archive that was never published'
$absentDir = Join-Path $tmpRoot 'install-absent'
Show "SAEL_RELEASE_BASE_URL=$Base/ok SAEL_INSTALL_DIR=$absentDir $WinPS -File install.ps1 -Version 9.9.9-nope -SkipSetup"
$r = Invoke-Installer -InstallDir $absentDir -BaseUrl "$Base/ok" -HostExe $WinPS -InstallerArgs @('-Version', '9.9.9-nope')
if ($r.Code -ne 0) { Pass "the installer failed on a 404-like download (exit $($r.Code))" } else { Fail 'the installer exited 0 for an archive that does not exist' }
if ($r.Output -match 'Download failed') { Pass 'the refusal names the reason' } else { Fail 'the installer failed without reporting a download failure' }
Assert-NothingInstalled $absentDir
Write-Host ''

# --- 6. the architecture this release does not build --------------------------
#
# install.ps1 reads the architecture from RuntimeInformation::OSArchitecture,
# which reports the real machine. There is no injection point, and an x64 runner
# cannot pretend to be one, so this branch cannot be exercised from here without
# editing the script under test. It is covered on Linux and macOS, where `uname`
# can be stubbed (see scripts/install-selftest.sh); the refusal code itself is
# platform-specific and this gap is real.
Write-Host '=== windows/arm64, which this release does not build'
Skip 'install.ps1 reads the architecture from RuntimeInformation; it cannot be forced on an x64 runner. Covered for linux/arm64 and darwin/amd64 by scripts/install-selftest.sh.'
Write-Host ''

}
finally {
    Cleanup
}

if ($script:Failures -ne 0) {
    Write-Host "$($script:Failures) check(s) FAILED`n"
    exit 1
}
Write-Host "all checks passed`n"
exit 0
