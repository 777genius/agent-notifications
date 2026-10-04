# Actual product platform clock module prequalification

SOURCE CHECKPOINT: UNQUALIFIED. Root CI execution and receipt review remain the
parent's responsibility. Worker checks are syntax/AST/pure parsers only in fresh
TEST copies. Do not run `--execute-ci` or `--isolated-cell` on the worker.

The fixture executes the stock official image as an embedded Bun interpreter
(`BUN_BE_BUN=1`), privately imports the exact copied `platform-clock.mjs`, then
calls `await createPlatformClock()`. It never starts OpenCode's application,
server, observer, model session, provider, installer, runtime or notifications.
Stock FFI denial is a failed case. There is no alternate Bun, Node clock,
API-copy implementation or automatic retry. The actual Go command is the full
checked-out `cmd/claude-notifications` main's `opencode-clock --protocol 1` path.
The existing E0 path does not depend on a newly ported parent C helper.

The five native jobs plan 1.18.33/Bun1.3.14 and 2.0.21/Bun1.4.2 each, plus
1.18.34/Bun1.3.14 only on Linux amd64: eleven cases. Current Linux arm64 is
explicitly excluded; its official floor/V2 cases exercise the actual arm64
product guard. Host OS/architecture, executable headers, interpreter tuple,
parent/child PIDs, kernel executing image, held current-image file, exact source
bytes and final rechecks must agree. Windows uses a native short `C:\PCLOCK`
base, canonical owned TEST children and owner-only ACLs, preserving actual CRLF
checkout bytes alongside original Git blob hashes.

The immutable 21-source/10-artifact closed input is fetched through `gh api`
from the independently reviewed API workflow head
`25000e2e11340b930615751293073a67af1e5801`. Its canonical JSON SHA256 must be
`73f9af516e9ebb4b1c4fe6cbefea27229906a24f49de3c3eebea70f9dabe3ddf`.
Parent may supply identical bytes with `--closed-inputs`; no caller can change
pins. Every original primary leaf is fetched at its closed commit and checked
against exact byte count/hash. Actual origin tags are independently dereferenced
through `gh`. Only immutable npm registry archive URLs use `curl`; no GitHub
access uses curl, urllib, browser or git network access. No npm install runs.
Archive SHA256 and SHA512 SRI are checked BEFORE any tar parsing. Extraction
permits bounded regular members, rejects duplicate/traversing/link/device names,
and copies only the exact `package/bin/opencode[.exe]` member.

The current 1.18.34 origin is staged separately at
`aec0b9a6d8898f68f923aaf08b7306d931fd9d76`. Its original package source must
match the staged primary's canonical JSON SHA256
`9816a52873e44e6b6d9d47a419834e0faf77e5747d0e7000ed86e643bfb25fb1`.
Current build, entry and flags leaves are retained at that exact origin, with
actual receipt hashes, distinct from floor primaries. No current binary pin is
invented: it is read from the existing product `clock-cells.mjs`.

Parent must stage the independently reviewed current official archive primary
with `--current-primary`, the dispatch input `current_primary_json`, or repository
variable `OPENCODE_PLATFORM_CLOCK_CURRENT_PRIMARY_JSON`. Its closed JSON schema is:

```json
{
  "version": "1.18.34",
  "commit": "aec0b9a6d8898f68f923aaf08b7306d931fd9d76",
  "bun": "1.3.14",
  "os": "linux",
  "arch": "x64",
  "archive": "https://registry.npmjs.org/opencode-linux-x64/-/opencode-linux-x64-1.18.34.tgz",
  "archiveSHA256": "<independently reviewed exact public archive SHA256>",
  "binarySHA256": "<existing product 1.18.34 Linux x64 pin>",
  "sri": "<independently reviewed public archive sha512 SRI>"
}
```

Those archive primaries were not supplied to this source-only worker. Missing
current primary is an explicit failed planned case, never an omitted row or a
successful skip. Worker performs no downloads and adds no image pins.

`SOURCE_COMMIT` comes explicitly from PR head/event SHA. It must be 40 lowercase
hex digits, actual Git HEAD, a descendant of the reviewed source base, and a
clean tracked checkout before/after build and sampling. `GITHUB_SHA` is never
overridden or treated as a fallback. The Bash launcher uses a nonempty array and
works on Bash 3.2. All candidate hashes, copied fixtures, Go binaries/build-info
and manifests are generated AFTER checkout/build in owned external TEST roots;
no tracked self-hash is embedded. The receipt binds the seven actual JS module
leaves, all original platform E0 clock leaves/main/go.mod/go.sum, the workflow
and harness, and every original Go build dependency leaf (including actual SDK
originals if linked), module sums and native compiler bytes. Git blobs and actual
checkout bytes have separate hashes. These are source closure receipts, **not**
canonical algorithm/semantic/qualification IDs. No obsolete 37-leaf ID is reused.

