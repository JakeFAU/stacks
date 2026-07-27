#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
dry_run=$(
	make -C "$repository_root" -n test-integration ENV_FILE=.env.example
)
retired_package="./internal/""analysis"

case "$dry_run" in
	*"go test "*"./internal/query"*) ;;
	*)
		echo "test-integration does not execute ./internal/query" >&2
		exit 1
		;;
esac

case "$dry_run" in
	*"go test "*"$retired_package"*)
		echo "test-integration still executes the retired analysis package" >&2
		exit 1
		;;
	*)
		;;
esac
