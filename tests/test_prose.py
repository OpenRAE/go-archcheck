"""Check how make prose passes file names to the two prose tools."""

import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RECORD_ARGUMENTS = 'printf "%s\\0" "$@" > "$0.args"\n'


class ProseTest(unittest.TestCase):
    """Run the Makefile with a small Git index and stand-ins for the linters."""

    def setUp(self):
        cache = ROOT / ".cache"
        cache.mkdir(exist_ok=True)
        self.root = Path(self.enterContext(tempfile.TemporaryDirectory(dir=cache)))
        self.run_command("git", "init", "--quiet")
        self.vale = self.root / "vale.sh"
        self.ste = self.root / "doc-tools" / "tools" / "ste-lint.sh"
        self.ste.parent.mkdir(parents=True)
        (self.ste.parent.parent / ".verified").touch()
        for tool in (self.vale, self.ste):
            tool.write_text(RECORD_ARGUMENTS, encoding="utf-8")

    def run_command(self, *args):
        result = subprocess.run(args, cwd=self.root, capture_output=True, check=False)
        self.assertEqual(result.returncode, 0, result.stderr.decode())

    def check_prose(self, *paths, options=()):
        self.run_command(
            "make", "-f", str(ROOT / "Makefile"), "prose",
            "DOC_TOOLS=doc-tools", "MISE=bash vale.sh", *options,
        )
        expected = [path.encode() for path in paths]
        vale = self.vale.with_suffix(".sh.args").read_bytes().split(b"\0")
        ste = self.ste.with_suffix(".sh.args").read_bytes().split(b"\0")
        self.assertEqual(vale, [b"exec", b"--", b"vale", b"--", *expected, b""])
        self.assertEqual(ste, [b"--", *expected, b""])

    def test_tracked_paths_reach_both_tools_as_single_arguments(self):
        (self.root / "testdata").mkdir()
        for path in ("-draft.md", "two words.md", "testdata/fixture.md"):
            (self.root / path).touch()
        self.run_command("git", "add", "--", "-draft.md", "two words.md", "testdata/fixture.md")
        self.check_prose("-draft.md", "two words.md")

    def test_explicit_paths_can_include_untracked_files(self):
        (self.root / "track.md").touch()
        self.run_command("git", "add", "--", "track.md")
        (self.root / "draft guide.md").touch()
        self.check_prose("draft guide.md", options=('PROSE_PATHS="draft guide.md"',))
