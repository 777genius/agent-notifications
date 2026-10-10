# R1387 bounded SOURCE handoff

Task DONE. SOURCE_FULL_FINITE_MATRIX_IMPLEMENTED **INCOMPLETE**. RuntimeProof
PENDING, nativeEligibility HOLD. Every kernel outcome below is NOT_RUN in the
provider. Syntax, pure checks, C builds and historical receipts are BUILD_ONLY
or PARTIAL. They cannot establish the whole contract or authorize native/E38.

The standalone `integrated_pair.py` constructs ORIGINAL OwnedNotificationsSession
and PassiveStrace together. `linux_contract.py --original-pair` delegates to it.
Original monitor, decoder, independent credential dispatcher, FD ledger, fresh
proc guards, checkpoints/ACK ABI, clocks and admission assertions retain the
exact e36 input behavior. R1379 changes ONLY the existing forced terminal
teardown seams. R1384 rejected its siginfo-based EXIT authority; R1387 corrects
only the C wait/TCB cleanup seam. Current C pins and BUILD_ONLY evidence are in
test-writer-handoff-r1387.json; independent SOURCE acceptance is still PENDING.
No native main, product, CI, vendor or real user project runs. Original 13-key schemas, target, consent/delivery, semantic
guards and unconditional require_native_observation_contract HOLD retain their
entry semantics; static preservation evidence is in the R1379 handoff. The driver does not add a Notify admission gate or a monitor
self-wait, and failed Hello never receives fallback credentials.

## Qualifications and build bindings

Root runs only after independent source review, outside the provider, on the
original qualified Ubuntu24.04.5/Linux-amd64/6.17.0-1022-azure/ext4 tuple and exact
approved machine ID. Original PID1 namespace, UID/GID1000, cleared groups,
loopback-only network namespace, ordinary stock packages/services, executable,
argv, cwd, HOME and fd0 guards remain binding. A private mount namespace maps
unique own workspace `tmp-display` storage to original /tmp display paths;
TMPDIR, caches and every build output stay under verified own /srv/workers
scratch. Root must prepare an own layout traversable by UID1000; the driver
refuses inaccessible ancestors and never changes their permissions or existing
homes. Runtime resources are registered before fallible qualification.

The provider observes cc/make/strace/dbus-daemon/dbus-monitor/dbus-send. Go and
dunst are absent on its PATH. This is not a SOURCE blocker or a claim that every
external path is absent. Root has separately prepared an official Go1.25.8
linux/amd64 toolchain; its actor build/execution remains ROOT PENDING. The actual
isolated helper prerequisite is Go1.25.8 + godbus/v5 v5.2.2 + x/sys v0.27.0, pinned
in matrix-go.mod/sum. Original session prerequisites additionally include real
dunst, Xvfb, xauth, dbus-send, nsenter and setpriv. No installation occurs here.

Root builds the exact reviewed three C sources into an honestly qualified derived
strace6.8 ELF. The stock 6.8-0ubuntu2 package is checked separately. Historical
ELF receipts cannot replace a current source/binary binding. All paths below must
be absolute. The build helper records source/driver/compiler/Go/ELF hashes,
actual Go module build info and joined build children/streams. It executes no
actor or kernel case. It also builds the held-child setup fixture.

```sh
python3 -B contract-fixtures-r1364/build_fixtures.py \
  --go /ABS/OWN/GO1.25.8/bin/go --compiler /ABS/cc \
  --derived /ABS/REVIEWED/derived-strace --derived-sha256 EXACT_ELF_SHA256 \
  --workspace /srv/workers/OWN/WORKSPACE \
  --scratch-parent /srv/workers/OWN/WORKSPACE/scratch

python3 -B linux_contract.py --original-pair --case clone-orders \
  --machine-id ROOT_APPROVED_ORIGINAL_MACHINE_ID \
  --derived /ABS/REVIEWED/derived-strace --derived-sha256 EXACT_ELF_SHA256 \
  --helper /ABS/BUILD/TEST-matrix-helper --helper-sha256 EXACT_HELPER_SHA256 \
  --actor /ABS/BUILD/TEST-matrix-actor --actor-sha256 EXACT_ACTOR_SHA256 \
  --build-bindings /ABS/BUILD/fixture-build-bindings.json \
  --workspace /srv/workers/OWN/WORKSPACE \
  --scratch-parent /srv/workers/OWN/WORKSPACE/scratch
```

