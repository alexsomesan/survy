# Operations runbook

How to install, update, roll back, check and remove survy, the dashboard, on
the VyOS router. On the router it still runs under its first name, `dash`
(container, image, scripts, paths); see `docs/architecture.md`, "Names".
Everything that changes the router is applied by the owner, by hand.
The repository never touches the router on its own, except `make load`,
`make deploy` and `make install-scripts`, which you run yourself.

Assumptions: `ssh vyos` reaches the router as an admin user with passwordless
`sudo` (override with `make ROUTER=host …`). The disk is `/dev/nvme0n1` with
p1 bios_grub, p2 ESP, p3 persistence, p4 `vyos-containers`, and about 133 GiB
free after p4.

| Piece | Source | On the router |
|---|---|---|
| Image | `Dockerfile` | `localhost/dash:<sha>` and `localhost/dash:latest` in podman |
| Config | `deploy/vyos.commands` | conntrack flow-accounting, br0 address, host name, firewall, task, container |
| Data partition | `deploy/partition-p5.sh` | `/dev/nvme0n1p5`, ext4, label `vyos-dash-data` |
| Boot mount | `deploy/preconfig-mount.sh` | `/config/scripts/dash/preconfig-mount.sh`, called from `/config/scripts/vyos-preconfig-bootup.script` |
| Inventory | `deploy/commit-hook/dash-inventory` | `/config/scripts/commit/post-hooks.d/dash-inventory` → `/mnt/dash-data/inventory.json` |
| ASN database | `deploy/fetch-asn.sh` | `/config/scripts/dash/fetch-asn.sh` → `/mnt/dash-data/ip2asn-combined.tsv.gz`, monthly |

