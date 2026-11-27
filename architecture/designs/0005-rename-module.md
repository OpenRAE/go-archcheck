---
design: 5
issue: 12
title: Rename the module to go-archcheck
---

# Design 0005: Rename the module to go-archcheck

## Problem

The repository is now `OpenRAE/go-archcheck`, but the model still states the module `github.com/OpenRAE/go-rae-archcheck`.

## Design

The model states the module `github.com/OpenRAE/go-archcheck`.
The packages of the `command` and `schema` components use the new module path.
The system name is `go-archcheck`.

No layer, component, dependency or rule changes.

Decision record [0007](../../docs/decisions/0007-rename-to-go-archcheck.md) states the rename.

## Alternatives

- Keep the old module path. The module path then does not match the repository.
