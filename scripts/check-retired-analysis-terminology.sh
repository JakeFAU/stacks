#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
script_path=scripts/check-retired-analysis-terminology.sh

vertical_term=manager""confidence
command_analyze=command""analyze
employee_entity=stacks""employeeentityid
manager_entity=stacks""managerentityid
analysis_prompt=analysis""prompt
paired_term=pair""analysis
analyze_v1=analyze""v1
analysis_package=internal""analysis

matches=$(
	git -C "$repository_root" ls-files |
		while IFS= read -r path; do
			if test "$path" = "$script_path"; then
				continue
			fi
			if printf '%s\n' "$path" |
				grep -Eq '^(\.superpowers/sdd/|docs/superpowers/(specs|plans)/)'; then
				continue
			fi
			test -f "$repository_root/$path" || continue

			normalized=$(
				LC_ALL=C tr '[:upper:]' '[:lower:]' <"$repository_root/$path" |
					LC_ALL=C tr -cd '[:alnum:]'
			)
			for retired in \
				"$vertical_term" \
				"$command_analyze" \
				"$employee_entity" \
				"$manager_entity" \
				"$analysis_prompt" \
				"$paired_term" \
				"$analyze_v1" \
				"$analysis_package"; do
				if printf '%s\n' "$normalized" | grep -Fq "$retired"; then
					printf '%s: retired normalized terminology %s\n' "$path" "$retired"
					break
				fi
			done
		done
)

if test -n "$matches"; then
	printf '%s\n' "$matches" >&2
	exit 1
fi
