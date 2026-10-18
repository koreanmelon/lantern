# Lantern

A local authoritative DNS server in development. It serves records from configured
zones without forwarding or recursive resolution. DHCP is deferred.

## Run

```sh
go run ./cmd/lantern --dns-listen 127.0.0.1:1053
dig @127.0.0.1 -p 1053 lantern.home.arpa A
dig @127.0.0.1 -p 1053 pi.home.arpa A +tcp
dig @127.0.0.1 -p 1053 missing.home.arpa A
dig @127.0.0.1 -p 1053 example.com A
```

On first start, a missing record file is seeded from `cmd/lantern/zone.go`:

- `home.arpa.` has SOA and NS records.
- `lantern.home.arpa.` has A address `192.168.1.2` (TTL 60 seconds).
- `pi.home.arpa.` aliases `lantern.home.arpa.`.
- Missing local names receive authoritative `NXDOMAIN`; external names receive
  `REFUSED`. Existing names without a requested type receive `NOERROR` with no
  matching answers (NODATA). Negative answers include the zone SOA.

The address is an example, not automatic interface discovery. Existing saved
records take precedence on later starts; use the UI to change them. Do not
configure this as a general-purpose DNS resolver: use split
DNS to route only the zones it owns to Lantern, leaving public DNS to another
resolver. Advertising an NS record does not itself configure split DNS or delegation.

Ctrl-C stops both listeners and closes
active connections. Port 1053 does not require elevated privileges. Binding to a
LAN address is explicit; do not expose the service to the public internet.

## Startup configuration

Lantern uses Viper at the application boundary. The DNS server and record manager
receive typed settings, not a Viper instance. Existing flags still work:

```sh
go run ./cmd/lantern --config configs/lantern.yaml
LANTERN_DNS_ADDRESS=127.0.0.1:2053 go run ./cmd/lantern --config configs/lantern.yaml
go run ./cmd/lantern --config configs/lantern.yaml --web-listen 127.0.0.1:8081
```

Precedence is **explicit flags > environment > configuration file > defaults**.
Unchanged flag defaults do not override the file. Files are optional: without
`--config`, only defaults, environment variables, and flags are used. There is no
automatic search in the working directory or home directory. An explicitly named
missing file, malformed file, unknown key, or invalid setting fails startup before
listeners open. `--help` does not load configuration.

See [`configs/lantern.yaml`](configs/lantern.yaml) for every
setting and its default. YAML, JSON, and TOML files are supported. Durations use
Go units such as `500ms`, `10s`, or `1m`, not bare numbers. Application timeouts,
concurrency, and HTTP header limits must be positive. All startup settings have
environment equivalents: prefix the dotted key with `LANTERN_`, uppercase it,
and replace dots with underscores:

```sh
export LANTERN_DNS_READ_TIMEOUT=3s
export LANTERN_DNS_CONCURRENCY=20
export LANTERN_WEB_ALLOWED_HOSTS=pi.home.arpa,lantern.example.ts.net
```

Host entries are trusted names or IP literals, without a URL scheme, port, or
wildcard. IPv6 literals in `web.allowed_hosts` have no brackets; listen addresses
use normal `[IPv6]:port` syntax. Unknown environment variables are ignored.
Explicit empty environment values are not silently replaced by file/default
values: an empty address is invalid, while an empty allowed-host list clears it.
Configuration is loaded once at startup; restart Lantern to apply file changes.

**Viper configures the process, including the `records_file` path; record data
lives in a separate JSON file.** The default is `records.json` relative to the
process working directory, not the startup config's directory. Override it with
an absolute path or `LANTERN_RECORDS_FILE`. Do not point it at the startup YAML.

## Record persistence

Lantern loads the record file before opening listeners. A missing file is seeded
once with the example zone. An invalid, incompatible, oversized, or wrong-zone
file fails startup and is never silently replaced with defaults. The format has
`version`, `origin`, `revision`, and a list of records. Normal records are readable:

```json
{
  "name": "grafana.home.arpa.",
  "type": "A",
  "ttl": 60,
  "data": "192.168.1.20"
}
```

