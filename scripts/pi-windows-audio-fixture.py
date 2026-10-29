"""Frozen, explicitly verified virtual audio input for native Windows CI only."""
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess

REPO = Path(__file__).resolve().parents[1]
DRIVER_SHA256 = "2dc35db3dfad0f25771a3e59af38e8b1268878ebe127476ea75fee109f2927dd"
ROUTE_KEYS = {"kind", "driver_instance", "render_endpoint_id", "capture_endpoint_id", "render_topology_id", "capture_topology_id"}


def require(ok, code):
    if not ok:
        raise ValueError(code)


def validate(value, source):
    require(isinstance(value, dict) and set(value) == {"schema_version", "source_sha", "driver", "route"}
            and type(value["schema_version"]) is int and value["schema_version"] == 1
            and value["source_sha"] == source, "windows_audio_fixture_source_invalid")
    driver, route = value["driver"], value["route"]
    require(isinstance(driver, dict) and set(driver) == {"instance_id", "image_path", "image_sha256", "inf_path", "catalog_path"}
            and driver["image_sha256"] == DRIVER_SHA256, "windows_audio_fixture_driver_invalid")
    require(isinstance(route, dict) and set(route) == ROUTE_KEYS and route["kind"] == "signed_virtual_cable_capture"
            and route["driver_instance"] == driver["instance_id"], "windows_audio_fixture_route_invalid")
    for text in [driver["instance_id"], driver["image_path"], driver["inf_path"], driver["catalog_path"], *route.values()]:
        require(isinstance(text, str) and 0 < len(text) <= 4096 and not re.search(r"[\x00-\x1f\x7f]", text),
                "windows_audio_fixture_atom_invalid")
    require(route["render_endpoint_id"] != route["capture_endpoint_id"]
            and route["render_topology_id"] != route["capture_topology_id"], "windows_audio_fixture_distinct_endpoints_required")
    return ["--virtual-cable", route["driver_instance"], route["render_endpoint_id"], route["capture_endpoint_id"],
            route["render_topology_id"], route["capture_topology_id"]]


def unique(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "windows_audio_fixture_duplicate_key")
        result[key] = value
    return result


class Fixture:
    def __init__(self, path):
        require(not Path(path).is_symlink(), "windows_audio_fixture_link_refused")
        self.path = Path(path).resolve(strict=True)
        raw = self.read()
        self.digest = hashlib.sha256(raw).hexdigest()
        self.value = json.loads(raw, object_pairs_hook=unique)
        self.source = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=REPO, text=True).strip()
        self.atoms = validate(self.value, self.source)

    def read(self):
        require(self.path.is_file() and not self.path.is_symlink() and 0 < self.path.stat().st_size <= 65536,
                "windows_audio_fixture_regular_bounded_file_required")
        return self.path.read_bytes()

    def arguments(self):
        require(os.name == "nt" and hashlib.sha256(self.read()).hexdigest() == self.digest,
                "windows_audio_fixture_changed_or_non_native")
        image = Path(self.value["driver"]["image_path"])
        require(image.is_file() and not image.is_symlink() and 0 < image.stat().st_size <= 64 * 1024 * 1024
                and hashlib.sha256(image.read_bytes()).hexdigest() == DRIVER_SHA256,
                "windows_audio_fixture_installed_driver_changed")
        result = subprocess.run(["pwsh.exe", "-NoProfile", "-File", str(REPO / "scripts/pi-windows-host.ps1"),
                                 "-VerifyOnly", "-Receipt", str(self.path)], stdin=subprocess.DEVNULL,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=30)
        require(result.returncode == 0 and len(result.stdout) + len(result.stderr) <= 65536,
                "windows_audio_fixture_live_driver_verification_failed")
        proof = json.loads(result.stdout)
        require(isinstance(proof, dict) and set(proof) == {"verified", "source_sha"}
                and proof["verified"] is True and proof["source_sha"] == self.source,
                "windows_audio_fixture_live_driver_receipt_invalid")
        return list(self.atoms)
