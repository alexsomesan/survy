#!/bin/sh
# Mount the dashboard data partition (label vyos-dash-data) at /mnt/dash-data.
#
# Install on the router as /config/scripts/dash/preconfig-mount.sh (mode 0755)
# and call it from /config/scripts/vyos-preconfig-bootup.script by appending:
#
#     # vyos-dash: mount the dashboard data partition before containers start
#     /config/scripts/dash/preconfig-mount.sh
#
# VyOS runs vyos-preconfig-bootup.script as root at boot, before the system
# configuration (and so the containers) is applied:
# https://docs.vyos.io/en/latest/automation/command-scripting.html
# Both files live under /config, which is carried over on image upgrades, so
# the mount survives `add system image`.
#
# It never fails the boot: if the partition is missing it logs and returns 0
# (the dash container will then fail to start; see docs/operations.md).
# POSIX sh, so it does not depend on bash being available that early.

LABEL=vyos-dash-data
MNT=/mnt/dash-data

log() { logger -t dash-mount "$*" 2>/dev/null || echo "dash-mount: $*" >&2; }

if mountpoint -q "$MNT"; then
	exit 0
fi

DEV="$(blkid -L "$LABEL" 2>/dev/null)"
if [ -z "$DEV" ]; then
	log "no filesystem labelled $LABEL; $MNT not mounted"
	exit 0
fi

mkdir -p "$MNT"
# Preen-mode check: fixes only what is safe to fix unattended.
e2fsck -p "$DEV" >/dev/null 2>&1
if mount -t ext4 -o noatime,nodev,nosuid,noexec "$DEV" "$MNT"; then
	log "mounted $DEV at $MNT"
else
	log "mounting $DEV at $MNT failed"
fi
exit 0
