#!/usr/bin/env bash
# Tests for deploy/commit-hook/dash-inventory: VyOS config JSON in, inventory.json out.
set -uo pipefail
# shellcheck source=deploy/test/lib.sh
source "$(dirname "$0")/lib.sh"

HOOK="$DEPLOY_DIR/commit-hook/dash-inventory"

hook() { "$HOOK" --config-json "$1" --out "$TMP/out/inventory.json"; }

test_writes_inventory_from_config() {
	mkdir "$TMP/out"
	hook "$FIXTURES/vyos-config.json"
	local want
	want='{"wireguard":{"interface":"wg0","listenPort":51820,"peers":[{"name":"laptop","publicKey":"FAKEpubLaptopAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","allowedIps":["10.0.0.21/32"]},{"name":"phone","publicKey":"FAKEpubPhoneAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","allowedIps":["10.0.0.22/32","fd00:10::22/128"]}]},"containers":[{"name":"airprint","addresses":[]},{"name":"unifi","addresses":["172.31.254.3","fd00:31:254::3"]},{"name":"unifi-db","addresses":["172.31.254.2"]}]}'
	assert_eq "$want" "$(jq -c . "$TMP/out/inventory.json")"
}

test_never_contains_secrets() {
	mkdir "$TMP/out"
	hook "$FIXTURES/vyos-config.json"
	local out
	out="$(cat "$TMP/out/inventory.json")"
	assert_not_contains "$out" "FAKE-NOT-A-SECRET"
	assert_not_contains "$out" "private"
	assert_not_contains "$out" "preshared"
}

test_file_is_world_readable_and_no_temp_left() {
	mkdir "$TMP/out"
	(umask 077 && hook "$FIXTURES/vyos-config.json")
	assert_eq "644" "$(stat -c %a "$TMP/out/inventory.json")" "mode"
	assert_eq "inventory.json" "$(ls -A "$TMP/out")" "directory contents"
}

test_reads_config_from_stdin() {
	mkdir "$TMP/out"
	"$HOOK" --config-json - --out "$TMP/out/inventory.json" <"$FIXTURES/vyos-config.json"
	assert_eq "51820" "$(jq .wireguard.listenPort "$TMP/out/inventory.json")"
}

test_other_wireguard_interface_by_env() {
	mkdir "$TMP/out"
	DASH_WG_INTERFACE=wg9 hook "$FIXTURES/vyos-config.json"
	assert_eq '"wg9" 51999 "other"' "$(jq -r '[.wireguard.interface, .wireguard.listenPort, .wireguard.peers[0].name] | map(tojson) | join(" ")' "$TMP/out/inventory.json")"
}

test_empty_config_gives_empty_inventory() {
	mkdir "$TMP/out"
	echo '{}' >"$TMP/cfg.json"
	hook "$TMP/cfg.json"
	assert_eq '{"wireguard":null,"containers":[]}' "$(jq -c . "$TMP/out/inventory.json")"
}

test_missing_data_dir_is_a_warning_not_an_error() {
	# The data partition may be unmounted; a post-commit hook must not make noise as a failure.
	local err
	err="$("$HOOK" --config-json "$FIXTURES/vyos-config.json" --out "$TMP/absent/inventory.json" 2>&1)"
	assert_contains "$err" "not writing"
	[[ ! -e "$TMP/absent" ]] || fail "created the missing directory"
}

test_invalid_json_keeps_previous_file() {
	mkdir "$TMP/out"
	echo '{"old":true}' >"$TMP/out/inventory.json"
	echo 'not json' >"$TMP/bad.json"
	if hook "$TMP/bad.json" 2>/dev/null; then fail "expected non-zero exit"; fi
	assert_eq '{"old":true}' "$(cat "$TMP/out/inventory.json")"
}

run_test "writes inventory from config" test_writes_inventory_from_config
run_test "never contains secrets" test_never_contains_secrets
run_test "file is 0644 and no temp file left" test_file_is_world_readable_and_no_temp_left
run_test "reads config from stdin" test_reads_config_from_stdin
run_test "DASH_WG_INTERFACE selects the interface" test_other_wireguard_interface_by_env
run_test "empty config gives empty inventory" test_empty_config_gives_empty_inventory
run_test "missing data dir is a warning" test_missing_data_dir_is_a_warning_not_an_error
run_test "invalid JSON keeps previous file" test_invalid_json_keeps_previous_file
finish
