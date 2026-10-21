# Lantern APT repository

The public repository is hosted at **https://koreanmelon.github.io/lantern/**.
It serves `stable/main` for Debian `amd64`, `arm64`, and `armhf` (choose the host's
OS architecture, not just its CPU). `amd64` is x86_64 Linux, including Intel and
AMD CPUs. GitHub Releases remain the authoritative package
archive. Generated indexes and package copies are deployed to Pages, never
committed to Git. There is no GitHub Packages registry.

## Configure a Linux host once

Use systemd-based Debian/Raspberry Pi OS with a supported architecture:

```sh
dpkg --print-architecture
```

Install the client tools from your existing OS repositories if necessary:

```sh
sudo apt update
sudo apt install ca-certificates curl gnupg
```

Download the public key and verify its **full primary fingerprint** before
installing it. The fingerprint below is the independently supplied maintainer
fingerprint, not a value downloaded alongside the key. Never use `apt-key`,
`trusted=yes`, or disable signature checks.

```sh
(
  set -eu
  arch="$(dpkg --print-architecture)"
  case "$arch" in amd64|arm64|armhf) ;; *) echo "Unsupported OS architecture: $arch" >&2; exit 1 ;; esac
  tmp="$(mktemp -d)"
  trap 'rm -rf "$tmp"' EXIT
  mkdir -m 0700 "$tmp/gpg"
  curl --fail --show-error --location \
    https://koreanmelon.github.io/lantern/lantern-archive-keyring.asc \
    --output "$tmp/lantern.asc"
  fingerprint="$(gpg --homedir "$tmp/gpg" --batch --show-keys --with-colons "$tmp/lantern.asc" |
    awk -F: '$1 == "fpr" {print $10; exit}')"
  test "$fingerprint" = C4B45272C3850D898F9B0A42AF494EF0B97CBBAC || {
    echo 'Repository key fingerprint mismatch; stopping.' >&2; exit 1;
  }
  sudo install -d -m 0755 /etc/apt/keyrings
  sudo install -m 0644 "$tmp/lantern.asc" /etc/apt/keyrings/lantern.asc
  sudo tee /etc/apt/sources.list.d/lantern.sources >/dev/null <<EOF
Types: deb
URIs: https://koreanmelon.github.io/lantern/
Suites: stable
Components: main
Architectures: $arch
Signed-By: /etc/apt/keyrings/lantern.asc C4B45272C3850D898F9B0A42AF494EF0B97CBBAC
EOF
)
sudo apt update
apt-cache policy lantern
```

Expect Lantern's version from the Pages repository to appear as a candidate. On
an initial install, or to upgrade an existing local `.deb` installation:

```sh
sudo apt install lantern
```

Installation/upgrades do **not** enable, start, or restart the service. Back up
`/etc/lantern/lantern.yaml` and `/var/lib/lantern/records.json` before an upgrade.
For an already running service, explicitly apply the new binary:

```sh
sudo systemctl daemon-reload
sudo systemctl restart lantern
sudo systemctl status lantern --no-pager
```

For a first installation, review the configuration and follow
[Pi/systemd verification](pi-verification.md) before enabling a production service.
APT setup does not change the router, Tailscale settings, DNS bind addresses, or
client resolver settings. The dashboard still has no authentication or TLS.

Older retained versions are available with `apt-cache madison lantern`. `amd64`
starts at `v0.1.5`; older releases remain ARM-only and are not rebuilt. All stable
releases from `v0.1.5` onward must contain all three architecture packages. An
intentional rollback requires choosing the exact version and allowing a
downgrade; newer state formats may not be backward compatible, so retain backups.

To stop using this repository, remove only its source and key files, then run
`sudo apt update`. This does not uninstall Lantern or delete record state.

## Maintainer workflow

`.github/workflows/apt.yml` runs after **release publication**, on manual dispatch,
and weekly to refresh the 30-day `Valid-Until` metadata. Drafts and prereleases
are excluded. A release published using the built-in `GITHUB_TOKEN` may not
trigger another workflow; dispatch APT publication explicitly in that case.

