# Lantern

Lantern is a local, **authoritative-only DNS server** and Go-rendered web record
manager, intended for a Raspberry Pi. It serves one zone; it is not a recursive
resolver and does not forward unknown names. DHCP is deferred.

Read `README.md`, `go.mod`, and the relevant implementation/tests before changes.
Prefer focused, readable code over speculative abstractions or scalability work.

## Layout and boundaries

- `cmd/lantern`: Cobra command, application wiring, coordinated startup/shutdown,
  and the example zone used to seed missing record storage.
- `pkg/config`: typed startup configuration, defaults, validation, Viper loading.
- `pkg/dns/server`: package name **`dnsserver`**; UDP/TCP listeners, framing,
  deadlines, concurrency limits, message decoding/encoding, callback dispatch.
- `pkg/dns/zone`: one zone's records, validation, synchronization, query handling,
  revision checks, and record-file persistence.
- `pkg/webui`: `net/http` record manager; HTML templates and CSS are Go constants.
- `configs/lantern.yaml`: canonical sample/default startup configuration.
- `init/lantern.service`: canonical systemd unit.
- `build/package/nfpm.yaml`: packaging metadata; references canonical files.
- `tests/integration`: socket/end-to-end/package tests, tagged `integration`.
- `.github/workflows`: CI and tag-triggered draft GitHub Releases.
- `dist/`: generated binaries/packages, gitignored; never commit them.

Use `pkg/` for these packages; the configuration package was deliberately moved
out of `internal/`. Do not recreate a separate `ZoneHandler` or `hosted` package.

## Design decisions to preserve

- Use `golang.org/x/net/dns/dnsmessage` directly. Do not add a message `Unpack`
  wrapper or duplicate its resource types.
- Standard library and `golang.org/x/*` are preferred. Cobra and Viper are
  explicitly accepted dependencies. Ask before adding unrelated libraries.
  nFPM/actionlint are build tools, not application dependencies.
- The server callback is a plain function, not a custom interface:

  ```go
  func(context.Context, dnsserver.Request) (dnsmessage.Message, error)
  ```

- `Request` contains `Message` and `ClientIP netip.Addr`. No client port. Use the
  immediate peer and unmap IPv4-mapped IPv6. Do not trust forwarded headers.
- `Zone.Handle` matches the callback directly: `Handler: z.Handle`.
- `Server.Config` owns all startup settings. Runtime bookkeeping stays on Server;
  do not reintroduce a separate runtime/state struct.
- Keep `ListenAndServe(ctx)` and `Serve(ctx, udp, tcp)`. Both transports must use
  the same numeric port, including port zero. An overlapping serving call returns
  a **plain error**, without taking ownership of its supplied listeners.
- Keep the shared buffered-channel semaphore (`slots`). Its `chan struct{}` was
  explicitly accepted; do not replace it with a worker pool without a requirement.
- Read, write, and cooperative handler timeouts are independently configured.
  UDP deadline-setting and sends must remain serialized on the shared socket.
- `Zone` uses an `RWMutex`, not immutable-snapshot/atomic-pointer machinery.
  Hold a read lock across the full lookup/CNAME chain. Avoid recursively taking
  public read locks inside an already locked lookup: pending writers can deadlock it.
- Records use `map[RecordKey][]dnsmessage.Resource`; existing names use
  `map[string]bool`, including empty intermediate names. Do not restore nested maps
  or `map[string]struct{}` for that name set.
- **No defensive deep copies of record bodies.** Supplied and returned bodies are
  borrowed and read-only. The mutex cannot protect external pointer mutations.
  Response header values may be separate; persistence serialization is necessary
  encoding, not an excuse to restore `copyMessage` on the query path.
- `Add` and whole-zone `ReplaceRecords` validate before publishing. Invalid edits
  must not leave partial indexes, names, SOA metadata, or revisions behind.
- Only local zone relationships are validated. No cross-zone validation/router.

## DNS behavior

