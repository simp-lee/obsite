// Package asset collects render-time asset references, accepts only
// publishable vault-relative file paths, excludes scan-skipped subtrees such as
// hidden paths, .obsidian, and node_modules, builds a single basename inventory
// over accepted regular non-symlink vault files, constrains outputs to assets/,
// reuses the pass-1 collision-aware destination plan when available, restores
// plain names for unique render-only assets, and copies only regular,
// non-symlink vault files with deterministic, case-safe output paths.
package asset
