# Contribute to go-rae-archcheck

go-rae-archcheck holds the architecture of the projects that use it, so its behavior must stay exact and predictable.
Contributions are welcome when they keep the checker generic, tested and documented.

## Choose the route

- For a small documentation or test fix, open a pull request.
- For a change of behavior, of the model format or of the command, open an issue first.
- Keep unrelated work in separate pull requests.
- Base each pull request on `dev`. The `main` branch holds the releases.

## Set up the repository

1. Install [mise](https://mise.jdx.dev/getting-started.html) 2026.10.0 or newer.
2. Run `make tools`.
3. Run `mise exec -- uv run --locked --no-build pre-commit install` to install the hooks.

## Check a change

`make ci-check` runs every check that CI runs: lint, security, tests, mutation testing, fuzzing, regression proof, the self-check, prose and the configuration checks.
`make hygiene` runs the file hygiene, workflow and secret hooks on every file.
Run `make help` for the individual checks.

## Follow the standards

- Write Go in the Google Go style. golangci-lint and the OpenRÆ `raesvet` analyzers enforce it.
- Keep statement coverage at or above 90% for each package.
- Keep the mutation test efficacy and the mutant coverage at or above 90%. `make mutation` checks them with Gremlins.
- Test the command contract with the testscript files in `cmd/archcheck/testdata/script`, and give each new parser a fuzz target.
- Name a regression test `TestRegression...` in a `*_regression_test.go` file. It must fail on the base revision.
- Add a design record in `architecture/designs` for each structural change of `architecture/model.yaml`.
- Reserve the number of each new design record with `archcheck reserve`. `make arch` fails on a record without its tag.
- Use a standard or widely used library before custom code, and record the reason for custom code.
- Write documentation in ASD-STE100 Simplified Technical English. Vale checks it.
- Write each pull request title as a Conventional Commit, for example `fix: report a missing design record`.

## Open a pull request

Fill in each section of the pull request template.
CI must pass before a merge.

Release Please makes each release from the Conventional Commit history on `main`.
It writes the version and `CHANGELOG.md`, so do not edit them in a pull request.

## Merge a pull request

Pull requests to `dev` merge through the merge queue.
The queue merges a pull request only when each required check passes on its merge group.
When the queue removes a pull request without a merge, the pull request gets the `merge-queue-removed` label.

Decision record [0004](docs/decisions/0004-dev-merge-queue.md) states the queue settings and the policy check of the branch protection.

## Upstream jobs

These checks run only in `OpenRAE/go-rae-archcheck`, because they need its secrets or its maintainer:

- The `SonarCloud` job analyzes the code and waits for the quality gate.
- The `GitGuardian Security Checks` job scans the commits of each pull request and merge group for secrets.
- The `maintainer-gate` job requires the maintainer as the author or an approver.

A fork skips these checks and the other upstream jobs.
Decision record [0006](docs/decisions/0006-optional-maintainer-workflows.md) lists each job.

The `Ground Control Phase E` workflow is private maintainer automation.
It runs only upstream, and contributors do not need it.
