# Linux portal TEST owner fence

Status: source-only, not executed or natively qualified. Host bootstrap remains
unqualified. This does not change a production adapter or dependency.

The opt-in `--restart-owner-fence-test` is exclusive with the existing restart
counterexample, recovery and Wayland modes. Existing counterexample/recovery
lanes retain their semantics and evidence.

Before AddNotification, each sender exclusively creates a bounded read-only
sidecar containing its original private bus GUID, notification unique owner,
portal frontend unique owner and immutable spec hash. A post-Add comparison
only provides conservative confidence; it does not establish atomic binding.
Each fresh callback compares public GetId/GetNameOwner results with the original
binding within its new continuous-clock budget before the exclusive TEST effect.

The new lane sends A, collects its sender, restarts only owned dunst, then sends B
and collects its sender. Numeric ID reuse is required. The normal `--remove` helper entry for an owner-fence fixture
refuses stale removal and never calls RemoveNotification, even if an owner check
would match. An explicit direct TEST ActivateAction(A), not a genuine click,
checks cold A rejection and zero A effects. Its outcome and exit must be known
before proceeding. B's XRes-bound window must remain; fresh provider checks and
zero observed Remove/Close precede exactly one genuine owned XTest B click.
The lane requires two native Notify calls, zero Remove/Close methods, one genuine
click and one cold B effect. Unknown outcomes stop without click or replay.

`ownerFenceQualified` is scoped to this TEST policy and requires the native
scenario plus child collection, immutable-input checks and outer exact-container
cleanup. It never qualifies GTK's generic restart behavior, atomic owner binding,
Wayland activation, an actual client route or chat rendering. Raw public D-Bus
observations and source/artifact hashes remain available for independent review.

Validation here is Python AST parsing and git diff checks only. Native execution
requires a separately admitted isolated TEST host and reviewed exact source.

The new PR CI lane is source-only until actual execution is retained. It uses a
fresh Ubuntu 24.04 GitHub runner and invokes the existing outer native probe once
with `--restart-owner-fence-test`, without retries. The controller verifies fixed
publisher asset/checksum digests and the annotated 1.22.1 tag commit through `gh`;
per-file Git blob comparison is not repeated. Only those public source acquisitions
receive the job token. Python/Docker/native children receive a minimal private
environment, owned HOME/TMPDIR and no GitHub/auth command-file variables.

The 1950-second outer deadline covers the existing 1800-second build, 60-second
native scenario and bounded cleanup. Timeout preserves partial streams, fails and
marks actor outcome unknown; the controller neither retries nor guesses external
cleanup/quiescence. A passing result requires the runner's source/immutable checks,
known collection, native stale-A refusal with no Remove/Close/effect, exactly one
genuine XTest B click/cold effect, and verified exact-container removal/absence.
Artifacts are limited to owned reports/logs/TEST receipts and frozen public source
inputs. Backend generic restart, Wayland, atomic binding and client navigation
remain unqualified. Local validation of this CI patch is strict TypeScript only;
no local image build, notification or native fixture execution is claimed.
