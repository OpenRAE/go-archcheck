# 0007: The repository and the module are go-archcheck

Date: 2026-11-27.

## Context

The GitHub repository `OpenRAE/go-rae-archcheck` is now `OpenRAE/go-archcheck`.
The module path, the schema ID and the repository files still used the old name.
Release v1.0.0 has the module path `github.com/OpenRAE/go-rae-archcheck`.

## Decision

The module path is `github.com/OpenRAE/go-archcheck`, and the tools module is `github.com/OpenRAE/go-archcheck/tools`.
The schema ID is `https://github.com/OpenRAE/go-archcheck/schema/model.schema.json`.
The model, the documentation, the configuration and the workflow comments use the new name.

These items keep the old name:

| Item | Reason |
| --- | --- |
| Decision records 0001 to 0006 | They are historical records. |
| The release entries in `CHANGELOG.md` | They are historical records. |
| The SonarCloud project key `OpenRAE_go-rae-archcheck` | The SonarCloud project keeps its key. |

The repository ID `1442018845` does not change, so the guards of the upstream jobs do not change.

## Consequences

The next release has the new module path.
A user of that release installs `github.com/OpenRAE/go-archcheck/cmd/archcheck`.
Release v1.0.0 keeps its module path, so a user of v1.0.0 keeps the old path.
Design record [0005](../../architecture/designs/0005-rename-module.md) states the change to the model.
