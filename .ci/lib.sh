#!/usr/bin/env bash

# Helpers shared by run-all-checks and the .ci/ scripts (every script that runs
# `source .ci/lib.sh`). Sourced, not run; the caller sets REPO_DIR (the git
# toplevel) before sourcing. Any function here that enumerates files does so
# with ci::ls_files so untracked scratch files are never touched.

# @description Print one INFO line to stderr, prefixed with the calling
#              script's name. No timestamp: CI and terminals already stamp
#              every line.
# @arg $1 message the text to print
function ci::log() {
  printf '[%s] INFO %s\n' "${0##*/}" "$1" >&2
}

# @description List files the way `git ls-files` does, but NUL-delimited and
#              verbatim. By default git quotes a non-ASCII name in C style
#              ("d/\303\274.md"), which then matches no case pattern and no
#              file on disk, so the file drops out of whatever check asked
#              without a word. Read the output with `mapfile -d ''` or
#              `read -r -d ''`.
# @arg $@ options and pathspecs, passed to `git ls-files` unchanged
# @stdout one repo-relative path per NUL
# shellcheck disable=SC2120 # the arguments come from callers in other scripts
function ci::ls_files() {
  git -C "${REPO_DIR}" -c core.quotePath=false ls-files -z "$@"
}

# @description Print the tracked shell scripts: *.sh, *.bash, plus
#              extensionless files whose first line is a bash shebang
#              (.ci/*, .githooks/*, run-all-checks).
# @noargs
# @stdout one repo-relative path per NUL, as ci::ls_files prints them
function ci::shell_files() {
  local file first_line
  while IFS= read -r -d '' file; do
    case "${file}" in
      *.sh | *.bash) printf '%s\0' "${file}" ;;
      *.go | *.md | *.json | *.yml | *.yaml | *.nix | *.toml | *.lock) ;;
      *)
        if [[ -f "${REPO_DIR}/${file}" ]] && IFS= read -r first_line < "${REPO_DIR}/${file}" \
          && [[ "${first_line}" == '#!/usr/bin/env bash' ]]; then
          printf '%s\0' "${file}"
        fi
        ;;
    esac
  done < <(ci::ls_files)
}

# @description Point a branch at a commit through the API: move it there,
#              forced, when it exists, or create it there when it does not.
#              Existence is read from the single-ref endpoint, which matches
#              exactly: a branch whose name only starts with this one's
#              (chore/release-staging for chore/release) is not taken for it.
# @arg $1 repo owner/name
# @arg $2 branch
# @arg $3 sha the commit to point it at
function ci::point_branch() {
  local -r repo="$1" branch="$2" sha="$3"
  if gh api "repos/${repo}/git/ref/heads/${branch}" > '/dev/null' 2>&1; then
    ci::log "moving ${branch} to ${sha}"
    gh api --method PATCH "repos/${repo}/git/refs/heads/${branch}" \
      --field "sha=${sha}" --field 'force=true' > '/dev/null'
  else
    ci::log "creating ${branch} at ${sha}"
    gh api --method POST "repos/${repo}/git/refs" \
      --field "ref=refs/heads/${branch}" --field "sha=${sha}" > '/dev/null'
  fi
}

# @description Delete a branch through the API.
# @arg $1 repo owner/name
# @arg $2 branch
function ci::delete_branch() {
  local -r repo="$1" branch="$2"
  ci::log "deleting ${branch}"
  gh api --method DELETE "repos/${repo}/git/refs/heads/${branch}" > '/dev/null'
}
