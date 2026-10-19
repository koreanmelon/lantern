#!/usr/bin/env python3
"""Build/sign/test Lantern's static APT repository. Python standard library only."""

import argparse
import contextlib
import datetime
import functools
import gzip
import hashlib
import http.server
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile
import threading
import urllib.error
import urllib.request


FINGERPRINT = "C4B45272C3850D898F9B0A42AF494EF0B97CBBAC"
ARCHITECTURES = ("arm64", "armhf")
TAG = re.compile(r"v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\Z")
POOL = re.compile(r"pool/main/l/lantern/lantern_[0-9]+\.[0-9]+\.[0-9]+-[1-9][0-9]*_(arm64|armhf)\.deb\Z")
BY_HASH = re.compile(r"dists/stable/main/binary-(arm64|armhf)/by-hash/(SHA256/[0-9a-f]{64}|SHA512/[0-9a-f]{128})\Z")


def run(*args, cwd=None, data=None, env=None):
    return subprocess.run(args, cwd=cwd, input=data, env=env, check=True,
                          stdout=subprocess.PIPE, timeout=300).stdout


def digest(data):
    return hashlib.sha256(data).hexdigest()


def fetch(url, limit=16 * 1024 * 1024):
    with urllib.request.urlopen(url, timeout=60) as response:
        data = response.read(limit + 1)
    if len(data) > limit:
        raise ValueError(f"Download exceeds size limit: {url}")
    return data


def write(path, data):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)


def fingerprint(home, secret=False):
    output = run("gpg", "--homedir", str(home), "--batch", "--with-colons",
                 "--list-secret-keys" if secret else "--list-keys").decode()
    primaries = []
    primary = False
    for line in output.splitlines():
        fields = line.split(":")
        if fields[0] in ("pub", "sec"):
            if fields[1] in ("r", "e", "d"):
                raise ValueError("Signing key is revoked, expired, or disabled")
            primary = True
        elif fields[0] == "fpr" and primary:
            primaries.append(fields[9])
            primary = False
    if primaries != [FINGERPRINT]:
        raise ValueError("Keyring must contain exactly the pinned signing key")


@contextlib.contextmanager
def keyring(key):
    with tempfile.TemporaryDirectory(prefix="lantern-apt-gpg-") as directory:
        home = Path(directory)
        home.chmod(0o700)
        try:
            run("gpg", "--homedir", str(home), "--batch", "--import", data=key)
            fingerprint(home)
            yield home
        finally:
            subprocess.run(["gpgconf", "--homedir", str(home), "--kill", "gpg-agent"],
                           check=False, timeout=30)


def release_hashes(release):
    """Read SHA256 entries from a verified Release, never an unverified catalog."""
    entries = {}
    active = False
    for line in release.decode().splitlines():
        if line == "SHA256:":
            active = True
        elif not line.startswith(" "):
            active = False
        elif active:
            sha, size, name = line.split()
            if not re.fullmatch(r"[0-9a-f]{64}", sha) or name in entries:
                raise ValueError("Invalid Release SHA256 table")
            entries[name] = (sha, int(size))
    return entries


def verified_release(home, signed):
    # Decryption alone also accepts unsigned OpenPGP literal-data messages.
    # Require a valid signature from the single pinned primary key first.
    run("gpg", "--homedir", str(home), "--batch", "--verify", data=signed)
    return run("gpg", "--homedir", str(home), "--batch", "--decrypt", data=signed)


def previous_repository(url, bootstrap, site):
    try:
        signed = fetch(url + "/dists/stable/InRelease")
    except urllib.error.HTTPError as error:
        if error.code == 404 and bootstrap:
            return {"packages": {}, "indexes": {}}
        raise
    public = fetch(url + "/lantern-archive-keyring.asc")
    with keyring(public) as home:
        release = verified_release(home, signed)
    catalog = fetch(url + "/dists/stable/catalog.json")
    expected = release_hashes(release).get("catalog.json")
    if expected != (digest(catalog), len(catalog)):
        raise ValueError("Previous catalog does not match signed metadata")
    previous = json.loads(catalog)
    validate_catalog(previous)
    # Preserve old index objects for clients with cached older Release metadata.
    for name, sha in previous["indexes"].items():
        data = fetch(url + "/" + name)
        verify_index(name, data, sha)
        write(site / name, data)
    return previous


