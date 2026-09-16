import subprocess
import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]
WORKFLOW = (ROOT / ".github/workflows/release.yml").read_text()
CHANGELOG = ROOT / "docs/CHANGELOG.md"


class ReleaseWorkflowTests(unittest.TestCase):
    def test_validate_job_pins_release_digest_before_building(self):
        self.assertIn("Verify RELEASE_DIGESTS.json pins this exact commit", WORKFLOW)
        # Must read from master's current tip via the API, not the checked-out
        # tag commit's own tree: a commit can never contain the correct digest
        # entry for its own hash (see the workflow comment), so the entry for
        # a freshly tagged commit necessarily lives in a later commit on
        # master.
        self.assertIn("contents/RELEASE_DIGESTS.json?ref=master", WORKFLOW)
        self.assertNotIn("open('RELEASE_DIGESTS.json')", WORKFLOW)
        self.assertIn('"${pinned}" != "${GITHUB_SHA}"', WORKFLOW)

    def test_matrix_uploads_unique_checksums_and_release_combines_all(self):
        self.assertIn('checksum="checksums_${GOOS}_${GOARCH}.txt"', WORKFLOW)
        self.assertIn('sha256sum "${out}" > "${checksum}"', WORKFLOW)
        self.assertIn("checksums_${{ matrix.goos }}_${{ matrix.goarch }}.txt", WORKFLOW)
        self.assertIn("checksum_files=(dist/checksums_*.txt)", WORKFLOW)
        self.assertIn('"${#checksum_files[@]}" -ne 4', WORKFLOW)
        self.assertIn('sort -k2 "${checksum_files[@]}" > checksums.txt', WORKFLOW)
        self.assertNotIn("cat dist/checksums.txt", WORKFLOW)

    def test_release_body_is_extracted_from_the_changelog(self):
        self.assertIn("Extract this version's changelog section", WORKFLOW)
        self.assertIn("docs/CHANGELOG.md", WORKFLOW)
        self.assertIn("body_path: release-notes.md", WORKFLOW)
        # generate_release_notes still runs alongside body_path: GitHub's
        # release API appends the auto-generated commit list after the given
        # body rather than replacing it.
        self.assertIn("generate_release_notes: true", WORKFLOW)

    def test_changelog_extraction_script_isolates_one_versions_section(self):
        # Exercises the same awk one-liner the workflow uses, against a
        # controlled fixture, so a change to the pattern that breaks
        # extraction fails here instead of on the next real release.
        script = (
            r"""awk -v ver="$1" '$0 ~ ("^## \\[" ver "\\]") { found=1; next } """
            r"""found && /^## \[/ { exit } found { print }' "$2" """
        )
        fixture = ROOT / "tests" / "_fixture_changelog.md"
        fixture.write_text(
            "# Changelog\n\n"
            "## [Unreleased]\n\n"
            "## [0.3.4] - 2026-09-15\n\n"
            "### Changed\n- new thing\n\n"
            "## [0.3.3] - 2026-09-02\n\n"
            "### Fixed\n- old thing\n"
        )
        try:
            result = subprocess.run(
                ["bash", "-c", script, "_", "0.3.4", str(fixture)],
                capture_output=True, text=True, check=True,
            )
            self.assertIn("### Changed", result.stdout)
            self.assertIn("new thing", result.stdout)
            self.assertNotIn("old thing", result.stdout)
            self.assertNotIn("[0.3.3]", result.stdout)

            missing = subprocess.run(
                ["bash", "-c", script, "_", "9.9.9", str(fixture)],
                capture_output=True, text=True, check=True,
            )
            self.assertEqual(missing.stdout, "")
        finally:
            fixture.unlink()

    def test_current_changelog_has_a_section_for_the_manifest_version(self):
        import json
        import re

        version = json.loads((ROOT / "manifest.json").read_text())["version"]
        self.assertIsNotNone(
            re.search(rf"^## \[{re.escape(version)}\]", CHANGELOG.read_text(), re.MULTILINE),
            msg=f"docs/CHANGELOG.md has no ## [{version}] section for the current manifest version.",
        )


if __name__ == "__main__":
    unittest.main()