The workflows are displayed as **CI**, **Release**, and **APT**, matching their
filenames. The APT deploy job uses the **`apt-stable`** GitHub environment: a
deployment gate/history, not a separate host. Its selected-ref rules allow the
`main` branch (manual and scheduled refreshes) and `v*` tags (published releases).
This policy does not restrict Git pushes to `main`. The old `github-pages`
environment has been removed; the Pages artifact name `github-pages` is
independent and unchanged.

Refresh is scheduled for Saturdays at **09:00 UTC**. GitHub may delay scheduled
runs; the schedule is not an exact execution-time guarantee. Weekly
refresh leaves margin before the 30-day expiry and does not upgrade any Pi.

The complete rebuild/sign/verify/deploy sequence is serialized. Every run:

1. Uses default-branch tooling and compiles `cmd/aptrepo` once in the read-only
   generation job. Subsequent jobs download that exact binary using verified
   workflow artifacts; signing never compiles code or exposes secrets to Go's
   build tools. The command uses only Go's standard library, with no new module
   dependencies. Python is not needed for publication or normal tests.
2. Fetches every published stable release and verifies its package checksums,
   package names, Debian versions, and architecture fields without installing or
   executing them.
3. Verifies the prior Pages signature against the pinned key, checks its signed
   catalog, rejects missing/replaced packages, and preserves historical by-hash
   indexes for cached clients.
4. Generates `Packages`, `Packages.gz`, `Release`, SHA256/SHA512 by-hash objects,
   and a retention catalog with `apt-ftparchive` from Ubuntu 24.04. The runner's
   distribution tools receive security updates; their installed versions are
   recorded in the run log, rather than frozen to potentially unavailable apt
   package versions.
5. Imports the dedicated key in a temporary GnuPG home in a separate signing job,
   verifies its identity, signs metadata, and exports only the public key. The
   passphrase is sent via stdin, never process arguments. The temporary agent and
   keyring are removed, including on errors.
6. Uses secret-free, isolated APT clients to update with signature verification
   and forced by-hash acquisition, then download every retained version for all
   architectures. No packages are installed and host APT sources are untouched.
7. Deploys the complete static site, then repeats the client checks against the
   public URL. A failed post-deployment check must be investigated; it cannot
   automatically undo an already completed Pages deployment.

Only the signing step receives `APT_SIGNING_KEY` and `APT_SIGNING_PASSPHRASE`.
Only the Pages deployment job receives `pages:write` and `id-token:write`.
Builds and pull-request tests never receive the signing secrets. Repository write
access can modify workflows and must therefore be restricted to trusted people.

### Bootstrap and retry

Enable **Settings → Pages → Source: GitHub Actions**. Once the workflow is on
`main`, the first deployment requires explicit permission for the missing site:

```sh
gh workflow run apt.yml --ref main -f bootstrap=true
```

This permits only an HTTP 404 for the previous `InRelease`. It does not bypass
an existing signature, checksum, or retention mismatch. After the first deployment:

```sh
gh workflow run apt.yml --ref main
```

Do not routinely use bootstrap: a vanished repository should fail closed and be
investigated. Network failures and invalid signatures always fail. CDN caching
may briefly expose old metadata after deployment; retained by-hash objects and
packages reduce this risk, but retry `apt update` after propagation if necessary.

### Retention and capacity

Keep the published release assets. Never change package bytes under an existing
version; issue a new version instead. The next rebuild fails if an indexed
package is removed or changed. Historical index hashes are recovered from the
previous signed site; deleting the Pages deployment loses that history.

GitHub Pages has a 1 GB published-site limit. Generation stops at 900 MiB, before
deployment. Retention pruning or migration to a larger host is an explicit future
maintenance operation, not silent automatic deletion. Pages is public; do not
put private record files, keys, or startup credentials in release assets or site
content.

### Signing key maintenance

Pinned primary fingerprint:

```text
C4B45272C3850D898F9B0A42AF494EF0B97CBBAC
```

The current key expires **2028-10-18**. Keep an offline private-key backup,
passphrase backup, and revocation certificate. Renew the same key before expiry
and update its secret; a new key requires a reviewed fingerprint change and a
documented client trust transition. Replacing the key on Pages alone does not
update keys already installed on clients.