Each named case is explicit; there is no automatic suite, global continuation,
restart/resync or retry after refusal. Original T0+110 work/cleanup clocks remain
binding, with T0+115 failure containment and an independent 118-second watchdog.
All readiness, event pauses and no-progress observations have absolute bounds.
The watchdog cannot turn missing evidence into PASS.

## Executable finite cases and required observations

- `clone-orders`: 64 genuine joined forks through the original paired-stop
  dispatcher. Actual parent/child wait bytes are bound to real request births
  and checkpoints. Both orders must appear; ambiguity, numeric TID reuse or
  missing order evidence refuses qualification. No order is synthesized.
- `fork`, `thread`, `vfork-exit`, `vfork-exec`: actual leader-specific clone flags,
  group/table relation and accepted original stop. vfork return is anchored to
  the actual parent's line after its checkpoint and must equal the admitted
  child's actual local/outer PID. Original terminal history must reconcile.
- `execve`, `execveat`, `exec-tid-remap`: original exec proc/exe/argv/HOME/fd0
  admission, actual opcode/held record, actual ordered dup/read/EOF, consumed
  bytes and real remap history. `wrong-home`, `wrong-exe`, `wrong-fd0` require the
  corresponding original live guard failure with an unacknowledged real stop.
- `bus-close`, `delayed-controls`: real godbus Dial/Auth/Hello, original sole
  connection/name/socket, real PID and UID controls independently stopped and
  resumed, both original roundtrips consumed, name departure and natural joins.
- `connect-refused`: the genuine owned socket path is moved only after original
  connect-entry intent. The actual kernel return must be ENOENT; restore the same
  original inode after consuming that return. No connect/credential success is
  invented. `auth-absent`, `auth-refused`, `hello-absent`, `hello-refused` require
  original refusal before destruction ACK. Failed first Hello sends a genuine
  invalid-argument call to the standard daemon and requires its actual monitored
  ERROR reply plus the real godbus error. `double-name` refuses a second actual
  connection; no failed-Hello fallback is allowed.
- `alias-close`, `dup2`, `dup3`, `shutdown`, `close-range-unshare`, `exit-group`:
  real destructive syscalls and original lifetime receipts. `shared-close`,
  `shared-alias-close`, `shared-dup2`, `shared-dup3`, `shared-shutdown`,
  `shared-close-range-unshare` execute in an independently locked Go OS thread;
  the actual TID must differ from connect intent and use the original shared
  table. `thread-exit` requires the original surviving-owner/table guard.
  `last-table-exit` actually unshares without closing the socket, then exits:
  actual kcmp must differ and original last-table refusal must retain the stop.
  `owner-exit` requires credential ACK before owner exit and refusal of subsequent
  destructive authority while another actual thread survives.
- `wrong-birth`: misbind one genuine request to a different actual owned live
  birth and call ORIGINAL ack_held; require original held-task-birth refusal,
  not corrupt ACK bytes. `wrong-table`: after actual original credentials, pass
  a different real original table in the held context to ORIGINAL complete_held;
  actual kcmp and original table-incarnation refusal are required. No original
  ledger/task/socket metadata is replaced. `wrong-name` sends a real signal from
  the wrong actual sender and requires original NameOwnerChanged authority
  refusal. `wrong-socket` replaces the actual owned listener inode at the same
  path/mode/UID/GID and requires original assert_continuous refusal. These last
  two guards are narrower than final dispatcher incarnation drift (missing below).
- `control-eof`, `monitor-eof`, `credential-deadline`: actual original channel/
  monitor loss or blocked monitor, first original failure and zero watched ACK/
  restart. `pid-control-eof`, `uid-control-eof` kill the independently qualified
  actual original dbus-send child and require its consumed empty stdout/nonzero
  join and original control refusal. `output-reader-failure`,
  `debug-reader-failure` close the actual own FileIO between original reads;
  the original pump must publish its real read error. `output-reader-deadline`
  pauses a genuine read return before any original lock and requires the actual
  original checkpoint deadline. `diagnostic-eof` observes real original Xvfb
  stderr EOF after actual own service death and original continuity refusal;
  this does not claim isolated EOF is itself an error in diagnostic_pump.