def validate_catalog(catalog):
    if set(catalog) != {"packages", "indexes"} or not isinstance(catalog["packages"], dict) or not isinstance(catalog["indexes"], dict):
        raise ValueError("Invalid repository catalog")
    for name, entry in catalog["packages"].items():
        if not POOL.fullmatch(name) or not isinstance(entry, dict) or set(entry) != {"sha256", "size", "version", "architecture"}:
            raise ValueError("Invalid package catalog entry")
        if not re.fullmatch(r"[0-9a-f]{64}", entry["sha256"]) or type(entry["size"]) is not int or entry["size"] <= 0:
            raise ValueError("Invalid package checksum or size")
        if name != f"pool/main/l/lantern/lantern_{entry['version']}_{entry['architecture']}.deb":
            raise ValueError("Package catalog metadata mismatch")
    for name, sha in catalog["indexes"].items():
        if not BY_HASH.fullmatch(name) or not re.fullmatch(r"[0-9a-f]{64}", sha):
            raise ValueError("Invalid by-hash catalog entry")


def verify_index(name, data, sha):
    algorithm, expected = name.split("/")[-2:]
    if digest(data) != sha or hashlib.new(algorithm.lower(), data).hexdigest() != expected:
        raise ValueError("Historical by-hash index was modified")


def check_retention(previous, current):
    for name, entry in previous["packages"].items():
        if current["packages"].get(name) != entry:
            raise ValueError(f"Previously published package missing or replaced: {name}")


def checksums(data, names):
    result = {}
    for line in data.decode("ascii").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  (?:\./)?([A-Za-z0-9_.-]+)", line)
        if not match or match[2] in result:
            raise ValueError("Malformed or duplicate SHA256SUMS entry")
        result[match[2]] = match[1]
    if set(result) != set(names):
        raise ValueError("SHA256SUMS must cover exactly both package files")
    return result


def release_packages(release):
    version = release["tag_name"][1:]
    names = [asset["name"] for asset in release["assets"]]
    if len(names) != len(set(names)) or "SHA256SUMS" not in names:
        raise ValueError("Missing checksums or duplicate release asset names")
    selected = []
    versions = set()
    for arch in ARCHITECTURES:
        pattern = re.compile(rf"lantern_({re.escape(version)}-[1-9][0-9]*)_{arch}\.deb\Z")
        matches = [(name, pattern.fullmatch(name)) for name in names if pattern.fullmatch(name)]
        if len(matches) != 1:
            raise ValueError(f"Expected one {arch} package in {release['tag_name']}")
        name, match = matches[0]
        versions.add(match[1])
        selected.append((name, match[1], arch))
    if len(versions) != 1:
        raise ValueError("Architecture package versions disagree")
    return selected