Expired or unavailable signing keys fail publication. If weekly refresh fails,
the existing metadata eventually expires and APT rejects it. Fix signing and
republish rather than disabling client expiry/signature checks.

## Tool commands and verification

`cmd/aptrepo` replaces the former Python script. Its operations retain the same
CLI roles and on-disk repository layout:

```sh
mkdir -p dist
go build -trimpath -o dist/aptrepo ./cmd/aptrepo
dist/aptrepo build --site dist/apt --repository koreanmelon/lantern \
  --url https://koreanmelon.github.io/lantern
# Only in an authorized signing environment with the two secrets set:
dist/aptrepo sign --site dist/apt
dist/aptrepo smoke --site dist/apt
dist/aptrepo smoke --site dist/apt --url https://koreanmelon.github.io/lantern
```

`build` requires a new output directory; it never overwrites an existing tree.
No command deploys Pages or installs packages. Do not use `go run` for signing
with production secrets: compile the tool before making secrets available, as
the workflow does. Runtime flags/environment cannot change the pinned key. Tests
build a separate fixture binary with a disposable fingerprint; never publish it.

Portable unit tests exercise validation, retention, signature-before-extraction
ordering, cleanup, subprocess secret filtering, limits, CLI errors, and mocked
build/sign flows:

```sh
go test -race -count=1 ./cmd/aptrepo
```

On Linux, install `apt-utils`, `dpkg`, and GnuPG before running the real-tool test:

```sh
go test -race -count=1 -tags=integration ./tests/integration \
  -run '^TestAPTRepositoryCLI$' -v
```

This test uses inert package fixtures and disposable keys. It covers ARM-only
history, amd64 introduction, all-version downloads, wrong passphrases, corrupt
signatures/catalogs/packages, unsigned OpenPGP messages, and second-generation
retention. It does not use the production signing key, install packages, or
contact GitHub/Pages. It skips on non-Linux hosts; Linux runs fail if tools are
missing, so CI cannot silently skip real APT validation.

### One-off Python-to-Go differential check

Baseline: commit `ac0c1837b969473ac9dba04b90792504728dc1a6` contains the last Python
implementation. Its Linux CI `39558570337` and published APT run `39559152621`
passed. The original Python validation suite also passed locally before removal;
Linux tests were skipped locally. A local live-signature baseline probe failed
on GnuPG agent initialization and is not counted as successful verification.

On a Linux verification host with Python available, export the historical script
to temporary storage and enable the optional differential portion of the same
test. This is the only test path requiring Python:

```sh
(
  set -eu
  baseline="$(mktemp)"
  trap 'rm -f "$baseline"' EXIT
  git show --no-ext-diff --no-textconv \
    ac0c1837b969473ac9dba04b90792504728dc1a6:scripts/apt_repository.py > "$baseline"
  LANTERN_APT_PYTHON_BASELINE="$baseline" \
    go test -race -count=1 -tags=integration ./tests/integration \
      -run '^TestAPTRepositoryCLI$' -v
)
```

Use a clone containing that commit (a depth-one CI checkout may not). The test
compares package catalogs semantically and decompressed index bytes, cross-signs
and consumes both implementations' artifacts, and builds each implementation on
the other's signed history. Compressed bytes and timestamps/signatures need not
be identical; Go and Python can produce different valid gzip streams. All prior
by-hash objects must remain available regardless.

Alternatively, manually run **CI** on `main` with the `apt_python_parity` input
enabled. This read-only, secret-free path fetches the pinned baseline explicitly;
normal push/PR/release tests do not need Python:

```sh
gh workflow run ci.yml --ref main -f apt_python_parity=true
```

**Migration acceptance:** the Linux test passed in CI run `39571762101`; the
optional differential comparison passed in `39571938772`. The full Go APT
pipeline `39572195677` subsequently passed generation, production signing,
shared-binary artifact transfer, deployment, and public client downloads for all
three architectures. These checks were initially deferred locally, then run
after explicit authorization. Repeat them when changing compatibility-sensitive
tooling; portable tests alone do not establish those properties. A passing Linux
smoke establishes index/signature/download behavior, not actual installation,
ARM execution, or systemd behavior on a client.
