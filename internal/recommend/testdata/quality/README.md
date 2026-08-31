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

`holdout/review-2026-08-31.json` records the fresh semantic re-adjudication of
81 corrected holdout pairs. Each language reviewer saw only the two article
contents and the fixed grade rubric; labels, discovery markers, production
rankings and scores, metrics, and Git history were withheld. The holdout label
metadata and manifest hash bind those reviewed grades to the frozen record.

The calibration split may be used only for the frozen 81-tuple parameter grid.
Holdout source review is permitted before calibration, but holdout rankings,
pools, labels, and metrics remain unopened until production parameters are
frozen. Holdout grades never authorize parameter changes.
