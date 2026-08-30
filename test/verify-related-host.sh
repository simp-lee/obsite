#!/bin/sh
set -eu

want_go='go version go1.26.2 linux/amd64'
actual_go=$(go version)
[ "$actual_go" = "$want_go" ] || {
  printf 'Go toolchain mismatch: got %s, want %s\n' "$actual_go" "$want_go" >&2
  exit 1
}
[ "$(go env GOOS)" = linux ] || { echo 'GOOS must be linux' >&2; exit 1; }
[ "$(go env GOARCH)" = amd64 ] || { echo 'GOARCH must be amd64' >&2; exit 1; }
grep -Fq 'Intel(R) Core(TM) i5-4590 CPU @ 3.30GHz' /proc/cpuinfo || {
  echo 'CPU must be Intel Core i5-4590' >&2
  exit 1
}
mem_total=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
[ "$mem_total" = 16300020 ] || {
  printf 'MemTotal mismatch: got %s kB, want 16300020 kB\n' "$mem_total" >&2
  exit 1
}
printf 'related performance host verified\n'