def build(site, repository, url, bootstrap):
    if site.exists():
        raise ValueError("Output directory must not already exist")
    site.mkdir(parents=True)
    previous = previous_repository(url, bootstrap, site)
    pages = json.loads(run("gh", "api", "--paginate", "--slurp", f"repos/{repository}/releases?per_page=100"))
    releases = [r for page in pages for r in page if not r["draft"] and not r["prerelease"] and TAG.fullmatch(r["tag_name"])]
    releases.sort(key=lambda r: tuple(map(int, TAG.fullmatch(r["tag_name"]).groups())))
    if not releases or len({r["tag_name"] for r in releases}) != len(releases):
        raise ValueError("Expected distinct published stable releases")
    catalog = {"packages": {}, "indexes": dict(previous["indexes"])}
    for release in releases:
        selected = release_packages(release)
        with tempfile.TemporaryDirectory(prefix="lantern-apt-download-") as directory:
            args = ["gh", "release", "download", release["tag_name"], "--repo", repository, "--dir", directory, "--pattern", "SHA256SUMS"]
            for name, _, _ in selected:
                args += ["--pattern", name]
            run(*args)
            sums = checksums((Path(directory) / "SHA256SUMS").read_bytes(), [name for name, _, _ in selected])
            for name, version, arch in selected:
                source = Path(directory) / name
                if source.stat().st_size > 64 * 1024 * 1024:
                    raise ValueError("Package exceeds 64 MiB limit")
                data = source.read_bytes()
                if digest(data) != sums[name]:
                    raise ValueError(f"Package checksum mismatch: {name}")
                for field, expected in (("Package", "lantern"), ("Version", version), ("Architecture", arch)):
                    if run("dpkg-deb", "--field", str(source), field).decode().strip() != expected:
                        raise ValueError(f"Package {field} mismatch: {name}")
                relative = "pool/main/l/lantern/" + name
                write(site / relative, data)
                catalog["packages"][relative] = {"sha256": digest(data), "size": len(data), "version": version, "architecture": arch}
    check_retention(previous, catalog)
    for arch in ARCHITECTURES:
        directory = f"dists/stable/main/binary-{arch}"
        packages = run("apt-ftparchive", "--arch", arch, "packages", "pool", cwd=site)
        for name, data in (("Packages", packages), ("Packages.gz", gzip.compress(packages, mtime=0))):
            write(site / directory / name, data)
            for algorithm in ("SHA256", "SHA512"):
                sha = hashlib.new(algorithm.lower(), data).hexdigest()
                relative = directory + f"/by-hash/{algorithm}/{sha}"
                write(site / relative, data)
                catalog["indexes"][relative] = digest(data)
    validate_catalog(catalog)
    write(site / "dists/stable/catalog.json", json.dumps(catalog, sort_keys=True, indent=2).encode() + b"\n")
    expiry = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(days=30)
    options = {"Origin": "Lantern", "Label": "Lantern", "Suite": "stable", "Codename": "stable",
               "Architectures": "arm64 armhf", "Components": "main", "Acquire-By-Hash": "yes",
               "MD5": "false", "SHA1": "false"}
    # The default Release scan includes Packages but not our retention catalog.
    args = ["apt-ftparchive", "-o", "APT::FTPArchive::Release::Patterns::=catalog.json"]
    for name, value in options.items():
        args += ["-o", f"APT::FTPArchive::Release::{name}={value}"]
    release = run(*args, "release", "dists/stable", cwd=site)
    # Noble's apt-ftparchive does not emit Valid-Until from a Release option.
    release = f"Valid-Until: {expiry:%a, %d %b %Y %H:%M:%S +0000}\n".encode() + release
    write(site / "dists/stable/Release", release)
    write(site / ".nojekyll", b"")
    write(site / "index.html", b"<!doctype html><title>Lantern APT repository</title><h1>Lantern APT repository</h1><p>Signed stable packages for arm64 and armhf.</p><p><a href='https://github.com/koreanmelon/lantern/blob/main/docs/apt-repository.md'>Setup instructions</a></p>\n")
    verify_site(site)


def verify_site(site):
    catalog = json.loads((site / "dists/stable/catalog.json").read_bytes())
    validate_catalog(catalog)
    allowed = {".nojekyll", "index.html", "lantern-archive-keyring.asc", "dists/stable/Release", "dists/stable/InRelease", "dists/stable/Release.gpg", "dists/stable/catalog.json"}
    for name, entry in catalog["packages"].items():
        data = (site / name).read_bytes()
        if digest(data) != entry["sha256"] or len(data) != entry["size"]:
            raise ValueError(f"Package changed after generation: {name}")
        allowed.add(name)
    for name, sha in catalog["indexes"].items():
        verify_index(name, (site / name).read_bytes(), sha)
        allowed.add(name)
    hashes = release_hashes((site / "dists/stable/Release").read_bytes())
    for name in ["catalog.json"] + [f"main/binary-{arch}/{index}" for arch in ARCHITECTURES for index in ("Packages", "Packages.gz")]:
        relative = "dists/stable/" + name
        data = (site / relative).read_bytes()
        if hashes.get(name) != (digest(data), len(data)):
            raise ValueError(f"Release checksum mismatch: {name}")
        allowed.add(relative)
    size = 0
    for path in site.rglob("*"):
        if path.is_symlink():
            raise ValueError("Symlinks are forbidden in repository artifacts")
        if path.is_file():
            if path.relative_to(site).as_posix() not in allowed:
                raise ValueError(f"Unexpected repository file: {path}")
            size += path.stat().st_size
    if size > 900 * 1024 * 1024:
        raise ValueError("Repository approaching GitHub Pages 1 GB limit")
    return catalog


def sign(site):
    verify_site(site)
    key = os.environ.pop("APT_SIGNING_KEY").encode()
    passphrase = os.environ.pop("APT_SIGNING_PASSPHRASE").encode()
    if b"\n" in passphrase or b"\r" in passphrase:
        raise ValueError("Signing passphrase must not contain newlines")
    with keyring(key) as home:
        fingerprint(home, secret=True)
        release = site / "dists/stable/Release"
        for filename, operation in (("InRelease", "--clearsign"), ("Release.gpg", "--detach-sign")):
            run("gpg", "--homedir", str(home), "--batch", "--yes", "--no-tty", "--pinentry-mode", "loopback",
                "--passphrase-fd", "0", "--local-user", FINGERPRINT, "--digest-algo", "SHA256", "--armor",
                "--output", str(release.with_name(filename)), operation, str(release), data=passphrase + b"\n")
        public = run("gpg", "--homedir", str(home), "--armor", "--export", FINGERPRINT)
    write(site / "lantern-archive-keyring.asc", public)
    with keyring(public) as home:
        plain = verified_release(home, release.with_name("InRelease").read_bytes())
        if plain != release.read_bytes():
            raise ValueError("Signed Release content mismatch")
        run("gpg", "--homedir", str(home), "--batch", "--verify", str(release.with_name("Release.gpg")), str(release))
    verify_site(site)


