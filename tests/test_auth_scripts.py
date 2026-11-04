"""Exercise the shipped PowerShell with mocked browser/network and a fake token."""
import os
from pathlib import Path
import re
import shutil
import subprocess
from tempfile import TemporaryDirectory
import unittest

ROOT = Path(__file__).resolve().parents[1]
PWSH = os.environ.get("KINDLE_TEST_PWSH") or shutil.which("pwsh") or shutil.which("powershell")


@unittest.skipUnless(PWSH, "PowerShell not available in this environment")
class PowerShellChecks(unittest.TestCase):
    def run_mock(self, mode):
        with TemporaryDirectory(prefix="Kindle PS русский ") as folder:
            root = Path(folder)
            text = (ROOT / "Yandex-Books/connect-kindle.ps1").read_text(encoding="utf-8-sig")
            # Replace only device enumeration: real drives are never changed.
            text, count = re.subn(r"    \$targets = @\(\n.*?\n    \)",
                                 "    $targets = @($env:KINDLE_TEST_TARGET)", text, count=1, flags=re.S)
            self.assertEqual(count, 1)
            prelude = r'''
$script:testState = ''
$script:readCount = 0
function Start-Process {
    param([string]$FilePath)
    $script:testState = [regex]::Match($FilePath, 'state=([^&]+)').Groups[1].Value
}
function Read-Host {
    param([string]$Prompt, [switch]$AsSecureString)
    $script:readCount++
    if ($script:readCount -gt 2) { throw 'The callback was never accepted' }
    $hostName = 'yx4483e97bab6e486a9822973109a14d05.oauth.yandex.ru'
    $state = $script:testState
    $extra = ''
    if ($script:readCount -eq 1) {
        if ($env:KINDLE_TEST_MODE -eq 'wrong-state') { $state = 'incorrect' }
        if ($env:KINDLE_TEST_MODE -eq 'wrong-host') { $hostName = 'attacker.example' }
        if ($env:KINDLE_TEST_MODE -eq 'duplicate') { $extra = '&access_token=y0_012345678901234567890123456789' }
    }
    $address = 'https://' + $hostName + '/callback#access_token=y0_012345678901234567890123456789&state=' + $state + $extra
    ConvertTo-SecureString $address -AsPlainText -Force
}
function Invoke-WebRequest {
    param([switch]$UseBasicParsing, [int]$MaximumRedirection, [int]$TimeoutSec, $Headers, [string]$UserAgent, [string]$Uri)
    if ($env:KINDLE_TEST_MODE -eq 'network-failure') { throw 'Simulated offline' }
    if ($Headers['Auth-Token'] -cne 'y0_012345678901234567890123456789') { throw 'Wrong token header' }
    if ($MaximumRedirection -ne 0) { throw 'Redirect must be disabled' }
    @{StatusCode=200}
}
'''
            fixture = root / "fixture.ps1"
            fixture.write_text(prelude+text, encoding="utf-8-sig")
            env = dict(os.environ, KINDLE_TEST_TARGET=str(root), KINDLE_TEST_MODE=mode)
            result = subprocess.run([str(PWSH), "-NoLogo", "-NoProfile", "-File", str(fixture)],
                                    env=env, capture_output=True, timeout=30)
            pairing = root / "pairing.txt"
            if mode == "network-failure":
                self.assertEqual(result.returncode, 1)
                self.assertFalse(pairing.exists())
            else:
                self.assertEqual(result.returncode, 0, result.stdout.decode("utf-8", errors="replace") + result.stderr.decode("utf-8", errors="replace"))
                self.assertEqual(pairing.read_bytes(), b"y0_012345678901234567890123456789\n")

    def test_correct_callback(self): self.run_mock("valid")
    def test_wrong_state_then_correct(self): self.run_mock("wrong-state")
    def test_wrong_origin_then_correct(self): self.run_mock("wrong-host")
    def test_duplicate_token_then_correct(self): self.run_mock("duplicate")
    def test_network_failure_does_not_save_token(self): self.run_mock("network-failure")


if __name__ == "__main__": unittest.main()
