"""Validate repository configuration before a change is published."""

import argparse
import json
import re
import subprocess
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]

# The required contexts of dev and the workflow job that produces each one.
PRODUCERS = {
    "Analyze (go)": ("codeql.yml", "analyze"),
    "Check": ("ci.yml", "check"),
    "GitGuardian Security Checks": ("secret-scan.yml", "ggshield"),
    "Lint PR title": ("pr-title-lint.yml", "lint-title"),
    "SonarCloud": ("ci.yml", "sonar"),
    "maintainer-gate": ("maintainer-gate.yml", "maintainer-gate"),
}
# The code scanning app produces the CodeQL context, and the GitGuardian App produces
# the GitGuardian context on main. No workflow in this repository produces them there.
MAIN_CHECKS = (set(PRODUCERS) - {"maintainer-gate"}) | {"CodeQL"}
ALWAYS = "always()"
# A job that needs an upstream secret, the upstream maintainer or the private Ground Control
# release runs only in OpenRAE/go-archcheck. The guard uses the repository ID, because a
# rename keeps the ID and each fork has another one. So a rename cannot skip an upstream check.
UPSTREAM = "github.repository_id == '1442018845'"
# A job condition must be true on each upstream merge group, so the queue always gets a real
# result. The SonarCloud job also skips pull requests from other repositories, which get no token.
JOB_CONDITIONS = {
    "SonarCloud": f"{UPSTREAM} && (github.event_name != 'pull_request' || "
    "github.event.pull_request.head.repo.full_name == github.repository)",
    "GitGuardian Security Checks": UPSTREAM,
    "maintainer-gate": UPSTREAM,
}
# The upstream jobs that no branch requires, and the exact condition of each one.
UPSTREAM_JOBS = {
    ("ground-control-phase-e.yml", "finalize"): f"{UPSTREAM} && (github.event_name == 'workflow_dispatch' "
    "|| github.event.pull_request.merged == true)",
    ("release-please.yml", "release-please"): UPSTREAM,
    ("scorecard.yml", "scorecard"): UPSTREAM,
    ("sync-main-to-dev.yml", "backmerge"): UPSTREAM,
}
BASELINE = Path(".github/branch-protection-baseline.json")
MERGE_METHODS = {"merge", "squash", "rebase"}


class UniqueKeysLoader(yaml.SafeLoader):
    """Reject duplicate mapping keys in repository YAML."""

    pass


def construct_mapping(loader: yaml.SafeLoader, node: yaml.nodes.MappingNode) -> dict[object, object]:
    """Build a YAML mapping while rejecting duplicate keys."""
    result = {}
    for key_node, value_node in node.value:
        key = loader.construct_object(key_node)
        if key in result:
            raise ValueError(f"duplicate YAML key: {key}")
        result[key] = loader.construct_object(value_node)
    return result


UniqueKeysLoader.add_constructor(
    yaml.resolver.BaseResolver.DEFAULT_MAPPING_TAG, construct_mapping
)


def check_producers(root: Path, contexts: set[str], branch: str) -> None:
    """Require a known, blocking producer on pull requests and merge groups for each context."""
    for context in sorted(contexts):
        assert context in PRODUCERS, f"No merge group producer configured for {context}"
        filename, job_id = PRODUCERS[context]
        document = yaml.load(
            (root / ".github/workflows" / filename).read_text(encoding="utf-8"),
            Loader=UniqueKeysLoader,
        )
        events = document.get("on", document.get(True, {}))
        for event in ("pull_request", "merge_group"):
            assert event in events, f"{context} must run on {event}"
            options = events[event] or {}
            assert not {"paths", "paths-ignore", "branches-ignore"} & options.keys(), (
                f"{context} must not filter {event} checks"
            )
            assert branch in options.get("branches", [branch]), f"{context} must run for {branch}"
            if event == "merge_group":
                assert "checks_requested" in options.get("types", ["checks_requested"]), (
                    f"{context} must run on merge_group checks_requested"
                )
        job = document["jobs"][job_id]
        assert job.get("name", job_id) == context, f"Producer must report {context}"
        assert job.get("if", ALWAYS) == JOB_CONDITIONS.get(context, ALWAYS), f"{context} must not skip its check"
        assert not job.get("continue-on-error"), f"{context} must block on failure"
        assert not any(step.get("continue-on-error") for step in job["steps"]), (
            f"{context} steps must block on failure"
        )


def check_upstream_jobs(root: Path) -> None:
    """Require the exact upstream condition on each upstream job that no branch requires."""
    for (filename, job_id), condition in UPSTREAM_JOBS.items():
        document = yaml.load(
            (root / ".github/workflows" / filename).read_text(encoding="utf-8"),
            Loader=UniqueKeysLoader,
        )
        assert document["jobs"][job_id].get("if") == condition, f"{filename} job {job_id} must run only upstream"


def check_merge_methods(branch: str, methods: list[str] | None) -> None:
    """Accept no restriction, or a non-empty list of distinct known merge methods."""
    assert methods is None or (
        isinstance(methods, list) and methods and set(methods) <= MERGE_METHODS
        and len(set(methods)) == len(methods)
    ), f"{branch} allowed merge methods must be null or a list of known methods"