def smoke(site, url=None):
    catalog = verify_site(site)
    with contextlib.ExitStack() as stack:
        if url is None:
            handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=str(site))
            server = stack.enter_context(http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler))
            threading.Thread(target=server.serve_forever, daemon=True).start()
            stack.callback(server.shutdown)
            url = f"http://127.0.0.1:{server.server_port}"
        for arch in ARCHITECTURES:
            with tempfile.TemporaryDirectory(prefix="lantern-apt-client-") as directory:
                root = Path(directory)
                for name in ("empty", "state/lists/partial", "cache/archives/partial", "log", "downloads"):
                    (root / name).mkdir(parents=True)
                (root / "status").touch()
                key = root / "lantern.asc"
                key.write_bytes((site / "lantern-archive-keyring.asc").read_bytes())
                sources = root / "lantern.sources"
                sources.write_text(f"Types: deb\nURIs: {url}\nSuites: stable\nComponents: main\nArchitectures: {arch}\nSigned-By: {key} {FINGERPRINT}\nBy-Hash: force\n")
                config = root / "apt.conf"
                settings = {"Dir::Etc::main": "/dev/null", "Dir::Etc::parts": str(root / "empty"),
                            "Dir::Etc::sourcelist": str(sources), "Dir::Etc::sourceparts": str(root / "empty"),
                            "Dir::Etc::preferences": "/dev/null", "Dir::Etc::preferencesparts": str(root / "empty"),
                            "Dir::Etc::trusted": "/dev/null", "Dir::Etc::trustedparts": str(root / "empty"),
                            "Dir::State": str(root / "state"), "Dir::State::status": str(root / "status"),
                            "Dir::Cache": str(root / "cache"), "Dir::Cache::pkgcache": "", "Dir::Cache::srcpkgcache": "",
                            "Dir::Log": str(root / "log"), "APT::Architecture": arch, "Acquire::Languages": "none"}
                config.write_text("\n".join(f'{name} "{value}";' for name, value in settings.items()) + f'\n#clear APT::Architectures;\nAPT::Architectures {{ "{arch}"; }};\n')
                env = dict(os.environ, APT_CONFIG=str(config), LC_ALL="C")
                run("apt-get", "--error-on=any", "update", env=env)
                packages = [(name, entry) for name, entry in catalog["packages"].items() if entry["architecture"] == arch]
                latest = max((entry["version"] for _, entry in packages), key=functools.cmp_to_key(compare_versions))
                policy = run("apt-cache", "policy", f"lantern:{arch}", env=env).decode()
                if f"Candidate: {latest}" not in policy:
                    raise ValueError(f"Unexpected APT candidate for {arch}: {policy}")
                for name, entry in packages:
                    run("apt-get", "download", f"lantern:{arch}={entry['version']}", cwd=root / "downloads", env=env)
                    data = (root / "downloads" / Path(name).name).read_bytes()
                    if digest(data) != entry["sha256"]:
                        raise ValueError("APT downloaded unexpected package bytes")


def compare_versions(left, right):
    if left == right:
        return 0
    result = subprocess.run(["dpkg", "--compare-versions", left, "lt", right], check=False, timeout=10)
    if result.returncode not in (0, 1):
        raise ValueError("Invalid Debian version comparison")
    return -1 if result.returncode == 0 else 1


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=("build", "sign", "smoke"))
    parser.add_argument("--site", type=Path, required=True)
    parser.add_argument("--repository", default="koreanmelon/lantern")
    parser.add_argument("--url")
    parser.add_argument("--bootstrap", action="store_true")
    args = parser.parse_args()
    site = args.site.resolve()
    if args.command == "build":
        if not args.url or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository):
            parser.error("build requires --url and a valid --repository")
        build(site, args.repository, args.url.rstrip("/"), args.bootstrap)
    elif args.command == "sign":
        sign(site)
    else:
        smoke(site, args.url)


if __name__ == "__main__":
    main()
