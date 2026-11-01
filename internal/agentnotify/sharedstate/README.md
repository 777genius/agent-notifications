# Shared MCP discovery state

This package stores one ephemeral authenticated loopback endpoint per portable
binding key and installed runtime SHA-256. Both inputs are lowercase hexadecimal
SHA-256 strings; the transport identity is `bindingKey:runtimeSHA`. The private
state path is `<dataRoot>/.shared-mcp/<SHA256(identity)>/endpoint.json`.

`Open` requires an existing owned private data root. It never creates, reads, or
migrates notification policy, installation journals, or profile configuration.
It creates only discovery directories and the permanent `start.lock` and
`server.lock` inodes. Keep the launcher election lock while checking discovery
and starting a daemon. Keep the server lock for the daemon's entire lifetime,
including `Publish` and its final conditional `Remove`. Lock acquisition requires
a context deadline; expiration does not remove or replace the permanent inode.

A version-1 endpoint record contains an exact `127.0.0.1:<port>` address, a random
32-byte token encoded as lowercase hex, and a diagnostic PID. PID never grants
authority to signal a process. The token is local authentication material and
must never be transmitted or logged. The transport performs mutual authentication
before sending notification content. Records are limited to 4 KiB and include
the exact binding/runtime identity. Unknown fields, invalid records, unowned
objects, corruption, and unsafe permissions are refused and preserved.

Publication uses a private temporary file and one replacing rename. A valid
stale record may be replaced only after obtaining the server lifetime lock.
Removal compares the entire expected record, preserving a replacement daemon's
record. Filesystem operations stay within validated physical directories;
root, namespace and identity-directory inode identities are rechecked on reuse.
Unix files require current ownership, exact 0600 mode, one link and no extended
ACL; directories require current ownership, exact 0700 mode and no extended ACL.
Windows uses held non-reparse ancestor handles and explicit protected private
DACLs at creation. Atomic publication uses FileRenameInfoEx with POSIX replacement
semantics, so existing discovery readers retain their immutable original inode
while new readers see the replacement. Its checks reject foreign read access in addition to writes,
including ACL grants that the installer's non-secret observation cache permits.
Existing access controls are never silently repaired.

The discovery record is an expendable hint, not durable daemon authority. After
a crash or reboot, a client must authenticate the current endpoint and may
coordinate a replacement through the permanent locks. Temporary publication
files from a crash contain no notification policy or queued content. Unsupported
platforms fail closed.