- Exactly one SOA and at least one NS at the apex. No non-apex SOA/NS delegation.
- Supported records: A, AAAA, CNAME, MX, NS, PTR, SOA, SRV, TXT.
- Case-insensitive ASCII name matching; preserve arbitrary supported record bytes.
- Outside-zone names: REFUSED, not NXDOMAIN.
- Missing in-zone name: NXDOMAIN. Existing name without requested type: NODATA.
  Include SOA for negative answers, TTL = min(SOA TTL, SOA minimum).
- Local CNAME chains have a 16-link limit and loop detection. Return external
  aliases without resolving targets. No recursion; RA remains false for Zone.
- No wildcards, DNSSEC, EDNS negotiation, zone transfers, DoH, or DNS UPDATE yet.
  Classic UDP response limit is 512 bytes; oversized responses truncate for TCP retry.
- Ordinary DNS negative results are responses with nil Go errors. Handler Go
  errors become SERVFAIL. Transport owns the response envelope/framing/truncation.

## Configuration, storage, and UI

- Fresh Viper instance per load; explicit `--config` path only, no automatic
  directory discovery or file watching. Flags > `LANTERN_*` env > file > defaults.
  Unknown file keys and invalid settings fail startup. Pass typed settings to
  services, not Viper instances. `--help` must not load files or start services.
- Development defaults: DNS `127.0.0.1:1053`, web `127.0.0.1:8080`, application DNS
  timeouts 10s/concurrency 10, shutdown 5s. Lower-level transport defaults differ;
  consult its Config rather than silently changing either layer's defaults.
- Record persistence is separate from startup YAML. `records_file` defaults to
  `records.json`, relative to the **process working directory**. Override with
  `LANTERN_RECORDS_FILE`; never use an actual user's state file for smoke tests.
- Application startup calls `Zone.EnablePersistence` before opening listeners.
  Load existing valid state or seed a missing file once. Invalid files must fail
  closed without being replaced by example records.
- File format: versioned JSON with origin, revision, and typed readable records;
  canonical DNS wire/base64 fallback preserves non-UTF-8 record data.
  Reject unknown/duplicate keys, null coercions, missing required structured
  fields, wrong origins/versions, trailing data, and files over 16 MiB.
- All mutations funnel through the zone's locked commit path. Save a same-directory
  temporary file (0600), sync/close, rename, then publish memory/revision. Failures
  before rename must leave live data unchanged. Directory sync after rename is
  best-effort; do not claim unconditional power-loss durability.
- Persist/advance revisions so stale browser forms cannot overwrite newer data,
  including across restarts. One process per state file; no cross-process locking.
- Never stage private record files or crash-left temporary record files.
- Frontend stays Go-only: server-rendered forms/templates and CSS, no JS build step
  or CDN. No authentication/authorization/TLS currently. Preserve Host allowlisting,
  cross-origin POST protection, HTML escaping, body limits, and revision checks.
  Default to loopback; do not expose unauthenticated writes publicly.
- TXT edit formatting must survive HTML/form serialization; NUL/newlines need
  lossless JSON escaping. Refuse destructive text editing of non-UTF-8 records.

## Packaging and releases

- Packaged paths: `/usr/bin/lantern`, `/etc/lantern/lantern.yaml` (conffile),
  `/usr/lib/systemd/system/lantern.service`. Do not duplicate source assets.
- systemd uses DynamicUser, CAP_NET_BIND_SERVICE, StateDirectory `lantern`, and
  WorkingDirectory `/var/lib/lantern`. Keep its state writable within the sandbox.
- nFPM v2.48.0: export VERSION, ARCH, MAINTAINER; build static Linux binaries in
  `dist/arm64/` or `dist/arm7/` first. `arm7` maps to Debian `armhf`.
- No package hooks automatically enable/start/restart/remove service state.
- The project uses the canonical root `LICENSE` (MIT); the Debian package installs
  it as `/usr/share/doc/lantern/copyright`. Keep packaged documentation/license
  contents synchronized with source files.
