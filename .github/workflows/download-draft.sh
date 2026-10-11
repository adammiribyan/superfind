#!/bin/sh
set -eu

tag=$1
dir=$2

mkdir -p "$dir"
gh api "repos/$GITHUB_REPOSITORY/releases?per_page=100" --jq ".[] | select(.draft and .tag_name == \"$tag\") | .assets[] | \"\(.id) \(.name)\"" > "$dir.assets"
[ -s "$dir.assets" ] || { echo "found no draft release for $tag with assets" >&2; exit 1; }
while read -r id name; do
  gh api -H "Accept: application/octet-stream" "repos/$GITHUB_REPOSITORY/releases/assets/$id" > "$dir/$name"
done < "$dir.assets"
ls -l "$dir"
