#!/usr/bin/env bash
# Tests for deploy/fetch-asn.sh, using file:// URLs instead of iptoasn.com.
set -uo pipefail
# shellcheck source=deploy/test/lib.sh
source "$(dirname "$0")/lib.sh"

SCRIPT="$DEPLOY_DIR/fetch-asn.sh"

# make_db N: writes a gzip'd ip2asn-combined.tsv with N rows to $TMP/src.tsv.gz.
make_db() {
	local i
	for ((i = 0; i < $1; i++)); do
		printf '1.0.%d.0\t1.0.%d.255\t13335\tUS\tCLOUDFLARENET\n' "$((i % 256))" "$((i % 256))"
	done | gzip >"$TMP/src.tsv.gz"
}

fetch() { DASH_ASN_URL="file://$1" DASH_ASN_MIN_ROWS=100 "$SCRIPT" "$TMP/data" 2>&1; }

test_installs_valid_database_world_readable() {
	mkdir "$TMP/data"
	make_db 200
	(umask 077 && fetch "$TMP/src.tsv.gz")
	cmp "$TMP/src.tsv.gz" "$TMP/data/ip2asn-combined.tsv.gz"
	assert_eq "644" "$(stat -c %a "$TMP/data/ip2asn-combined.tsv.gz")"
	assert_eq "ip2asn-combined.tsv.gz" "$(ls -A "$TMP/data")" "no temp files left"
}

test_corrupt_download_keeps_previous_file() {
	mkdir "$TMP/data"
	echo previous >"$TMP/data/ip2asn-combined.tsv.gz"
	echo 'not gzip' >"$TMP/bad.gz"
	local out
	if out="$(fetch "$TMP/bad.gz")"; then fail "expected failure"; fi
	assert_eq "previous" "$(cat "$TMP/data/ip2asn-combined.tsv.gz")"
	assert_eq "ip2asn-combined.tsv.gz" "$(ls -A "$TMP/data")" "no temp files left"
}

test_truncated_database_is_rejected() {
	mkdir "$TMP/data"
	make_db 10
	local out
	if out="$(fetch "$TMP/src.tsv.gz")"; then fail "expected failure"; fi
	assert_contains "$out" "rows"
	[[ ! -e "$TMP/data/ip2asn-combined.tsv.gz" ]] || fail "installed a truncated file"
}

test_failed_download_keeps_previous_file() {
	mkdir "$TMP/data"
	echo previous >"$TMP/data/ip2asn-combined.tsv.gz"
	if fetch "$TMP/does-not-exist.gz" >/dev/null; then fail "expected failure"; fi
	assert_eq "previous" "$(cat "$TMP/data/ip2asn-combined.tsv.gz")"
}

test_missing_data_dir_fails() {
	make_db 200
	local out
	if out="$(fetch "$TMP/src.tsv.gz")"; then fail "expected failure"; fi
	assert_contains "$out" "not mounted"
}

run_test "installs a valid database, 0644, atomically" test_installs_valid_database_world_readable
run_test "corrupt download keeps previous file" test_corrupt_download_keeps_previous_file
run_test "truncated database is rejected" test_truncated_database_is_rejected
run_test "failed download keeps previous file" test_failed_download_keeps_previous_file
run_test "missing data dir fails" test_missing_data_dir_fails
finish
