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

`dnsserver.Server` accepts a function in its `Handler` field:

```go
authority, err := dns.NewAuthoritative(zone)
if err != nil {
    return err
}
s := dnsserver.Server{Handler: authority.Handle}
return s.ListenAndServe(ctx, "127.0.0.1:1053")
```

The function must be safe for concurrent calls and honor cancellation. A nil handler
returns `REFUSED`. Handler errors become `SERVFAIL`. Malformed packets and packets
marked as responses are dropped (TCP connections are closed). Unsupported opcodes
receive `NOTIMP`; queries must contain exactly one question.

UDP responses are capped at 512 bytes, with oversized responses reduced to a
truncated reply for TCP retry. TCP supports multiple sequential queries per
connection, but not out-of-order pipelined processing. Defaults are a five-second
I/O/handler timeout and a shared limit of 128 UDP requests/TCP connections.
The command currently overrides these defaults to 10 seconds and 10 respectively.
Handlers must honor cancellation; Go cannot forcibly terminate handler code.

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
