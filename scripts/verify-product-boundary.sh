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

  imports=$(go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./...)
  cmd_product_imports=$(printf '%s\n' "$imports" | awk -F'|' '$1 == "github.com/simp-lee/obsite/cmd/obsite" {print $2}' | tr ' ' '\n' | grep '^github.com/simp-lee/obsite/' || true)
  if [ "$cmd_product_imports" != "github.com/simp-lee/obsite/internal/cli" ]; then
    printf 'cmd/obsite product imports must contain only internal/cli, got:\n%s\n' "$cmd_product_imports" >&2
    exit 1
  fi

  invalid_cli=$(printf '%s\n' "$imports" | awk -F'|' '$1 != "github.com/simp-lee/obsite/cmd/obsite" && $2 ~ /github.com\/simp-lee\/obsite\/internal\/cli/ {print $1}')
  if [ -n "$invalid_cli" ]; then
    printf 'packages outside cmd/obsite import internal/cli:\n%s\n' "$invalid_cli" >&2
    exit 1
  fi

  invalid_build=$(printf '%s\n' "$imports" | awk -F'|' '$1 != "github.com/simp-lee/obsite/internal/build" && $1 != "github.com/simp-lee/obsite/internal/cli" && $2 ~ /github.com\/simp-lee\/obsite\/internal\/build/ {print $1}')
  if [ -n "$invalid_build" ]; then
    printf 'lower packages import internal/build:\n%s\n' "$invalid_build" >&2
    exit 1
  fi
}

case "$mode" in
  --search-only) check_search ;;
  --packages-only) check_packages ;;
  all) check_search; check_packages ;;
  *) echo "usage: $0 [--search-only|--packages-only]" >&2; exit 2 ;;
esac