Each case has one stock-image start and three actual helpers. The parent waits
for each helper's natural exit AND both pipe EOFs before sending any raw response
to the JS fixture. A killed/late helper cannot pass. The owned weight cap is four;
normal sampling owns only a module and one helper. The Linux privileged process
is launched only AFTER fetching/building, under `sudo env -i`, using the TEST
root's fresh HOME/XDG/config/data/tmp. It enters a new loopback-only netns and
communicates only with pipes. No user HOME/config/auth is inherited by native
children. Native Windows/macOS do not claim a network namespace.

Budgets are fixed: preparation 2s, JS 25s, Go 20s, individual helper 224ms, whole
job 900s absolute, sample cap 512 and chunks of at most 32. Three rounds sample
102 actual values (108 on Windows, including separate native-wall/Date brackets).
Parent and JS deadlines include comparison, disposal, final
hash/image checks and natural host exit/EOF. Each immediate helper-file SHA runs
inside the original 2s operation and absolute job deadline, checked before and
after hashing. Only then is the 224ms allowance armed immediately before launch,
capped by those same original deadlines and the unchanged 20s Go limit.
Cleanup kills all live owned children first, then waits and joins both pipe
readers within one shared 5s deadline. Repeated finally paths reuse that deadline;
unproved wait/EOF leaves ownership open. Cleanup cannot qualify a timed-out
observation or reset any operation budget. Sampling has no sleeps,
slow-machine compensation, enlarged bounds, inferred offsets, wall-clock
fallback or process-hrtime coordinate.

Go intervals must be causally contained between before/after actual JS source
samples, with identical boot/domain/raw kind and actual bigint wall/counter
fields. Windows uses native precise FILETIME with zero wall quantum allowance;
Date/native predicates are separately recorded and may be false. Linux/Darwin
local Date rounding comparison uses the fixed 2ms **conditional observation**
assumption, never a newly qualified wall/R/T budget. The Linux reader's existing
10ms proc interval and Q2/T430 refusals are unchanged. Disposal is observed on
the actual object, repeated disposal is tested, and subsequent sampling must
refuse. Linux/Darwin first post-disposal kernel FD snapshots must not increase.
Windows TEST resource acceptance uses a temporal amendment: retain the first
live post-disposed handle snapshot, then, only on growth, recheck the same live
host until its count is <= the unchanged post-import/pre-sampler baseline,
strictly before the original absolute 2s operation deadline. Resource-only
rechecks pause at most 10ms each within that deadline; there are no new sampler
reads, baseline changes, threshold increases or whole-case retries. Initial
postDispose/after/delta fields remain initial observations. The separate
nativeResourceSettlingObservation records immediate/final count, extra read
count, elapsed time, remaining original operation time and the temporal criterion. An immediate pass adds no
resource read. Expiry, dead/unreadable host or a low count read at/after expiry
denies. Final image verification, finish, natural Wait and EOF still must finish
within the original operation budget. Natural exit is not a zero-count fallback.
This accepts eventual total-count nonincrease, not proven synchronous Bun
cleanup or resource ownership: unrelated handle closure can mask a persistent
sampler leak. It does not authenticate mappings or prove release by call tracing.

Signed operational assumptions remain explicit: trusted process globals and
builtins, protected immutable OS mappings, Darwin's protected libSystem/dyld,
LP64/little-endian ABI and stable Mach timebase, Windows's protected default
System32/Win64 precise-interrupt API-set/NT class90 exact32/GUID/FILETIME path,
and Linux's trusted proc mount/current calling-thread time namespace. Actual
errors always produce bounded unqualified diagnostics. No SDK/source event wall
accuracy, clock rate, suspend continuity, final span, registry or product policy
is inferred from the observations.

Only `platform-clock-safe-evidence.json` is public. Its schema projects closed
numeric bounds/checks, source/archive/binary/build receipts, case/lifecycle and
helper start/close counts. Raw UUID/domain IDs/ticks, PIDs, file identities,
loader paths and logs stay in fresh owned TEST roots; never upload those roots.
Every qualification flag, including TimePolicy, runtime eligibility, R/T,
source-wall, suspend, final-span and registry, remains literal FALSE. Passing
module observations fill no qualification rows.

For worker pure checks, copy these harness files and this test into a fresh owned
TEST directory, then run `python -B test_fixture.py --node <staged-node>` there.
The test imports inert pure functions and exercises actual harness ownership
methods with scripted process/EOF states and fake elapsed boundaries. A private
file's real SHA256 tests preflight allowances and deadline/hash refusal; cleanup
vectors test kill-before-wait ordering, one shared ceiling and unproved closure.
Python pure checks guard against any actual child start. Tests use independent
literal frames and header/budget/identity vectors, and check Python AST and Node
syntax. They never import an actual product clock module, mock FFI, mirror product
source, execute the native runner or start any product/helper/build/provider. Retain
patch, exact hashes and executed receipts under the durable artifact directory
specified by the task; repeat a separate filesystem read to verify persistence.
