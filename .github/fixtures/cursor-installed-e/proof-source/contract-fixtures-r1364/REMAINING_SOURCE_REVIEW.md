# R1368 continuation source review — INCOMPLETE

This is source review only. No kernel outcome was run or inferred as PASS.

The exact original ordinary restart boundary is src/strace.c ptrace_restart:
it returns success for ESRCH. Killing an own traced task and reporting ESRCH
cannot prove the missing ordinary restart-failure route. An executable case
must obtain an actual non-ESRCH ptrace error at an ordinary unheld restart,
while a separate original held stop remains outstanding, observe the original
fatal cleanup/refusal route, and join every own resource. No ptrace wrapper,
interposed result, copied diagnostic or altered original source is admissible.

The original live trace-output stream comes from os.pipe in PassiveStrace.
The original parent closes its writer after spawning the real tracer. Closing
the driver's FileIO reader produces an actual read exception, not actual EOF.
Synthetic consume_trace(b'') or replacing the reader cannot fill this gap.
A real writer-end closure, its original reader EOF, original first refusal,
zero watched ACK and retained genuine held-stop custody must all be witnessed.
Killing a tracer alone also does not establish retained stops: kernel detach
and task state must be observed, not assumed.

Original complete_held obtains held_credentials, then freshly validates task,
FD table/socket aliases and monitored name before send. Current wrong-socket
replaces the real listener and invokes assert_continuous earlier; this is not
final dispatcher socket-incarnation revalidation. Current wrong-name uses a
real unauthorized signal sender; it is not actual admitted name loss after
consumed credentials. The final-ACK-race scheduling gate observes real original
held_credentials return and independently consumed controls, but its actual
diagnostic failure cannot substitute name/socket incarnation drift. A new
finite case needs an actual original incarnation change at that gate, the
specific original guard, and refusal custody without ledger/name mutation or
new external authority. A generic deadline or earlier continuity failure
must refuse qualification for this obligation.

These three SOURCE gaps remain. Existing executable cases need independent
source review and external root execution; syntax/build/pure receipts do not
establish correctness. Original native/C source hashes remain unchanged.


## R1373 current disposition (supersedes the three-gap count above)

SOURCE INCOMPLETE; task handoff only. RuntimeProof PENDING, native HOLD. No kernel
case or Go helper was executed here. Original source hashes match the four
READONLY pins. Historical receipts and the preceding R1368 review stay intact.

The finite `final-incarnation-drift` source now exports the actual selected
AF_UNIX endpoint over the actor's already inherited stage socket using real
SCM_RIGHTS. Qualified PID1 retains this actual duplicate; its device/inode must
match the ORIGINAL connection. After ORIGINAL held_credentials has returned
and both original monitor roundtrips are independently consumed, PID1 performs
real shutdown on that duplicate. The original monitor must consume the daemon's
real unique-name departure while its own services remain live and error-free.
Only then does profiling release the ORIGINAL complete_held dispatcher. Success
requires the original `tracked bus socket is not freshly connected` or `active
monitored name incarnation` guard with complete_held in the first exception's
actual traceback, zero watched ACK and the original tracer/held-stop retention.
No ledger, name, receipt, return value or trace bytes are changed. This tests
external destruction of a genuine endpoint, not listener-path replacement.
The export is an actor effect, never an observer FD-lineage admission or RPC
receipt. Its applicability to the accepted invariant, exact Go build, daemon
name-departure timing and kernel behavior require ROOT independent review and
execution. Any earlier reader/parser/RPC error or deadline makes it FAIL.

