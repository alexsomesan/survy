#!/usr/bin/env bash
# Create the dashboard data partition: GPT partition 5, 10 GiB, ext4, label
# "vyos-dash-data", in the free space directly after p4 ("vyos-containers").
#
# Run ON THE ROUTER. Dry run by default: it inspects the disk, prints exactly
# what it would do, and writes nothing. Pass --apply (as root) to act.
#
#   sudo ./partition-p5.sh            # dry run: checks + plan
#   sudo ./partition-p5.sh --apply    # create p5 and format it (asks first)
#
# It refuses (and changes nothing) unless ALL of these hold:
#   - the disk has a GPT partition table
#   - it has exactly 4 partitions, numbered 1-4 (so p5 is absent)
#   - p4 is named "vyos-containers" and is the last partition on the disk
#   - the 1 MiB-aligned range after p4 is free and large enough
#   - no filesystem labelled "vyos-dash-data" exists anywhere yet
# It only ever appends p5 (`sfdisk --append`); it never modifies p1-p4.
# Before formatting it re-reads the table and checks p5 is exactly the planned
# range, and that p5 carries no existing filesystem signature.
# T_* variables are assigned by `eval "$(read_table)"`.
# shellcheck disable=SC2154
set -euo pipefail

DISK=/dev/nvme0n1
SIZE_GIB=10
LABEL=vyos-dash-data
P4_NAME=vyos-containers
LINUX_FS_GUID=0FC63DAF-8483-4772-8E79-3D69D8477DE4 # "Linux filesystem data"
ALIGN=2048                                         # sectors (1 MiB at 512 B)
APPLY=false
ASSUME_YES=false

usage() {
	cat >&2 <<EOF
usage: $0 [--apply [--yes]] [--disk DEV] [--size-gib N]
  default: dry run against $DISK, ${SIZE_GIB} GiB
EOF
	exit 2
}

refuse() {
	echo "REFUSING: $*" >&2
	echo "Nothing was changed." >&2
	exit 1
}

# refuse_after_create: like refuse, once p5 exists in the partition table.
refuse_after_create() {
	echo "REFUSING: $*; NOT formatting" >&2
	echo "p5 was created but left unformatted. Inspect with 'sudo wipefs --no-act $P5_NODE'." >&2
	exit 1
}

while [[ $# -gt 0 ]]; do
	case "$1" in
	--apply) APPLY=true ;;
	--yes) ASSUME_YES=true ;;
	--disk) DISK="${2:?}" && shift ;;
	--size-gib) SIZE_GIB="${2:?}" && shift ;;
	*) usage ;;
	esac
	shift
done
[[ "$SIZE_GIB" =~ ^[1-9][0-9]*$ ]] || usage

if [[ "$DISK" =~ [0-9]$ ]]; then P5_NODE="${DISK}p5"; else P5_NODE="${DISK}5"; fi

# read_table: prints shell assignments describing the table, from sfdisk --json.
read_table() {
	sfdisk --json "$DISK" | python3 -c '
import json, re, sys, shlex
t = json.load(sys.stdin)["partitiontable"]
parts = t.get("partitions", [])
def num(p):
    m = re.search(r"(\d+)$", p["node"])
    return int(m.group(1)) if m else -1
nums = sorted(num(p) for p in parts)
p4 = next((p for p in parts if num(p) == 4), None)
last = max(parts, key=lambda p: p["start"] + p["size"]) if parts else None
out = {
  "T_LABEL": t.get("label", ""),
  "T_SECTOR": t.get("sectorsize", 512),
  "T_LASTLBA": t.get("lastlba", 0),
  "T_COUNT": len(parts),
  "T_NUMS": " ".join(map(str, nums)),
  "T_P4_NAME": (p4 or {}).get("name", ""),
  "T_P4_END": (p4["start"] + p4["size"] - 1) if p4 else 0,
  "T_P4_LAST": "yes" if p4 is not None and last is p4 else "no",
  "T_P5_START": next((p["start"] for p in parts if num(p) == 5), ""),
  "T_P5_SIZE": next((p["size"] for p in parts if num(p) == 5), ""),
}
for k, v in out.items():
    print(f"{k}={shlex.quote(str(v))}")
'
}

