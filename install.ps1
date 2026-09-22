# Installer for sael on Windows.
#
#   iex (irm https://raw.githubusercontent.com/cipherTing/sael/main/install.ps1)
#
# Or, with options:
#
#   .\install.ps1 -Version 0.0.1-rc1 -SkipSetup
#
# sael is one static Go binary with no runtime dependencies, so this script does
# three things: fetch a release archive, prove it is the archive the release
# published, and put the binary where an unprivileged user can write. The Hermes
# installer this UX was modelled on contributes its prompts and nothing else --
# it is ~148 KB because it also provisions Python, uv, Node and Playwright, and
# there is no equivalent here to provision.
#
# Two properties are deliberate:
#
#   1. The SHA-256 is always checked against the release's checksums.txt. An
#      installer people paste into a shell without reading it is the
#      highest-value place in the project to plant a bad binary, and "we trust
#      GitHub" is not a property of the bytes on disk. -NoVerify exists for a
#      release whose checksums file is itself broken, and it shouts.
#   2. Nothing is installed into Program Files and nothing needs administrator
#      rights. The USER PATH is edited, never the machine PATH, so a standard
#      account can install sael and no other account on the box is affected.
#
# Failures are raised as terminating errors and never as `exit`. Under
# `iex (irm ...)` this code runs inside the operator's own session, where `exit`
# closes the window they were working in; a terminating error prints, leaves the
# session alive, and still makes `powershell -File install.ps1` return non-zero.
# Hermes wraps its whole body in a try/catch for the same reason.

