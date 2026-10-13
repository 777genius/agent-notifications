"""Pure decoder for the TEST compositor observer; kernel/focus binding belongs to its consumer."""
import hashlib
import json


FIELDS = {
    'ready': {'kind', 'compositorPID'},
    'fault': {'kind', 'reason'},
    'connection': {'kind', 'connection', 'pid', 'uid', 'gid', 'birth', 'pidfd'},
    'client_destroy': {'kind', 'connection'},
    'resource_live': {'kind', 'connection', 'interface', 'object'},
    'resource_destroy': {'kind', 'connection', 'interface', 'object'},
    'destroy': {'kind', 'connection', 'interface', 'object'},
    'xdg_surface': {'kind', 'connection', 'xdgSurface', 'surface'},
    'toplevel': {'kind', 'connection', 'xdgSurface', 'toplevel'},
    'activate': {'kind', 'connection', 'surface', 'tokenHex'},
}


def selected_server_surface(records, compositor_pid, pid, birth, token_sha256):
    """Reject ambiguous/stale role joins; returned pidfd still requires live kernel validation."""
    def require(value, reason):
        if not value: raise RuntimeError(reason)

    require(isinstance(records, list) and 0 < len(records) <= 512, 'server_record_bound')
    connections, resources, roles, activations = {}, {}, {}, []
    ready = False
    resource_creations = 0

    def resource(connection, interface, identifier):
        return resources.get((connection, interface, identifier))

    def live_role(connection, role):
        return (connections[connection]['alive'] and role.get('xdgGeneration') is not None
            and role.get('toplevelGeneration') is not None
            and resource(connection, 'wl_surface', role['surface']) == role['surfaceGeneration']
            and resource(connection, 'xdg_surface', role['xdgSurface']) == role['xdgGeneration']
            and resource(connection, 'xdg_toplevel', role['toplevel']) == role['toplevelGeneration'])

    def identity(connection, role):
        return (connection, role['surface'], role['surfaceGeneration'], role['xdgSurface'],
            role['xdgGeneration'], role['toplevel'], role['toplevelGeneration'], role['requestIndex'])

    for index, row in enumerate(records):
        require(isinstance(row, dict) and row.get('kind') in FIELDS and set(row) == FIELDS[row['kind']], 'server_record_schema')
        kind = row['kind']
        require(kind != 'fault', 'server_observer_fault')
        for field in set(row) - {'kind', 'interface', 'tokenHex', 'reason'}:
            require(type(row[field]) is int and 0 < row[field] < 2**64, 'server_integer_invalid')
        if kind == 'ready':
            require(index == 0 and not ready and row['compositorPID'] == compositor_pid, 'server_ready_identity')
            ready = True; continue
        require(ready, 'server_ready_missing')
        connection = row['connection']
        if kind == 'connection':
            require(connection == len(connections) + 1 and len(connections) < 128, 'server_connection_epoch')
            require(row['uid'] == row['gid'] == 1000 and row['pid'] != compositor_pid, 'server_peer_credentials')
            require(row['pidfd'] >= 3, 'server_peer_pidfd')
            connections[connection] = dict(row, alive=True); continue
        require(connection in connections, 'server_connection_unknown')
        peer = connections[connection]
        if kind == 'client_destroy':
            require(peer['alive'], 'server_duplicate_client_destroy')
            peer['alive'] = False; continue
        # Resource destruction follows the client destroy listener during disconnect.
        require(peer['alive'] or kind == 'resource_destroy', 'server_request_after_disconnect')
        if kind in ('resource_live', 'resource_destroy', 'destroy'):
            require(row['interface'] in ('wl_surface', 'xdg_surface', 'xdg_toplevel'), 'server_resource_interface')
            key = (connection, row['interface'], row['object'])
            if kind == 'resource_live':
                resource_creations += 1
                require(resource_creations <= 256, 'server_resource_creation_bound')
                require(key not in resources, 'server_duplicate_live_resource')
                resources[key] = index
                role = roles.get((connection, row['object'])) if row['interface'] == 'xdg_surface' else None
                if role is not None:
                    require(role.get('xdgGeneration') is None, 'server_duplicate_role_creation')
                    role['xdgGeneration'] = index
                if row['interface'] == 'xdg_toplevel':
                    matches = [value for (epoch, _), value in roles.items()
                        if epoch == connection and value.get('toplevel') == row['object']
                        and value.get('toplevelGeneration') is None]
                    require(len(matches) == 1, 'server_toplevel_creation_without_unique_request')
                    matches[0]['toplevelGeneration'] = index
            elif kind == 'resource_destroy':
                require(key in resources, 'server_resource_destroy_without_live_object')
                del resources[key]
            else:
                require(key in resources, 'server_destroy_request_without_live_object')
            continue
        if kind == 'xdg_surface':
            generation = resource(connection, 'wl_surface', row['surface'])
            require(generation is not None and resource(connection, 'xdg_surface', row['xdgSurface']) is None,
                'server_role_request_without_live_surface')
            roles[(connection, row['xdgSurface'])] = dict(surface=row['surface'],
                surfaceGeneration=generation, xdgSurface=row['xdgSurface'], requestIndex=index)
        elif kind == 'toplevel':
            role = roles.get((connection, row['xdgSurface']))
            require(role is not None and role.get('xdgGeneration') is not None
                and resource(connection, 'xdg_surface', row['xdgSurface']) == role['xdgGeneration']
                and 'toplevel' not in role, 'server_toplevel_request_without_live_role')
            role['toplevel'] = row['toplevel']
        elif kind == 'activate':
            value = row['tokenHex']
            require(isinstance(value, str) and 0 < len(value) <= 8192 and len(value) % 2 == 0
                and all(character in '0123456789abcdef' for character in value), 'server_token_encoding')
            if hashlib.sha256(bytes.fromhex(value)).hexdigest() == token_sha256:
                require(peer['pid'] == pid and peer['birth'] == birth, 'server_token_used_by_foreign_peer')
                candidates = [value for (epoch, _), value in roles.items() if epoch == connection
                    and value['surface'] == row['surface'] and live_role(connection, value)]
                require(len(candidates) == 1, 'server_activation_without_unique_live_toplevel')
                activations.append(identity(connection, candidates[0]))

    require(ready and len(activations) == 1, 'server_sole_selected_activation_unproved')
    candidates = [(connection, role) for (connection, _), role in roles.items()
        if connections[connection]['pid'] == pid and connections[connection]['birth'] == birth
        and live_role(connection, role)]
    require(len(candidates) == 1 and identity(*candidates[0]) == activations[0], 'server_unique_current_toplevel_unproved')
    connection, role = candidates[0]
    peer = connections[connection]
    relevant = [row for row in records if row.get('connection') in
        {epoch for epoch, value in connections.items() if value['pid'] == pid and value['birth'] == birth}]
    return dict(connection=connection, pid=pid, birth=birth, observerPidfd=peer['pidfd'],
        surface=role['surface'], xdgSurface=role['xdgSurface'], toplevel=role['toplevel'],
        surfaceGeneration=role['surfaceGeneration'], toplevelGeneration=role['toplevelGeneration'],
        selectedRecordsSHA256=hashlib.sha256(json.dumps(relevant, sort_keys=True).encode()).hexdigest(),
        kernelBound=False, focusQualified=False)