That is one record, not a complete file: retain the top-level metadata and required
SOA/NS records. Data is an IP/name string for address and alias records, an array
of strings for TXT, or an object for MX/SRV/SOA. For rare non-UTF-8 names or TXT
chunks, a `wire` base64 fallback preserves a single DNS record losslessly instead
of replacing binary bytes. Prefer the UI; if editing the JSON manually, stop
Lantern first, preserve its metadata, and restart to validate and load the file.
There is no file watcher. Maximum file size is 16 MiB. Schema field names are
lower-case ASCII; unknown/duplicate fields, null values, and missing structured
record fields are rejected instead of silently becoming zero or empty data.

All zone mutation methods—including UI edits, `Add`, and revision-checked
replacement—serialize competing updates and save before publishing new DNS data.
Writes use a same-directory temporary file, file sync, and atomic rename. Failure
before rename leaves the live zone and revision unchanged; a failed UI save is
reported rather than pretending it was applied. Directory sync is best-effort
after rename, the commit point; power-loss guarantees depend on the filesystem
and storage hardware. Record files use mode 0600 and new parent directories 0700.
Keep backups and use **one running process per record file**; there is no
cross-process locking. Do not modify borrowed record-body pointers externally.

Revisions survive restarts and advance at startup, so old browser forms cannot
overwrite newer data. The default local state file and temporary files are
gitignored. Embedders can keep a zone in memory by not calling
`Zone.EnablePersistence`; the application enables it automatically.

## Debian packaging

