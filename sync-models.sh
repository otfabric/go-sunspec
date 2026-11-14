#!/usr/bin/env bash
#
# sync-models.sh
#
# Downloads the SunSpec JSON model definitions and their licence from the
# official sunspec/models GitHub repository into the local models/ directory.
# Everything comes from one commit: the head of the master branch when the
# script starts. The script then writes models/SHA256SUMS, the SHA-256 of
# every file it downloaded, and records the commit and the date of retrieval
# in models/README.md. `make models-verify` checks models/ against
# SHA256SUMS; CI runs it.
#
# A file is checked against the git blob hash upstream lists for it before
# anything in models/ is replaced: a run that fails leaves models/ as it was.
# A model that upstream no longer has is not deleted: the script names it,
# and `make models-verify` fails until it is removed.
#
# https://github.com/sunspec/models/tree/master/json
#
# Usage: ./sync-models.sh
#
# Set GITHUB_TOKEN to authenticate the API requests (higher rate limit).
#

set -euo pipefail

REPO="sunspec/models"
BRANCH="master"
API_URL="https://api.github.com/repos/${REPO}"
DEST_DIR="$(cd "$(dirname "$0")" && pwd)/models"

for tool in curl jq git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "sync-models: $tool is required" >&2; exit 2; }
done
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$@"; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$@"; }
else
  echo "sync-models: sha256sum or shasum is required" >&2
  exit 2
fi

auth=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
  auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

# ${auth[@]+...}: an empty array is "unbound" under `set -u` in bash 3.2 (macOS).
api() {
  curl -sf ${auth[@]+"${auth[@]}"} -H "Accept: application/vnd.github+json" "$1"
}

echo "Resolving ${REPO}@${BRANCH} ..."
head="$(api "${API_URL}/commits/${BRANCH}")" || {
  echo "sync-models: could not resolve ${REPO}@${BRANCH}" >&2
  exit 2
}
commit="$(jq -r '.sha' <<<"$head")"
commit_date="$(jq -r '.commit.committer.date' <<<"$head" | cut -c1-10)"
if ! [[ "$commit" =~ ^[0-9a-f]{40}$ ]]; then
  echo "sync-models: unexpected answer for ${REPO}@${BRANCH}" >&2
  exit 2
fi
echo "Commit ${commit} (${commit_date})"

tree="$(api "${API_URL}/git/trees/${commit}?recursive=1")" || {
  echo "sync-models: could not fetch the file list of ${commit}" >&2
  exit 2
}
if [ "$(jq -r '.truncated' <<<"$tree")" != "false" ]; then
  echo "sync-models: the upstream file list was truncated; cannot sync reliably" >&2
  exit 2
fi

# "<blob sha> <path>" for json/*.json and for the licence files at the root.
files="$(jq -r '.tree[] | select(.type == "blob")
  | select(.path | test("^json/[^/]+\\.json$") or . == "LICENSE" or . == "NOTICE")
  | "\(.sha) \(.path)"' <<<"$tree")"
if ! grep -q ' json/' <<<"$files" || ! grep -q ' LICENSE$' <<<"$files"; then
  echo "sync-models: no JSON models or no LICENSE upstream; the repository layout may have changed" >&2
  exit 2
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

RAW_BASE="https://raw.githubusercontent.com/${REPO}/${commit}"
while read -r sha path; do
  name="$(basename "$path")"
  echo "Downloading ${name} ..."
  curl -sf "${RAW_BASE}/${path}" -o "${tmp}/${name}" || {
    echo "sync-models: could not download ${path}" >&2
    exit 2
  }
  if [ "$(git hash-object "${tmp}/${name}")" != "$sha" ]; then
    echo "sync-models: ${path} is not what upstream lists for ${commit}" >&2
    exit 2
  fi
done <<<"$files"

sums="$(cd "$tmp" && LC_ALL=C ls | LC_ALL=C sort | while read -r name; do sha256 "$name"; done)"
printf '%s\n' "$sums" >"${tmp}/SHA256SUMS"

mkdir -p "$DEST_DIR"
for local_file in "$DEST_DIR"/*.json "$DEST_DIR"/NOTICE; do
  [ -e "$local_file" ] || continue
  name="$(basename "$local_file")"
  [ -e "${tmp}/${name}" ] || echo "sync-models: models/${name} is not upstream any more: remove it (make models-verify fails until then)" >&2
done
cp "$tmp"/* "$DEST_DIR"/

# The commit and the date of retrieval, in the table of models/README.md.
readme="${DEST_DIR}/README.md"
if [ -f "$readme" ]; then
  sed -i.bak -E \
    -e "s#^\\| Commit \\|.*#| Commit | \`${commit}\` (${commit_date}) |#" \
    -e "s#^\\| Retrieved \\|.*#| Retrieved | $(date -u +%Y-%m-%d) |#" "$readme"
  rm -f "${readme}.bak"
fi

echo "Done. $(grep -c ' json/' <<<"$files") JSON files of ${REPO}@${commit} saved to ${DEST_DIR}; SHA256SUMS and README.md updated."
