---
title: Decision Records Should Capture Rejected Options
---
# Decision Records Should Capture Rejected Options

A decision record that says “we chose PostgreSQL” preserves a result but not the reasoning. Six months later, a new constraint appears and nobody knows whether it was considered, rejected, or simply invisible.

## Write the pressure and the boundary

Describe the problem in terms of workload, ownership, failure tolerance, and deadlines. List the serious alternatives and the evidence that ruled each one out. A rejected option may become appropriate when scale or staffing changes, so avoid dismissive labels such as “too complicated” without context.

## Revisit without rewriting history

When a decision changes, add a superseding record and link the two. Do not edit the old conclusion until it appears timeless. The old record explains migrations, compatibility code, and operational habits that otherwise look irrational.

A map such as [[04-engineering-moc|Engineering Reliability Map]] should link to current decisions while retaining a route to their history.
