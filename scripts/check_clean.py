#!/usr/bin/env python3
"""Check an exact Git tree for a small set of public-repository hazards."""

from __future__ import annotations

import argparse
import hashlib
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


MAX_SOURCE_BYTES = 1_000_000
ARCHIVE_SUFFIXES = (".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm", ".mbox")
ALLOWED_ARCHIVES = {
    "trawlers/telegram/internal/telegramdesktop/postbox/testdata/sqlcipher_v4.db":
        "91339dac348ae4e20d1b52c6acfa50a11f25b87af79a5e578f7fc52d008a325c",
}
PLACEHOLDER_USERS = {"you", "yourname", "example", "user", "username", "runner"}
PHONE = re.compile(r"\+[0-9]{10,13}")
HOME = re.compile(r"(?:/Users|/home)/([a-z][a-z0-9]+)/", re.IGNORECASE)


@dataclass(frozen=True)
class Blob:
    path: str
    oid: str
    size: int


def git(root: Path, *args: str, input_data: bytes | None = None) -> bytes:
    return subprocess.run(
        ["git", *args],
        cwd=root,
        input=input_data,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=True,
    ).stdout


def tree_blobs(root: Path, tree: str) -> list[Blob]:
    raw = git(root, "ls-tree", "-r", "-z", "--long", tree)
    blobs: list[Blob] = []
    for record in raw.split(b"\0"):
        if not record:
            continue
        metadata, encoded_path = record.split(b"\t", 1)
        _mode, object_type, oid, encoded_size = metadata.split()
        if object_type != b"blob":
            continue
        blobs.append(
            Blob(
                path=encoded_path.decode("utf-8", errors="surrogateescape"),
                oid=oid.decode("ascii"),
                size=int(encoded_size),
            )
        )
    return blobs


def blob_contents(root: Path, blobs: list[Blob]) -> dict[str, bytes]:
    if not blobs:
        return {}
    proc = subprocess.Popen(
        ["git", "cat-file", "--batch"],
        cwd=root,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    assert proc.stdin is not None
    assert proc.stdout is not None
    for blob in blobs:
        proc.stdin.write(blob.oid.encode("ascii") + b"\n")
    proc.stdin.close()

    contents: dict[str, bytes] = {}
    for blob in blobs:
        header = proc.stdout.readline().rstrip(b"\n").split()
        if len(header) != 3 or header[1] != b"blob":
            proc.kill()
            raise RuntimeError(f"could not read Git blob for {blob.path}")
        size = int(header[2])
        data = proc.stdout.read(size)
        if len(data) != size or proc.stdout.read(1) != b"\n":
            proc.kill()
            raise RuntimeError(f"truncated Git blob for {blob.path}")
        contents[blob.oid] = data

    stderr = proc.stderr.read() if proc.stderr is not None else b""
    if proc.wait() != 0:
        raise RuntimeError(stderr.decode("utf-8", errors="replace"))
    return contents


def inspect_blob(blob: Blob, data: bytes) -> list[str]:
    findings: list[str] = []
    path = blob.path

    if blob.size > MAX_SOURCE_BYTES:
        findings.append(f"{path}: file exceeds 1 MB")

    if path.lower().endswith(ARCHIVE_SUFFIXES):
        expected = ALLOWED_ARCHIVES.get(path)
        digest = hashlib.sha256(data).hexdigest()
        if expected is None:
            findings.append(f"{path}: archive-like files are not allowed")
        elif digest != expected:
            findings.append(f"{path}: approved synthetic fixture content changed")

    if path.endswith((".sum", ".lock")) or b"\0" in data:
        return findings

    text = data.decode("utf-8", errors="replace")
    for line_number, line in enumerate(text.splitlines(), start=1):
        for match in PHONE.finditer(line):
            if not match.group().startswith("+1555"):
                findings.append(f"{path}:{line_number}: use a +1555 synthetic phone number")
        for match in HOME.finditer(line):
            if match.group(1).lower() not in PLACEHOLDER_USERS:
                findings.append(f"{path}:{line_number}: replace a named home path with ~ or $HOME")
    return findings


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Inspect the staged tree, or an explicit Git tree, for public-repository hazards."
    )
    source = parser.add_mutually_exclusive_group()
    source.add_argument(
        "--tree",
        help="Git commit or tree to inspect exactly; defaults to the current index",
    )
    source.add_argument(
        "--range",
        dest="revision_range",
        help="Git revision range whose commit trees must all be inspected",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = Path(git(Path.cwd(), "rev-parse", "--show-toplevel").decode().strip())
    if args.revision_range:
        trees = git(root, "rev-list", "--reverse", args.revision_range).decode().splitlines()
    else:
        trees = [args.tree or git(root, "write-tree").decode().strip()]

    findings: list[str] = []
    for tree in trees:
        blobs = tree_blobs(root, tree)
        contents = blob_contents(root, blobs)
        prefix = f"{tree[:12]} " if args.revision_range else ""
        findings.extend(
            prefix + finding
            for blob in blobs
            for finding in inspect_blob(blob, contents[blob.oid])
        )
    if findings:
        print("check-clean found public-repository hazards:")
        for finding in findings:
            print(f"  {finding}")
        print(
            "\nThis mechanical guard is not proof that a repository contains no private data.",
            file=sys.stderr,
        )
        return 1
    print("check-clean: ok (mechanical public-data rules only; not privacy proof)")
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, RuntimeError, subprocess.CalledProcessError) as error:
        print(f"check-clean could not inspect the Git tree: {error}", file=sys.stderr)
        raise SystemExit(2) from error
