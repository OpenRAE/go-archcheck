---
design: 4
issue: 38
title: Reserve design numbers in GitHub
---

# Design 0004: Reserve design numbers in GitHub

## Problem

Two open branches can select the same next design number.
The second branch to merge must then renumber its record and each link to it.

## Design

The model adds the external system `github`, the GitHub REST API.
The `checker` container reserves design numbers with it over HTTPS.
The command layer makes the calls with the standard library, so no layer gets a new external entry.

Decision record [0005](../../docs/decisions/0005-design-number-reservations.md) states the reservation mechanism.

## Alternatives

- Keep GitHub out of the model. The model then does not show a call that the checker makes.
- Add a layer for the GitHub client. Only the command uses the client.