def check_dev_queue(queue: dict | None) -> None:
    """Require an active dev queue that groups several passing pull requests."""
    assert queue is not None, "dev must use an active merge queue before relaxing strict checks"
    counts = {"max_entries_to_build", "min_entries_to_merge", "max_entries_to_merge",
              "min_entries_to_merge_wait_minutes", "check_response_timeout_minutes"}
    assert set(queue) == counts | {"merge_method", "grouping_strategy"}, "Unknown queue settings"
    for name in counts:
        assert type(queue[name]) is int and queue[name] >= (0 if name.endswith("wait_minutes") else 1), (
            f"Queue {name} must be a valid integer"
        )
    assert queue["max_entries_to_build"] > 1, "dev must allow concurrent queue builds"
    assert queue["max_entries_to_merge"] > 1, "dev must allow multiple pull requests per merge"
    assert queue["min_entries_to_merge"] <= queue["max_entries_to_merge"], "Invalid merge limits"
    assert queue["grouping_strategy"] == "ALLGREEN", "Every queued pull request must pass"
    assert queue["merge_method"] in {"SQUASH", "MERGE", "REBASE"}, "Unknown queue merge method"


def check_branch_settings(root: Path, settings: dict) -> None:
    """Check the baseline or the same projection of live effective settings."""
    assert set(settings) == {"branches"}, "Protection settings must declare branches only"
    assert set(settings["branches"]) == {"dev", "main"}, "Protection must cover dev and main"
    for branch, protection in settings["branches"].items():
        assert set(protection) == {"required_status_checks", "merge_queue", "allowed_merge_methods"}, (
            f"{branch} must declare status checks, its merge queue and its allowed merge methods"
        )
        check_merge_methods(branch, protection["allowed_merge_methods"])
        checks = protection["required_status_checks"]
        assert set(checks) == {"strict", "contexts"}, f"{branch} must declare strict and contexts"
        assert type(checks["strict"]) is bool, f"{branch} strict must be a boolean"
        assert isinstance(checks["contexts"], list) and all(
            isinstance(context, str) for context in checks["contexts"]
        ), f"{branch} contexts must be a list of strings"
        contexts = set(checks["contexts"])
        assert len(contexts) == len(checks["contexts"]), f"{branch} has duplicate required checks"
        expected = set(PRODUCERS) if branch == "dev" else MAIN_CHECKS
        assert expected <= contexts, f"{branch} lost required checks: {sorted(expected - contexts)}"
        queue = protection["merge_queue"]
        if branch == "dev":
            assert checks["strict"] is False, "dev must not require strict status checks"
            check_dev_queue(queue)
            check_producers(root, contexts, branch)
        else:
            assert queue is None, "main must stay outside the merge queue"
            assert checks["strict"] is True, "main must retain strict status checks"
            assert contexts == MAIN_CHECKS, f"Unknown main checks: {sorted(contexts - MAIN_CHECKS)}"


def github_json(path: str, *, paginate: bool = False) -> dict | list:
    """Read GitHub with existing CLI authentication; never change repository settings."""
    command = ["gh", "api", "--hostname", "github.com", "--method", "GET", path]
    if paginate:
        command.extend(["--paginate", "--slurp"])
    return json.loads(subprocess.check_output(command, text=True, timeout=30))


def effective_rules(repository: str, branch: str) -> list[dict]:
    """Return every active ruleset rule for a branch across all pages."""
    pages = github_json(f"repos/{repository}/rules/branches/{branch}?per_page=100", paginate=True)
    return [rule for page in pages for rule in page]


def effective_merge_methods(rules: list[dict]) -> list[str] | None:
    """Intersect the methods of each pull request rule; None means no restriction."""
    allowed = [set(rule["parameters"]["allowed_merge_methods"]) for rule in rules if rule["type"] == "pull_request"]
    return sorted(set.intersection(*allowed)) if allowed else None


def read_branch_settings(repository: str) -> dict:
    """Combine classic checks and active rulesets for the authenticated operator check."""
    assert re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repository), "Invalid repository identity"
    settings = {"branches": {}}
    for branch in ("dev", "main"):
        protection = github_json(f"repos/{repository}/branches/{branch}/protection")
        checks = protection["required_status_checks"]
        strict = checks["strict"]
        assert type(strict) is bool, f"{branch} classic strict must be a boolean"
        contexts = set(checks["contexts"])
        rules = effective_rules(repository, branch)
        for rule in rules:
            if rule["type"] == "required_status_checks":
                parameters = rule["parameters"]
                value = parameters["strict_required_status_checks_policy"]
                assert type(value) is bool, f"{branch} ruleset strict must be a boolean"
                strict = strict or value
                contexts.update(check["context"] for check in parameters["required_status_checks"])
        queues = [rule["parameters"] for rule in rules if rule["type"] == "merge_queue"]
        assert len(queues) <= 1, f"{branch} has multiple active merge queue rules"
        settings["branches"][branch] = {
            "required_status_checks": {"strict": strict, "contexts": sorted(contexts)},
            "merge_queue": queues[0] if queues else None,
            "allowed_merge_methods": effective_merge_methods(rules),
        }
    return settings


def main(*, live: str | None = None) -> None:
    """Check workflow policy offline, or also compare the live settings of the repository live."""
    paths = sorted((ROOT / ".github" / "workflows").glob("*.yml"))
    for path in paths:
        with path.open(encoding="utf-8") as stream:
            yaml.load(stream, Loader=UniqueKeysLoader)
    check_upstream_jobs(ROOT)
    baseline = json.loads((ROOT / BASELINE).read_text(encoding="utf-8"))
    check_branch_settings(ROOT, baseline)
    if live:
        actual = read_branch_settings(live)
        check_branch_settings(ROOT, actual)
        assert actual == baseline, f"Live protection differs from {BASELINE}"
        print("Live GitHub protection matches the baseline")
    print(f"Validated {len(paths)} workflow files, the upstream jobs and branch protection")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--live", metavar="OWNER/NAME", help="Compare the live protection of the repository using authenticated gh CLI"
    )
    main(live=parser.parse_args().live)
