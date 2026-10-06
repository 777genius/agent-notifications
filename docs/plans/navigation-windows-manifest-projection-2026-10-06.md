# Windows retained manifest projection diagnostic

Source-only checkpoint. Actual run 37506435343 at source
052b08b52113872466d10f3ad205845a19f76bcf verified the official ARM64 package
signature and collected SDK metadata successfully. Its Windows PowerShell 5.1
manifest process timed out at the unchanged 15-second bound with no stdout or
stderr. This does not identify startup, XML loading or serialization as the cause.

The explicit manual `manifest_fixture` mode in the already registered
`navigation-windows-vendor-metadata.yml` workflow reads only
artifact 11431669982 from that run. It requires the official API digest, exact
232904-byte ZIP SHA256 bd44b0946079032d4855bfd7abbc649066243e120c7303e5039caf11c333a476,
source/run identity, five exact member names/sizes and three retained input hashes.
Only the 8082-byte manifest and two inert JSON reports are extracted. The contained
executable is never extracted or run; the 909238426-byte MSIX is not downloaded.
This mode skips native compilation and signature-tool selection. Its artifact
contains only the small XML controls and diagnostic report. Artifact read permission
is scoped to the job; its token reaches only the explicit fixture step and is
removed from parser/extractor child environments. Default PR syntax checks and
the explicit `inspect_metadata` mode retain their existing behavior.

The same production TEST manifest script now flushes fixed elapsed-time phase
checkpoints to stderr. The diagnostic projects the hash-bound XML with the same
PowerShell 5.1 executable and 15-second budget, verifies its known two applications
and identity against retained SDK metadata, then requires a well-formed internal
DTD fixture to fail at XML load. This control catches enabling DTD processing.
Timeouts and partial checkpoints remain failed evidence, with no automatic retry.

Existing signature and SDK checks are preserved for future official inspections.
The diagnostic records prior signature evidence as retained, not reverified. It
does not establish package installation, protocol dispatch, callbacks, navigation
or a repaired timeout cause. No native diagnostic execution is claimed here.

Microsoft documents [module autoloading](https://learn.microsoft.com/en-us/powershell/module/microsoft.powershell.core/about/about_modules)
and [bounded XML reader settings](https://learn.microsoft.com/en-us/dotnet/api/system.xml.xmlreadersettings).
Module lookup is a possible execution boundary, not a cause inferred from this run.

Retained-fixture run37510580732 on e092bdd timed out after entry27ms/path_start64ms, before xml_load_start. Artifact11436295990 SHA2564e8900f1c66b30c57e7f951065ce24268689b02efc3dd232f7c8e49552cd6563 was independently verified. This isolates the gap to path lookup/file length/reader construction, not XML parsing or the official package download. It does not prove the cmdlet autoload mechanism.

The next bounded repair uses native cwd plus an explicit equality check against the controller-owned expected cwd, .NET Path/FileInfo APIs and individual checkpoints. Provider cmdlets are no longer required for these operations. The 15s bound, 2MiB guard, DTD rejection control, PID/nonce correlation and no-effect fixture mode stay intact. Windows execution remains pending.
