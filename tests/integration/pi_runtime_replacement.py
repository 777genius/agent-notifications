"""Observe a real Pi across the normal managed runtime's executable update.

Every mutation uses Cursor's existing runtime writer or common setup wizard.
Cursor is installed through the real SDK/filesystem boundary; no native Cursor
agent is started and no Cursor desktop or hook qualification is claimed.
"""
import json


def exercise(root, profile, args, env, sink, *, fixture, product, command, native):
    replacement = args.replacement_runtime_stage / args.candidate.name
    old_hash, new_hash = product.sha(args.candidate), product.sha(replacement)
    product.require(old_hash != new_hash, "runtime_replacement_requires_distinct_executable_bytes")
    old = product.installed(root / "control", profile)
    product.require(old["runtime"]["primary_sha256"] == old_hash, "runtime_replacement_initial_image_mismatch")
    policy = root / "control/agent-notifications.json"

    def ledger():
        return json.loads((root / "control/ownership.json").read_text(encoding="utf-8"))

    baseline = ledger()["Consumers"]

    def cursor(action, executable):
        profile_root = root / ".cursor"
        profile_root.mkdir(mode=0o700, exist_ok=True)
        update = action == "update"
        argv = ["setup-notifications", "wizard", "--action", "install" if update else action, "--agents", "cursor",
                "--hooks", "false", "--agent-notify", "true", "--package", args.package if action == "install" else args.updated_package,
                "--control-root", root / "control", "--runtime-root", root / "runtime",
                "--global-config", root / "global/config.json", "--scope-root", profile_root,
                "--client-executable", executable, "--yes", "--json"]
        if update:
            argv += ["--install-or-update"]
        value = json.loads(command(executable, argv, root, env))
        product.require(value.get("outcome") in ("completed", "unchanged"), "runtime_replacement_cursor_setup_failed")

    def writer(executable, stage, consumer, remove=False):
        argv = ["internal-install-runtime", "--target", root / "runtime/bin", "--entry", args.candidate.name,
                "--control-root", root / "control", "--consumer", consumer]
        argv += ["--remove"] if remove else ["--stage", stage]
        return command(executable, argv, root, env)

    cursor("install", args.candidate)
    retained = ledger()["Consumers"]
    cursor_keys = [key for key, value in retained.items() if value.get("Registration")
                   and json.loads(value["Registration"]).get("integration") == "cursor"]
    product.require(len(cursor_keys) == 1 and set(retained) == set(baseline) | set(cursor_keys)
                    and all(retained[key] == value for key, value in baseline.items()),
                    "runtime_replacement_cursor_changed_existing_consumers")
    saved_policy = policy.read_bytes()
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
        # This is prepare_cursor_runtime's exact existing writer route. Pi
        # discovery must already be fresh before Cursor's integration update;
        # no Pi setup, metadata repair or artificial retainer participates.
        writer(replacement, args.replacement_runtime_stage, "claude-hooks")
        prepared = ledger()
        product.require(set(prepared["Consumers"]) == set(retained) | {"claude-hooks"}
                        and all(prepared["Consumers"][key] == value for key, value in retained.items()),
                        "runtime_replacement_cursor_writer_changed_existing_consumers")
        product.installed(root / "control", profile)
        cursor("update", replacement)
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
        product.require(installed["Consumers"] == prepared["Consumers"] and not installed.get("PendingMutation")
                        and policy.read_bytes() == saved_policy,
                        "runtime_replacement_cursor_update_changed_siblings_or_policy")
        product.require(all(path.is_file() and product.sha(path) == digest for path, digest in before_foreign.items()),
                        "runtime_replacement_changed_foreign_profile_bytes")
        cursor_consumers = {key: installed["Consumers"][key] for key in cursor_keys}

        def cursor_files():
            return {str(path.relative_to(root / ".cursor")): product.sha(path)
                    for path in (root / ".cursor").rglob("*") if path.is_file()}

        saved_cursor_files = cursor_files()
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
        result = {"case": "cursor-only-shared-runtime-replacement", "old_primary_sha256": old_hash, "new_primary_sha256": new_hash,
                "package_projection_unchanged": True, "derived_and_owned_executable_match": True,
                "old_native_counts": old_counts, "old_provider_requests": len(provider.requests),
                "old_webhook_count": 0, "old_child_collected": True,
                "fresh_native_counts": fresh["native_counts"], "fresh_provider_requests": fresh["provider_requests"],
                "fresh_webhook_count": len(sink.rows) - before_delivery, "fresh_child_collected": fresh["child_collected"],
                "foreign_bytes_preserved": True, "sibling_consumers_preserved": True, "policy_bytes_preserved": True,
                "pi_setup_repeated": False, "real_cursor_sdk_lifecycle": True,
                "cursor_native_qualified": False, "cursor_and_writer_retired": False,
                "cursor_preserved_during_pi_lifecycle": False}

        def retire_cursor():
            # The caller first exercises Pi-only update/repair/removal while
            # Cursor stays installed. Retire only Cursor and its byte writer.
            consumers = ledger()["Consumers"]
            product.require(all(consumers.get(key) == value for key, value in cursor_consumers.items())
                            and cursor_files() == saved_cursor_files,
                            "pi_only_lifecycle_changed_retained_cursor_binding_or_files")
            result["cursor_preserved_during_pi_lifecycle"] = True
            expected = {key: value for key, value in consumers.items()
                        if key not in cursor_keys and key != "claude-hooks"}
            cursor("uninstall", replacement)
            writer(replacement, args.replacement_runtime_stage, "claude-hooks", remove=True)
            product.require(ledger()["Consumers"] == expected,
                            "runtime_replacement_cursor_retirement_changed_existing_authority")
            result["cursor_and_writer_retired"] = True

        return result, retire_cursor
    finally:
        if live:
            live.close()
        provider.close()
