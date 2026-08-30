# Related-article quality assets

This directory contains fixed, local-only quality evaluation assets. Production
code does not embed or publish these files.

`manifest.json` records every source article's split, language, semantic
buckets, content SHA-256, original-source/license statement, author, and an
independent review. Label-set files record calibration or holdout candidate
pools with explicit discovery provenance and manually reviewed grades `0`, `1`,
or `2`.

All articles authored for this repository use `source: original` and
`license: project-license`. They are not network-scraped. Performance fixtures
and generated token templates are not accepted as quality sources.

The calibration split may be used only for the frozen 81-tuple parameter grid.
Holdout source review is permitted before calibration, but holdout rankings,
pools, labels, and metrics remain unopened until production parameters are
frozen. Holdout grades never authorize parameter changes.
