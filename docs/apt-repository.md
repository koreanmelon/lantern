# Lantern APT repository

The public repository is hosted at **https://koreanmelon.github.io/lantern/**.
It serves `stable/main` for Debian `arm64` and `armhf` (choose the Pi's OS
architecture, not its CPU). GitHub Releases remain the authoritative package
archive. Generated indexes and package copies are deployed to Pages, never
committed to Git. There is no GitHub Packages registry.

## Configure a Pi once

Use Raspberry Pi OS/Debian with a supported architecture:

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
  case "$arch" in arm64|armhf) ;; *) echo "Unsupported OS architecture: $arch" >&2; exit 1 ;; esac
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

Older retained versions are available with `apt-cache madison lantern`. An
intentional rollback requires choosing the exact version and allowing a
downgrade; newer state formats may not be backward compatible, so retain backups.

To stop using this repository, remove only its source and key files, then run
`sudo apt update`. This does not uninstall Lantern or delete record state.

## Maintainer workflow

`.github/workflows/apt.yml` runs after **release publication**, on manual dispatch,
and weekly to refresh the 30-day `Valid-Until` metadata. Drafts and prereleases
are excluded. A release published using the built-in `GITHUB_TOKEN` may not
trigger another workflow; dispatch APT publication explicitly in that case.

The complete rebuild/sign/verify/deploy sequence is serialized. Every run:

1. Uses default-branch tooling, pinned to one commit for all jobs.
2. Fetches every published stable release and verifies both package checksums,
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
   and forced by-hash acquisition, then download every retained version for both
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

## Local verification

Pure validation tests work with Python's standard library. On Linux with
`apt-utils`, `dpkg`, and GnuPG, the same command also creates disposable keys and
package fixtures and exercises real isolated APT clients:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/tests -v
```

CI runs the Linux checks. They do not use the real repository signing key. A
passing Linux client smoke proves index/signature/download behavior—not actual
ARM execution, package installation, or systemd behavior on a Pi.