[CmdletBinding()]
param(
    # The bare version number, as in -Version 0.0.1-rc1. Empty means "latest
    # release", which is resolved at run time and never hardcoded.
    [string]$Version = "",

    # Root holding the per-version directories. Each version gets its own
    # directory so an upgrade is a sibling install rather than an overwrite, and
    # a rollback is a PATH edit away.
    #
    # The default is left empty on anything that is not Windows rather than
    # resolved here: $env:LOCALAPPDATA is unset there, and Join-Path would reject
    # a null path during parameter binding -- before the friendly "this is the
    # Windows installer" message below ever gets a chance to print.
    [string]$InstallDir = $(if ($env:SAEL_INSTALL_DIR) { $env:SAEL_INSTALL_DIR } else { '' }),

    [switch]$SkipSetup,
    [switch]$NoVerify,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'
# Invoke-WebRequest draws a progress bar that costs more time than the download
# on PowerShell 5.1, which is the version most Windows machines still ship.
$ProgressPreference = 'SilentlyContinue'

$Repo = 'cipherTing/sael'
# Two modules (sdk/ and cli/) are tagged in their own namespaces, because a
# subdirectory module's tag must carry the directory name as a prefix. The CLI
# tag is `cli/v0.0.1-rc1`, not `v0.0.1-rc1`; the download path must match it or
# every fetch 404s.
$TagPrefix = 'cli/'
$ReleasesUrl = "https://github.com/$Repo/releases"
$BaseUrl = $env:SAEL_RELEASE_BASE_URL

function Write-Info    { param([string]$Message) Write-Host "-> $Message" -ForegroundColor Cyan }
function Write-Success { param([string]$Message) Write-Host "[ok] $Message" -ForegroundColor Green }
function Write-Warn    { param([string]$Message) Write-Host "[!!] $Message" -ForegroundColor Yellow }
function Write-Err     { param([string]$Message) Write-Host "[xx] $Message" -ForegroundColor Red }

function Show-Usage {
    # Single-quoted here-string: this text is handed to the operator verbatim, so
    # neither `$env:` nor the backticks around command names may be substituted.
    Write-Host @'
Install sael from a GitHub Release. Windows.

Usage: install.ps1 [OPTIONS]

  -Version <v>        Install this version instead of the latest release; the
                      bare number, as in -Version 0.0.1-rc1
  -InstallDir <path>  Root holding the per-version directories
                      (default %LOCALAPPDATA%\sael)
  -SkipSetup          Install only; skip the `sael setup` onboarding
  -NoVerify           Do not check the archive against checksums.txt; only for a
                      release whose checksums file is itself broken
  -Help               Show this help

Environment:
  SAEL_RELEASE_BASE_URL  Fetch archives from here instead of GitHub. Replaces
                         the whole base URL, so it must already include the
                         version -- pass -Version alongside it.
  SAEL_INSTALL_DIR       Same as -InstallDir.
  SAEL_HOME              Config directory for `sael setup` (default ~\.Sael);
                         passed straight through.
  SAEL_API_KEY           API key `sael setup` writes without asking.

sael is installed user-scoped into the USER PATH. Administrator rights are never
used, and the machine PATH is never modified.
'@
}

# The release publishes one Windows build. Refusing beats installing the nearest
# thing that exists: an arm64 machine handed the amd64 binary fails deep inside
# the runtime with no hint of where it came from.
function Get-Architecture {
    $arch = ''
    try {
        $arch = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    } catch {
        # RuntimeInformation needs .NET 4.7.1; PowerShell 5.1 on an older build of
        # Windows 10 does not have it, and there PROCESSOR_ARCHITECTURE is the
        # only source of the answer.
        $arch = ''
    }
    if (-not $arch) { $arch = $env:PROCESSOR_ARCHITECTURE }

    switch ($arch) {
        'X64'   { return 'amd64' }
        'Amd64' { return 'amd64' }
        default {
            Write-Err "sael is not built for Windows/$arch."
            Write-Info "This release publishes:"
            Write-Host '   darwin/arm64    (Apple silicon, use install.sh)'
            Write-Host '   linux/amd64     (use install.sh)'
            Write-Host '   windows/amd64   (this installer)'
            Write-Host ''
            Write-Info "Or build it from source:"
            Write-Host "   git clone https://github.com/$Repo && cd sael/cli && go build -o sael.exe ./cmd/sael"
            throw "unsupported platform windows/$arch"
        }
    }
}

# Read the tag that the /releases/latest redirect lands on. No JSON at all, and
# no API quota. It cannot see a prerelease, though, and the first tag this
# project publishes is 0.0.1-rc1, so the API list -- which does include
# prereleases -- is the fallback.
function Get-FinalUri {
    param($Response)
    # 5.1 hands back an HttpWebResponse, 7 an HttpResponseMessage; the property
    # that carries the final URL is different in each.
    if ($Response.BaseResponse.ResponseUri) { return $Response.BaseResponse.ResponseUri.AbsoluteUri }
    if ($Response.BaseResponse.RequestMessage.RequestUri) { return $Response.BaseResponse.RequestMessage.RequestUri.AbsoluteUri }
    return ''
}

# A tag carries the module prefix ("cli/v0.0.1-rc1") because a subdirectory
# module's tag must; the asset name and the version directory use the bare number.
# Accept "v0.0.1-rc1" and "cli/v0.0.1-rc1" as well as the bare one, so pasting the
# tag off the releases page does the obvious thing instead of 404ing on a name the
# user cannot see is wrong.
function ConvertFrom-Tag {
    param([string]$Tag)
    if ($Tag.StartsWith($TagPrefix + 'v')) { return $Tag.Substring($TagPrefix.Length + 1) }
    if ($Tag.StartsWith('v'))              { return $Tag.Substring(1) }
    return $Tag
}

function Resolve-Version {
    # Normalised on the way in as well as on the way out: a version given with
    # -Version never reaches the tag lookup below, so leaving the conversion at
    # the end of this function would silently skip it for every pinned install.
    if ($Version) { $script:Version = ConvertFrom-Tag $Version; return }

    # A mirror (CI, or a maintainer testing a snapshot) has no version to offer,
    # so name the directory after what it stands in for rather than reaching out
    # to GitHub from inside the test that redirected it here.
    if ($BaseUrl) { $script:Version = '0.0.0-local'; return }

    $tag = ''
    try {
        $final = Get-FinalUri (Invoke-WebRequest -UseBasicParsing -Uri "$ReleasesUrl/latest" -MaximumRedirection 5)
        if ($final -match '/releases/tag/(.+)$') { $tag = $Matches[1] }
    } catch {
        # No release, no redirect, or no network. The API list below is the
        # fallback, and it is the one that reports the failure if it also fails.
        $tag = ''
    }

    if (-not $tag) {
        try {
            # Unauthenticated API calls are rate-limited per IP, which a shared
            # build agent can exhaust; the redirect above needs no quota, which
            # is why it is tried first rather than the other way round.
            $releases = @(Invoke-RestMethod -UseBasicParsing -Uri "https://api.github.com/repos/$Repo/releases?per_page=1")
            if ($releases.Count -gt 0) { $tag = $releases[0].tag_name }
        } catch {
            # Rate-limited or offline; the message below says what to do about it.
            $tag = ''
        }
    }

    if (-not $tag) {
        Write-Err "Could not determine the latest release of $Repo."
        Write-Info "Name it explicitly:  .\install.ps1 -Version 0.0.1-rc1"
        throw "could not resolve the latest sael release"
    }

    $script:Version = ConvertFrom-Tag $tag
}

function Get-ReleaseFile {
    param([string]$Url, [string]$Destination)
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Destination
}

function Test-Checksum {
    param([string]$ArchivePath, [string]$ChecksumsPath, [string]$Asset)

    $expected = $null
    foreach ($line in (Get-Content -LiteralPath $ChecksumsPath)) {
        # goreleaser writes "<hash>  <name>", and the name carries a leading '*'
        # when the hash was produced in binary mode, so strip that before
        # comparing.
        $parts = $line -split '\s+'
        if ($parts.Count -ge 2 -and $parts[1].TrimStart('*') -eq $Asset) { $expected = $parts[0]; break }
    }

    if (-not $expected) {
        Write-Err "$Asset is not listed in checksums.txt."
        Write-Err "A release that does not vouch for its own asset cannot be verified."
        throw "no checksum published for $Asset"
    }

    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $ArchivePath).Hash
    if ($actual -ne $expected) {
        Write-Host ''
        Write-Err "Checksum mismatch for $Asset."
        Write-Err "  expected: $expected"
        Write-Err "  actual:   $actual"
        Write-Err "The download is not the published file. Nothing was installed."
        throw "checksum mismatch for $Asset"
    }

    Write-Success "SHA-256 verified: $($actual.ToLower())"
}

