#!/usr/bin/env bash
# Tests for deploy/partition-p5.sh. Disk tools are replaced by stubs on PATH
# that serve a recorded `sfdisk --json` table and log every other call.
set -uo pipefail
# shellcheck source=deploy/test/lib.sh
source "$(dirname "$0")/lib.sh"

SCRIPT="$DEPLOY_DIR/partition-p5.sh"
P5_START=220729344 # first 1 MiB-aligned sector after p4 (ends at 220729343)
P5_SIZE=20971520   # 10 GiB in 512-byte sectors

# setup_stubs [TABLE_JSON]: writes stub tools into $TMP/bin.
setup_stubs() {
	local table="${1:-$FIXTURES/sfdisk-router.json}"
	mkdir -p "$TMP/bin"
	cp "$table" "$TMP/table.json"
	: >"$TMP/calls.log"
	cat >"$TMP/bin/sfdisk" <<EOF
#!/usr/bin/env bash
if [[ "\$1" == --json ]]; then cat "$TMP/table.json"; exit 0; fi
echo "sfdisk \$* <<< \$(cat)" >>"$TMP/calls.log"
# Simulate the append: add p5 as described on stdin.
jq --argjson s $P5_START --argjson z $P5_SIZE \
  '.partitiontable.partitions += [{"node":"/dev/nvme0n1p5","start":\$s,"size":\$z,"name":"vyos-dash-data"}]' \
  "$TMP/table.json" >"$TMP/table.new" && mv "$TMP/table.new" "$TMP/table.json"
EOF
	cat >"$TMP/bin/blkid" <<EOF
#!/usr/bin/env bash
echo "blkid \$*" >>"$TMP/calls.log"
if [[ "\$1" == -L && -n "\${FAKE_LABEL_EXISTS:-}" ]]; then echo /dev/sda1; exit 0; fi
if [[ "\$1" == -L ]]; then exit 2; fi
# Like the real blkid -p on a GPT disk: a fresh partition still reports its
# partition-table entry and exits 0, so its exit code says nothing about data.
echo '/dev/nvme0n1p5: PART_ENTRY_SCHEME="gpt" PART_ENTRY_NAME="vyos-dash-data"'
exit 0
EOF
	cat >"$TMP/bin/wipefs" <<EOF
#!/usr/bin/env bash
echo "wipefs \$*" >>"$TMP/calls.log"
[[ "\$*" == *--no-act* ]] || { echo "WRITE: wipefs without --no-act" >>"$TMP/calls.log"; exit 1; }
if [[ -n "\${FAKE_P5_SIGNATURE:-}" ]]; then echo "/dev/nvme0n1p5 0x438 ext4 ef 53"; fi
exit 0
EOF
	for tool in mkfs.ext4 partx udevadm; do
		# shellcheck disable=SC2016 # $* and $1 belong to the stub, not to us
		printf '#!/usr/bin/env bash\necho "%s $*" >>"%s"\n' "$tool" "$TMP/calls.log" >"$TMP/bin/$tool"
	done
	# shellcheck disable=SC2016
	printf '#!/usr/bin/env bash\n[[ "$1" == -u ]] && echo "${FAKE_UID:-1000}"\n' >"$TMP/bin/id"
	chmod +x "$TMP/bin/"*
}

run_script() { PATH="$TMP/bin:$PATH" "$SCRIPT" "$@" 2>&1; }

writes() { grep -Ev '^(blkid|wipefs) ' "$TMP/calls.log" || true; }

test_dry_run_by_default_prints_plan_and_writes_nothing() {
	setup_stubs
	local out
	out="$(run_script)"
	assert_contains "$out" "DRY RUN"
	assert_contains "$out" "start=$P5_START, size=$P5_SIZE"
	assert_contains "$out" "name=vyos-dash-data"
	assert_eq "" "$(writes)" "no disk writes in dry run"
}

test_refuses_when_p5_exists() {
	local table="$TMP/with-p5.json"
	jq '.partitiontable.partitions += [{"node":"/dev/nvme0n1p5","start":230000000,"size":2048}]' \
		"$FIXTURES/sfdisk-router.json" >"$table"
	setup_stubs "$table"
	local out
	if out="$(run_script)"; then fail "expected refusal"; fi
	assert_contains "$out" "REFUSING"
	assert_contains "$out" "expected exactly 4 partitions"
}

