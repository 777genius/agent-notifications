"""Source-pinned wire recipe only. Does not import vendor code or launch anything.
Coordinator may import these local helpers into a finite loopback HTTP fixture.
This module has no main block, network access, credentials, or native execution.
"""
import struct

def varint(value):
    assert value >= 0
    result = bytearray()
    while value > 127:
        result.append((value & 127) | 128)
        value >>= 7
    result.append(value)
    return bytes(result)

def uint(field, value):
    return varint(field << 3) + varint(value)

def blob(field, value):
    if isinstance(value, str):
        value = value.encode('utf-8')
    return varint((field << 3) | 2) + varint(len(value)) + value

def frame(payload, flags=0):
    return bytes([flags]) + struct.pack('>I', len(payload)) + payload

def read_varint(data, at):
    value = 0
    for shift in range(0, 70, 7):
        if at >= len(data):
            raise ValueError('truncated varint')
        byte = data[at]
        at += 1
        value |= (byte & 127) << shift
        if byte < 128:
            return value, at
    raise ValueError('oversized varint')

def fields(data):
    """Bounded wire scanner; repeated fields remain repeated. No vendor schema eval."""
    if len(data) > 1024 * 1024:
        raise ValueError('body budget')
    at = 0
    result = []
    while at < len(data):
        tag, at = read_varint(data, at)
        number, wire = tag >> 3, tag & 7
        if number == 0:
            raise ValueError('zero tag')
        if wire == 0:
            value, at = read_varint(data, at)
        elif wire == 2:
            size, at = read_varint(data, at)
            value = data[at:at + size]
            at += size
        elif wire in (1, 5):
            size = 8 if wire == 1 else 4
            value = data[at:at + size]
            at += size
        else:
            raise ValueError('unsupported wire type')
        if at > len(data):
            raise ValueError('truncated field')
        result.append((number, wire, value))
    return result

def first(data, number, default=None):
    return next((value for n, wire, value in fields(data) if n == number), default)

def model_details(model_id='TEST-native-stop'):
    return b''.join(blob(n, model_id) for n in (1, 3, 4, 5))

def unary_responses():
    """Only the native HTTP/1.1 Connect/protobuf callsites traced in this audit.
    GetUsableModels/DefaultModel here are AiService paths, despite AgentService
    descriptors also exposing identically named methods.
    """
    return {
        '/aiserver.v1.ServerConfigService/GetServerConfig': uint(7, 1),
        '/aiserver.v1.AiService/GetUsableModels': blob(1, model_details()),
        '/aiserver.v1.AiService/GetDefaultModelForCli': blob(1, model_details()),
        '/aiserver.v1.AiService/AvailableModels': b'',
        '/aiserver.v1.DashboardService/GetMe': blob(1, 'TEST-loopback-identity') + uint(2, 1),
        '/aiserver.v1.DashboardService/GetUserPrivacyMode': b'',
        '/aiserver.v1.DashboardService/GetTeamAdminSettingsOrEmptyIfNotInTeam': b'',
        '/aiserver.v1.DashboardService/GetEffectiveUserPlugins': b'',
        '/aiserver.v1.DashboardService/GetManagedSkills': b'',
    }

def decode_append(body):
    """Input is unframed unary BidiAppendRequest protobuf (decompress first)."""
    request_id = first(first(body, 2), 1).decode()
    sequence = first(body, 3, 0)
    data = first(body, 4) or bytes.fromhex(first(body, 1, b'').decode('ascii'))
    return request_id, sequence, data

def stop_frame(conversation_id, generation_id, model_id='TEST-native-stop'):
    query = (blob(1, 'completed') + uint(2, 0) + blob(3, conversation_id)
             + (blob(4, generation_id) if generation_id else b'')
             + blob(5, model_id) + blob(6, model_id))
    # AgentServerMessage.execServerMessage(2) -> ExecServerMessage:
    # id(1), execId(15), executeHookArgs(27) -> request(1) -> stop(11).
    args = blob(1, blob(11, query))
    message = uint(1, 1) + blob(15, 'TEST-stop-1') + blob(27, args)
    return frame(blob(2, message))

def text_frame(text='TEST deterministic turn complete'):
    # AgentServerMessage.interactionUpdate(1) -> textDelta(1) -> text(1).
    return frame(blob(1, blob(1, blob(1, text))))

def terminal_frames():
    # interactionUpdate(1) -> turnEnded(14), followed by Connect EndStream JSON.
    return frame(blob(1, blob(14, b''))) + frame(b'{}', 2)

def classify_client_message(data):
    run = first(data, 1)
    if run is not None:
        return {'kind': 'run', 'conversation': first(run, 5, b'').decode(),
                'generation': first(run, 25, b'').decode(),
                'model': first(first(run, 3, b''), 1, b'').decode()}
    exec_result = first(data, 2)
    if exec_result is not None:
        hook = first(exec_result, 27)
        if hook is None:
            return {'kind': 'unexpected-exec-result'}
        stop_response = first(first(hook, 1, b''), 11)
        return {'kind': 'stop-result', 'id': first(exec_result, 1, 0),
                'execId': first(exec_result, 15, b'').decode(),
                'hasStop': stop_response is not None,
                'followup': first(stop_response or b'', 1, b'').decode()}
    control = first(data, 5)
    if control is not None:
        if first(control, 2) is not None:
            return {'kind': 'exec-throw'}
        close = first(control, 1)
        return {'kind': 'exec-close', 'id': first(close, 1, 0)} if close is not None else {'kind': 'exec-control'}
    if first(data, 7) is not None:
        return {'kind': 'heartbeat'}
    return {'kind': 'unexpected-client-message'}
