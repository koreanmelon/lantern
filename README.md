# Lantern

A local DNS and DHCP server in development. Currently only the DNS transport
layer is implemented. A server with no handler refuses valid queries. The command
currently contains a dummy handler returning `192.168.1.20` with a 60-second TTL
for every A/INET question, and `REFUSED` for other types/classes. This is for
transport testing, not real name resolution. DHCP, configurable local
records, forwarding, and EDNS are not implemented yet.

## Run

```sh
go run ./cmd/lantern -dns-listen 127.0.0.1:1053
dig @127.0.0.1 -p 1053 example.com A
dig @127.0.0.1 -p 1053 example.com A +tcp
```

The command's handler determines the answers. Ctrl-C stops both listeners and closes
active connections. Port 1053 does not require elevated privileges. Binding to a
LAN address is explicit; do not expose the service to the public internet.

## Structure

- `cmd/lantern`: process startup and shutdown; eventually wires DNS and DHCP.
- `pkg/dns`: DNS codec wrapper, handler contract, and UDP/TCP server.
- `pkg/dhcp`: will be added with DHCP; lease management will live there initially.

`dns.Server` accepts a concurrent, cancellation-aware `dns.Handler`. A nil handler
returns `REFUSED`. Handler errors become `SERVFAIL`. Malformed packets and packets
marked as responses are dropped (TCP connections are closed). Unsupported opcodes
receive `NOTIMP`; queries must contain exactly one question.

UDP responses are capped at 512 bytes, with oversized responses reduced to a
truncated reply for TCP retry. TCP supports multiple sequential queries per
connection, but not out-of-order pipelined processing. Defaults are a five-second
I/O/handler timeout and a shared limit of 128 UDP requests/TCP connections.
Handlers must honor cancellation; Go cannot forcibly terminate handler code.

## Test

```sh
go test -race ./...
go test -race -tags=integration ./...
go vet ./...
```

Integration tests bind ephemeral loopback ports; no external DNS is needed.
