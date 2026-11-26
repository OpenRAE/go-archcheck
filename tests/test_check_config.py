"""The configuration check accepts this repository, in a fork too."""

import io
import os
import shutil
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from scripts import check_config
from scripts.check_config import main


class CheckConfigTest(unittest.TestCase):
    """Run the configuration check on the repository files."""

    def test_repository_configuration_passes(self):
        with patch.object(sys, "stdout", io.StringIO()) as output:
            main()
        self.assertIn("Validated", output.getvalue())

    def test_fork_identity_passes(self):
        with patch.dict(os.environ, {"GITHUB_REPOSITORY": "someone/fork"}), \
                patch.object(sys, "stdout", io.StringIO()) as output:
            main()
        self.assertIn("Validated", output.getvalue())

    def test_ground_control_files_are_not_needed(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            shutil.copytree(check_config.ROOT / ".github", root / ".github")
            with patch.object(check_config, "ROOT", root), \
                    patch.object(sys, "stdout", io.StringIO()) as output:
                main()
        self.assertIn("Validated", output.getvalue())


if __name__ == "__main__":
    unittest.main()
