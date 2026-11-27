"""Reject merge queue regressions and compare live protection outside CI."""

import copy
import io
import json
import os
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

import yaml

from scripts import check_config


ROOT = Path(__file__).resolve().parents[1]
REPOSITORY = "OpenRAE/go-archcheck"


def settings():
    """Return the committed protection baseline for mutation fixtures."""
    return json.loads((ROOT / check_config.BASELINE).read_text())


class PolicyTest(unittest.TestCase):
    def test_existing_producers_and_declared_protection_pass(self):
        check_config.check_branch_settings(ROOT, settings())

    def test_invalid_branch_settings_fail(self):
        mutations = [
            ("no dev queue", lambda value: value["dev"].update(merge_queue=None)),
            ("strict dev", lambda value: value["dev"]["required_status_checks"].update(strict=True)),
            ("relaxed main", lambda value: value["main"]["required_status_checks"].update(strict=False)),
            ("main queue", lambda value: value["main"].update(merge_queue=value["dev"]["merge_queue"])),
            ("single build", lambda value: value["dev"]["merge_queue"].update(max_entries_to_build=1)),
            ("single merge", lambda value: value["dev"]["merge_queue"].update(max_entries_to_merge=1)),
            ("failed groups", lambda value: value["dev"]["merge_queue"].update(grouping_strategy="HEADGREEN")),
            ("lost dev check", lambda value: value["dev"]["required_status_checks"]["contexts"].remove("SonarCloud")),
            ("lost gate", lambda value: value["dev"]["required_status_checks"]["contexts"].remove("maintainer-gate")),
            ("lost main check", lambda value: value["main"]["required_status_checks"]["contexts"].remove("CodeQL")),
            ("unknown dev check", lambda value: value["dev"]["required_status_checks"]["contexts"].append("Unknown")),
            ("unknown main check", lambda value: value["main"]["required_status_checks"]["contexts"].append("Unknown")),
            ("boolean count", lambda value: value["dev"]["merge_queue"].update(min_entries_to_merge=True)),
            ("integer strict", lambda value: value["dev"]["required_status_checks"].update(strict=0)),
            ("invalid limits", lambda value: value["dev"]["merge_queue"].update(min_entries_to_merge=6)),
            ("unknown method", lambda value: value["dev"]["merge_queue"].update(merge_method="UNKNOWN")),
            ("unknown field", lambda value: value["dev"]["merge_queue"].update(ignored_setting=True)),
            ("duplicate check", lambda value: value["dev"]["required_status_checks"]["contexts"].append("Check")),
            ("no merge methods", lambda value: value["dev"].update(allowed_merge_methods=[])),
            ("unknown merge method", lambda value: value["dev"].update(allowed_merge_methods=["fast-forward"])),
            ("duplicate merge method", lambda value: value["dev"].update(allowed_merge_methods=["squash", "squash"])),
            ("undeclared merge methods", lambda value: value["main"].pop("allowed_merge_methods")),
        ]
        for name, mutate in mutations:
            with self.subTest(mutation=name):
                value = settings()
                mutate(value["branches"])
                with self.assertRaises(AssertionError):
                    check_config.check_branch_settings(ROOT, value)

    def test_missing_or_malformed_data_fails(self):
        for value in ({}, {"branches": {}}, {"errors": [{"message": "denied"}]}, None):
            with self.subTest(value=value), self.assertRaises((AssertionError, TypeError)):
                check_config.check_branch_settings(ROOT, value)

    def test_offline_check_needs_no_api_access(self):
        with patch.object(check_config, "read_branch_settings") as read, \
                patch.object(sys, "stdout", io.StringIO()):
            check_config.main()
        read.assert_not_called()

    def test_live_check_compares_the_baseline_including_queue_limits(self):
        with patch.object(check_config, "read_branch_settings", return_value=settings()) as read, \
                patch.object(sys, "stdout", io.StringIO()) as output:
            check_config.main(live=REPOSITORY)
        read.assert_called_once_with(REPOSITORY)
        self.assertIn("Live GitHub protection matches the baseline", output.getvalue())
        for change in (
            lambda value: value["dev"]["merge_queue"].update(max_entries_to_build=6),
            lambda value: value["main"].update(allowed_merge_methods=["merge"]),
        ):
            drift = settings()
            change(drift["branches"])
            with patch.object(check_config, "read_branch_settings", return_value=drift):
                with self.assertRaisesRegex(AssertionError, "Live protection differs"):
                    check_config.main(live=REPOSITORY)

    def test_each_required_producer_rejects_missing_triggers_and_nonblocking_jobs(self):
        for context, (filename, job_id) in check_config.PRODUCERS.items():
            original = yaml.safe_load((ROOT / ".github/workflows" / filename).read_text())
            mutations = [
                ("missing pull request", lambda value: value[True].pop("pull_request")),
                ("missing merge group", lambda value: value[True].pop("merge_group")),
                ("wrong merge event", lambda value: value[True]["merge_group"].update(types=["destroyed"])),
                ("wrong branch", lambda value: value[True]["merge_group"].update(branches=["main"])),
                ("path filter", lambda value: value[True].update(pull_request={"paths-ignore": ["CHANGELOG.md"]})),
                ("ignored branch", lambda value: value[True]["merge_group"].update({"branches-ignore": ["dev"]})),
                ("renamed context", lambda value: value["jobs"][job_id].update(name="Different check")),
                ("skip job", lambda value: value["jobs"][job_id].update({"if": "false"})),
                ("skip merge group", lambda value: value["jobs"][job_id].update(
                    {"if": "github.event_name == 'pull_request'"})),
                ("other repository", lambda value: value["jobs"][job_id].update(
                    {"if": "github.repository_id == '1'"})),
                ("allow failure", lambda value: value["jobs"][job_id].update({"continue-on-error": True})),
                ("allow step failure", lambda value: value["jobs"][job_id]["steps"][0].update(
                    {"continue-on-error": True})),
            ]
            for name, mutate in mutations:
                with self.subTest(context=context, mutation=name), tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    path = root / ".github/workflows" / filename
                    path.parent.mkdir(parents=True)
                    value = copy.deepcopy(original)
                    mutate(value)
                    path.write_text(yaml.safe_dump(value))
                    with self.assertRaises(AssertionError):
                        check_config.check_producers(root, {context}, "dev")


    def test_each_guarded_producer_requires_its_exact_condition(self):
        for context, condition in check_config.JOB_CONDITIONS.items():
            self.assertTrue(condition.startswith(check_config.UPSTREAM))
            filename, job_id = check_config.PRODUCERS[context]
            original = yaml.safe_load((ROOT / ".github/workflows" / filename).read_text())
            for name, value in (("no guard", None), ("always", "always()")):
                with self.subTest(context=context, mutation=name), tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    path = root / ".github/workflows" / filename
                    path.parent.mkdir(parents=True)
                    mutated = copy.deepcopy(original)
                    mutated["jobs"][job_id].pop("if", None)
                    if value is not None:
                        mutated["jobs"][job_id]["if"] = value
                    path.write_text(yaml.safe_dump(mutated))
                    with self.assertRaisesRegex(AssertionError, "must not skip its check"):
                        check_config.check_producers(root, {context}, "dev")

    def test_each_upstream_job_requires_its_exact_guard(self):
        check_config.check_upstream_jobs(ROOT)
        for (filename, job_id), condition in check_config.UPSTREAM_JOBS.items():
            self.assertTrue(condition.startswith(check_config.UPSTREAM))
            original = yaml.safe_load((ROOT / ".github/workflows" / filename).read_text())
            for name, value in (("no guard", None), ("other repository", "github.repository_id == '1'"),
                                ("always", "always()")):
                with self.subTest(job=job_id, mutation=name), tempfile.TemporaryDirectory() as directory:
                    root = Path(directory)
                    for other in {f for f, _ in check_config.UPSTREAM_JOBS}:
                        path = root / ".github/workflows" / other
                        path.parent.mkdir(parents=True, exist_ok=True)
                        path.write_text((ROOT / ".github/workflows" / other).read_text())
                    mutated = copy.deepcopy(original)
                    mutated["jobs"][job_id].pop("if", None)
                    if value is not None:
                        mutated["jobs"][job_id]["if"] = value
                    (root / ".github/workflows" / filename).write_text(yaml.safe_dump(mutated))
                    with self.assertRaisesRegex(AssertionError, "must run only upstream"):
                        check_config.check_upstream_jobs(root)


class LiveReaderTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        stub = root / "gh"
        stub.write_text('''#!/usr/bin/env python3
import json
import os
import sys
assert sys.argv[sys.argv.index("--hostname") + 1] == "github.com"
assert sys.argv[sys.argv.index("--method") + 1] == "GET"
path = next(value for value in sys.argv if value.startswith("repos/"))
branch = "dev" if "/dev" in path else "main"
rules = "/rules/" in path
target = branch + ("_rules" if rules else "_protection")
if target == os.environ.get("FAIL_API"):
    sys.exit(1)
value = json.loads(os.environ["TEST_SNAPSHOT"])["branches"][branch]
if not rules:
    checks = dict(value["required_status_checks"])
    checks["contexts"] = [context for context in checks["contexts"] if context != "maintainer-gate"]
    print(json.dumps({"required_status_checks": checks}))
else:
    assert "--paginate" in sys.argv and "--slurp" in sys.argv
    data = []
    if branch == "dev":
        mutation = os.environ.get("RULE_MUTATION")
        data.append({"type": "required_status_checks", "parameters": {
            "strict_required_status_checks_policy": 1 if mutation == "malformed" else mutation == "strict",
            "required_status_checks": [{"context": "maintainer-gate"}],
        }})
    if value["merge_queue"] is not None:
        data.append({"type": "merge_queue", "parameters": value["merge_queue"]})
        if os.environ.get("RULE_MUTATION") == "duplicate":
            data.append(data[-1])
    if value["allowed_merge_methods"] is not None:
        data.append({"type": "pull_request", "parameters": {"allowed_merge_methods": ["merge", "rebase", "squash"]}})
        data.append({"type": "pull_request", "parameters": {"allowed_merge_methods": value["allowed_merge_methods"]}})
    print(json.dumps([data[:1], data[1:]]))
''')
        stub.chmod(0o700)
        environment = patch.dict(os.environ, {
            "PATH": f"{root}{os.pathsep}{os.environ['PATH']}", "TEST_SNAPSHOT": json.dumps(settings()),
            "FAIL_API": "", "RULE_MUTATION": "",
        })
        environment.start()
        self.addCleanup(environment.stop)
        output = patch.object(sys, "stdout", io.StringIO())
        output.start()
        self.addCleanup(output.stop)

    def test_reader_combines_classic_and_paginated_rules(self):
        self.assertEqual(check_config.read_branch_settings(REPOSITORY), settings())

    def test_each_api_failure_blocks_the_live_check(self):
        for failure in ("dev_protection", "dev_rules", "main_protection", "main_rules"):
            with self.subTest(api=failure), patch.dict(os.environ, {"FAIL_API": failure}):
                with self.assertRaises(subprocess.CalledProcessError):
                    check_config.main(live=REPOSITORY)

    def test_strict_ruleset_cannot_hide_behind_relaxed_classic_protection(self):
        with patch.dict(os.environ, {"RULE_MUTATION": "strict"}):
            with self.assertRaisesRegex(AssertionError, "dev must not require strict"):
                check_config.main(live=REPOSITORY)

    def test_ambiguous_or_malformed_rules_fail(self):
        for mutation in ("duplicate", "malformed"):
            with self.subTest(mutation=mutation), patch.dict(os.environ, {"RULE_MUTATION": mutation}):
                with self.assertRaises(AssertionError):
                    check_config.read_branch_settings(REPOSITORY)

    def test_invalid_repository_fails_before_api_access(self):
        with patch.object(check_config, "github_json") as read:
            with self.assertRaisesRegex(AssertionError, "Invalid repository"):
                check_config.read_branch_settings("/another/host")
        read.assert_not_called()

    def test_invalid_json_and_timeouts_are_failures(self):
        with patch.object(check_config.subprocess, "check_output", return_value="not JSON"):
            with self.assertRaises(json.JSONDecodeError):
                check_config.github_json(f"repos/{REPOSITORY}")
        with patch.object(check_config.subprocess, "check_output", side_effect=subprocess.TimeoutExpired("gh", 30)):
            with self.assertRaises(subprocess.TimeoutExpired):
                check_config.github_json(f"repos/{REPOSITORY}")


if __name__ == "__main__":
    unittest.main()
