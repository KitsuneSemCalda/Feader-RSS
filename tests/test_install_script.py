import hashlib
import os
import re
import shutil
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]
INSTALL_SCRIPT_TEXT = (ROOT / "scripts/install.sh").read_text()
REPO = "KitsuneSemCalda/Feader-RSS"
VERSION = re.search(r'"version"\s*:\s*"([^"]+)"', (ROOT / "manifest.json").read_text()).group(1)
ASSET_NAME = f"feader-rss-fetch_{VERSION}_linux_amd64"
ASSET_CONTENT = "fake-binary-bytes-for-tests"
ASSET_SHA256 = hashlib.sha256(ASSET_CONTENT.encode()).hexdigest()
API_RESOLVED_SHA = "1111111111111111111111111111111111111a"


def _write_stub(path, body):
    path.write_text("#!/usr/bin/env bash\n" + body + "\n")
    mode = path.stat().st_mode
    path.chmod(mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)


class InstallScriptSourceGuardTests(unittest.TestCase):
    """Cheap text-level guards that fail immediately if the fix is reverted,
    even before anyone notices the behavioral tests below regressed."""

    def test_downloads_are_bounded(self):
        self.assertIn("--connect-timeout", INSTALL_SCRIPT_TEXT)
        self.assertIn("--max-time", INSTALL_SCRIPT_TEXT)
        self.assertIn("--max-filesize", INSTALL_SCRIPT_TEXT)

    def test_attestation_pins_a_commit_digest_not_a_movable_tag(self):
        self.assertIn("--source-digest", INSTALL_SCRIPT_TEXT)
        # The old flag name may still appear in an explanatory comment; what
        # must never come back is an actual invocation pinning to the tag ref.
        self.assertNotIn('--source-ref "refs/tags/v${version}"', INSTALL_SCRIPT_TEXT)

    def test_both_downloads_are_attestation_checked(self):
        self.assertIn(
            'for downloaded in "${checksums_path}" "${asset_path}"', INSTALL_SCRIPT_TEXT
        )


