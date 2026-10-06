#!/usr/bin/env python3
"""Independent server-wire fixtures; no compositor, process or notification effects."""
import copy
import hashlib
import importlib.util
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('server_observation',
    Path(__file__).with_name('navigation-linux-server-surface-observation.py'))
observer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(observer)
TOKEN = hashlib.sha256(b'test-native-token').hexdigest()


def wire():
    return [
        dict(kind='ready', compositorPID=700),
        dict(kind='connection', connection=1, pid=812, uid=1000, gid=1000, birth=1600, pidfd=12),
        dict(kind='resource_live', connection=1, interface='wl_surface', object=20),
        dict(kind='xdg_surface', connection=1, xdgSurface=21, surface=20),
        dict(kind='resource_live', connection=1, interface='xdg_surface', object=21),
        dict(kind='toplevel', connection=1, xdgSurface=21, toplevel=22),
        dict(kind='resource_live', connection=1, interface='xdg_toplevel', object=22),
        dict(kind='activate', connection=1, surface=20, tokenHex='746573742d6e61746976652d746f6b656e'),
    ]


def decode(rows):
    return observer.selected_server_surface(rows, 700, 812, 1600, TOKEN)


class ServerSurface(unittest.TestCase):
    def test_actual_resources_and_connection_join_without_kernel_claim(self):
        result = decode(wire())
        self.assertEqual((result['connection'], result['surface'], result['xdgSurface'], result['toplevel']), (1, 20, 21, 22))
        self.assertEqual((result['observerPidfd'], result['surfaceGeneration'], result['toplevelGeneration']), (12, 2, 6))
        self.assertFalse(result['kernelBound'])
        self.assertFalse(result['focusQualified'])
        self.assertNotIn('tokenHex', result)

    def test_requests_do_not_substitute_for_server_resource_creation(self):
        rows = wire(); del rows[6]
        with self.assertRaises(RuntimeError): decode(rows)

    def test_foreign_peer_and_reused_birth_are_rejected(self):
        for field, value in [('pid', 813), ('birth', 1601), ('uid', 0)]:
            rows = wire(); rows[1][field] = value
            with self.subTest(field=field), self.assertRaises(RuntimeError): decode(rows)

    def test_closed_connection_and_duplicate_activation_are_rejected(self):
        for row in [dict(kind='client_destroy', connection=1), wire()[-1]]:
            rows = wire() + [copy.deepcopy(row)]
            with self.subTest(kind=row['kind']), self.assertRaises(RuntimeError): decode(rows)

    def test_recycled_surface_ids_cannot_reuse_an_old_activation(self):
        rows = wire()
        for interface, identifier in [('xdg_toplevel', 22), ('xdg_surface', 21), ('wl_surface', 20)]:
            rows.append(dict(kind='resource_destroy', connection=1, interface=interface, object=identifier))
        rows += wire()[2:7]
        with self.assertRaises(RuntimeError): decode(rows)

    def test_two_live_toplevels_do_not_identify_the_focused_surface(self):
        rows = wire()
        rows += [dict(kind='resource_live', connection=1, interface='wl_surface', object=30),
            dict(kind='xdg_surface', connection=1, xdgSurface=31, surface=30),
            dict(kind='resource_live', connection=1, interface='xdg_surface', object=31),
            dict(kind='toplevel', connection=1, xdgSurface=31, toplevel=32),
            dict(kind='resource_live', connection=1, interface='xdg_toplevel', object=32)]
        with self.assertRaises(RuntimeError): decode(rows)

    def test_resource_bound_counts_destroyed_generations_too(self):
        rows = wire() + [dict(kind='resource_live', connection=1, interface='wl_surface', object=value)
            for value in range(1000, 1253)]
        for tail in [
            [dict(kind='resource_live', connection=1, interface='wl_surface', object=2000)],
            [dict(kind='resource_destroy', connection=1, interface='wl_surface', object=1000),
             dict(kind='resource_live', connection=1, interface='wl_surface', object=1000)],
        ]:
            with self.subTest(recycled=len(tail) == 2), self.assertRaises(RuntimeError):
                decode(rows + tail)


if __name__ == '__main__':
    unittest.main()
