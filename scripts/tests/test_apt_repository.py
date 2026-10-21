import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("apt_repository", Path(__file__).parents[1] / "apt_repository.py")
apt = importlib.util.module_from_spec(spec)
spec.loader.exec_module(apt)


class ValidationTests(unittest.TestCase):
    def test_release_assets(self):
        names = ["SHA256SUMS", "lantern_0.1.2-1_arm64.deb", "lantern_0.1.2-1_armhf.deb"]
        release = {"tag_name": "v0.1.2", "assets": [{"name": n} for n in names]}
        self.assertEqual(len(apt.release_packages(release)), 2)
        for altered in (names[:-1], names + [names[-1]], [names[0], names[1], "lantern_0.1.2-2_armhf.deb"]):
            with self.subTest(altered=altered), self.assertRaises(ValueError):
                apt.release_packages(dict(release, assets=[{"name": n} for n in altered]))

    def test_amd64_release_assets(self):
        names = ["SHA256SUMS"] + [f"lantern_0.1.5-1_{arch}.deb" for arch in apt.ARCHITECTURES]
        release = {"tag_name": "v0.1.5", "assets": [{"name": n} for n in names]}
        self.assertEqual(len(apt.release_packages(release)), 3)
        for arch in apt.ARCHITECTURES:
            altered = [name for name in names if not name.endswith(f"_{arch}.deb")]
            with self.subTest(missing=arch), self.assertRaises(ValueError):
                apt.release_packages(dict(release, assets=[{"name": n} for n in altered]))
        altered = [name.replace("0.1.5-1_amd64", "0.1.5-2_amd64") for name in names]
        with self.assertRaises(ValueError):
            apt.release_packages(dict(release, assets=[{"name": n} for n in altered]))

    def test_checksum_manifest(self):
        sha = "a" * 64
        self.assertEqual(apt.checksums(f"{sha}  ./a.deb\n".encode(), ["a.deb"]), {"a.deb": sha})
        for data in (b"not a checksum", f"{sha}  ../a.deb\n".encode(), f"{sha}  a.deb\n{sha}  a.deb\n".encode(), f"{sha}  b.deb\n".encode()):
            with self.subTest(data=data), self.assertRaises(ValueError):
                apt.checksums(data, ["a.deb"])

    def test_retention(self):
        entry = {"sha256": "a" * 64, "size": 10, "version": "0.1.0-1", "architecture": "arm64"}
        name = "pool/main/l/lantern/lantern_0.1.0-1_arm64.deb"
        previous = {"packages": {name: entry}, "indexes": {}}
        apt.validate_catalog(previous)
        apt.check_retention(previous, previous)
        for current in ({"packages": {}}, {"packages": {name: dict(entry, sha256="b" * 64)}}):
            with self.assertRaises(ValueError):
                apt.check_retention(previous, current)
        with self.assertRaises(ValueError):
            apt.validate_catalog({"packages": {"../../escape": entry}, "indexes": {}})

    def test_by_hash_tampering(self):
        data = b"Packages fixture"
        for algorithm in ("SHA256", "SHA512"):
            sha = hashlib.new(algorithm.lower(), data).hexdigest()
            name = f"dists/stable/main/binary-arm64/by-hash/{algorithm}/{sha}"
            apt.verify_index(name, data, apt.digest(data))
            with self.assertRaises(ValueError):
                apt.verify_index(name, b"tampered", apt.digest(data))

    def test_wrong_fingerprint(self):
        wrong = b"pub:u:3072:1:key:1:2:::::sc:\nfpr:::::::::AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA:\n"
        with patch.object(apt, "run", return_value=wrong), self.assertRaises(ValueError):
            apt.fingerprint(Path("unused"))


