# shellcheck shell=bash
# Tiny test helpers shared by deploy/test/*.test.sh. Sourced, not executed.

TESTS_RUN=0
TESTS_FAILED=0
FIXTURES="$(cd "$(dirname "${BASH_SOURCE[0]}")/fixtures" && pwd)"
DEPLOY_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail() {
	printf '    FAIL: %s\n' "$*" >&2
	exit 1
}

assert_eq() { # expected actual [message]
	[[ "$1" == "$2" ]] || fail "${3:-values differ}"$'\n'"      expected: $1"$'\n'"      actual:   $2"
}

assert_contains() { # haystack needle
	[[ "$1" == *"$2"* ]] || fail "expected output to contain: $2"$'\n'"      output: $1"
}

assert_not_contains() { # haystack needle
	[[ "$1" != *"$2"* ]] || fail "expected output NOT to contain: $2"
}

# run_test NAME FUNCTION: runs FUNCTION in a subshell with a fresh $TMP.
# Any failing command or assertion fails the test (fail exits the subshell).
run_test() {
	local name="$1" fn="$2" rc
	TESTS_RUN=$((TESTS_RUN + 1))
	# Not inside an `if`: bash ignores set -e in conditional contexts.
	(
		set -e
		TMP="$(mktemp -d)"
		trap 'rm -rf "$TMP"' EXIT
		"$fn"
	)
	rc=$?
	if [[ "$rc" -eq 0 ]]; then
		printf 'ok   %s\n' "$name"
	else
		TESTS_FAILED=$((TESTS_FAILED + 1))
		printf 'FAIL %s\n' "$name"
	fi
}

finish() {
	printf '%d run, %d failed\n' "$TESTS_RUN" "$TESTS_FAILED"
	[[ "$TESTS_FAILED" -eq 0 ]]
}