function Add-ToUserPath {
    param([string]$Directory)

    $current = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $current) { $current = '' }

    # Every earlier sael version directory is dropped, so an upgrade leaves
    # exactly one sael reachable. Without this each install would prepend another
    # entry and the PATH would grow a stale shim per release.
    $root = $InstallDir.TrimEnd('\')
    $kept = @($current -split ';' | Where-Object {
        $_ -and -not $_.TrimEnd('\').StartsWith($root, [StringComparison]::OrdinalIgnoreCase)
    })
    $updated = (@($Directory) + $kept) -join ';'

    if ($updated -ne $current) {
        # 'User', never 'Machine': the machine PATH needs administrator rights and
        # would change sael for every account on the box.
        [Environment]::SetEnvironmentVariable('Path', $updated, 'User')
        Write-Success "Prepended to the user PATH: $Directory"
    } else {
        Write-Info "User PATH already points at $Directory"
    }

    # The stored variable only reaches processes started afterwards, so the
    # current session is patched too -- otherwise the onboarding below would run
    # an exe the user cannot then find.
    $env:Path = "$Directory;$env:Path"
}

# CONIN$ names the console input device even when standard input has been
# redirected, which is the Windows analogue of /dev/tty. Opening it is the only
# reliable probe: the device is genuinely absent in a service or a detached CI
# job, where `< CONIN$` would fail rather than prompt.
function Test-ConsoleInput {
    try {
        $stream = [System.IO.File]::Open('CONIN$', [System.IO.FileMode]::Open,
            [System.IO.FileAccess]::Read, [System.IO.FileShare]::ReadWrite)
        $stream.Close()
        return $true
    } catch {
        return $false
    }
}

function Invoke-SaelSetup {
    param([string]$Exe, [string]$ScratchDir)

    if ($SkipSetup) {
        Write-Info "Skipping configuration (-SkipSetup)."
        Write-Info "Run '$Exe setup' whenever you want to configure it."
        return 0
    }

    # Under `iex (irm ...)` standard input is still the console and the wizard
    # reads straight from it. Under `irm ... | iex` it is a pipe that the download
    # already drained, and the wizard would see end-of-input at its first
    # question; that is what the CONIN$ route below is for.
    if (-not [Console]::IsInputRedirected) {
        & $Exe setup
        # Cast, not returned raw: $LASTEXITCODE is $null if no native command has
        # run, and `$null -ne 0` is TRUE in PowerShell, so an unset value would be
        # read downstream as a failed wizard.
        return [int]$LASTEXITCODE
    }

    if (-not (Test-ConsoleInput)) {
        Write-Warn "No console is attached, so configuration was skipped."
        Write-Info "sael is installed and will run, but it is not configured."
        Write-Info "Run '$Exe setup' from an interactive PowerShell window."
        return 0
    }

    # The redirection is written into a .cmd file rather than passed to
    # `cmd /c` as one string: the install directory routinely contains spaces
    # ("C:\Users\First Last\AppData\Local\sael"), and cmd's quote-stripping rules
    # make the inline form fail on exactly those machines.
    $shim = Join-Path $ScratchDir 'sael-setup.cmd'
    Set-Content -LiteralPath $shim -Encoding Ascii -Value @(
        '@echo off',
        "`"$Exe`" setup < CONIN$"
    )
    & cmd.exe /c $shim
    return [int]$LASTEXITCODE
}

function Main {
    if ($Help) { Show-Usage; return }

    Write-Host ''
    Write-Host 'sael installer' -ForegroundColor White
    Write-Host "Measure a request's content against a moderation question set." -ForegroundColor DarkGray
    Write-Host ''

    if ($env:OS -ne 'Windows_NT') {
        Write-Err "This is the Windows installer, but this is not Windows."
        Write-Info "On Linux or macOS, use install.sh:"
        Write-Host "   curl -fsSL https://raw.githubusercontent.com/$Repo/main/install.sh | bash"
        throw 'not running on Windows'
    }

    $arch = Get-Architecture

    # Resolved here rather than in the param block, because the default needs
    # %LOCALAPPDATA%, which only exists on Windows and only exists by the time
    # the guard above has passed.
    if (-not $InstallDir) { $script:InstallDir = Join-Path $env:LOCALAPPDATA 'sael' }

    Resolve-Version
    $base = $BaseUrl
    if (-not $base) { $base = "$ReleasesUrl/download/$TagPrefix" + "v$Version" }

    $asset = "sael_${Version}_windows_${arch}.zip"
    Write-Info "Platform:  windows/$arch"
    Write-Info "Version:   $Version"
    Write-Info "Source:    $base"
    Write-Host ''

    $scratch = Join-Path ([System.IO.Path]::GetTempPath()) ('sael-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $scratch | Out-Null

    try {
        $archive = Join-Path $scratch $asset
        try {
            Get-ReleaseFile -Url "$base/$asset" -Destination $archive
        } catch {
            Write-Err "Download failed: $base/$asset"
            Write-Info "If that release does not exist yet, list what does:"
            Write-Host "   $ReleasesUrl"
            throw
        }

        if ($NoVerify) {
            # Not a quiet warning: the script has just installed bytes it cannot
            # account for, and a pasted install log is how that gets caught.
            Write-Host ''
            Write-Warn '============================================================='
            Write-Warn "-NoVerify: the archive was NOT checked against checksums.txt."
            Write-Warn "Nothing here proves $asset is what the release published."
            Write-Warn '============================================================='
            Write-Host ''
        } else {
            Write-Info "Verifying $asset against checksums.txt"
            $checksums = Join-Path $scratch 'checksums.txt'
            try {
                Get-ReleaseFile -Url "$base/checksums.txt" -Destination $checksums
            } catch {
                Write-Err "Could not download $base/checksums.txt"
                Write-Info "Without it the archive cannot be verified. -NoVerify skips"
                Write-Info "the check entirely; use it only if you know why it is missing."
                throw
            }
            Test-Checksum -ArchivePath $archive -ChecksumsPath $checksums -Asset $asset
        }

        $extract = Join-Path $scratch 'extract'
        Expand-Archive -LiteralPath $archive -DestinationPath $extract -Force
        $unpacked = Join-Path $extract 'sael.exe'
        if (-not (Test-Path -LiteralPath $unpacked)) {
            Write-Err "$asset has no file named 'sael.exe' at its root."
            Write-Info "Found instead:"
            Get-ChildItem -LiteralPath $extract | ForEach-Object { Write-Host "   $($_.Name)" }
            throw "archive layout is not the published one"
        }

        $versionDir = Join-Path $InstallDir $Version
        if (Test-Path -LiteralPath $versionDir) { Write-Info "Replacing the existing $Version install" }
        New-Item -ItemType Directory -Force -Path $versionDir | Out-Null

        # Copied to a staging name and moved into place, because a move within one
        # directory is atomic: a killed installer cannot leave a truncated exe
        # exactly where the PATH entry points.
        $target = Join-Path $versionDir 'sael.exe'
        $staged = Join-Path $versionDir ('.sael.tmp.{0}.exe' -f $PID)
        Copy-Item -LiteralPath $unpacked -Destination $staged -Force
        try {
            Move-Item -LiteralPath $staged -Destination $target -Force
        } catch {
            Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
            # Windows will not let a running executable be replaced at all, which
            # is the one case an upgrade genuinely cannot do in place.
            Write-Err "Could not replace $target."
            Write-Info "A running sael.exe is almost always the cause; close it and re-run."
            throw
        }
        Write-Success "Installed $target"

        Add-ToUserPath -Directory $versionDir

        $rc = 0
        try {
            $rc = Invoke-SaelSetup -Exe $target -ScratchDir $scratch
        } catch {
            Write-Err "sael setup could not run: $($_.Exception.Message)"
            $rc = 1
        }

        Write-Host ''
        if ($rc -ne 0) {
            # Raised, not swallowed. An install whose onboarding failed is not a
            # successful install, and reporting success would make the exit
            # status useless to whoever is scripting this.
            Write-Err "sael setup exited with status $rc."
            Write-Err "sael $Version is installed at $target but is NOT configured,"
            Write-Err "and will fail until it is."
            Write-Info "Re-run '$target setup' to try again."
            throw "sael $Version is installed but unconfigured"
        }

        Write-Success "sael $Version is installed."
        Write-Host ''
        Write-Info 'Commands:'
        Write-Host '   sael --help        Show what sael can do'
        Write-Host '   sael setup         Configure the API endpoint, model and key'
        Write-Host ''
        Write-Info 'The PATH change reaches new terminals. In this one:'
        Write-Host "   `$env:Path = '$versionDir;' + `$env:Path"
        Write-Host ''
    } finally {
        Remove-Item -LiteralPath $scratch -Recurse -Force -ErrorAction SilentlyContinue
    }
}

# Dot-sourcing loads the functions above without running an install. That is how
# the checksum comparison and the version parsing are exercised from a machine
# that is not Windows, which is where this file is developed; the Windows-only
# steps below cannot run there. Hermes gates its entry point the same way.
if ($MyInvocation.InvocationName -eq '.') { return }

try {
    Main
} catch {
    # Rethrown rather than swallowed, so `powershell -File install.ps1` returns
    # non-zero. There is deliberately no `exit` here: this code also runs through
    # `iex (irm ...)` inside the operator's own session, where exiting would close
    # the window they were working in.
    Write-Host ''
    Write-Host "Installation did not complete: $($_.Exception.Message)" -ForegroundColor Red
    Write-Host ''
    throw
}
