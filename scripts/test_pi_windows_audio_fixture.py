"""Refuse forged/stale explicit routes; these tests do not qualify native audio."""
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location("pi_windows_audio_fixture", Path(__file__).with_name("pi-windows-audio-fixture.py"))
A = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(A)
SOURCE = "a" * 40


def receipt():
    return {"schema_version": 1, "source_sha": SOURCE,
            "driver": {"instance_id": "ROOT\\MEDIA\\0000", "image_path": "C:\\Windows\\TEST-driver.sys",
                       "image_sha256": A.DRIVER_SHA256, "inf_path": "C:\\TEST\\vendor.inf", "catalog_path": "C:\\TEST\\vendor.cat"},
            "route": {"kind": "signed_virtual_cable_capture", "driver_instance": "ROOT\\MEDIA\\0000",
                      "render_endpoint_id": "opaque-render", "capture_endpoint_id": "opaque-capture",
                      "render_topology_id": "opaque-render-adapter", "capture_topology_id": "opaque-capture-adapter"}}


class ExplicitAudioFixtureTests(unittest.TestCase):
    def test_opaque_ids_become_literal_atoms_without_default_capture_selection(self):
        self.assertEqual(A.validate(receipt(), SOURCE), ["--virtual-cable", "ROOT\\MEDIA\\0000", "opaque-render",
                         "opaque-capture", "opaque-render-adapter", "opaque-capture-adapter"])

    def test_wrong_source_driver_or_route_never_selects_another_device(self):
        for change in (lambda value: value.update(source_sha="b" * 40),
                       lambda value: value.update(schema_version=True),
                       lambda value: value["driver"].update(image_sha256="b" * 64),
                       lambda value: value["route"].update(driver_instance="ROOT\\MEDIA\\0001"),
                       lambda value: value["route"].update(kind="default_microphone"),
                       lambda value: value["route"].update(capture_endpoint_id="opaque-render"),
                       lambda value: value["route"].update(capture_topology_id="opaque-render-adapter"),
                       lambda value: value["route"].update(capture_endpoint_id="bad\nvalue"),
                       lambda value: value["route"].pop("render_topology_id"),
                       lambda value: value.update(extra=True)):
            value = receipt(); change(value)
            with self.assertRaises(ValueError):
                A.validate(value, SOURCE)

    def test_duplicate_proof_or_identity_fields_are_refused(self):
        value = receipt()
        raw = json.dumps(value)
        for old, new in [('"schema_version": 1', '"schema_version": 1, "schema_version": 1'),
                         ('"capture_endpoint_id": "opaque-capture"', '"capture_endpoint_id": "opaque-capture", "capture_endpoint_id": "foreign"')]:
            with self.assertRaises(ValueError):
                json.loads(raw.replace(old, new), object_pairs_hook=A.unique)

    def test_frozen_receipt_changes_are_rejected_before_native_admission(self):
        with tempfile.TemporaryDirectory(prefix="TEST-pi-audio-proof-") as root:
            path = Path(root) / "receipt.json"
            path.write_text(json.dumps(receipt()))
            with mock.patch.object(A.subprocess, "check_output", return_value=SOURCE):
                fixture = A.Fixture(path)
            path.write_text(json.dumps(receipt()) + "\n")
            # Replace only the module's OS view; pathlib must keep its host type.
            with mock.patch.object(A, "os", mock.Mock(name="nt")):
                A.os.name = "nt"
                with self.assertRaisesRegex(ValueError, "changed_or_non_native"):
                    fixture.arguments()


if __name__ == "__main__":
    unittest.main()
