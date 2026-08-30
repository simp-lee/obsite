---
title: Engineering Reliability Map
aliases: [Reliability MOC]
tags: [moc, engineering]
---
# Engineering Reliability Map

This map starts from operational questions rather than repository boundaries. Each link names the decision it supports, so readers can choose a path without opening every note.

## State and delivery

- [[00-event-log|Rebuilding state from an event log]] — replay boundaries, schema history, and reconciliation.
- [[01-outbox|Transactional outbox cleanup]] — atomic intent, duplicate delivery, and retention.

## Capacity and bursts

- [[02-load-shape|Traffic-shaped load tests]] — arrival models, tenant skew, and stop conditions.
- [[03-cache-collapse|Cache stampede prevention]] — coordinated refresh and stale policy.

## Maintenance rule

A new article joins this map only when it changes an existing decision or introduces a distinct question. Temporary incident links stay in an inbox until they have context. Decision history is maintained through [[05-decision-records|Decision Records Should Capture Rejected Options]].