Packaging follows the [Go project-layout conventions](https://github.com/golang-standards/project-layout):

```text
configs/lantern.yaml       Default/sample startup config; installed as a conffile
init/lantern.service       Canonical systemd unit
build/package/nfpm.yaml    nFPM metadata and file mappings
dist/<architecture>/       Generated binaries (gitignored)
dist/*.deb                Generated packages (gitignored)
```

Build from the repository root using [nFPM](https://nfpm.goreleaser.com/docs/install/).
It is a build tool, not an application dependency. The manifest references the
canonical config and unit directly; no duplicate packaging copies are maintained.
For a 64-bit Pi OS:

```sh
export VERSION=0.1.0
export ARCH=arm64
export MAINTAINER='Your Name <you@example.com>' # Replace with your actual details.
mkdir -p "dist/$ARCH"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o "dist/$ARCH/lantern" ./cmd/lantern
nfpm package --config build/package/nfpm.yaml --packager deb --target dist/
```

For a 32-bit Pi 3 OS, use `ARCH=arm7` and compile with `GOOS=linux GOARCH=arm
GOARM=7 CGO_ENABLED=0` to `dist/arm7/lantern`. nFPM maps `arm7` to Debian `armhf`;
`arm64` remains `arm64`. Match the Pi's OS (`dpkg --print-architecture`), not merely
the CPU. The build architecture must agree with the manifest architecture—nFPM
does not compile or verify the input executable for you. Set all three exported
metadata variables before packaging. To use a pinned tool without installing it:

```sh
go run github.com/goreleaser/nfpm/v2/cmd/nfpm@v2.48.0 package \
  --config build/package/nfpm.yaml --packager deb --target dist/
```

The package revision is `1`, so version `0.1.0` produces
`lantern_0.1.0-1_arm64.deb` (or `_armhf.deb`). Change the package revision when
repackaging the same upstream version. Files are root-owned with explicit modes:

| Source | Installed destination |
|---|---|
| `dist/$ARCH/lantern` | `/usr/bin/lantern` (0755) |
| `configs/lantern.yaml` | `/etc/lantern/lantern.yaml` (0644, Debian conffile) |
| `init/lantern.service` | `/usr/lib/systemd/system/lantern.service` (0644) |
| `README.md` | `/usr/share/doc/lantern/README.md` (0644) |
| `LICENSE` | `/usr/share/doc/lantern/copyright` (0644) |
| `docs/pi-verification.md` | `/usr/share/doc/lantern/docs/pi-verification.md` (0644) |

The conffile designation lets dpkg preserve administrator edits during upgrades
(or prompt about changes). No Go runtime is required on the target. This package
targets systemd-based Debian installations and declares `systemd (>= 235)`.
There are no maintainer scripts or implicit service actions. Lantern uses the
[MIT license](LICENSE), included as `/usr/share/doc/lantern/copyright`.

On a Debian machine, inspect the result with `dpkg-deb --info` and
`dpkg-deb --contents` before installing. There is also an opt-in, cross-platform
archive test (no installation or target execution) that checks the package's
config/unit against their canonical sources, conffile registration, file modes,
root ownership, lack of lifecycle scripts, and static ELF architecture:

```sh
LANTERN_DEB_FILE="$PWD/dist/lantern_0.1.0-1_arm64.deb" \
  go test -tags=integration ./tests/integration -run '^TestDebianPackage$' -v
```

The normal integration suite skips that test unless `LANTERN_DEB_FILE` is set.
Building or inspecting packages does not install them or alter system services.

## GitHub Releases

`.github/workflows/ci.yml` checks formatting, module files, vet, and race-enabled
unit/integration tests on pull requests, pushes to `main`, and manual runs. The
release workflow reuses these checks for tagged source.

Before releasing, set repository **Actions variable** `PACKAGE_MAINTAINER` to
your actual `Name <email>` in Settings → Secrets and variables → Actions →
Variables. It becomes public Debian package metadata; it is not a credential.
`PACKAGE_MAINTAINER` is our chosen repository-variable name, not a predefined
GitHub/Debian environment name. The workflow maps it to `MAINTAINER`, which our
nFPM manifest expands into Debian's standard `Maintainer` field. Both variable
names are wiring conventions; the package field is what Debian uses.
No PAT is needed: only the draft-creation job receives `contents: write` through
the built-in token. Organization policy must permit that job permission.

Merge the workflows first, then tag a commit containing them (commands below
are a procedure, not something the application runs):

```sh
git tag -a v0.1.0 -m 'Lantern v0.1.0'
git push origin v0.1.0
```

Only stable `vMAJOR.MINOR.PATCH` tags are accepted, without leading zeros or
prerelease suffixes. The workflow cross-compiles ARM64/ARMv7 binaries, packages
them with pinned nFPM, checks package metadata and contents without installation,
and creates a **draft** GitHub Release with both `.deb` files and `SHA256SUMS`.
Build jobs have read-only repository permissions; only the final job can create
the draft, and it does not build or execute project code. Actions are pinned by
commit SHA. Packages are also retained as workflow artifacts for 14 days.

To build an existing tag manually (for example, if its push did not trigger a
run), first ensure the manual-trigger workflow is on `main`. Open **Actions →
Release → Run workflow**, choose **main** as the workflow branch, and enter the
existing tag, such as `v0.1.0`. Or use GitHub CLI:

```sh
gh workflow run release.yml --ref main -f tag=v0.1.0
```

The manual path validates the tag, resolves its commit, and tests and packages
that exact source—not current `main`. Both trigger paths share a per-tag release
concurrency group and refuse to release if the tag moves before draft creation.
Neither path moves tags, publishes automatically, or overwrites existing releases.

Wait for the workflow to finish, review the draft/metadata/assets, complete the
[Pi/systemd verification](docs/pi-verification.md), and publish the draft manually.
Users can download matching packages and verify `sha256sum --check SHA256SUMS`.
There is no APT repository, GitHub Packages registry, automatic service install,
or deployment step. Protect `v*` tags with repository rulesets and allow only
trusted maintainers to create them; do not move release tags.

Rerunning a workflow does not overwrite an existing draft or published release.
If an asset upload fails after creating a draft, inspect/remove only that
incomplete draft in GitHub before rerunning. Never replace a published release's
assets; use a new version tag for subsequent releases.

## Running under systemd

Before production activation, follow the isolated test-unit procedure in
[`docs/pi-verification.md`](docs/pi-verification.md). It covers permissions,
privileged ports, UI/DNS updates, restarts, failed saves, and cleanup without
using production records. These steps are not executed automatically.

[`init/lantern.service`](init/lantern.service) is the canonical Linux system unit.
It runs as an unprivileged dynamic user, grants only `CAP_NET_BIND_SERVICE` for
port 53, uses a read-only filesystem sandbox with a writable systemd-managed
`StateDirectory=lantern`, logs to journald, and restarts on
failure. `SIGTERM` is already handled by Lantern's coordinated shutdown. Keep
`TimeoutStopSec` longer than your configured `shutdown_timeout`.

When ready, copy the matching `.deb` to the Pi and run from the directory
containing it (these commands are not run automatically):

```sh
sudo apt install ./lantern_0.1.0-1_arm64.deb # Use the matching architecture/version.
# Review /etc/lantern/lantern.yaml before enabling the service.
sudo systemctl daemon-reload
sudo systemctl enable --now lantern
sudo systemctl status lantern
sudo journalctl -u lantern -f
```

For an upgrade, review configuration, run `systemctl daemon-reload`, then restart
the service deliberately to use the new executable/unit. Stop and disable the
service before package removal; after removing it, reload systemd. This minimal
package does not perform those lifecycle actions automatically. Existing units
under `/etc/systemd/system/lantern.service` override the packaged unit: inspect
`systemctl cat lantern` and migrate any older `/usr/local/bin/lantern` unit rather
than silently overwriting administrator configuration.

Build on the Pi, or cross-compile for its **OS architecture**: `GOOS=linux
GOARCH=arm64` for a 64-bit OS, or `GOOS=linux GOARCH=arm GOARM=7` for a 32-bit
Pi 3 OS. Configure the Pi's static IP in Linux separately. Verify that no existing
service owns the chosen DNS/HTTP addresses. The sample configuration intentionally
keeps DNS on loopback port 1053 and the UI on loopback port 8080; change DNS to
`192.168.1.2:53` deliberately when ready for LAN clients. The dashboard still has
no authentication: keep it loopback-only or on a trusted network. For remote
administration without exposing it, an SSH tunnel is an option:

```sh
ssh -L 8080:127.0.0.1:8080 pi@192.168.1.2
```

The unit sets `WorkingDirectory=/var/lib/lantern`, so the default relative
`records_file: records.json` lives at `/var/lib/lantern/records.json`. Systemd
creates a private state directory and makes it writable despite
`ProtectSystem=strict`; it survives service restarts. An absolute file path can
also be configured, but it must be writable inside the service sandbox. Back up
the record file and startup configuration before upgrades. Package upgrades do
not replace the runtime record file. No systemd service is installed by the application.

## Record manager

Open **http://127.0.0.1:8080** for the dashboard. The application serves DNS and
the dashboard together; Ctrl-C shuts down both.

The dashboard is implemented entirely in Go with `net/http`, `html/template`, and
Go string assets. A small, locally served vanilla-JavaScript enhancement makes
success notifications dismissible overlay toasts, without a frontend build step,
CDN, or additional dependency. With JavaScript disabled, notices remain readable
inline; validation errors always stay inline and preserve form input. Toasts do
not auto-dismiss. It includes a responsive record table, search/type filters, add/edit
forms, deletion, a record-format reference, and inline validation errors. All
nine supported types can be managed, including the apex SOA and NS. The full zone
is validated before each change. Concurrent edits use revisions, so stale tabs
receive a conflict instead of overwriting newer data. Changes immediately reach
the running DNS service, but client-side DNS caches still follow TTLs.

Edits are saved to the configured record file before they become live and survive
restarts. The UI displays whether its zone is file-backed or in-memory (for library
embedders). SOA serials are editable and are not automatically incremented on
other record changes. Browser revisions are separate from DNS SOA serials.

```sh
go run ./cmd/lantern --dns-listen 127.0.0.1:1053 --web-listen 127.0.0.1:8080
# Deliberate trusted-LAN exposure (example Pi address):
go run ./cmd/lantern --dns-listen 192.168.1.2:53 --web-listen 192.168.1.2:8080
```

There is **no authentication, authorization, or HTTPS** on the dashboard. Keep
the default loopback bind or explicitly bind an address on a trusted network;
do not expose it publicly. Cross-origin POST checks and an HTTP Host allowlist
protect against common browser attacks, but are not access controls. Access the
UI using loopback names or the explicit hostname/IP supplied to `--web-listen`.
Wildcard binds do not automatically trust arbitrary Host headers. Embedders can
pass additional trusted names to `webui.New`; forwarded headers are not trusted.

Record owner names can be short (`grafana`), absolute (`grafana.home.arpa.`), or
`@` for the apex. Target names without a trailing dot are relative to the zone.
TXT accepts literal UTF-8 text or a JSON array of strings for multiple chunks,
each at most 255 bytes. Non-UTF-8 record data can be viewed, but is not editable
through text forms; use the Go API for those records.

## Structure

- `cmd/lantern`: Cobra CLI and process startup/shutdown; eventually wires DHCP.
- `pkg/config`: typed startup configuration loaded through Viper.
- `configs/`: canonical sample/default startup configuration.
- `init/`: systemd unit configuration.
- `build/package/`: nFPM package definition.
- `pkg/dns/zone`: synchronized single-zone records, JSON persistence, validation, and queries.
- `pkg/dns/server`: UDP/TCP transport and request processing, using
  `dnsmessage.Message.Unpack` directly (package name `dnsserver`).
- `pkg/webui`: server-rendered record manager with built-in templates and CSS.
- `tests/integration`: black-box socket tests, built with the `integration` tag.

## Zones

`zone.NewZone(origin, records)` accepts absolute names (with a trailing dot) and
IN-class `dnsmessage.Resource` values. Each zone requires exactly one apex SOA and
at least one apex NS. Supported record types are A, AAAA, CNAME, NS, SOA, MX, TXT,
SRV, and PTR. PTR records for reverse resolution belong in the appropriate
`in-addr.arpa.` or `ip6.arpa.` zone.

Construction validates ownership, body/type agreement, RRset TTL consistency,
CNAME exclusivity, and NS/MX/SRV targets that alias another name in the same zone.
Relationships between different zones are not validated. Record bodies and their
nested slices are not deep-copied at construction or when answering queries.
Callers must treat supplied record data and returned records as read-only.
Use `Zone.Add` or `Zone.ReplaceRecords` for changes; these methods synchronize and
validate updates. The mutex cannot protect mutations through external record-body
references. Wire-format validation is retained during construction and updates.
Queries match names case-insensitively, preserving the original question.
Identical duplicate records should be avoided; they are not deduplicated automatically.

`Zone.Handle` directly matches the server callback. Each zone answers only for its
fixed origin; outside names receive `REFUSED`. Intermediate names with descendants
exist even if they have no records. Negative SOA TTLs use the lesser of the SOA TTL
and its MinTTL. CNAME chains are followed within this zone up to 16 links; loops
or longer chains receive `SERVFAIL`. Targets outside the zone are returned
without resolution. There is no multi-zone router or cross-zone traversal.

ANY, zone transfers, and non-IN questions are refused. Wildcards, delegation,
DNSSEC, dynamic updates, EDNS negotiation, and hot reload are unsupported. Unknown
ordinary query types use normal NODATA/NXDOMAIN behavior. The handler always
advertises recursion as unavailable.

### Updating zones while serving

Keep the zone's handler installed and replace its records:

```go
return z.ReplaceRecords(updatedRecords) // Include the zone's SOA and NS records.
```

`ReplaceRecords` builds and validates replacement indexes before acquiring a write
lock to install them. The origin stays fixed, and invalid replacements leave the
previous records intact. Queries hold a read lock throughout lookup and CNAME
traversal. `Add` holds the write lock while rebuilding and validating the resulting
zone, so concurrent additions do not lose records and failed additions leave no
partial changes. This favors straightforward validation over optimized updates.
Updates wait for active readers, and pending writers can delay new queries.
No server restart or callback replacement is needed. To remove ordinary records,
replace them with a smaller set that still contains the required SOA and NS.

Concurrent replacements are safe, with the last assignment winning. Applications
composing edits must serialize them or implement revision checks to avoid lost
updates. Do not copy a `Zone` after first use. Its zero value refuses queries;
use `NewZone` before adding or replacing records. `EnablePersistence(path)` loads
or seeds file-backed storage; the application calls it automatically before
serving. There is no file watcher. DNS caches can retain previous positive and
negative answers until their TTLs expire.

All server settings are in `dnsserver.Config`, including the handler function:

```go
z, err := zone.NewZone("home.arpa.", records)
if err != nil {
    return err
}
s := dnsserver.Server{Config: dnsserver.Config{
    Address: "127.0.0.1:1053",
    Handler: z.Handle,
}}
return s.ListenAndServe(ctx)
```

The callback receives `Request{Message, ClientIP}`. `ClientIP` is a `netip.Addr`
for the immediate UDP/TCP peer, without its port, with IPv4-mapped IPv6 addresses
unmapped. An invalid address means unavailable. It is not an authenticated
identity. Behind a proxy it identifies the proxy; forwarded-header handling and
DoH are not implemented. `Zone.Handle` accepts this request directly; currently
its record lookup does not use the client IP.

The function must be safe for concurrent calls and honor cancellation. A nil handler
returns `REFUSED`. Handler errors become `SERVFAIL`. Malformed packets and packets
marked as responses are dropped (TCP connections are closed). Unsupported opcodes
receive `NOTIMP`; queries must contain exactly one question.

The handler owns the response RCODE, AA/RA flags, and answer/authority/additional
records. The transport restores the request ID, question, opcode, and RD flag,
sets QR, and handles TCP framing and UDP truncation. Other handler flags are
preserved. Handlers must not mutate the request's question slice or change their
returned data after returning. DNS outcomes such as NXDOMAIN are returned as
messages with a nil Go error; a Go error discards the handler's message and sends
SERVFAIL instead.

UDP responses are capped at 512 bytes, with oversized responses reduced to a
truncated reply for TCP retry. TCP supports multiple sequential queries per
connection, but not out-of-order pipelined processing. The shared limit is 100
UDP requests/TCP connections by default. Timeouts are independently configurable:

- `ReadTimeout`: one complete TCP frame (prefix and payload), default one second.
  It does not impose an idle timeout on UDP reception.
- `WriteTimeout`: one TCP response write or UDP send, default one second. UDP
  sends serialize deadline-setting and writing because the socket is shared;
  time waiting for the send lock is not part of the write timeout.
- `HandlerTimeout`: cooperative handler deadline, default one second. It does
  not forcibly interrupt a handler or impose a total request-duration limit.

The default address is `127.0.0.1:1053`. Startup populates zero-valued settings
in `Config` from the package's default constants via `Normalize`, which also
rejects negative timeouts/concurrency. The application's default settings use all
three timeouts of 10 seconds and concurrency of 10; Viper configuration can
override them. Both listeners bind to the same
numeric port, including when `Address` specifies port zero.
Handlers must honor cancellation; Go cannot forcibly terminate handler code.

Each `Server` owns its concurrency limit and active workers. Only one `Serve`
call may run on an instance at a time; overlapping calls return
an error without closing their supplied listeners. Once
`Serve` returns and its workers have stopped, the instance can serve again.
Do not copy a server after first use. Set `Config` before serving;
live record changes belong in the handler's data, not the transport options.

## Test

```sh
go test -race ./...
go test -race -tags=integration ./tests/integration/...
go vet ./...
```

The integration suite includes `TestCompiledAppPersistence`: it builds a native
binary, starts it on ephemeral loopback ports with a temporary record file,
rejects an invalid TXT form, saves A/TXT records through HTTP, queries them over
UDP/TCP, stops/restarts the process with SIGTERM, and verifies persistence and
stale-form rejection. It does not use your existing record file or systemd unit.
CI runs it automatically with the rest of the integration suite. To run just this
smoke test (Go and Bash only; no curl/jq/dig dependencies):

```sh
bash scripts/smoke-test.sh
# Or directly, including race detection in the test driver:
go test -race -tags=integration ./tests/integration -run '^TestCompiledAppPersistence$' -count=1 -v
```

This is not a substitute for the Pi/systemd checks above: the smoke binary runs
natively on the test host and does not validate ARM execution or sandbox behavior.

Integration tests are in a separate package and use only
public APIs. They bind ephemeral loopback ports; no external DNS is needed.
Unit tests remain alongside the code they test. To run both suites together, use
`go test -race -tags=integration ./...`.