- `fatal-die`: apply allocation pressure only to the freshly qualified own TEST
  tracer while another real stop is held, then have another real original
  descendant read /dev/zero. Require actual C Out-of-memory diagnostic, cleanup
  guard channel refusal, live retained tracer and original held stop. An absent
  fatal diagnostic or a different failure cannot qualify this case. This does
  not cover ordinary unheld ptrace_restart failure.
- `session-child-failure`, `session-fd-failure`, `session-netfd-failure`,
  `session-identity-failure`, `tracer-child-failure`, `tracer-identity-failure`:
  real own child exit/death, real namespace-open/allocation failure or failed
  qualification. `session-thread-start-1` through `-8` and
  `tracer-thread-start-1` through `-3` lower only this disposable TEST process's
  address-space ceiling at the selected actual original thread start, restore
  it immediately, and require real pthread RuntimeError. Cached-stack success
  refuses fault qualification. Before outer compensation, require every original
  child/FD/worker/task absence, first-error publication and no unresolved cleanup
  notes. Later containment cannot hide an original constructor defect.
- `diagnostic-race` retains the original exclusive-open failure/publication-lock
  probe. `final-ack-race` first completes original fresh credentials and both
  independently consumed roundtrips, pauses before final ACK with no original
  lock held, then lets that real original diagnostic failure win publication.
  Release/join the original worker and require first-error identity, retained
  stop and zero watched send/restart. All pauses are bounded and released in
  finally. No original method or byte stream is mocked.

## Same five setup regressions and OLD binding

Root's R1367 packet reports actual OLD RED (stall-ready watchdog/owned survivor,
other FD leaks), NEW 3/5 GREEN_SETUP_ONLY, and RED stderr-open/spawn-failure with
cleanup exceptions despite eventual joins/FD closure/PID absence. Packet NEW
stdout digest is c246dc4c6888d13be458e2e8a2b077a1b39ffebacd0eda1ee9368567737914e2.
The post-group-kill live-identity race is SOURCE INFERENCE, not a reproduced
exception cause. This change skips redundant admission of an overlapping
unreaped own Popen/task, checks that exact stored birth, contains other admitted
live tasks before group kill, and validates the root's actual birth/parent/group/
session tuple for own-child cleanup. Unknown errors and reuse still remain RED.
Root must rerun all five; no NEW five-case GREEN is claimed here.

```sh
python3 -B contract-fixtures-r1364/lifecycle.py \
  --harness /ABS/NEW/implementation/linux_contract.py \
  --harness-sha256 EXACT_CURRENT_LINUX_SHA256 \
  --native-sha256 6711958e74161cc2bbc0c71404694dedcddb5cdf8dbe486cfb70ef1a919af240 \
  --fixture /ABS/BUILD/TEST-held-child --fixture-sha256 EXACT_HELD_ELF_SHA256 \
  --workspace /srv/workers/OWN/WORKSPACE \
  --scratch-parent /srv/workers/OWN/WORKSPACE/scratch --expected NEW_GREEN
```

Run the same command with `--expected OLD_RED`, the preserved original harness
SHA ef77db92d6adc58b6dbf18c52ab3085118981ae15a2824fa46add7e7ef467752 and
its exact separately supplied original imported native SHA. Both imports and
fixture ELF are pinned before launch. Missing input/import/startup errors never
count as OLD RED. OLD evidence requires a watchdog plus actual owned survivor or
an observable descriptor leak, followed by independently joined containment.

The original-pair constructor/publication comparison also has an executable
`--comparison OLD --original-source-root /ABS/PRESERVED/OLD` binding. It requires
exact native07047821/C7953842c/defs99140574/syscall390ff1bc sources and the separately
reviewed matching OLD ELF/build-only receipt; build_fixtures.py accepts those
same options. OLD is limited to constructor cases and diagnostic/final-ACK races.
Only actually observed pre-outer child/FD/worker/task leaks or actual publication
across the lock can produce OBSERVED_OLD_RED_BOUNDARY. A different exception,
missing method, pin/build/qualification failure or clean constructor refusal
cannot. Compensation is explicitly separate from original cleanup. Compatibility
with the exact preserved OLD module and independent review remain ROOT PENDING;
this provider does not substitute a different historical patch stage.

