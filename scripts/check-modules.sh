#!/bin/sh
set -eu

manifest_modules=$(sed -e '/^[[:space:]]*#/d' -e '/^[[:space:]]*$/d' modules.txt | sort)
filesystem_modules=$(
  find . -type f -name go.mod \
    -not -path './.git/*' \
    -not -path './vendor/*' \
    -not -path './.worktrees/*' \
    -not -path './worktrees/*' |
    sed -e 's#/go.mod$##' -e 's#^\./$#.#' |
    sort
)
workspace_modules=$(go work edit -json | sed -n 's/.*"DiskPath": "\(.*\)".*/\1/p' | sort)

if test "$manifest_modules" != "$filesystem_modules"; then
  printf '%s\n%s\n' "$manifest_modules" "$filesystem_modules" | awk 'NF && !seen[$0]++'
  exit 1
fi

if test "$manifest_modules" != "$workspace_modules"; then
  printf '%s\n%s\n' "$manifest_modules" "$workspace_modules" | awk 'NF && !seen[$0]++'
  exit 1
fi
