#!/usr/bin/env bash
# Download the iptoasn.com IP-to-ASN/organisation database into the dashboard
# data directory, replacing the previous copy only if the new one is valid.
#
#   fetch-asn.sh [DATA_DIR]      # default /mnt/dash-data
#
# Source: https://iptoasn.com/ (ip2asn-combined.tsv.gz, IPv4+IPv6), licensed
# PDDL v1.0 (https://opendatacommons.org/licenses/pddl/1-0/). The dashboard
# shows this attribution in its footer and reloads the file when it changes.
#
# On the router it is installed as /config/scripts/dash/fetch-asn.sh and run
# monthly by the VyOS task scheduler (as root; see deploy/vyos.commands):
# https://docs.vyos.io/en/latest/configuration/system/task-scheduler.html
#
# Environment (mainly for tests):
#   DASH_ASN_URL       download URL (default the iptoasn.com URL below)
#   DASH_ASN_MIN_ROWS  minimum rows for a plausible file (default 100000;
#                      the real file has several hundred thousand)
set -euo pipefail

URL="${DASH_ASN_URL:-https://iptoasn.com/data/ip2asn-combined.tsv.gz}"
MIN_ROWS="${DASH_ASN_MIN_ROWS:-100000}"
DIR="${1:-/mnt/dash-data}"
NAME=ip2asn-combined.tsv.gz

log() { echo "fetch-asn: $*" >&2; }

if [[ ! -d "$DIR" ]]; then
	log "$DIR does not exist (data partition not mounted?)"
	exit 1
fi

# Temp file in the target directory, so the final mv is an atomic rename.
tmp="$(mktemp "$DIR/.$NAME.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

if ! curl --fail --silent --show-error --location --max-time 600 \
	--retry 3 --retry-delay 30 --output "$tmp" "$URL"; then
	log "download of $URL failed; keeping previous $DIR/$NAME"
	exit 1
fi

if ! gzip -t "$tmp" 2>/dev/null; then
	log "download is not a valid gzip file; keeping previous $DIR/$NAME"
	exit 1
fi

# Each row: range_start, range_end, AS number, country code, AS description.
rows="$(gzip -dc "$tmp" | awk -F'\t' 'NF == 5 && $3 ~ /^[0-9]+$/ { n++ } END { print n + 0 }')"
if ((rows < MIN_ROWS)); then
	log "download has only $rows valid rows (< $MIN_ROWS); keeping previous $DIR/$NAME"
	exit 1
fi

chmod 0644 "$tmp"
mv -f "$tmp" "$DIR/$NAME"
trap - EXIT
log "installed $DIR/$NAME ($rows rows)"