Data files in `/mnt/dash-data` (the container's `/data`): the History
database, `inventory.json`, `aliases.json` (yours to edit),
`ip2asn-combined.tsv.gz` and, for HTTPS, `tls/fullchain.pem` and
`tls/privkey.pem` (see "HTTPS").

## First install

Do these in order. Each step is safe to stop after.

### 1. Build and test locally

```sh
make test
make image          # localhost/dash:<sha> and :latest
```

### 2. Create the data partition (p5)

```sh
scp deploy/partition-p5.sh vyos:/tmp/
ssh vyos 'sudo bash /tmp/partition-p5.sh'            # dry run: read the plan
ssh -t vyos 'sudo bash /tmp/partition-p5.sh --apply' # asks you to type "create p5"
```

The script refuses, changing nothing, unless the disk is GPT, has exactly p1–p4,
p4 is `vyos-containers` and the last partition, 10 GiB fits after p4, and no
`vyos-dash-data` label exists yet. It only appends p5, then formats p5 after
re-checking its range. Check the result with `sudo sfdisk -l /dev/nvme0n1` and
`sudo blkid -L vyos-dash-data`.

### 3. Install the scripts and mount the partition

```sh
make install-scripts
```

This copies the commit hook into `/config/scripts/commit/post-hooks.d/` and
`preconfig-mount.sh` and `fetch-asn.sh` into `/config/scripts/dash/`. Then, on
the router, hook the mount into the pre-config boot script (it runs as root at
boot, before the configuration and containers come up, and lives under
`/config`, so it survives image upgrades):

```sh
sudo tee -a /config/scripts/vyos-preconfig-bootup.script <<'EOF'

# vyos-dash: mount the dashboard data partition before containers start
/config/scripts/dash/preconfig-mount.sh
EOF
sudo /config/scripts/dash/preconfig-mount.sh   # mount it now, without a reboot
mountpoint /mnt/dash-data && findmnt /mnt/dash-data   # expect noatime
```

### 4. Fill the data partition

```sh
sudo /config/scripts/commit/post-hooks.d/dash-inventory   # writes inventory.json
sudo cat /mnt/dash-data/inventory.json                    # names, public keys, IPs; no private keys
sudo /config/scripts/dash/fetch-asn.sh                    # downloads the ASN database
```

If the hook prints `cannot read running config`, run
`/opt/vyatta/bin/vyatta-op-cmd-wrapper show configuration json` by hand. The
hook relies on that op-mode command. As a commit hook it reads the config as
the committing user, then writes as root through `sudo -n`, because VyOS runs
commit hooks without root privileges.

### 5. Load the image

```sh
make load      # docker save | ssh vyos sudo podman load
ssh vyos 'sudo podman images localhost/dash'
```

### 6. Apply the configuration

Read `deploy/vyos.commands` first. In particular, check where firewall rules
900–902 land: run `show firewall ipv4 input filter` and renumber them so they
come before any rule that would already accept or drop TCP 80. Then:

```
configure
(paste deploy/vyos.commands)
compare
commit-confirm 10
```

Within 10 minutes, check that SSH still works and the dashboard opens (see
"Verifying" below), then `confirm` and `save`. If you don't confirm, VyOS
reverts to the previous configuration.

This commit also runs the commit hook, so `inventory.json` is refreshed.

## Verifying

From a LAN machine and from a WireGuard peer:

```sh
curl -sI http://survy.home.arpa/            # 200
curl -s 'http://survy.home.arpa/api/top-talkers?window=1h' | head -c 300
curl -sN http://survy.home.arpa/api/live | head -n 5   # SSE events every second
```

On the router:

```sh
show container                                     # dash is running
sudo podman logs --tail 50 dash
# Running the image you just loaded? The two IDs must match.
sudo podman inspect dash --format '{{.Image}}'
sudo podman image inspect localhost/dash:latest --format '{{.Id}}'
# Limits and priority.
sudo podman inspect dash --format '{{.HostConfig.NanoCpus}} {{.HostConfig.Memory}} {{.HostConfig.CapAdd}}'
#   expect 100000000 (0.1 CPU), 536870912 (512 MB), [CAP_NET_ADMIN]
ps -o pid,ni,comm -C survy                         # NI 19
sudo ionice -p "$(pgrep -x survy)"                  # idle
sudo ss -ltnp | grep ':80 '                        # only 192.168.2.4:80 and 10.0.0.1:80
sysctl net.netfilter.nf_conntrack_acct             # = 1
show firewall ipv4 input filter                    # rules 900-902 (and 903-905 with HTTPS) have packet counts
```

From outside (the WAN, a mobile network), `http://<WAN address>/` must not
answer from the dashboard.

## Privileges

The container runs as host root (VyOS gives it no user namespace) with the
host's `/proc` mounted at `/host/proc`. As root, `/host/proc/1/root/…` would
reach the whole host filesystem, including `/config/config.boot`. The binary
closes this itself, independent of podman options:

1. While root, it opens everything that needs privileges: the conntrack
   netlink sockets (dump and destroy-event listener) and the :80 and :443
   listeners.
2. It chowns `/data` to `DASH_UID:DASH_GID` (default 65534) and reads the
   group of the Kea lease file (`0640`, gid 102 on the router).
3. It drops every capability except `CAP_NET_ADMIN` from its bounding set,
   sets its supplementary groups to just that group and its gid and uid to
   65534, keeps only `CAP_NET_ADMIN` as effective and permitted (on every
   thread), clears ambient capabilities and sets `PR_SET_NO_NEW_PRIVS`.
4. It checks that `/host/proc/1/root` can no longer be opened (the kernel's
   ptrace-access check refuses another user's process) and exits if it can,
   or if it is still uid 0.

The sockets opened in step 1 are never reopened. A bound listener needs
nothing further, but conntrack netlink checks `CAP_NET_ADMIN` against the
current process as well as the socket opener, which is why that one
capability is kept (ADR 0005). Without it every dump fails with "operation
not permitted". If a conntrack dump fails, the error is logged and Top
Talkers stay empty until a restart.

Capabilities needed: `net-admin` (added in `deploy/vyos.commands`), plus
podman's defaults `chown`, `setuid`, `setgid` and `net-bind-service` (ports 80
and 443), used only before the drop. `DASH_DROP_PRIVS=0` skips the drop for local
development; started as a non-root user the dashboard skips it by itself and
logs so.

Check after a deploy:

```sh
sudo podman logs dash | grep -E 'privileges|self-check'
#   privileges: dropped to uid 65534 gid 65534 groups [102]
#   self-check: /host/proc/1/root is not reachable
ps -o user,group,comm -C survy         # 65534 65534 survy
grep -E 'Cap(Eff|Prm|Bnd)|NoNewPrivs' /proc/$(pgrep -x survy)/status
#   CapPrm/CapEff/CapBnd 0000000000001000 (CAP_NET_ADMIN only), NoNewPrivs 1
```

## HTTPS

survy serves HTTPS itself, next to plain HTTP on port 80, when
`DASH_TLS_LISTEN` is set. It is off by default. It reads a certificate chain
and key from the data partition and needs no restart when they are renewed.

| Setting | Meaning |
|---|---|
| `DASH_TLS_LISTEN` | comma-separated addresses, normally `192.168.2.4:443,10.0.0.1:443`; unset means HTTPS is off |
| `DASH_TLS_CERT` | PEM certificate chain, leaf first; default `/data/tls/fullchain.pem` |
| `DASH_TLS_KEY` | PEM private key (PKCS#8 or PKCS#1 RSA or EC); default `/data/tls/privkey.pem` |

On the router the files are `/mnt/dash-data/tls/fullchain.pem` and
`/mnt/dash-data/tls/privkey.pem`.

**Who writes the files.** Whatever issues the certificate (an ACME client and a
deploy script, run as root) installs the two files and **must make them
readable by the dashboard's user**, because the dashboard runs as uid 65534
after its startup. At startup it chowns `/data` recursively, but a file
written later by root is not covered:

```sh
sudo install -d -o 65534 -g 65534 -m 0700 /mnt/dash-data/tls
sudo install -o 65534 -g 65534 -m 0644 fullchain.pem /mnt/dash-data/tls/fullchain.pem
sudo install -o 65534 -g 65534 -m 0600 privkey.pem  /mnt/dash-data/tls/privkey.pem
```

**What it does with them.**
- Checks the two files on every handshake (two `stat` calls) and parses them
  again only when their size or modification time changed. A renewal is
  served from the next connection on.
- Rejects a half-written pair, such as a new certificate whose key has not
  landed yet, and keeps serving the previous certificate. The log says so
  once: `tls: cannot load the new certificate, still serving the previous one`.
- Starts even when the files do not exist yet, so a boot-time ordering race
  with the deploy script does no harm. Handshakes fail until they appear, and
  the log says `tls: no certificate to serve yet`.
- Offers TLS 1.2 and later, with HTTP/2. The live stream (`/api/live`) works
  over it.

**Names.** The page must be opened under a name the certificate covers. A
wildcard such as `*.home.somesan.de` covers `survy.home.somesan.de`, which
needs a `system static-host-mapping` to `192.168.2.4` like `survy.home.arpa`
has. It does not cover `survy.home.arpa`, so browsers will reject that name
over HTTPS.

**Apply.** `deploy/vyos.commands` has the optional firewall rules 903–905 for
port 443 and the `DASH_TLS_LISTEN` variable. Apply them as in "Apply the
configuration" (check the rule numbers first), then restart the container
once: the listeners are opened at startup.

**Verify.**

```sh
sudo podman logs dash | grep -E 'tls:|serving'
#   tls: certificate "*.home.somesan.de" [*.home.somesan.de] valid until <date>
#   serving https on 192.168.2.4:443
sudo ss -ltnp | grep ':443 '                      # only 192.168.2.4:443 and 10.0.0.1:443
curl -sI https://survy.home.somesan.de/           # 200, certificate trusted
echo | openssl s_client -connect 192.168.2.4:443 -servername survy.home.somesan.de 2>/dev/null | openssl x509 -noout -dates -subject
```

After a renewal, the same `openssl` command shows the new dates without a
restart.

**Not covered.** There is no redirect from port 80 to port 443: both stay
up. Client certificates are not supported.

## Updating

```sh
make test && make deploy
```

`make deploy` builds the image, tags it `localhost/dash:<sha>` and `:latest`,
loads it with `sudo podman load` and runs `restart container dash`. Then
check that the running image ID matches the new `:latest` (see "Verifying").
If it still shows the old ID, the restart reused the old container: re-create it
with `sudo systemctl restart vyos-container-dash.service`, or in `configure` run
`set container name dash image 'localhost/dash:<sha>'` and `commit`.

The commit hook, boot mount and ASN fetcher change rarely. Update them with
`make install-scripts`.

Old images pile up in podman. List them with `sudo podman images localhost/dash`.
Keep the last two or three for rollback, and remove the rest with
`sudo podman rmi localhost/dash:<oldsha>`.

## Rolling back

Point `:latest` at an earlier image and restart:

```sh
ssh vyos 'sudo podman images localhost/dash'     # pick the previous <sha>
ssh vyos 'sudo podman tag localhost/dash:<sha> localhost/dash:latest && /opt/vyatta/bin/vyatta-op-cmd-wrapper restart container dash'
```

The History database stays on `/mnt/dash-data` and is not rolled back. A
configuration change is rolled back with VyOS's own `rollback` /
`commit-confirm`.

## Recovering

- **Container doesn't start after a reboot:** is `/mnt/dash-data` mounted?
  Check `mountpoint /mnt/dash-data` and `sudo journalctl -t dash-mount`.
  Without the mount, the container would write into an empty directory on the
  system partition, so fix the mount and then `restart container dash`.
- **Unknown WireGuard peers:** `sudo cat /mnt/dash-data/inventory.json`. Re-run
  the hook by hand (step 4) and look at its stderr.
- **No organisation names:** `ls -l /mnt/dash-data/ip2asn-combined.tsv.gz`. Run
  `sudo /config/scripts/dash/fetch-asn.sh`. It keeps the old file if a download
  is broken.

## Uninstalling

```
configure
delete container name dash
delete system task-scheduler task dash-fetch-asn
delete firewall ipv4 input filter rule 900
delete firewall ipv4 input filter rule 901
delete firewall ipv4 input filter rule 902
delete firewall ipv4 input filter rule 903   # only if HTTPS rules were applied
delete firewall ipv4 input filter rule 904
delete firewall ipv4 input filter rule 905
delete firewall group address-group DASH-ADDRS
delete system static-host-mapping host-name survy.home.arpa
delete interfaces bridge br0 address 192.168.2.4/24
delete system conntrack flow-accounting   # only if nothing else needs it
commit-confirm 10
confirm
save
```

Then on the router:

```sh
sudo rm /config/scripts/commit/post-hooks.d/dash-inventory
sudo rm -r /config/scripts/dash
# remove the vyos-dash lines from /config/scripts/vyos-preconfig-bootup.script
sudo podman rmi $(sudo podman images -q localhost/dash)
sudo umount /mnt/dash-data
```

To remove the data partition too, which deletes all History:
`sudo sfdisk --delete /dev/nvme0n1 5`, then `sudo partx -d --nr 5 /dev/nvme0n1`.
Check with `sudo sfdisk -l /dev/nvme0n1` first that partition 5 is
`vyos-dash-data`.
