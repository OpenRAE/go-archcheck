# 0004: Pull requests to dev merge through a merge queue

Date: 2026-11-22.

## Context

With strict status checks, each merge into `dev` makes the other open pull requests out of date.
Each update runs the full CI again.
A required check reports on a merge group only when its producer runs on the `merge_group` event.

## Decision

Ordinary pull requests to `dev` merge through the GitHub merge queue.
`main` stays outside the queue, keeps strict status checks, and the maintainer merges into `main`.

The maintainer's private readiness automation adds a ready pull request to the queue.
It has maintainer authorization for this step on `dev` only.

`dev` does not require strict status checks, because the queue tests each merge group against the latest `dev`.
The queue builds up to five merge groups at the same time and merges up to five pull requests together.
`ALLGREEN` grouping requires each pull request in a merge group to pass its checks.

Each required context of `dev` comes from a workflow job in this repository:

| Context | Producer |
| --- | --- |
| `Analyze (go)` | `codeql.yml` job `analyze` |
| `Check` | `ci.yml` job `check` |
| `GitGuardian Security Checks` | `secret-scan.yml` job `ggshield` |
| `Lint PR title` | `pr-title-lint.yml` job `lint-title` |
| `SonarCloud` | `ci.yml` job `sonar` |
| `maintainer-gate` | `maintainer-gate.yml` job `maintainer-gate` |

Each producer runs on `pull_request` and `merge_group` events with no path or branch filter that skips `dev`.
A failed check on a merge group removes the pull request from the queue.

## Policy

`.github/branch-protection-baseline.json` records the protection of `dev` and `main`.
`scripts/check_config.py` checks the baseline and the producers in CI.
The check fails in these conditions:

- A required producer of `dev` has no `merge_group` or `pull_request` trigger, or a filter skips `dev`.
- A producer job has a different name, a skip condition, or `continue-on-error`.
- `dev` has strict status checks, no merge queue, or queue limits that stop groups of more than one pull request.
- `main` has a merge queue, no strict status checks, or a changed set of required checks.

The job condition of `SonarCloud` skips pull requests from other repositories, which get no token.
The upstream condition of [0006](0006-optional-maintainer-workflows.md) also applies.
Each condition is true on each upstream merge group, so the queue always gets a real result.

The built-in GitHub token cannot read classic branch protection, so CI does not compare the live settings.
After a change to the branch rules, an operator runs `python scripts/check_config.py --live OpenRAE/go-rae-archcheck`.
That command reads the classic protection and the active rulesets with the GitHub CLI, and fails when they differ from the baseline.

## Consequences

A change to the branch rules also changes the baseline in the same pull request.
A new required check of `dev` needs a producer in `PRODUCERS` in `scripts/check_config.py`.
