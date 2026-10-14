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

## Structure

- `cmd/lantern`: process startup and shutdown; eventually wires DNS and DHCP.
- `pkg/dns`: validated immutable zones and authoritative answers.
- `pkg/dns/server`: UDP/TCP transport and request processing, using
  `dnsmessage.Message.Unpack` directly (package name `dnsserver`).
- `tests/integration`: black-box socket tests, built with the `integration` tag.

## Zones

`dns.NewZone(origin, records)` accepts absolute names (with a trailing dot) and
IN-class `dnsmessage.Resource` values. Each zone requires exactly one apex SOA and
at least one apex NS. Supported record types are A, AAAA, CNAME, NS, SOA, MX, TXT,
SRV, and PTR. PTR records for reverse resolution belong in the appropriate
`in-addr.arpa.` or `ip6.arpa.` zone.

Construction validates ownership, body/type agreement, RRset TTL consistency,
CNAME exclusivity, and locally known NS/MX/SRV targets. Zone data is copied at
construction, and responses do not expose mutable storage. Queries match names
case-insensitively, preserving the original question. Identical duplicate records
should be avoided; they are not deduplicated automatically.

`dns.NewAuthoritative(zones...)` returns an authoritative answerer. The most specific zone
wins when origins overlap. Intermediate names with descendants exist even if they
have no records. Negative SOA TTLs use the lesser of the SOA TTL and its MinTTL.
CNAME chains are followed within configured zones up to 16 links; loops or longer
chains receive `SERVFAIL`. External CNAME targets are returned without resolution.

ANY, zone transfers, and non-IN questions are refused. Wildcards, delegation,
DNSSEC, dynamic updates, EDNS negotiation, and hot reload are unsupported. Unknown
ordinary query types use normal NODATA/NXDOMAIN behavior. The handler always
advertises recursion as unavailable.

### Updating zones while serving

Keep the authoritative handler installed and replace its data:

```go
updated, err := dns.NewZone("home.arpa.", updatedRecords)
if err != nil {
    return err
}
return authority.ReplaceZones(updated) // Supply every zone to retain.
```

`ReplaceZones` validates the entire replacement before publishing it atomically.
Failures leave the previous data live. Each query uses one immutable snapshot,
including all steps of a cross-zone CNAME chain; in-flight queries can finish on
the old snapshot while new queries see the replacement. Calling it without zones
removes all authority. No server restart or callback replacement is needed.

Concurrent replacements are safe, with the last publication winning. Applications
composing edits must serialize them or implement revision checks to avoid lost
updates. Do not copy an `Authoritative` after first use. There is no UI, file
watcher, or persistence layer yet. DNS caches can retain previous positive and
negative answers until their TTLs expire.

All server settings are in `dnsserver.Config`, including the handler function:

```go
authority, err := dns.NewAuthoritative(zone)
if err != nil {
    return err
}
s := dnsserver.Server{Config: dnsserver.Config{
    Address: "127.0.0.1:1053",
    Handler: authority.Handle,
}}
return s.ListenAndServe(ctx)
```

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
connection, but not out-of-order pipelined processing. Defaults are a one-second
I/O/handler timeout and a shared limit of 100 UDP requests/TCP connections.
The default address is `127.0.0.1:1053`. Startup populates zero-valued settings
in `Config` from the package's default constants.
The command currently overrides these defaults to 10 seconds and 10 respectively.
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

Integration tests are in the external `integration_test` package and use only
public APIs. They bind ephemeral loopback ports; no external DNS is needed.
Unit tests remain alongside the code they test. To run both suites together, use
`go test -race -tags=integration ./...`.