Ordinary unheld restart: frozen dispatch_event supplies PTRACE_SYSCALL/CONT or
LISTEN and zero/a valid wait signal. ptrace_restart treats ESRCH as success.
For the inspected upstream Linux implementation, missing/dead/nonstopped tasks
produce ESRCH; CONT/SYSCALL reject invalid signals, which the frozen actors do
not supply. LISTEN's seized event-stop requirement is established by the actual
wait event. SIGCONT schedules notification rather than rewriting that event's
siginfo. Hence ordinary actor exit/kill/stop scheduling supplies no non-ESRCH
failure trigger under these interfaces. This is a source inference bounded by
those kernel paths, not a claim about an uninspected Ubuntu kernel defect.
See [upstream ptrace.c](https://raw.githubusercontent.com/torvalds/linux/v6.17/kernel/ptrace.c)
and [signal handling](https://raw.githubusercontent.com/torvalds/linux/v6.8/kernel/signal.c).
Minimal ROOT review candidate: retain this reachability argument and separately
witness an original interrupted-with-held-stops cleanup/refusal at a real stop,
with first error, zero ACK, original retained tracer custody and every owned
join. That preserves the cleanup invariant but DOES NOT cover the requested
ordinary restart origin. Only ROOT may approve an explicit contract disposition;
no errno interposition, invalid-signal injection, ESRCH or allocator substitution
is implemented or counted for the missing case.

Trace-output EOF: PassiveStrace closes its parent writer after Popen; the live
tracer owns both the inherited pipe writer and the opened /proc/self/fd output
reference. EOF requires every writer reference closed. The frozen ACK channel
has no writer-close operation. C refusal/output errors retain the output and
held custody; die/cleanup/detach reach observer_cleanup_guard before exit or
writer teardown. Killing the tracer unlinks ptrace custody; a remaining group
stop cannot satisfy ORIGINAL tracer identity and held-stop checks. Closing the
reader is a read error. No authorized actor operation can close only these
tracer-owned writers while retaining the original ptrace relationship.
Minimal ROOT review candidate: a separately reviewed finite TEST observer
operation closing BOTH actual writer references at an existing genuine held
stop, without a restart, ACK, fabricated record or metadata admission. Then
use the unchanged original pump EOF/first-error and held-stop guard plus owned
joins. It would change a frozen C interface, so it is not implemented here.
The requested EOF path remains missing; deadlines/reader errors are not coverage.

R1368-F1: the real tracer-identity fault also gates its direct kill; fixture-owned
force_contain, catch-all tracer kill and adopted-child
watchdog kill now require successful fresh PID1 containment, the actual
namespace supervisor join, and fresh whole-namespace absence. Failure records
unresolved cleanup and preserves live tracer streams/FDs/custody. The watchdog
requires a real joined supervisor receipt or an actual adopted supervisor wait;
missing qualification/join cannot authorize a catch-all kill. ROOT must resolve
this sequence using actual Linux evidence (held exit events can impede joins).
The READONLY native PassiveStrace constructor still calls its own force_contain
before an external supervisor join. It is outside the fixture-owned fix and
remains a ROOT review risk; this handoff does not claim a global all-routes fix.

R1368-F2: setup passes require observed actor allocation/birth and the exact
original readiness/open/spawn failure origin. First exception, raw readiness
bytes/EOF and cleanup BEFORE outer compensation are retained. Missing actor,
import or unrelated failure is NOT_RUN/FAIL, even with complete cleanup. Only
an actual qualified old hang/survivor or boundary-bound descriptor leak can be
RED. The exact old executor ef77db92 is recovered byte-for-byte from the retained
first-implementation.patch and exported with its origin/hash, without weakening
NEW pins. OLD native remains pinned to 07047821; independent executor review is
ROOT PENDING, not asserted here. Do not run OLD with the NEW module by accident.

R1368-F3: control-eof now requires ORIGINAL dispatch_held recvmsg returning real
EOF, its exact first error and traceback origin, and the unique genuine C
`control EOF` refusal diagnostic. An original credential deadline, prerequisite
or unrelated stream failure cannot qualify it. Original publication lock and
fresh held-stop checks remain untouched.


## R1379 F1 correction and authoritative scope disposition

The preceding R1368/R1373 notes are historical input findings. R1379 implements a
candidate correction of F1-pre-supervisor, F1-standalone and F1-fatal-order-cycle
at the existing Python containment/C cleanup seams. SOURCE acceptance awaits
independent review; RuntimeProof PENDING, native HOLD. Actual target kernel
behavior remains UNKNOWN; no confirmed runtime failure or cleanup PASS is
claimed. Exact changed-observer provenance and complete LOC are in the generated
R1379 handoff/pins, with entry snapshots and reconstruction patches in tmp.

R1384 SOURCE REJECT is binding: R1379 siginfo/eventmsg did not authenticate EXIT.
R1387 retains actual kernel wait statuses before fatal checks/dispatch and uses
only WIFSTOPPED(status) plus (unsigned status >> 16) == PTRACE_EVENT_EXIT for
terminal CONT, bound to existing TCB/actual clone-child birth custody. Fresh
birth and GETSIGINFO/GETEVENTMSG are supplementary checks. It consumes that
authority before the attempt and consumes real __WALL final waits. Only ECHILD
ends this refusing tracer; a SIGKILL request, missing parser, EOF or ESRCH cannot
authorize its exit. No ordinary restart,
ACK, detach or foreign/unknown stop gains authority. Already consumed EXIT
notifications survive current/queued dispatch. Restarts, subsequent waits, death,
TCB retirement/reuse and exec remapping invalidate prior stop authority.

Native force_contain first refuses/preserves the first failure, freshly qualifies
owned private PID1 and requests fatal namespace containment, then waits the
tracer's actual terminal reaping before namespace absence and owner supervisor
join. Constructor/provisional Popen custody never licenses a tracer kill.
Standalone generic cleanup excludes the tracer from child kills even after
failed group/task checks; it freshly inventories traced owned-group threads,
requests containment and waits ptrace teardown before actor joins. Integrated
and adopted watchdog cleanup retain full completion/absence gates. Failed gates
preserve unresolved live tracer streams/FDs and do not PASS. The original final
ACK/publication locks, callback/roundtrip schedule, readiness0x5f and first-error
classifiers remain byte/AST equivalent to input outside these terminal seams.

Primary upstream v6.17 signal.c ptrace_do_notify and exit.c do_exit support the
EXIT-event interpretation; pid_namespace.c zap_pid_ns_processes documents the
externally traced-child reaping dependency. These are source inferences, not
exact Ubuntu-kernel qualification. ROOT must independently review and execute
all accepted OLD_RED/NEW_GREEN/Linux obligations after source acceptance.

Disposition02c839c0 supersedes the earlier required-origin/impossibility language
ONLY for the two compound coverage proposals. Their status stays NOT_RUN/
INCOMPLETE; they are not separately enumerated acceptance cases. No source or
case is deleted, substituted or credited as PASS, and no impossibility claim is
accepted. All accepted EOF/error/deadline/held cleanup and enumerated real Linux
behaviors remain mandatory. Complete coherent size acceptance remains a ROOT
review concern; R1387 measures from coherent TEST4970 with required docs included,
and grants no LOC exception. Current handoff records exact bytes/BUILD_ONLY
compilation. SOURCE acceptance PENDING, RuntimeProof PENDING, target UNKNOWN,
native NOT_RUN/HOLD. ROOT refreshed the integrated driver's C source pin to
f5aad706; the current observer ELF remains unqualified for this correction.

## R1396 diagnostic refusal and retained cleanup handoff

R1389 rejected late cleanup; R1394 found first debug-error ordinary restart.
observer_check now refuses sticky stderr errors before ordinary dispatch/ACK;
the terminal guard still gates normal cleanup, interrupt/detach helpers and final
tracer output before a dependent release or ordinary exit. Genuine waits precede
debug output; ordinary detach/CONT attempts consume their retained stop first.
The accepted raw EXIT discriminator, fresh birth binding and terminal reap remain.
Both existing Python C pins alone are refreshed; Python behavior is unchanged.
SOURCE PENDING, RuntimeProof UNKNOWN/PENDING, target UNKNOWN, current ELF UNQUALIFIED,
native NOT_RUN/HOLD. BUILD_ONLY is one object, never runtime or source approval.
Coherent TEST5200 =4680source +509required-docs +11dependency-metadata LOC;
prior coherent5198/increment74 remain historical. ROOT LOC acceptance is required,
with no exception. All accepted Linux/runtime/native and final join gates remain.