test_refuses_when_not_enough_free_space_after_p4() {
	local table="$TMP/small.json"
	jq '.partitiontable.lastlba = 220729344 + 1000' "$FIXTURES/sfdisk-router.json" >"$table"
	setup_stubs "$table"
	local out
	if out="$(run_script)"; then fail "expected refusal"; fi
	assert_contains "$out" "not enough free space"
}

test_refuses_when_p4_is_not_vyos_containers() {
	local table="$TMP/renamed.json"
	jq '.partitiontable.partitions[3].name = "something-else"' "$FIXTURES/sfdisk-router.json" >"$table"
	setup_stubs "$table"
	local out
	if out="$(run_script)"; then fail "expected refusal"; fi
	assert_contains "$out" "vyos-containers"
}

test_refuses_when_p4_is_not_last() {
	local table="$TMP/reordered.json"
	# p3 moved behind p4: the space "after p4" is no longer the end of the disk.
	jq '.partitiontable.partitions[2].start = 300000000' "$FIXTURES/sfdisk-router.json" >"$table"
	setup_stubs "$table"
	local out
	if out="$(run_script)"; then fail "expected refusal"; fi
	assert_contains "$out" "not the last partition"
}

test_refuses_non_gpt() {
	local table="$TMP/dos.json"
	jq '.partitiontable.label = "dos"' "$FIXTURES/sfdisk-router.json" >"$table"
	setup_stubs "$table"
	local out
	if out="$(run_script)"; then fail "expected refusal"; fi
	assert_contains "$out" "not GPT"
}

test_refuses_when_label_already_exists() {
	setup_stubs
	local out
	if out="$(FAKE_LABEL_EXISTS=1 run_script)"; then fail "expected refusal"; fi
	assert_contains "$out" "already has label vyos-dash-data"
}

test_apply_requires_root() {
	setup_stubs
	local out
	if out="$(run_script --apply --yes)"; then fail "expected refusal"; fi
	assert_contains "$out" "root"
	assert_eq "" "$(writes)" "no disk writes"
}

test_apply_as_root_creates_p5_and_formats_it() {
	setup_stubs
	local out
	out="$(FAKE_UID=0 run_script --apply --yes)" || fail "apply failed: $out"
	local calls
	calls="$(writes)"
	assert_contains "$calls" "sfdisk --append --no-reread /dev/nvme0n1 <<< start=$P5_START, size=$P5_SIZE, type=0FC63DAF-8483-4772-8E79-3D69D8477DE4, name=vyos-dash-data"
	assert_contains "$calls" "partx --add --nr 5 /dev/nvme0n1"
	assert_contains "$calls" "mkfs.ext4 -L vyos-dash-data /dev/nvme0n1p5"
	# Nothing addresses p1-p4.
	assert_not_contains "$calls" "nvme0n1p1"
	assert_not_contains "$calls" "nvme0n1p4"
}

test_apply_refuses_to_format_over_an_existing_signature() {
	setup_stubs
	local out
	if out="$(FAKE_UID=0 FAKE_P5_SIGNATURE=1 run_script --apply --yes)"; then fail "expected refusal"; fi
	assert_contains "$out" "already carries a filesystem signature"
	assert_contains "$out" "p5 was created"
	assert_not_contains "$(writes)" "mkfs.ext4"
}

test_rejects_unknown_arguments() {
	setup_stubs
	local out
	if out="$(run_script --force)"; then fail "expected refusal"; fi
	assert_contains "$out" "usage"
}

run_test "dry run by default prints plan, writes nothing" test_dry_run_by_default_prints_plan_and_writes_nothing
run_test "refuses when p5 exists" test_refuses_when_p5_exists
run_test "refuses without 10 GiB free after p4" test_refuses_when_not_enough_free_space_after_p4
run_test "refuses when p4 is not vyos-containers" test_refuses_when_p4_is_not_vyos_containers
run_test "refuses when p4 is not last" test_refuses_when_p4_is_not_last
run_test "refuses a non-GPT disk" test_refuses_non_gpt
run_test "refuses when the label already exists" test_refuses_when_label_already_exists
run_test "--apply requires root" test_apply_requires_root
run_test "--apply as root creates and formats p5 only" test_apply_as_root_creates_p5_and_formats_it
run_test "--apply leaves p5 unformatted when it holds a signature" test_apply_refuses_to_format_over_an_existing_signature
run_test "rejects unknown arguments" test_rejects_unknown_arguments
finish
