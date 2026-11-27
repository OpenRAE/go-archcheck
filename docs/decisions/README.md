# Decision records

Each record states one design decision of the checker, its context, the options that it did not select and its consequences.
A new decision gets the next number, and a changed decision gets a new record that replaces the earlier one.
Design records of the model of the checker live in `architecture/designs`.

| Record | Decision |
| --- | --- |
| [0001](0001-one-model-for-structure-and-checks.md) | One model states the architecture and configures the checks. |
| [0002](0002-schema-validation.md) | The JSON schema validates the structure of each model. |
| [0003](0003-custom-mechanisms.md) | Four small mechanisms stay custom, because no maintained library fits them. |
| [0004](0004-dev-merge-queue.md) | Pull requests to `dev` merge through a merge queue. |
| [0005](0005-design-number-reservations.md) | Annotated tags in GitHub reserve design numbers. |
| [0006](0006-optional-maintainer-workflows.md) | Forks and contributors need no maintainer services. |
| [0007](0007-rename-to-go-archcheck.md) | The repository and the module are `go-archcheck`. |

## Module map

The repository has one module, at the root path `.`.
All seven records apply to that module.

| Path | Implementation files | Records |
| --- | --- | --- |
| `.` | [`architecture/model.yaml`](../../architecture/model.yaml), [`Makefile`](../../Makefile), [`.github/workflows/ci.yml`](../../.github/workflows/ci.yml), [`scripts/check_config.py`](../../scripts/check_config.py), [`cmd/archcheck/reserve.go`](../../cmd/archcheck/reserve.go) | 0001, 0002, 0003, 0004, 0005, 0006, 0007 |
