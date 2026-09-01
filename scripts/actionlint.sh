#!/bin/sh
set -eu

exec go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.7 "$@"
