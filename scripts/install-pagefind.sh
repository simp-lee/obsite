#!/bin/sh

set -eu

usage() {
	printf '%s\n' "usage: $0 [project-dir]" >&2
	exit 1
}

if [ "$#" -gt 1 ]; then
	usage
fi

project_dir=${1:-.}
pagefind_version=${PAGEFIND_VERSION:-1.5.2}
target_dir=$project_dir/tools
target_path=$target_dir/pagefind_extended
tmpdir=$(mktemp -d)

cleanup() {
	rm -rf "$tmpdir"
}

trap cleanup EXIT INT TERM HUP

mkdir -p "$target_dir"

printf '%s\n' "Installing pagefind@$pagefind_version into $target_path"

(
	cd "$tmpdir"
	npm install "pagefind@$pagefind_version" --no-save >/dev/null
)

source_path=$(find "$tmpdir/node_modules" -path '*/bin/pagefind_extended' -type f | sed -n '1p')
if [ -z "$source_path" ]; then
	printf '%s\n' "pagefind_extended was not found in the npm install tree" >&2
	exit 1
fi

cp "$source_path" "$target_path"
chmod 755 "$target_path"

if [ ! -x "$target_path" ]; then
	printf '%s\n' "installed binary is not executable: $target_path" >&2
	exit 1
fi

if ! version_output=$("$target_path" --version 2>&1); then
	printf '%s\n' "failed to run $target_path --version" >&2
	printf '%s\n' "$version_output" >&2
	exit 1
fi

case $version_output in
	*"$pagefind_version"*)
		printf '%s\n' "Installed $target_path ($version_output)"
		;;
	*)
		printf '%s\n' "installed binary version output does not include $pagefind_version: $version_output" >&2
		exit 1
		;;
esac