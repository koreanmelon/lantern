# Raspberry Pi / systemd verification

Run these checks **on the Pi before production activation**. CI archive checks do
not prove capabilities, filesystem sandbox behavior, or SD-card durability. Use
the separate unit and state directory below—not production records. These checks
do not configure your router, DHCP, or clients' DNS settings.

## 1. Inspect and install

```sh
dpkg --print-architecture
systemd --version
sha256sum --check SHA256SUMS
dpkg-deb --info ./lantern_0.1.0-1_arm64.deb
dpkg-deb --contents ./lantern_0.1.0-1_arm64.deb
sudo apt install ./lantern_0.1.0-1_arm64.deb
sudo ss -lntup
systemctl cat lantern
```

Substitute the actual version and `armhf` for a 32-bit OS. Put the packages and
checksum file in the same directory. If only one architecture was downloaded,
use `sha256sum --check --ignore-missing SHA256SUMS` and confirm the selected
package is explicitly reported `OK`. Installation does not activate the service.
An existing `/etc/systemd/system/lantern.service` overrides the packaged unit;
inspect it rather than overwriting administrator configuration blindly.

Check `/etc/lantern/lantern.yaml`. The test uses unused loopback ports **1053**
and **18080**; change the override and queries if needed. Do not stop an existing
resolver merely to free a port.

## 2. Isolate the verification service

Copy the installed canonical unit to exercise its actual hardening. The empty
`StateDirectory=` line clears the production setting before adding the test
directory. Guards prevent overwriting a previous test setup. No unit is enabled
for boot by these commands.

```sh
(
  set -eu
  for path in /etc/systemd/system/lantern-verify.service \
      /etc/systemd/system/lantern-verify.service.d \
      /var/lib/lantern-verify /var/lib/private/lantern-verify; do
    if sudo test -e "$path" || sudo test -L "$path"; then
      printf 'Verification path exists: %s. Inspect it or choose another name.\n' "$path" >&2
      exit 1
    fi
  done
  sudo install -m 0644 /usr/lib/systemd/system/lantern.service /etc/systemd/system/lantern-verify.service
  sudo install -d -m 0755 /etc/systemd/system/lantern-verify.service.d
  sudo tee /etc/systemd/system/lantern-verify.service.d/override.conf >/dev/null <<'EOF'
[Service]
StateDirectory=
StateDirectory=lantern-verify
WorkingDirectory=/var/lib/lantern-verify
Environment=LANTERN_RECORDS_FILE=/var/lib/lantern-verify/records.json
Environment=LANTERN_DNS_ADDRESS=127.0.0.1:1053
Environment=LANTERN_WEB_ADDRESS=127.0.0.1:18080
EOF
  sudo systemctl daemon-reload
  sudo systemctl start lantern-verify
)
```

Inspect status, logs, sandbox settings, and permissions:

```sh
sudo systemctl status lantern-verify --no-pager
sudo journalctl -u lantern-verify -n 60 --no-pager
sudo systemctl show lantern-verify \
  -p DynamicUser -p StateDirectory -p WorkingDirectory \
  -p ProtectSystem -p AmbientCapabilities -p CapabilityBoundingSet
sudo stat -Lc '%a %U:%G %n' /var/lib/lantern-verify /var/lib/lantern-verify/records.json
```

Expect an active service, `DynamicUser=yes`, verification state/working directory,
`ProtectSystem=strict`, and `CAP_NET_BIND_SERVICE`. Expect mode **700** on the
directory and **600** on the JSON file. Owner names can appear as numeric IDs or
`nobody` with some systemd/id-mapped configurations; do not manually chown
production state. A successful save demonstrates writable state in the sandbox.

From your laptop, tunnel instead of exposing the unauthenticated dashboard:

```sh
ssh -L 18080:127.0.0.1:18080 pi@192.168.1.2
```

Open `http://127.0.0.1:18080` and confirm it reports file-backed storage.

## 3. Exercise edits and restarts

1. Add `verify` / A / TTL 60 / `192.0.2.77` (a documentation address).
2. Edit it to `192.0.2.78`.
3. Add a TXT record with `["hello", "line\u0000break"]`.
4. Try `["hello", null]`: it must fail without changing the record.
5. Leave an edit form open in a second tab.

On the Pi (`dig` is in Debian's `dnsutils` package if needed):

```sh
dig @127.0.0.1 -p 1053 verify.home.arpa A +norecurse
dig @127.0.0.1 -p 1053 verify.home.arpa A +tcp +norecurse
sudo systemctl restart lantern-verify
dig @127.0.0.1 -p 1053 verify.home.arpa A +norecurse
```

Expect authoritative (`aa`) `192.0.2.78` answers before and after restart. TXT
bytes should survive too. Submit the old pre-restart form: expect a conflict, not
an overwrite. Delete test records and confirm that change survives another
restart. Removing the sole SOA or final apex NS must be rejected.

**Privileged-port check:** only if `127.0.0.1:53` is free, change the verification
override's DNS address to that value, reload systemd, restart the verification
unit, and query without `-p 1053`. It should bind as a dynamic user, not root.
Do not disable a working resolver for this test. An `address already in use`
error is not a capability error; inspect the journal.

## 4. Verify failed-save behavior

Use **only the verification directory** to temporarily deny directory writes:

```sh
sudo chmod 0500 /var/lib/lantern-verify
```

Attempt an edit. Expect an error, unchanged DNS answers, and no revision advance.
Immediately restore permissions, then retry and confirm success:

```sh
sudo chmod 0700 /var/lib/lantern-verify
```

Restore permissions before restarting. Making only the JSON file read-only is
insufficient: atomic writes replace it through its directory. Do not unplug the
Pi during writes to test power-loss behavior.

## 5. Stop and clean up the isolated unit

```sh
sudo systemctl stop lantern-verify
sudo ss -lntup
sudo rm /etc/systemd/system/lantern-verify.service.d/override.conf
sudo rmdir /etc/systemd/system/lantern-verify.service.d
sudo rm /etc/systemd/system/lantern-verify.service
sudo systemctl daemon-reload
```

Confirm the test process released its sockets. These commands remove only the
unit files created above; keep `/var/lib/lantern-verify` until its test data is
reviewed/backed up, and remove it separately only if desired. Do not remove the
package while a production service uses it. Never delete `/var/lib/lantern`
as part of these tests.

After verification, back up production records/configuration, review bind
addresses, and deliberately enable/start `lantern.service`. Production DNS can
use `192.168.1.2:53` once the static IP is configured and that address is free.
