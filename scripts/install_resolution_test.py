"""Exercise real installer resolution functions without downloading/installing binaries."""
import json
import pathlib
import shlex
import shutil
import subprocess
import sys
import unittest

ROOT = pathlib.Path(__file__).resolve().parent.parent


def function(file, marker):
    source = file.read_text()
    start = source.index(marker)
    end = source.index("\n}\n", start) + 3
    return source[start:end]


class InstallerResolutionTest(unittest.TestCase):
    def fixtures(self, tag):
        first = json.dumps([{"tag_name": "gateway/v9.0.0"}] * 100)
        second = json.dumps([{"tag_name": "sdk/v9.0.0"}, {"tag_name": tag}])
        return first, second

    @unittest.skipIf(sys.platform == "win32", "Bash installer runs on Linux and macOS")
    def test_bash_ignores_gateway_latest_and_finds_cli_on_next_page(self):
        first, second = self.fixtures("cli/v0.2.0-rc.1")
        script = function(ROOT / "install.sh", "resolve_version() {")
        program = f'''
set -euo pipefail
VERSION=""; BASE_URL=""; REPO=cipherTing/sael; TAG_PREFIX=cli/
RELEASES_URL=https://github.com/cipherTing/sael/releases
log_error() {{ echo "$*" >&2; }}
log_info() {{ echo "$*" >&2; }}
curl() {{
  case "$*" in
    *'/latest'*) echo 'https://github.com/cipherTing/sael/releases/tag/gateway/v9.0.0' ;;
    *'&page=1') echo {shlex.quote(first)} ;;
    *'&page=2') echo {shlex.quote(second)} ;;
    *) echo '[]' ;;
  esac
}}
{script}
resolve_version
echo "$VERSION"
'''
        result = subprocess.run(["bash", "-c", program], capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "0.2.0-rc.1")

    @unittest.skipUnless(shutil.which("pwsh"), "PowerShell resolution runs in Windows CI")
    def test_powershell_ignores_gateway_latest_and_finds_cli_on_next_page(self):
        first, second = self.fixtures("cli/v0.2.0-rc.1")
        source = ROOT / "install.ps1"
        functions = "\n".join(function(source, "function " + name + " {") for name in ["Get-FinalUri", "ConvertFrom-Tag", "Resolve-Version"])
        program = f'''
$ErrorActionPreference = 'Stop'
$Version = ''; $BaseUrl = ''; $Repo = 'cipherTing/sael'; $TagPrefix = 'cli/'; $ReleasesUrl = "https://github.com/$Repo/releases"
function Write-Err {{ param($text) throw $text }}
function Write-Info {{ param($text) Write-Host $text }}
function Invoke-WebRequest {{ return @{{ BaseResponse = @{{ ResponseUri = @{{ AbsoluteUri = 'https://github.com/cipherTing/sael/releases/tag/gateway/v9.0.0' }} }} }} }}
function Invoke-RestMethod {{
  param($Uri, [switch]$UseBasicParsing)
  if ($Uri.EndsWith('page=1')) {{ return ConvertFrom-Json '{first}' }}
  if ($Uri.EndsWith('page=2')) {{ return ConvertFrom-Json '{second}' }}
  return @()
}}
{functions}
Resolve-Version
Write-Output $script:Version
'''
        result = subprocess.run(["pwsh", "-NoProfile", "-Command", program], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), "0.2.0-rc.1")


if __name__ == "__main__":
    unittest.main()
