#!/usr/bin/env bash
#
# check-models.sh
#
# Compares the SunSpec JSON model definitions in models/ with the official
# sunspec/models GitHub repository (master branch) and reports models that
# are new, changed or removed upstream. Nothing in models/ is modified:
# files are compared by their git blob hash, which the GitHub API lists for
# every upstream file. Run ./sync-models.sh and `make generate` to update.
#
# With --diff, the new and changed models are fetched (to a temporary
# directory) and compared with the local copies: for every changed model the
# script prints the points that differ, followed by a unified diff of the JSON,
# and for every new model a short description.
#
# https://github.com/sunspec/models/tree/master/json
#
# Usage: ./check-models.sh [--diff | --diff-file FILE]
#
#   --diff            print the differences after the summary
#   --diff-file FILE  write the differences to FILE instead of standard output
#
# Exit status: 0 = in sync, 1 = upstream differs, 2 = the check itself failed.
# Set GITHUB_TOKEN to authenticate the API request (higher rate limit).
#

set -euo pipefail

show_diff=false
diff_file=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --diff) show_diff=true ;;
    --diff-file)
      [ "$#" -ge 2 ] || { echo "check-models: --diff-file needs a file name" >&2; exit 2; }
      show_diff=true
      diff_file="$2"
      shift
      ;;
    -h | --help)
      sed -n '2,/^$/s/^# \{0,1\}//p' "$0"
      exit 0
      ;;
    *)
      echo "check-models: unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

REPO="sunspec/models"
BRANCH="master"
API_URL="https://api.github.com/repos/${REPO}/git/trees/${BRANCH}?recursive=1"
RAW_BASE="https://raw.githubusercontent.com/${REPO}/${BRANCH}/json"
DEST_DIR="$(cd "$(dirname "$0")" && pwd)/models"

for tool in curl jq git diff; do
  command -v "$tool" >/dev/null 2>&1 || { echo "check-models: $tool is required" >&2; exit 2; }
done

auth=()
if [ -n "${GITHUB_TOKEN:-}" ]; then
  auth=(-H "Authorization: Bearer ${GITHUB_TOKEN}")
fi

# ${auth[@]+...}: an empty array is "unbound" under `set -u` in bash 3.2 (macOS).
tree="$(curl -sf ${auth[@]+"${auth[@]}"} -H "Accept: application/vnd.github+json" "$API_URL")" || {
  echo "check-models: could not fetch the file list from ${REPO}@${BRANCH}" >&2
  exit 2
}
if [ "$(jq -r '.truncated' <<<"$tree")" != "false" ]; then
  echo "check-models: the upstream file list was truncated; cannot compare reliably" >&2
  exit 2
fi

# "<blob sha> <file name>" for every upstream json/*.json, sorted by file name.
upstream="$(jq -r '.tree[] | select(.type == "blob") | select(.path | test("^json/[^/]+\\.json$")) | "\(.sha) \(.path | sub("^json/"; ""))"' <<<"$tree" | sort -k2)"
if [ -z "$upstream" ]; then
  echo "check-models: no JSON models found upstream; the repository layout may have changed" >&2
  exit 2
fi

new=() changed=() removed=()

while read -r sha name; do
  local_file="${DEST_DIR}/${name}"
  if [ ! -f "$local_file" ]; then
    new+=("$name")
  elif [ "$(git hash-object "$local_file")" != "$sha" ]; then
    changed+=("$name")
  fi
done <<<"$upstream"

for local_file in "$DEST_DIR"/*.json; do
  name="$(basename "$local_file")"
  if ! grep -q " ${name}\$" <<<"$upstream"; then
    removed+=("$name")
  fi
done

echo "Compared models/ with ${REPO}@${BRANCH} ($(wc -l <<<"$upstream" | tr -d ' ') upstream files)."

if [ "${#new[@]}" -eq 0 ] && [ "${#changed[@]}" -eq 0 ] && [ "${#removed[@]}" -eq 0 ]; then
  echo "In sync: no new, changed or removed models."
  exit 0
fi

report() {
  local title="$1"
  shift
  [ "$#" -gt 0 ] || return 0
  echo
  echo "${title} ($#):"
  printf '  %s\n' "$@"
}

report "New upstream" "${new[@]+"${new[@]}"}"
report "Changed upstream" "${changed[@]+"${changed[@]}"}"
report "Removed upstream" "${removed[@]+"${removed[@]}"}"
echo
echo "Run ./sync-models.sh and 'make generate' to update."

if [ "$show_diff" = true ]; then
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT

  fetch() {
    curl -sf "$RAW_BASE/$1" -o "$tmp/$1" || {
      echo "check-models: could not download $1 from ${REPO}@${BRANCH}" >&2
      exit 2
    }
  }

  # One line per point: what the registry is generated from. A model's points
  # live in its top-level group and, for repeating blocks, in nested groups.
  points() {
    jq -r '
      def fields: "type=\(.type) size=\(.size) sf=\(.sf // "-") units=\(.units // "-") " +
        "mandatory=\(.mandatory // "-") access=\(.access // "-") symbols=\((.symbols // []) | length)";
      def walk(prefix): ((.points // [])[] | "\(prefix)\(.name): \(fields)"),
        ((.groups // [])[] | walk("\(prefix)\(.name)/"));
      if (.group | type) == "object" then .group | walk("") else empty end' "$1" 2>/dev/null || true
  }

  describe() {
    jq -r '"id \(.id // "?"), \(.group.label // .group.name // "no label"), " +
      "\([.group | .. | objects | select(has("points")) | .points[]] | length) points"' "$1" 2>/dev/null ||
      echo "not a model definition"
  }

  write_diff() {
    local name
    for name in ${changed[@]+"${changed[@]}"}; do
      fetch "$name"
      echo "=== ${name} (changed): $(describe "$tmp/$name")"
      echo
      echo "Points:"
      points "${DEST_DIR}/${name}" >"$tmp/points.local"
      points "$tmp/$name" >"$tmp/points.upstream"
      if diff "$tmp/points.local" "$tmp/points.upstream" >"$tmp/points.diff"; then
        echo "  no point changed name, type, size, scale factor, units, mandatory flag, access or symbol count"
      else
        grep '^[<>]' "$tmp/points.diff" | sed -e 's/^< /-/' -e 's/^> /+/'
      fi
      echo
      echo "JSON:"
      diff -u --label "a/models/${name}" --label "b/${REPO}/json/${name}" "${DEST_DIR}/${name}" "$tmp/$name" || true
      echo
    done
    for name in ${new[@]+"${new[@]}"}; do
      fetch "$name"
      echo "=== ${name} (new): $(describe "$tmp/$name")"
      echo
    done
    for name in ${removed[@]+"${removed[@]}"}; do
      echo "=== ${name} (removed upstream): $(describe "${DEST_DIR}/${name}")"
      echo
    done
  }

  if [ -n "$diff_file" ]; then
    write_diff >"$diff_file"
    echo "Differences written to ${diff_file}."
  else
    echo
    write_diff
  fi
fi

exit 1