## Cleanup receipts and remaining SOURCE

Every per-case positive or refusal receipt requires original first failure,
qualified held stop/no watched ACK on refusal, own namespace containment before
tracer kill, all child/pump/credential-worker joins, closed streams/FDs and fresh
owned-birth/whole-namespace absence. Natural completion and forced containment
are separate. Emergency cleanup uses actual kernel parent/adopted-child custody,
not a PID file alone, and cannot convert a missing original receipt, watchdog,
unknown cleanup error or survivor into PASS. Source/binary bindings and genuine
trace/debug/monitor bytes accompany each receipt. Whole-contract status remains
INCOMPLETE even when an individual case passes.

Exact missing SOURCE obligations:

1. Genuine ordinary **unheld** ptrace_restart failure while another original stop
   remains in custody, with that separately witnessed fatal cleanup route.
2. Original live trace-output **EOF** trigger with held-stop retention. The actual
   read errors/checkpoint deadline and service/monitor EOF cases are distinct.
3. Final dispatcher name/socket incarnation drift after consumed credentials.
   Actual sender/path refusals above exercise earlier original guards and must
   not substitute the final incarnation revalidation case.

These gaps prevent SOURCE_FULL_FINITE_MATRIX_IMPLEMENTED. Root actor build,
actual both-order witnessing, exact independent review, real OLD/NEW matrix,
whole kernel proof and eventual native eligibility are separately ROOT PENDING.
No compiler-absence, informal 30-minute budget, owner approval or unavailable
external artifact copy is used as a SOURCE stopping condition.


## R1373 source-only correction handoff

Current source disposition is INCOMPLETE with two structurally unsupported
frozen-interface triggers, ordinary non-ESRCH restart and trace-output EOF.
REMAINING_SOURCE_REVIEW.md records their source/OS reasons and minimal ROOT
review candidates. It also records the finite real `final-incarnation-drift`
actor/dispatcher scheduling and its strict original final-guard qualification.
Use the same original-pair invocation above with `--case final-incarnation-drift`
after ROOT rebuilds the current helper/controller/source-bound fixtures. The
existing stage channel transports a real socket duplicate only; neither its
control reply nor helper evidence admits a task, connection, credential or ACK.

Fixture cleanup gates now preserve tracer custody on failed containment/join.
The frozen original constructor's own force_contain route still needs ROOT
review. Setup receipt qualification preserves exact first error, actual actor
allocation, exact original failure boundary and pre-outer cleanup. NEW uses the
current reviewed linux_contract.py and native11f3cd87 pins; OLD uses executor
ef77db92 and native07047821, exported/reviewed independently by ROOT. Missing
inputs are NOT_RUN/FAIL and cannot become OLD RED or NEW GREEN. Control EOF must
show original recvmsg EOF, original first-error origin and real C refusal.

Provider checks are syntax, five pure receipt-classifier tests and two strict
C fixture builds only. Go helper build, original-pair cases, cleanup sequence,
OLD/NEW setup comparison, kernel/matrix/native/CI and independent final review
remain ROOT responsibilities. No native counter/budget reset or FULL claim.


## R1379 teardown candidate with R1387 C wait-authority correction

SOURCE awaits independent review. RuntimeProof PENDING; native HOLD. Target
kernel outcomes are UNKNOWN/NOT_RUN. This correction changes the existing TEST
observer: prior C6d2216a5/native11f3cd87 pins describe the input, never the NEW
build. defs.h, syscall.c, Go helper and exact OLD module/executor remain readonly.
A NEW derived executable and current source/fixture build bindings must be
prepared by ROOT after acceptance; no historical ELF is relabelled current.

