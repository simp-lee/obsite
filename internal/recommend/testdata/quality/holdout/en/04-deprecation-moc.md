---
title: Documentation Deprecation Map
tags: [moc, documentation]
---
# Documentation Deprecation Map

This map helps a maintainer retire a public procedure without breaking the trail that existing readers follow.

## Before announcing removal

- Identify the current replacement and the cases it does not cover.
- Find inbound links, copied snippets, and runbooks that still name the old path.
- Assign an owner and a date for the final compatibility check.

## During the transition

- [[05-runbook-owner|Runbooks Need an Executable Owner]] — verify that operational instructions move with responsibility.
- Redirect old entry points to a notice that explains the replacement rather than dropping readers on a homepage.
- Keep examples for both versions only for the stated overlap window.

## After retirement

Preserve a short historical page for search and incident archaeology. Remove it from primary navigation, but retain dates and the superseding link.