@unittest.skipUnless(shutil.which("bash"), "requires bash")
class FetchBinaryProvenanceTests(unittest.TestCase):
    """Actually runs scripts/install.sh's download path against stubbed
    omarchy/gh/curl/uname, to catch regressions the text guards above can't:
    wrong flag values, wrong file bound to the wrong check, etc."""

    def _make_plugin_dir(self, base, with_git):
        plugin_dir = base / "plugin"
        plugin_dir.mkdir()
        for name in ("manifest.json", "BarWidget.qml", "Panel.qml", "README.md", "example-config.json"):
            shutil.copy(ROOT / name, plugin_dir / name)
        scripts_dir = plugin_dir / "scripts"
        scripts_dir.mkdir()
        for name in ("install.sh", "backup.sh", "database-tools.sh"):
            dest = scripts_dir / name
            shutil.copy(ROOT / "scripts" / name, dest)
            dest.chmod(dest.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
        if with_git:
            run = lambda *args: subprocess.run(args, cwd=plugin_dir, check=True, capture_output=True)
            run("git", "init", "-q")
            run("git", "config", "user.email", "test@example.com")
            run("git", "config", "user.name", "Test")
            run("git", "add", "-A")
            run("git", "commit", "-q", "-m", "test")
            head = subprocess.run(
                ["git", "rev-parse", "HEAD"], cwd=plugin_dir, check=True, capture_output=True, text=True
            ).stdout.strip()
            return plugin_dir, head
        return plugin_dir, None

    def _make_stub_bin(self, base):
        bin_dir = base / "bin"
        bin_dir.mkdir()

        _write_stub(
            bin_dir / "uname",
            'case "$1" in -s) echo "Linux" ;; -m) echo "x86_64" ;; esac',
        )
        _write_stub(bin_dir / "omarchy", "exit 0")
        _write_stub(bin_dir / "go", 'echo "stub go: build always fails" >&2\nexit 1')

        _write_stub(
            bin_dir / "curl",
            "\n".join(
                [
                    'out="" prev=""',
                    'for a in "$@"; do',
                    '  if [[ "$prev" == "-o" ]]; then out="$a"; fi',
                    '  prev="$a"',
                    "done",
                    'url="${!#}"',
                    'printf \'%s\\n\' "$*" >> "$CURL_LOG"',
                    'case "$url" in',
                    "  *checksums.txt) printf '%s\\n' \"$CHECKSUMS_LINE\" > \"$out\" ;;",
                    "  *) printf '%s' \"$ASSET_CONTENT\" > \"$out\" ;;",
                    "esac",
                    "exit 0",
                ]
            ),
        )

        _write_stub(
            bin_dir / "gh",
            "\n".join(
                [
                    'printf \'%s\\n\' "$*" >> "$GH_LOG"',
                    'case "$1" in',
                    "  api) printf '%s\\n' \"$API_RESOLVED_SHA\" ;;",
                    "  attestation)",
                    '    if [[ "${GH_ATTEST_FAIL:-0}" == "1" ]]; then exit 1; fi',
                    "    exit 0",
                    "    ;;",
                    "esac",
                ]
            ),
        )
        return bin_dir

    def _run_install(self, base, plugin_dir, bin_dir, extra_env=None):
        config_home = base / "config"
        state_home = base / "state"
        curl_log = base / "curl.log"
        gh_log = base / "gh.log"
        curl_log.touch()
        gh_log.touch()

        env = {
            **os.environ,
            "PATH": str(bin_dir) + os.pathsep + os.environ.get("PATH", ""),
            "XDG_CONFIG_HOME": str(config_home),
            "XDG_STATE_HOME": str(state_home),
            "CURL_LOG": str(curl_log),
            "GH_LOG": str(gh_log),
            "CHECKSUMS_LINE": f"{ASSET_SHA256}  {ASSET_NAME}",
            "ASSET_CONTENT": ASSET_CONTENT,
            "API_RESOLVED_SHA": API_RESOLVED_SHA,
        }
        if extra_env:
            env.update(extra_env)

        result = subprocess.run(
            ["bash", str(plugin_dir / "scripts/install.sh")],
            cwd=plugin_dir,
            env=env,
            capture_output=True,
            text=True,
        )
        return result, curl_log.read_text(), gh_log.read_text()

    def test_downloads_pass_bounds_and_attestation_pins_resolved_commit_when_no_local_checkout(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            plugin_dir, _ = self._make_plugin_dir(base, with_git=False)
            bin_dir = self._make_stub_bin(base)

            result, curl_log, gh_log = self._run_install(base, plugin_dir, bin_dir)

            self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)

            for line in curl_log.splitlines():
                self.assertIn("--connect-timeout", line)
                self.assertIn("--max-time", line)
                self.assertIn("--max-filesize", line)

            self.assertIn(f"api repos/{REPO}/commits/v{VERSION}", gh_log)

            verify_calls = [line for line in gh_log.splitlines() if line.startswith("attestation verify")]
            self.assertEqual(len(verify_calls), 2, msg=gh_log)
            for call in verify_calls:
                self.assertIn(f"--source-digest {API_RESOLVED_SHA}", call)
                self.assertNotIn("--source-ref", call)
            # The downloaded asset is staged as plain "feader-rss-fetch" on
            # disk (the versioned/platform asset_name is only the checksums.txt
            # lookup key), so distinguish the two verify calls by that filename.
            self.assertTrue(any("checksums.txt" in call for call in verify_calls))
            self.assertTrue(
                any("checksums.txt" not in call and "feader-rss-fetch" in call for call in verify_calls)
            )

    def test_attestation_pins_local_head_instead_of_movable_tag_when_run_from_a_checkout(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            plugin_dir, head = self._make_plugin_dir(base, with_git=True)
            bin_dir = self._make_stub_bin(base)

            result, _curl_log, gh_log = self._run_install(
                base, plugin_dir, bin_dir, extra_env={"FEADER_RSS_ALLOW_REMOTE_FALLBACK": "1"}
            )

            self.assertEqual(result.returncode, 0, msg=result.stdout + result.stderr)

            verify_calls = [line for line in gh_log.splitlines() if line.startswith("attestation verify")]
            self.assertEqual(len(verify_calls), 2, msg=gh_log)
            for call in verify_calls:
                self.assertIn(f"--source-digest {head}", call)
                self.assertNotIn(API_RESOLVED_SHA, call)

    def test_install_refuses_binary_when_attestation_fails(self):
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            plugin_dir, _ = self._make_plugin_dir(base, with_git=False)
            bin_dir = self._make_stub_bin(base)

            result, _curl_log, _gh_log = self._run_install(
                base, plugin_dir, bin_dir, extra_env={"GH_ATTEST_FAIL": "1"}
            )

            self.assertNotEqual(result.returncode, 0)
            self.assertIn("attestation", (result.stdout + result.stderr).lower())
            destination = base / "config" / "omarchy" / "plugins" / "io.github.kitsunesemcalda.feader-rss"
            self.assertFalse((destination / "feader-rss-fetch").exists())


if __name__ == "__main__":
    unittest.main()