Forced cleanup first publishes/refuses the original first failure. Fresh owned
PID1 birth, private namespace, UID/GID/groups/maps and TracerPid bookends permit
its fatal signal. The corrected C guard requires retained actual kernel wait status:
WIFSTOPPED(status) and (unsigned status >> 16) == PTRACE_EVENT_EXIT, bound to
the owned TCB/clone-child birth. GETSIGINFO/GETEVENTMSG and fresh birth bookends
are supplementary. It consumes that authority before a terminal CONT attempt
and performs real __WALL waits through ECHILD. It never
sends ACK, executes an ordinary syscall restart, detaches an uncontained stop or
exits merely because SIGKILL was requested. Wait capture precedes fatal checks
and preserves current/queued EXIT notifications. Restarts, later waits, death,
TCB retirement/reuse and exec remapping invalidate old authority. Known
provisional TCBs and actual clone child references are cleanup
custody, never admission. Unknown nonterminal stops remain held on failed proof.

The original constructor now waits its unreaped own tracer, proves actual
namespace/thread absence, and closes/joins every retained FD/stream/worker.
Standalone cleanup refuses the channel, freshly discovers qualified traced
threads in the original own group, contains them, and waits the tracer before
actor joins. No generic child kill can kill that tracer after a failed check.
Integrated cleanup uses the same original terminal sequence before supervisor
join. The watchdog receives C terminal reaping after real owner/channel loss;
its adopted-child kill gate still requires actual supervisor join and namespace
absence. Failed proof or wait retains live tracer FDs/custody and reports
incomplete cleanup while keeping the first error. Forced and natural receipts
remain distinct. All final own children, workers, namespace and supervisor joins
remain mandatory; no signal or source check substitutes for those observations.

ROOT disposition02c839c0 governs the two retained compound origin proposals:
ordinary unheld non-ESRCH restart with a distinct held stop, and live output EOF
by closing all writers while stops remain retained. They remain NOT_RUN/
INCOMPLETE additional coverage, without substitution, PASS or impossibility
claims. No errno or writer-close operation was introduced for them. All accepted
EOF/error/deadline refusal and enumerated Linux cases still require real proof.

R1387 refreshes only build_fixtures.py's existing C source hash; native remains
6711958e. ROOT refreshed integrated_pair.py's existing C source pin to f5aad706
after this writer handoff; no driver behavior changed. A newly reviewed observer
ELF remains required before runtime execution. Go/helper/
actor source and existing ELF lineage are unchanged: reuse ROOT's independently
verified existing helper1828667794e57e32f63e33441f43ea9aaeaf9293bf2a3351612cd710563c5c45
and actor artifacts. No Go toolchain/download/build or actor recompilation was
performed or is required by this correction. Historical build receipts are not
silently relabelled as a NEW observer build.

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

## Phase80 transport history and Phase84 SOURCE correction

