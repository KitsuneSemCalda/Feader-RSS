import unittest
from pathlib import Path


ROOT = Path(__file__).parents[1]
WORKFLOW = (ROOT / ".github/workflows/release.yml").read_text()


class ReleaseWorkflowTests(unittest.TestCase):
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
