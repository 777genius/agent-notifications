# Local shared service RPC

Each MCP bridge retains its own SDK session and captures caller origin and boot
deadline. `Client` forwards those values unchanged over one connection per call.
Only `notify` and read-only `status` exist; requests cannot select files, profiles,
targets, executables, configuration, or daemon lifecycle operations.

Composition owns private endpoint discovery and daemon admission. Its `Dial`
callback must verify the private record and call `AuthenticateClient` before
returning a connection. `Serve` requires a loopback TCP listener, a fixed provider,
and the binding/runtime identity and 32-byte random secret from that record.

Authentication and encryption use standard TLS 1.3 with mutual Ed25519
certificate possession and exact peer public-key pinning. Per-role private keys
are derived with HMAC-SHA256 from the random secret and length-framed role and
binding/runtime identity; client and server use different keys. Self-signed
certificate metadata contains no identity or secret. No network PKI, hostname
lookup, operating-system credential store, or wall-clock trust is involved.

Both sides require the exact TLS version, `agent-notify-rpc-v1` ALPN, expected
peer SPKI, and peer role EKU. Client CA/hostname verification is replaced only by
those explicit checks. Session resumption is disabled. `AuthenticateClient`
returns a secured `net.Conn` which the Dial callback must return and use for all
RPC I/O. The server likewise authenticates and wraps before reading any RPC data.

TLS protects against handshake replay and encrypts RPC content even if a malicious
process at a stale loopback port actively proxies the handshake to the real
service. The secret and identity are never transmitted. This does not protect
against an attacker able to read the user's private secret or process memory.

Frames use a four-byte big-endian byte count followed by at most 64 KiB of strict
JSON. Unknown fields, duplicate keys, invalid Unicode, excess nesting, and excess
container entries fail closed. Authentication is limited to three seconds, RPC
I/O to fifteen seconds, and active connections to at most 256 (including pending
authentication). The server checks the fixed provider and original boot deadline
again before notification dispatch; a different boot, expiry, or budget over
fifteen seconds is rejected. Durable admission, deduplication, and rate limits
remain backend responsibilities.

No RPC request is automatically resent. Connect/authentication or encoding
failure before the payload write attempt is a retry-safe rejection. Any uncertain
transport result after the write attempt is `unknown` with `retry_safe=false`,
even if a write reports zero bytes. A lost reply may follow a completed effect.

Bridge cancellation closes its connection. The server watches for disconnect or
extra input while a backend call is active and cancels that call. Shutdown closes
admission, cancels all calls, and joins handlers before returning; composition
then closes backend resources. Idle exit (default ten minutes) happens only when
there are no active connections. Backends must honor context cancellation.
