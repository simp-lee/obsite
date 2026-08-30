#!/bin/sh
set -eu

[ "$#" -eq 1 ] || { echo "usage: $0 PROFILE_DIR" >&2; exit 2; }
dir=$1
normal_profiles='post-tokenization during-scoring page-output build-page-output'
for name in $normal_profiles retained-owner-control; do
  file=$dir/$name.pprof
  [ -s "$file" ] || { echo "missing profile $file" >&2; exit 1; }
  go tool pprof -top -nodefraction=0 -edgefraction=0 -inuse_space "$file" >"$dir/$name.pprof.top"
done

owner_pattern='newRelatedSemanticOwner|newRelatedSemanticFixtureOwner|newDocumentTermCountOwner|newTermFieldCountOwner|newDocumentFrequencyOwner|newTermIDOwner|newSelectedFeatureOwner|newSelectedDFOwner'
contains_forbidden_owner() {
  grep -Eq "$owner_pattern" "$1"
}

for name in $normal_profiles; do
  if contains_forbidden_owner "$dir/$name.pprof.top"; then
    echo "temporary semantic/document-term owner retained in $name" >&2
    grep -E "$owner_pattern" "$dir/$name.pprof.top" >&2 || true
    exit 1
  fi
done

control=$dir/retained-owner-control.pprof.top
if ! contains_forbidden_owner "$control"; then
  echo 'retained-owner negative control was not rejected' >&2
  exit 1
fi
for symbol in newRelatedSemanticFixtureOwner newDocumentTermCountOwner newTermFieldCountOwner newDocumentFrequencyOwner newTermIDOwner newSelectedFeatureOwner newSelectedDFOwner; do
  grep -Fq "$symbol" "$control" || {
    echo "retained-owner negative control is missing $symbol" >&2
    exit 1
  }
done
printf 'related memory lifetime profiles verified with retained-owner negative control\n'
