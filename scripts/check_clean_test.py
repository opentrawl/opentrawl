#!/usr/bin/env python3

from __future__ import annotations

import importlib.util
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("check_clean", ROOT / "scripts/check_clean.py")
assert SPEC is not None and SPEC.loader is not None
CHECK_CLEAN = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = CHECK_CLEAN
SPEC.loader.exec_module(CHECK_CLEAN)


class CheckCleanTests(unittest.TestCase):
    def run_guard(self, path: str, content: bytes) -> subprocess.CompletedProcess[str]:
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
            target = repo / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(content)
            subprocess.run(["git", "add", path], cwd=repo, check=True)
            return subprocess.run(
                ["python3", str(ROOT / "scripts/check_clean.py")],
                cwd=repo,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=False,
            )

    def test_allows_synthetic_phone_number(self) -> None:
        result = self.run_guard("sample.go", ("+" + "15550102030").encode())
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)

    def test_rejects_phone_number_in_test_source(self) -> None:
        result = self.run_guard("sample_test.go", ("+" + "14155550100").encode())
        self.assertEqual(result.returncode, 1)

    def test_rejects_phone_number_in_testdata(self) -> None:
        result = self.run_guard("testdata/sample.txt", ("+" + "14155550100").encode())
        self.assertEqual(result.returncode, 1)

    def test_rejects_named_home_path(self) -> None:
        result = self.run_guard("sample.txt", ("/" + "Users/private/project").encode())
        self.assertEqual(result.returncode, 1)

    def test_rejects_unapproved_archive(self) -> None:
        result = self.run_guard("testdata/archive.db", b"synthetic")
        self.assertEqual(result.returncode, 1)

    def test_approved_archive_is_content_addressed(self) -> None:
        fixture_path, expected = next(iter(CHECK_CLEAN.ALLOWED_ARCHIVES.items()))
        fixture = (ROOT / fixture_path).read_bytes()
        self.assertEqual(CHECK_CLEAN.hashlib.sha256(fixture).hexdigest(), expected)
        blob = CHECK_CLEAN.Blob(fixture_path, "unused", len(fixture))
        self.assertEqual(CHECK_CLEAN.inspect_blob(blob, fixture), [])
        self.assertNotEqual(CHECK_CLEAN.inspect_blob(blob, fixture + b"changed"), [])

    def test_revision_range_catches_content_removed_later(self) -> None:
        with tempfile.TemporaryDirectory() as directory:
            repo = Path(directory)
            subprocess.run(["git", "init", "-q"], cwd=repo, check=True)
            subprocess.run(["git", "config", "user.email", "agent@example.com"], cwd=repo, check=True)
            subprocess.run(["git", "config", "user.name", "Example Agent"], cwd=repo, check=True)
            (repo / "safe.txt").write_text("safe\n")
            subprocess.run(["git", "add", "."], cwd=repo, check=True)
            subprocess.run(["git", "commit", "-qm", "baseline"], cwd=repo, check=True)
            baseline = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=repo, text=True).strip()

            (repo / "sample.txt").write_text("+" + "14155550100")
            subprocess.run(["git", "add", "."], cwd=repo, check=True)
            subprocess.run(["git", "commit", "-qm", "add unsafe fixture"], cwd=repo, check=True)
            (repo / "sample.txt").unlink()
            subprocess.run(["git", "add", "-u"], cwd=repo, check=True)
            subprocess.run(["git", "commit", "-qm", "remove unsafe fixture"], cwd=repo, check=True)

            result = subprocess.run(
                [
                    "python3",
                    str(ROOT / "scripts/check_clean.py"),
                    "--range",
                    f"{baseline}..HEAD",
                ],
                cwd=repo,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
                check=False,
            )
            self.assertEqual(result.returncode, 1)


if __name__ == "__main__":
    unittest.main()
