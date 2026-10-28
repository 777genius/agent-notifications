"""Observe a real Pi across the normal managed runtime's executable update.

Every mutation uses the existing runtime writer or common setup wizard. The
foreign TEST retainer is a truthful shared-runtime consumer, never an authority
record manufactured by this fixture.
"""
import json


def exercise(root, profile, args, env, sink, *, fixture, product, command, setup, native):
    replacement = args.replacement_runtime_stage / args.candidate.name
    old_hash, new_hash = product.sha(args.candidate), product.sha(replacement)
    product.require(old_hash != new_hash, "runtime_replacement_requires_distinct_executable_bytes")
    old = product.installed(root / "control", profile)
    product.require(old["runtime"]["primary_sha256"] == old_hash, "runtime_replacement_initial_image_mismatch")
    policy = root / "control/agent-notifications.json"
    saved_policy = policy.read_bytes()

    def ledger():
        return json.loads((root / "control/ownership.json").read_text())

    baseline = ledger()["Consumers"]
    retainer = "TEST-pi-runtime-retainer"
    product.require(retainer not in baseline, "runtime_replacement_retainer_already_exists")

    def writer(executable, stage, consumer, remove=False):
        argv = ["internal-install-runtime", "--target", root / "runtime/bin", "--entry", args.candidate.name,
                "--control-root", root / "control", "--consumer", consumer]
        argv += ["--remove"] if remove else ["--stage", stage]
        return command(executable, argv, root, env)

    writer(args.candidate, args.runtime_stage, retainer)
    retained = ledger()["Consumers"]
    product.require(set(retained) == set(baseline) | {retainer}
                    and all(retained[key] == value for key, value in baseline.items()),
                    "runtime_replacement_retainer_changed_existing_consumers")
    case_root = root / "runtime-byte-old-A"
    provider, live = fixture.Provider("success"), None
    try:
        native_env = fixture.prepare(case_root, provider, "success", agent_dir=profile)
        live = fixture.PiProcess(args.pi_executable, case_root, native_env, "rpc")
        live.send({"id": "TEST-runtime-A-loaded", "type": "get_commands"})
        while live.next().get("id") != "TEST-runtime-A-loaded":
            pass
        foreign = [profile / name for name in ("settings.json", "models.json", "extensions/TEST-witness.ts")]
        before_foreign = {path: product.sha(path) for path in foreign}
        # The replacement writer commits actual executable bytes and truthful
        # preparation ownership. The unchanged package's normal wizard must
        # refresh derived discovery and retire only that preparation record.
        writer(replacement, args.replacement_runtime_stage, "pi-runtime-preparation")
        prepared = ledger()
        product.require(set(prepared["Consumers"]) == set(retained) | {"pi-runtime-preparation"}
                        and all(prepared["Consumers"][key] == value for key, value in retained.items()),
                        "runtime_replacement_preparation_changed_existing_consumers")
        setup("install", executable=replacement)
        current, installed = product.installed(root / "control", profile), ledger()
        executable = current["runtime"]["executable"]
        old_revision = old["runtime"]["loaded_revision"]
        new_revision = current["runtime"]["loaded_revision"]
        product.require(current["runtime"]["primary_sha256"] == new_hash
                        and new_revision["primary_sha256"] == new_hash
                        and product.sha(executable) == new_hash
                        and installed["Files"][executable]["SHA256"] == new_hash,
                        "runtime_replacement_derived_or_owned_executable_mismatch")
        product.require({key: value for key, value in old_revision.items() if key != "primary_sha256"}
                        == {key: value for key, value in new_revision.items() if key != "primary_sha256"},
                        "runtime_replacement_changed_package_or_binding_identity")
        product.require(installed["Consumers"] == retained and not installed.get("PendingMutation")
                        and policy.read_bytes() == saved_policy,
                        "runtime_replacement_handoff_changed_siblings_policy_or_preparation")
        product.require(all(path.is_file() and product.sha(path) == digest for path, digest in before_foreign.items()),
                        "runtime_replacement_changed_foreign_profile_bytes")
        live.send({"id": "TEST-runtime-A-stale", "type": "prompt", "message": "TEST say done"})
        while live.next().get("type") != "agent_settled":
            pass
        old_counts = fixture.verify_case("success", fixture.witness(case_root), provider.requests)
        live.child.stdin.close()
        product.require(live.wait() == 0 and live.stderr_bytes == 0, "runtime_replacement_old_pi_not_collected")
        sink.quiet(1)
        before_delivery = len(sink.rows)
        fresh = native("success")
        product.require(len(sink.rows) == before_delivery + 1 and fresh["webhook"] == "task_complete",
                        "runtime_replacement_fresh_pi_did_not_deliver_once")
        writer(replacement, args.replacement_runtime_stage, retainer, remove=True)
        product.require(ledger()["Consumers"] == baseline and policy.read_bytes() == saved_policy,
                        "runtime_replacement_retainer_retirement_changed_existing_authority")
        product.asset_receipts(root / "control", root / "runtime", args.assets_root)
        return {"case": "runtime-byte-replacement", "old_primary_sha256": old_hash, "new_primary_sha256": new_hash,
                "package_projection_unchanged": True, "derived_and_owned_executable_match": True,
                "old_native_counts": old_counts, "old_provider_requests": len(provider.requests),
                "old_webhook_count": 0, "old_child_collected": True,
                "fresh_native_counts": fresh["native_counts"], "fresh_provider_requests": fresh["provider_requests"],
                "fresh_webhook_count": len(sink.rows) - before_delivery, "fresh_child_collected": fresh["child_collected"],
                "foreign_bytes_preserved": True, "sibling_consumers_preserved": True, "policy_bytes_preserved": True,
                "preparation_retired": True, "test_retainer_retired": True}
    finally:
        if live:
            live.close()
        provider.close()
