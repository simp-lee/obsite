#!/bin/sh
set -eu

[ "$#" -eq 1 ] || { echo "usage: $0 PROFILE_DIR" >&2; exit 2; }
dir=$1
for name in post-tokenization.pprof during-scoring.pprof page-output.pprof build-page-output.pprof; do
  file=$dir/$name
  [ -s "$file" ] || { echo "missing profile $file" >&2; exit 1; }
  go tool pprof -top -nodefraction=0 -edgefraction=0 -inuse_space "$file" >"$dir/$name.top"
done

for name in post-tokenization.pprof during-scoring.pprof page-output.pprof build-page-output.pprof; do
  if grep -Eq 'RelatedSemanticDocument|semanticOwner' "$dir/$name.top"; then
    echo "semantic owner retained in $name" >&2
    exit 1
  fi
done
for name in during-scoring.pprof page-output.pprof build-page-output.pprof; do
  if grep -Eq 'documentTermCounts|termFieldCounts|documentFrequency|selectedDF' "$dir/$name.top"; then
    echo "document-term/DF owner retained in $name" >&2
    exit 1
  fi
done
printf 'related memory lifetime profiles verified\n'
