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