ROOT accepted C656ccbcd/133140B via R1400 and the measured coherent TEST5200 exception; prior PENDING wording is historical.
C80 workflow/MATRIX were rejected by independent HIGH79 F1-F6; that history is preserved. The corrected existing `cursor-installed-e` reads exactly21 mode100644 blobs with exact length/hash/readback from trusted `${{ github.sha }}` (`CURSOR_FIXTURE_SHA`), fetched through the retained route under consume authorization. ROOT publication and actual public readback of those21 paths plus workflow are later effects. Product9f014e72 stays distinct.
Fresh SAME-job physical/P1 commands reuse original Capture/Revalidate, `-count=1` and required positive markers; any failed command or NOT_RUN positive refuses. Only then is actual nonempty machine ID bound to those logs/tuple, carried unchanged as `CURSOR_PROOF_MACHINE_ID` and rechecked before proof effects. Existing / and /srv are qualified without links; only absent /srv/workers is created root:root0755, with no repair of existing ancestors. Unique owned source/scratch remain UID1000-traversable/ext4. Full fresh official NEW/OLD builds explicitly bind recorded resolved gcc as CC for configure/make and retain patch/config/compiler/link/ELF lineage; stock strace remains separate.
Commands are unchanged: `build_fixtures.py` NEW and OLD (`--comparison OLD --original-source-root`), lower `linux_contract.py --derived --derived-sha256` (stock passive OLD, four NEW modes,11 invalid ACKs plus EOF/deadline), `lifecycle.py` NEW_GREEN/OLD_RED (five each), and `linux_contract.py --original-pair` NEW68/OLD19 with matching ELF/helper/actor/build-bindings and source root. No receipt is generated inside contract-fixtures.
Job210/proof190 retains all110/115/118-second internal caps and all87 cases. The492s preparation pool is checked before dependent work; it never kills a tracing/fixture cleanup supervisor. The124s pair scheduling allowance accounts118s watchdog +4s emergency +2s wait, without an outer direct-supervisor SIGKILL. Observed qualified fit remains UNKNOWN/NOT_RUN; synchronous close/fsync/replace/Popen and ordinary I/O are not claimed wall-bounded. The wrapper preserves the first error and joins any launched tracing/pair cleanup owner before exceptional exit with streams open, without signaling that supervisor. After command exit receipt readback it retains the exact elapsed used for the124s timing decision, with an explicit measurement boundary before decision-receipt publication. A pair over124s is LATE/FAIL; remaining budget is rechecked after decision-receipt readback before dependent work. Missing/unproved cleanup remains UNKNOWN. Proof admission still requires exact source/readback and fresh SAME physical/P1 qualification, actual timely receipts for all68 NEW+19 OLD, original targets/noReplace/first-failure/noACK/zeroresume, and genuine all child/thread/credential-worker/FD/namespace/supervisor joins; direct-child exit alone proves none of those joins. Hard190/210 termination may leave RUNNING/UNKNOWN; no unattended retry or inferred joins is admitted. The120s cleanup/export reserve and overall caps are unchanged. Arithmetic492+87*124+120=11400s and13+190+3+3=209min is historical scheduling arithmetic, not proof of full fit. Non-tracing tools use exact owned session-group cancellation and bounded direct-child waits; descendant joins remain UNKNOWN unless original driver receipts prove them. Live supervisors finish their existing refusal/containment/terminal-wait paths; unavoidable external hard termination can leave RUNNING/UNKNOWN receipts. Fresh fit remains NOT_RUN. Required logs/receipts are exported first through the unchanged nofollow16MiB/file,1GiBtotal,16384entry copier; only selected generated configuration/patch/provenance/ELF/bindings/build streams and lower/lifecycle/pair observations follow. Complete official trees/archive/cache are excluded; missing/overbound errors remain. Owned proof setup is within try/finally and shell failure emits HOLD when executable; forced termination cannot promise finally. Proof-only always stops before existing `--execute`.
WholeContract INCOMPLETE, runtime UNKNOWN/NOT_RUN and native HOLD remain; full ELF/qualified runner/whole proof acceptance and later native readiness/carrier synchronization are separate ROOT gates.


## Phase86 bounded R1–R4 source handoff

Lower existing consumption seams now retain only bytes actually read/sent in
per-case .trace/.control/.actor.stdout files. The existing case receipt records
path/length/SHA256/disposition, control packet offset/direction and first failure;
no unread trace/actor bytes are drained for evidence. Trace/control each cap at
8MiB, actor at65536+64B and packet inventory at16384. The existing lower held
fixture source/actual compiler command/compiler digest/resulting ELF digest are
bound in held-child-build-binding.json and the lower receipt; C bytes are frozen.
Lifecycle worker.stdout/stderr retain actual communicate output before parsing,
including timeout prefixes and replacement by a complete retry (never appended).
NEW cleanup owners survive the outer observation timeout; OLD's intentional
setup hang retains original contained/adopted-child joins. Builder's six existing
label paths retain bounded actual partial communicate bytes after owned group
cancellation/bounded joins, with the original first failure and join disposition.
Lifecycle/builder streams cap at16MiB/file; cap or retention error refuses success.
Finite exports catch each lifecycle case, lower/source root and NEW/OLD provenance
failure independently, preserving logs-first, no-follow identity checks and
16MiB/file,1GiB total,16384 entries. Missing evidence remains FAIL/HOLD; trees,
archives and caches are excluded. SOURCE PENDING independent HIGH review;
Linux/native/E38 NOT_RUN, wholeContract INCOMPLETE, unconditional native HOLD.
Original compound origins remain NOT_RUN/INCOMPLETE; no final installed graph claim.
