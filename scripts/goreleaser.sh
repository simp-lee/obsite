#!/bin/sh
set -eu

exec go run github.com/goreleaser/goreleaser/v2@v2.18.0 "$@"
