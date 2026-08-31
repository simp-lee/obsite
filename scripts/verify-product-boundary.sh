#!/bin/sh
set -eu

mode=${1:-all}

check_search() {
  if git grep -n -E 'pagefind|_pagefind|HasSearch|SearchConfig|data-obsite-search-ui|obsite-search-root|install-pagefind|cfg\.Search' -- \
    README.md Makefile .gitignore internal templates test scripts \
    ':(exclude)**/*_test.go' \
    ':(exclude)scripts/verify-product-boundary.sh'; then
    echo "Pagefind/search product contract remains" >&2
    exit 1
  fi
}

check_packages() {
  packages=$(go list -f '{{.ImportPath}} {{.Name}}' ./...)
  public=$(printf '%s\n' "$packages" | awk '$1 !~ /\/internal\// && $1 != "github.com/simp-lee/obsite/cmd/obsite" {print}')
  if [ -n "$public" ]; then
    printf 'public product packages remain:\n%s\n' "$public" >&2
    exit 1
  fi
}

case "$mode" in
  --search-only) check_search ;;
  --packages-only) check_packages ;;
  all) check_search; check_packages ;;
  *) echo "usage: $0 [--search-only|--packages-only]" >&2; exit 2 ;;
esac
