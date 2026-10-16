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

The example zone is defined in `cmd/lantern/zone.go`, not a configuration file:

- `home.arpa.` has SOA and NS records.
- `lantern.home.arpa.` has A address `192.168.1.2` (TTL 60 seconds).
- `pi.home.arpa.` aliases `lantern.home.arpa.`.
- Missing local names receive authoritative `NXDOMAIN`; external names receive
  `REFUSED`. Existing names without a requested type receive `NOERROR` with no
  matching answers (NODATA). Negative answers include the zone SOA.

The address is an example, not automatic interface discovery. Add or change records
in Go for now. Do not configure this as a general-purpose DNS resolver: use split
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

**Viper configures the process; it does not save DNS records.** Startup records
remain defined in Go, and UI edits still reset on restart.

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

The conffile designation lets dpkg preserve administrator edits during upgrades
(or prompt about changes). No Go runtime is required on the target. This package
targets systemd-based Debian installations and declares `systemd (>= 235)`.
There are no maintainer scripts or implicit service actions. No project license
is currently declared; choose the appropriate license before public distribution.

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

## Running under systemd

[`init/lantern.service`](init/lantern.service) is the canonical Linux system unit.
It runs as an unprivileged dynamic user, grants only `CAP_NET_BIND_SERVICE` for
port 53, uses a read-only filesystem sandbox, logs to journald, and restarts on
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

**Record persistence is not implemented yet.** Automatic service restarts restore
the startup records, not your last UI edits. This unit/configuration support does
not make those edits durable. No systemd service is installed by the application.

## Record manager

Open **http://127.0.0.1:8080** for the dashboard. The application serves DNS and
the dashboard together; Ctrl-C shuts down both.

The dashboard is implemented entirely in Go with `net/http`, `html/template`, and
Go string assets. There is no JavaScript, frontend build step, CDN, or additional
dependency. It includes a responsive record table, search/type filters, add/edit
forms, deletion, a record-format reference, and inline validation errors. All
nine supported types can be managed, including the apex SOA and NS. The full zone
is validated before each change. Concurrent edits use revisions, so stale tabs
receive a conflict instead of overwriting newer data. Changes immediately reach
the running DNS service, but client-side DNS caches still follow TTLs.

**Edits are in memory and reset on restart.** There is no persistence or import
pipeline yet. The UI displays this limitation. SOA serials are editable and are
not automatically incremented on other record changes.

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
- `pkg/dns/zone`: synchronized single-zone record storage, validation, and query handling.
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
use `NewZone` before adding or replacing records. There is no file watcher or
persistence layer yet. DNS caches can retain previous positive and
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

Integration tests are in a separate package and use only
public APIs. They bind ephemeral loopback ports; no external DNS is needed.
Unit tests remain alongside the code they test. To run both suites together, use
`go test -race -tags=integration ./...`.
