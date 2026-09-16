#!/usr/bin/env bash

# Helpers shared by run-all-checks, .ci/run-lint-checks, .ci/run-fixers,
# .ci/run-fuzz, .ci/refresh-pull-request and .ci/fuzz-pull-request. Sourced,
# not run; the caller sets REPO_DIR (the git toplevel) before sourcing. Every
# function lists files with `git ls-files` so untracked scratch files are never
# touched.

# @description Print one INFO line to stderr, prefixed with the calling
#              script's name. No timestamp: CI and terminals already stamp
#              every line.
# @arg $1 message the text to print
function ci::log() {
  printf '[%s] INFO %s\n' "${0##*/}" "$1" >&2
}

# @description Print the tracked shell scripts, one per line: *.sh, *.bash,
#              plus extensionless files whose first line is a bash shebang
#              (.ci/*, .githooks/*, run-all-checks).
# @noargs
# @stdout one repo-relative path per line
function ci::shell_files() {
  local file first_line
  while IFS= read -r file; do
    case "${file}" in
      *.sh | *.bash) printf '%s\n' "${file}" ;;
      *.go | *.md | *.json | *.yml | *.yaml | *.nix | *.toml | *.lock) ;;
      *)
        if [[ -f "${REPO_DIR}/${file}" ]] && IFS= read -r first_line < "${REPO_DIR}/${file}" \
          && [[ "${first_line}" == '#!/usr/bin/env bash' ]]; then
          printf '%s\n' "${file}"
        fi
        ;;
    esac
  done < <(git -C "${REPO_DIR}" ls-files)
}