@unittest.skipUnless(all(shutil.which(tool) for tool in ("apt-ftparchive", "apt-get", "dpkg-deb", "gpg")), "requires Linux APT repository tools")
class AptClientTests(unittest.TestCase):
    def test_signed_repository_and_failures(self):
        with tempfile.TemporaryDirectory(prefix="lantern-apt-test-") as directory:
            root = Path(directory)
            fixtures = root / "fixtures"
            fixtures.mkdir()
            releases = []
            for version in ("0.1.0", "0.1.5", "0.1.6"):
                assets = []
                sums = []
                release_dir = fixtures / ("v" + version)
                release_dir.mkdir()
                for arch in apt.ARCHITECTURES:
                    if version == "0.1.0" and arch == "amd64":
                        continue
                    package = root / f"package-{version}-{arch}"
                    (package / "DEBIAN").mkdir(parents=True)
                    (package / "DEBIAN/control").write_text(f"Package: lantern\nVersion: {version}-1\nArchitecture: {arch}\nMaintainer: Fixture <fixture@example.invalid>\nDescription: APT test fixture\n")
                    # Build an archive only. No package is installed or executed.
                    name = f"lantern_{version}-1_{arch}.deb"
                    apt.run("dpkg-deb", "--build", "--root-owner-group", str(package), str(release_dir / name))
                    assets.append({"name": name})
                    sums.append(f"{apt.digest((release_dir / name).read_bytes())}  ./{name}\n")
                (release_dir / "SHA256SUMS").write_text("".join(sums))
                releases.append({"tag_name": "v" + version, "draft": version == "0.1.6", "prerelease": False, "assets": assets + [{"name": "SHA256SUMS"}]})
            releases += [{"tag_name": "v0.2.0-rc.1", "draft": False, "prerelease": True}]
            original_run = apt.run

            def fake_github(*args, **kwargs):
                if args[:2] == ("gh", "api"):
                    return json.dumps([releases]).encode()
                if args[:3] == ("gh", "release", "download"):
                    destination = Path(args[args.index("--dir") + 1])
                    for file in (fixtures / args[3]).iterdir():
                        shutil.copyfile(file, destination / file.name)
                    return b""
                return original_run(*args, **kwargs)

            site = root / "site"
            legacy_site = root / "legacy-site"
            empty = {"packages": {}, "indexes": {}}
            with patch.object(apt, "run", side_effect=fake_github), patch.object(apt, "previous_repository", return_value=empty):
                # Main can receive the new tooling before any amd64 release is
                # published. Exercise the empty amd64 index and smoke fallback.
                releases[1]["draft"] = True
                apt.build(legacy_site, "test/lantern", "https://unused.invalid", True)
                releases[1]["draft"] = False
                apt.build(site, "test/lantern", "https://unused.invalid", True)
            self.assertEqual(len(apt.verify_site(legacy_site)["packages"]), 2)
            catalog = apt.verify_site(site)
            self.assertEqual(len(catalog["packages"]), 5)
            self.assertFalse(any("0.1.0-1_amd64" in name for name in catalog["packages"]))
            metadata = (site / "dists/stable/Release").read_text()
            self.assertIn("Acquire-By-Hash: yes", metadata)
            self.assertIn("Valid-Until:", metadata)

            home = root / "key"
            home.mkdir(mode=0o700)
            try:
                original_run("gpg", "--homedir", str(home), "--batch", "--pinentry-mode", "loopback", "--passphrase", "fixture-passphrase",
                             "--quick-generate-key", "Lantern disposable test key", "ed25519", "sign", "1d")
                listing = original_run("gpg", "--homedir", str(home), "--with-colons", "--list-keys").decode()
                test_fingerprint = next(line.split(":")[9] for line in listing.splitlines() if line.startswith("fpr:"))
                secret = original_run("gpg", "--homedir", str(home), "--batch", "--pinentry-mode", "loopback", "--passphrase", "fixture-passphrase", "--armor", "--export-secret-keys", test_fingerprint).decode()
                with patch.object(apt, "FINGERPRINT", test_fingerprint):
                    with patch.dict(os.environ, APT_SIGNING_KEY=secret, APT_SIGNING_PASSPHRASE="fixture-passphrase"):
                        apt.sign(legacy_site)
                    apt.smoke(legacy_site)
                    with patch.dict(os.environ, APT_SIGNING_KEY=secret, APT_SIGNING_PASSPHRASE="wrong-passphrase"), self.assertRaises(subprocess.CalledProcessError):
                        apt.sign(site)
                    with patch.dict(os.environ, APT_SIGNING_KEY=secret, APT_SIGNING_PASSPHRASE="fixture-passphrase"):
                        apt.sign(site)
                    apt.smoke(site)

                    # Verify previous signatures, retain every by-hash object,
                    # and reject a tampered signed catalog.
                    def previous_fetch(url, limit=16 * 1024 * 1024):
                        return (site / url.removeprefix("https://previous.invalid/")).read_bytes()

                    with patch.object(apt, "fetch", side_effect=previous_fetch):
                        previous = apt.previous_repository("https://previous.invalid", False, root / "next")
                        self.assertEqual(previous, catalog)
                        for name in catalog["indexes"]:
                            self.assertEqual((root / "next" / name).read_bytes(), (site / name).read_bytes())
                        manifest = site / "dists/stable/catalog.json"
                        saved = manifest.read_bytes()
                        manifest.write_bytes(saved + b" ")
                        with self.assertRaises(ValueError):
                            apt.previous_repository("https://previous.invalid", False, root / "invalid")
                        manifest.write_bytes(saved)

                        # A second complete generation adds a published version
                        # without losing cached indexes or any old package.
                        releases[2]["draft"] = False
                        with patch.object(apt, "run", side_effect=fake_github):
                            apt.build(root / "second", "test/lantern", "https://previous.invalid", False)
                        rebuilt = apt.verify_site(root / "second")
                        self.assertEqual(len(rebuilt["packages"]), 8)
                        self.assertTrue(catalog["indexes"].items() <= rebuilt["indexes"].items())

                        # --decrypt accepts unsigned literal-data packets; the
                        # repository must require signature verification too.
                        inrelease = site / "dists/stable/InRelease"
                        original_signed = inrelease.read_bytes()
                        unsigned = original_run("gpg", "--homedir", str(home), "--batch", "--store", data=(site / "dists/stable/Release").read_bytes())
                        inrelease.write_bytes(unsigned)
                        with self.assertRaises(subprocess.CalledProcessError):
                            apt.previous_repository("https://previous.invalid", False, root / "unsigned")
                        inrelease.write_bytes(original_signed)

                    # APT must reject corrupt signed metadata and package bytes.
                    signed = site / "dists/stable/InRelease"
                    saved = signed.read_bytes()
                    signed.write_bytes(saved.replace(b"Origin: Lantern", b"Origin: altered"))
                    with self.assertRaises(subprocess.CalledProcessError):
                        apt.smoke(site)
                    signed.write_bytes(saved)
                    package = site / next(iter(catalog["packages"]))
                    saved = package.read_bytes()
                    package.write_bytes(saved + b"corrupt")
                    with self.assertRaises(ValueError):
                        apt.verify_site(site)
                    package.write_bytes(saved)

                # Pinning a different fingerprint must make the APT client fail.
                with patch.object(apt, "FINGERPRINT", "A" * 40), self.assertRaises(subprocess.CalledProcessError):
                    apt.smoke(site)
            finally:
                subprocess.run(["gpgconf", "--homedir", str(home), "--kill", "gpg-agent"], check=False)


if __name__ == "__main__":
    unittest.main()
