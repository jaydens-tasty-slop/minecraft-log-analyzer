#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"

if ! command -v gobco >/dev/null 2>&1; then
	printf '%s\n' 'gobco is required for branch coverage.' 'Install it with: go install github.com/rillig/gobco@v1.3.4' >&2
	exit 1
fi
if [ "$(gobco -version)" != "1.3.4" ]; then
	printf '%s\n' 'scripts/coverage.sh requires gobco v1.3.4.' 'Install it with: go install github.com/rillig/gobco@v1.3.4' >&2
	exit 1
fi

mcla_tmp_dir=$(mktemp -d)
trap 'rm -rf "$mcla_tmp_dir"' EXIT HUP INT TERM

go test ./... -coverprofile="$mcla_tmp_dir/coverage.out"
mcla_line_coverage=$(go tool cover -func="$mcla_tmp_dir/coverage.out" | awk '$1 == "total:" {gsub("%", "", $3); print $3}')
if [ -z "$mcla_line_coverage" ]; then
	printf '%s\n' 'could not read total coverage from go tool cover' >&2
	exit 1
fi

go list -f '{{.Dir}}' ./... > "$mcla_tmp_dir/packages"
mcla_branch_covered=0
mcla_branch_total=0
while IFS= read -r mcla_package; do
	mcla_output=$(gobco -branch "$mcla_package" 2>&1)
	mcla_ratio=$(printf '%s\n' "$mcla_output" | awk '$1 == "Branch" && $2 == "coverage:" {print $3}')
	if [ -z "$mcla_ratio" ]; then
		printf '%s\n' "$mcla_output" >&2
		printf 'could not read branch coverage for %s\n' "$mcla_package" >&2
		exit 1
	fi
	mcla_covered=${mcla_ratio%/*}
	mcla_total=${mcla_ratio#*/}
	mcla_branch_covered=$((mcla_branch_covered + mcla_covered))
	mcla_branch_total=$((mcla_branch_total + mcla_total))
	mcla_package_label=${mcla_package#"$repo_dir"}
	mcla_package_label=${mcla_package_label#/}
	if [ -z "$mcla_package_label" ]; then
		mcla_package_label=.
	fi
	printf 'branch coverage %-42s %s\n' "$mcla_package_label" "$mcla_ratio"
done < "$mcla_tmp_dir/packages"

mcla_branch_percent=$(awk -v covered="$mcla_branch_covered" -v total="$mcla_branch_total" 'BEGIN {printf "%.1f", 100 * covered / total}')
printf 'Go statement coverage: %s%%\n' "$mcla_line_coverage"
printf 'Branch coverage: %s/%s = %s%%\n' "$mcla_branch_covered" "$mcla_branch_total" "$mcla_branch_percent"

if ! awk -v coverage="$mcla_line_coverage" 'BEGIN {exit !(coverage >= 80)}'; then
	printf '%s\n' 'Go statement coverage is below 80%.' >&2
	exit 1
fi
if [ $((mcla_branch_covered * 100)) -lt $((mcla_branch_total * 80)) ]; then
	printf '%s\n' 'Branch coverage is below 80%.' >&2
	exit 1
fi
