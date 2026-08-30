---
title: Rebuilding State from an Event Log
tags: [architecture, data]
---
# Rebuilding State from an Event Log

An append-only event log is useful only if the team can explain how current state emerges from it. “We can replay everything” is not a recovery plan when ten years of events include renamed fields, retired event types, and side effects that must not happen twice.

## Separate projection from effects

A replayable projection reads immutable facts and writes a disposable view. Email, billing, and webhook delivery belong behind an explicit live-processing boundary, otherwise a recovery exercise can contact real users. Projection checkpoints need a version as well as an offset, so code changes can choose between continuing and rebuilding.

## Test with an old slice

Keep a small historical segment containing schema transitions and deleted entities. Rebuild it in CI, compare totals and selected records, and fail on unknown events. Production recovery should record the input range, code version, elapsed time, and reconciliation result.

Publishing new events safely also requires an atomic boundary with database writes. [[01-outbox|A Transactional Outbox Has a Cleanup Cost]] examines that adjacent problem.