- GitHub Releases retain the authoritative packages; published stable releases
  also feed a signed APT repository on this repository's GitHub Pages site.
  No GitHub Packages registry. Generated packages/indexes never enter Git.
  `scripts/apt_repository.py` uses Python's standard library and Ubuntu APT/GPG
  tooling (build tools, not application dependencies). Preserve checksum/metadata
  validation, signed retention checks, historical by-hash indexes, fingerprint
  pinning, and separate generation/signing/deployment jobs. Only the signing step
  receives APT secrets; only the deployment job gets Pages write/OIDC permissions.
  See `docs/apt-repository.md` for bootstrap, refresh, retention, and key rotation.
  Stable `vMAJOR.MINOR.PATCH` tag pushes build verified packages and SHA256SUMS,
  then create a **draft** for manual review/publication.
- Repository variable `PACKAGE_MAINTAINER` must contain real public package
  metadata. Only the draft job gets contents:write; never give PR/build jobs a
  release token. Pin action SHAs and tool versions. Never overwrite published assets.
- Do not commit, tag, push, publish, install packages, or start system services
  unless explicitly requested. Commit style is short Conventional Commits, e.g.
  `feat: configuration and Debian packaging`, `chore: move config to pkg`.

## Verification

```sh
go test -race ./...
go test -race -shuffle=on -count=1 -tags=integration ./tests/integration/...
go vet -tags=integration ./...
git diff --check
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/ci.yml .github/workflows/release.yml .github/workflows/apt.yml
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/tests -v
```

Keep pure unit tests alongside code and real-socket tests in `tests/integration`.
Use temporary state paths, ephemeral loopback ports, and bounded deadlines.
`TestCompiledAppPersistence` builds a native binary and automates HTTP edits,
UDP/TCP queries, SIGTERM/restart, JSON reload, and stale-form rejection. CI runs it
with the integration suite. A local shortcut is `bash scripts/smoke-test.sh`.
It must never use a real record file; its child environment filters `LANTERN_*`
and explicitly selects temporary state. Do not replace this with production
service restarts. Manual Pi/systemd checks live in `docs/pi-verification.md` and
use an isolated verification unit/state directory.
Rebuild packages after canonical payload changes (including README/unit/config),
then inspect without installing:

```sh
LANTERN_DEB_FILE="$PWD/dist/lantern_0.1.0-1_arm64.deb" \
  go test -tags=integration ./tests/integration -run '^TestDebianPackage$' -v
```

Repeat for armhf. Do not claim Linux/systemd or GitHub Actions execution was
verified merely because cross-compilation, archive inspection, or actionlint passed.

## Current verification handoff

- Lantern releases through `v0.1.2` are published. The user reports running the
  installed ARM64 binary under systemd on the Pi and querying it over Tailscale.
- Signed APT hosting is live at `https://koreanmelon.github.io/lantern/`.
  Bootstrap run `39209488997` and normal refresh `39209683374` both passed,
  including real isolated APT updates/downloads for both architectures against
  the public URL. Existing stable release versions are retained.
- Linux CI `39209269798` passed the repository tests, including disposable signing
  keys, wrong-key/passphrase failures, unsigned-message rejection, tampering,
  and second-generation retention. Review found no remaining actionable issue.
- Local race tests, integration tests, vet, actionlint, and both rebuilt package
  inspections passed. Local `0.0.0` packages use verification-only metadata and
  are ignored under `dist/`; they must not be distributed.
- `APT_SIGNING_KEY` and `APT_SIGNING_PASSPHRASE` exist as repository secrets.
  Never read/export them locally or place them in artifacts. The pinned public
  fingerprint and rotation procedure are documented in `docs/apt-repository.md`.
- **Still manual:** configure APT trust on the user's Pi using that guide and
  verify an actual install/upgrade. Cloud APT client downloads do not establish
  Pi package installation, systemd sandbox behavior, or storage durability.
- No new application release was needed for APT hosting. Preserve concurrent
  edits; future commits, pushes, releases, deployments, and Pi service actions
  still require the user's authorization.