eval "$(read_table)"

[[ "$T_LABEL" == gpt ]] || refuse "$DISK partition table is '$T_LABEL', not GPT"
[[ "$T_COUNT" -eq 4 && "$T_NUMS" == "1 2 3 4" ]] ||
	refuse "expected exactly 4 partitions (1 2 3 4) on $DISK, found: $T_NUMS"
[[ "$T_P4_NAME" == "$P4_NAME" ]] ||
	refuse "p4 is named '$T_P4_NAME', expected '$P4_NAME'; is this the right disk?"
[[ "$T_P4_LAST" == yes ]] || refuse "p4 is not the last partition on $DISK"
[[ "$T_SECTOR" -eq 512 ]] || refuse "sector size $T_SECTOR is not 512; recompute sizes by hand"

START=$(((T_P4_END + 1 + ALIGN - 1) / ALIGN * ALIGN))
SIZE=$((SIZE_GIB * 1024 * 1024 * 1024 / T_SECTOR))
END=$((START + SIZE - 1))
((END <= T_LASTLBA)) ||
	refuse "not enough free space after p4: need sectors $START-$END, disk ends at $T_LASTLBA"

if existing="$(blkid -L "$LABEL")"; then
	refuse "$existing already has label $LABEL; the data partition seems to exist already"
fi

SPEC="start=$START, size=$SIZE, type=$LINUX_FS_GUID, name=$LABEL"
FREE_GIB=$(((T_LASTLBA - T_P4_END) * T_SECTOR / 1024 / 1024 / 1024))

cat <<EOF
Disk:        $DISK (GPT, p1-p4 present, p4 '$P4_NAME' ends at sector $T_P4_END)
Free after:  ~${FREE_GIB} GiB (up to sector $T_LASTLBA)
New p5:      $P5_NODE  sectors $START-$END (${SIZE_GIB} GiB), label $LABEL, ext4
Commands:
  echo '$SPEC' | sfdisk --append --no-reread $DISK
  partx --add --nr 5 $DISK
  mkfs.ext4 -L $LABEL $P5_NODE
EOF

if [[ "$APPLY" != true ]]; then
	echo
	echo "DRY RUN: nothing was changed. Re-run with --apply (as root) to do this."
	exit 0
fi

[[ "$(id -u)" -eq 0 ]] || refuse "--apply must run as root (use sudo)"

if [[ "$ASSUME_YES" != true ]]; then
	read -r -p "Type 'create p5' to continue: " answer </dev/tty
	[[ "$answer" == "create p5" ]] || refuse "not confirmed"
fi

# --no-reread: p3/p4 are mounted, so the kernel cannot re-read the whole table;
# partx then tells the kernel about the one new partition only.
echo "$SPEC" | sfdisk --append --no-reread "$DISK"
partx --add --nr 5 "$DISK"
udevadm settle

# Re-read and confirm p5 is exactly what was planned before formatting anything.
eval "$(read_table)"
[[ "$T_P5_START" == "$START" && "$T_P5_SIZE" == "$SIZE" ]] ||
	refuse_after_create "after sfdisk, p5 is start=$T_P5_START size=$T_P5_SIZE, not the planned range. Inspect with 'sudo sfdisk -l $DISK'"

# Any filesystem, RAID or other signature means the range held data, so leave
# it alone. wipefs --no-act only lists signatures. (blkid -p is no use here: on
# GPT it reports the new partition's table entry and exits 0 even when empty.)
signatures="$(wipefs --no-act --noheadings "$P5_NODE")" ||
	refuse_after_create "could not check $P5_NODE for signatures"
[[ -z "$signatures" ]] ||
	refuse_after_create "$P5_NODE already carries a filesystem signature ($signatures)"

mkfs.ext4 -L "$LABEL" "$P5_NODE"

echo
echo "Done: $P5_NODE created and formatted with label $LABEL."
echo "Next: install the pre-config mount snippet (see docs/operations.md)."
