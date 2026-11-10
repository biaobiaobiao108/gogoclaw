#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

bench_output="${GOGOCLAW_BENCH_OUTPUT:-build/benchmarks/$(date -u +%Y%m%dT%H%M%SZ)-$(go env GOOS)-$(go env GOARCH).txt}"
bench_time="${GOGOCLAW_BENCH_TIME:-2s}"
bench_count="${GOGOCLAW_BENCH_COUNT:-3}"
bench_tags="${GOGOCLAW_BENCH_TAGS:-goolm,stdjson}"
mkdir -p "$(dirname "$bench_output")"

{
	printf 'GoGoClaw benchmark report\n'
	printf 'UTC time: %s\n' "$(date -u +%FT%TZ)"
	printf 'Commit: %s\n' "$(git rev-parse HEAD)"
	printf 'Go: %s\n' "$(go version)"
	printf 'Target: %s/%s\n' "$(go env GOOS)" "$(go env GOARCH)"
	printf 'CPU: %s\n' "$(uname -a)"
	printf 'Logical CPUs: %s\n' "$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf unknown)"
	printf 'GOMAXPROCS: %s\n' "${GOMAXPROCS:-default}"
	printf 'Tags: %s\n' "$bench_tags"
	printf 'Benchtime: %s\n' "$bench_time"
	printf 'Count: %s\n' "$bench_count"
	printf '\nBenchmark command: go test -run=^$ -bench=. -benchmem -benchtime=%s -count=%s -tags=%s ./pkg/agent ./pkg/memory ./pkg/seahorse ./pkg/utils\n\n' \
		"$bench_time" "$bench_count" "$bench_tags"
	go test -run='^$' -bench=. -benchmem -benchtime="$bench_time" -count="$bench_count" \
		-tags="$bench_tags" ./pkg/agent ./pkg/memory ./pkg/seahorse ./pkg/utils
} 2>&1 | tee "$bench_output"

printf '\nSaved benchmark report: %s\n' "$bench_output"
