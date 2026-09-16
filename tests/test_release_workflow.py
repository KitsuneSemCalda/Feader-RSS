import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]
WORKFLOW = (ROOT / ".github/workflows/release.yml").read_text()


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


if __name__ == "__main__":
    unittest.main()
