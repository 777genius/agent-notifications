// Narrow TEST observer. Real Terminal/Shell input + external WASAPI capture.
// Reuse the existing WTS/input/audio inventory and token-query primitives.
#define _WIN32_WINNT 0x0A00
#define wmain piPrerequisiteEntry
#include "pi_native_preflight_windows.cpp"
#undef wmain
#include "navigation_windows_token_queries.h"
#include "pi_windows_shell_image.h"
#include <UIAutomation.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mfreadwrite.h>
#include <filesystem>
#include <fstream>
#include <algorithm>
#include <cwctype>
#include <stdexcept>
#include <tlhelp32.h>
#include <map>
#include <memory>

namespace fs = std::filesystem;
static constexpr uint64_t observationSeconds = 45;
// The 20ms cadence plus at most 64 retained descendant verification scans.
static constexpr unsigned processWatchScanLimit = observationSeconds * 50 + 65;
static void need(bool value, const char* code) { if (!value) throw std::runtime_error(code); }
struct NativeHRESULT : std::runtime_error {
    HRESULT value;
    explicit NativeHRESULT(HRESULT result) : std::runtime_error("native_hresult:" + std::to_string(result)), value(result) {}
};
static void hrcheck(HRESULT value) { if (FAILED(value)) throw NativeHRESULT(value); }
static std::string utf8(const std::wstring& value) {
    const int size = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    std::string result(size, 0);
    WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), result.data(), size, nullptr, nullptr);
    return result;
}
struct OwnedProcess {
    HANDLE handle = nullptr;
    DWORD pid = 0;
    ULONGLONG created = 0;
    std::wstring image;
    bool terminationRequested = false;
    DWORD terminationError = ERROR_SUCCESS;
    ~OwnedProcess() { if (handle) CloseHandle(handle); }
};
static bool running(HANDLE process) {
    const auto state = WaitForSingleObject(process, 0);
    need(state == WAIT_OBJECT_0 || state == WAIT_TIMEOUT, "owned_process_wait_unavailable");
    return state == WAIT_TIMEOUT;
}
static bool sameImage(const std::wstring& a, const std::wstring& b) {
    const auto left = fs::path(a).lexically_normal().wstring(), right = fs::path(b).lexically_normal().wstring();
    return fs::path(a).is_absolute() && fs::path(b).is_absolute()
        && CompareStringOrdinal(left.c_str(), -1, right.c_str(), -1, TRUE) == CSTR_EQUAL;
}
static void processBirth(OwnedProcess& process) {
    FILETIME created{}, exited{}, kernel{}, user{};
    need(GetProcessTimes(process.handle, &created, &exited, &kernel, &user), "owned_process_birth_unavailable");
    process.created = (static_cast<ULONGLONG>(created.dwHighDateTime) << 32) | created.dwLowDateTime;
}
static void processFacts(OwnedProcess& process) {
    processBirth(process);
    wchar_t image[32768]{}; DWORD length = 32768;
    need(QueryFullProcessImageNameW(process.handle, 0, image, &length) && length > 0 && length < 32768,
         "owned_process_image_unavailable");
    process.image.assign(image, length);
    NavigationTokenTEST::Token token, self;
    need(OpenProcessToken(process.handle, TOKEN_QUERY, &token.handle)
         && OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &self.handle), "owned_process_token_unavailable");
    const auto actual = NavigationTokenTEST::facts(token.handle), expected = NavigationTokenTEST::facts(self.handle);
    need(actual.sid == expected.sid && actual.auth == expected.auth && actual.session == expected.session,
         "owned_process_user_or_session_changed");
}
static std::unique_ptr<OwnedProcess> processIdentity(DWORD pid,
        DWORD access = PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_TERMINATE | SYNCHRONIZE) {
    need(pid != 0, "owned_process_pid_required");
    auto process = std::make_unique<OwnedProcess>(); process->pid = pid;
    process->handle = OpenProcess(access, FALSE, pid);
    if (!process->handle) {
        need(GetLastError() == ERROR_INVALID_PARAMETER, "owned_process_open_unavailable");
        return nullptr;
    }
    if (!running(process->handle)) return nullptr;
    processFacts(*process);
    if (!running(process->handle)) return nullptr;
    return process;
}
static std::unique_ptr<OwnedProcess> heldProcess(HANDLE handle, ULONGLONG expectedBirth, const std::wstring& image) {
    auto process = std::make_unique<OwnedProcess>(); process->handle = handle;
    process->pid = GetProcessId(handle); need(process->pid != 0, "owned_process_handle_invalid");
    processBirth(*process);
    need(!expectedBirth || process->created == expectedBirth, "owned_process_birth_changed");
    if (running(handle)) {
        processFacts(*process);
        need(sameImage(process->image, image), "owned_process_image_changed");
    } else {
        // This is the same retained kernel object whose live image/token were
        // verified at binding. Holding it across /quit prevents PID recycling.
        need(expectedBirth != 0, "owned_process_exited_before_binding");
        process->image = image;
    }
    return process;
}
static HANDLE duplicateHeld(HANDLE original) {
    HANDLE duplicate = nullptr;
    need(DuplicateHandle(GetCurrentProcess(), original, GetCurrentProcess(), &duplicate, 0, FALSE, DUPLICATE_SAME_ACCESS),
         "owned_process_handle_duplicate_failed");
    return duplicate;
}
static std::map<DWORD, DWORD> processParents() {
    OwnedProcess snapshot;
    snapshot.handle = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    need(snapshot.handle != INVALID_HANDLE_VALUE, "owned_process_snapshot_unavailable");
    PROCESSENTRY32W entry{}; entry.dwSize = sizeof entry;
    std::map<DWORD, DWORD> parents;
    BOOL found = Process32FirstW(snapshot.handle, &entry);
    while (found) {
        need(parents.size() < 65536, "owned_process_snapshot_bound");
        parents.emplace(entry.th32ProcessID, entry.th32ParentProcessID);
        found = Process32NextW(snapshot.handle, &entry);
    }
    need(GetLastError() == ERROR_NO_MORE_FILES, "owned_process_snapshot_incomplete");
    return parents;
}
struct TimelineProcess {
    std::unique_ptr<OwnedProcess> process;
    DWORD parent = 0;
    std::string role, imageLeaf;
    ULONGLONG firstSeen = 0, lastObserved = 0, signaledObserved = 0;
    bool signaled = false;
    DWORD exitCode = 0;
};
static void screenshot(const fs::path& path);
static std::string watchOwnedProcesses(std::unique_ptr<OwnedProcess> root, const std::wstring& notifierImage,
        const fs::path& ready, const fs::path& stop, ULONGLONG deadline) {
    const auto started = GetTickCount64(), originalBirth = root->created;
    need(deadline > started && deadline - started <= observationSeconds * 1000, "process_watch_original_deadline_required");
    need(running(root->handle), "process_watch_live_original_required");
    wchar_t system[32768]{}; const auto count = GetSystemDirectoryW(system, 32768);
    need(count > 0 && count < 32768 && fs::path(notifierImage).is_absolute(), "process_watch_image_required");
    const auto systemPowerShell = (fs::path(system) / L"WindowsPowerShell" / L"v1.0" / L"powershell.exe").wstring();
    auto knownLeaf = [](const std::wstring& image) -> std::string {
        const auto leaf = fs::path(image).filename().wstring();
        for (const auto expected : {L"pi.exe", L"claude-notifications-windows-amd64.exe", L"powershell.exe"})
            if (CompareStringOrdinal(leaf.c_str(), -1, expected, -1, TRUE) == CSTR_EQUAL) return utf8(expected);
        return "other";
    };
    std::vector<TimelineProcess> observed;
    const auto rootLeaf = knownLeaf(root->image);
    observed.push_back({std::move(root), 0, "original_pi", rootLeaf, 0, 0, 0, false, 0});
    if (!ready.empty()) {
        need(!fs::exists(fs::symlink_status(ready)) && !fs::exists(fs::symlink_status(stop)), "process_watch_fresh_markers_required");
        auto pending = ready; pending += L".pending";
        need(!fs::exists(fs::symlink_status(pending)), "process_watch_fresh_pending_required");
        processBirth(*observed.front().process);
        need(observed.front().process->created == originalBirth && running(observed.front().process->handle),
             "process_watch_ready_original_changed");
        {
            std::ofstream file(pending);
            file << "{\"ready\":true,\"observation_only\":true,\"original_identity_verified\":true,\"deadline_tick_ms\":" << deadline << "}\n";
            need(file.good(), "process_watch_ready_write_failed");
        }
        need(GetTickCount64() < deadline, "process_watch_ready_expired");
        fs::rename(pending, ready);
    }
    unsigned scans = 0, missed = 0;
    std::string reason = "deadline";
    // Temporary, nonqualifying pixels from a retained system PowerShell child.
    // No notification API or second watcher is introduced before delivery.
    OwnedProcess* deliveryPowerShell = nullptr;
    ULONGLONG deliveryBirth = 0, deliveryExit = 0;
    DWORD deliveryExitCode = 0;
    bool followupAttempted = false;
    std::string deliveryFrames = "[";
    auto captureDelivery = [&](const char* phase, const wchar_t* name) {
        if (GetTickCount64() >= deadline) return;
        processBirth(*deliveryPowerShell);
        need(deliveryPowerShell->created == deliveryBirth && !running(deliveryPowerShell->handle), "delivery_frame_retained_child_changed");
        const auto path = ready.parent_path() / name;
        need(!fs::exists(fs::symlink_status(path)), "delivery_frame_fresh_path_required");
        const auto began = GetTickCount64();
        if (began >= deadline) return;
        bool captured = false;
        try {
            screenshot(path); captured = GetTickCount64() < deadline;
        } catch (...) {} // Missing/late pixels remain unknown, never absence proof.
        const auto completed = GetTickCount64();
        if (deliveryFrames.size() > 1) deliveryFrames += ',';
        deliveryFrames += "{\"phase\":\"" + std::string(phase) + "\",\"pid\":" + std::to_string(deliveryPowerShell->pid)
            + ",\"birth_filetime\":\"" + std::to_string(deliveryBirth) + "\",\"exit_code\":" + std::to_string(deliveryExitCode)
            + ",\"exit_observed_ms\":" + std::to_string(deliveryExit - started) + ",\"started_ms\":" + std::to_string(began - started)
            + ",\"completed_ms\":" + std::to_string(completed - started) + ",\"original_deadline_ms\":" + std::to_string(deadline - started)
            + ",\"captured\":" + jsonBool(captured) + "}";
    };
    auto inventory = [&]() {
        need(scans < processWatchScanLimit && GetTickCount64() < deadline, "process_watch_scan_bound");
        ++scans;
        return processParents();
    };
    auto original = [&]() {
        processBirth(*observed.front().process);
        need(observed.front().process->created == originalBirth, "process_watch_original_changed");
    };
    try {
        while (GetTickCount64() < deadline) {
            if (!stop.empty() && fs::exists(stop)) { reason = "stop_requested"; break; }
            if (scans >= processWatchScanLimit) { reason = "scan_bound"; break; }
            original();
            const auto parents = inventory();
            original();
            for (const auto& [candidate, parent] : parents) {
                if (GetTickCount64() >= deadline) break;
                if (std::any_of(observed.begin(), observed.end(), [&](const auto& row) { return row.process->pid == candidate; })) continue;
                const auto ancestor = std::find_if(observed.begin(), observed.end(), [&](const auto& row) { return row.process->pid == parent; });
                if (ancestor == observed.end()) continue;
                need(observed.size() < 64, "process_watch_handle_bound");
                const auto parentCreated = ancestor->process->created;
                // Observation handles cannot terminate/suspend any actor. Keep
                // each admitted kernel object until the watcher is collected.
                auto child = processIdentity(candidate, PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE);
                if (!child) { ++missed; continue; }
                const auto current = inventory();
                original();
                const auto entry = current.find(candidate);
                if (entry == current.end() || entry->second != parent || child->created < parentCreated) { ++missed; continue; }
                const auto first = GetTickCount64();
                if (first >= deadline) { ++missed; break; }
                std::string role = "other_descendant", leaf = "other";
                if (sameImage(child->image, notifierImage)) {
                    role = parent == observed.front().process->pid ? "direct_candidate" : "candidate_descendant";
                    leaf = knownLeaf(child->image);
                } else if (sameImage(child->image, systemPowerShell)) {
                    role = "system_powershell_descendant"; leaf = "powershell.exe";
                }
                observed.push_back({std::move(child), parent, role, leaf, first - started, first - started, 0, false, 0});
            }
            for (auto& row : observed) {
                if (GetTickCount64() >= deadline) break;
                const auto birth = row.process->created;
                processBirth(*row.process);
                need(row.process->created == birth, "process_watch_retained_identity_changed");
                const bool exited = !row.signaled && !running(row.process->handle);
                DWORD exitCode = 0;
                if (exited) need(GetExitCodeProcess(row.process->handle, &exitCode), "process_watch_exit_unavailable");
                const auto now = GetTickCount64();
                if (now >= deadline) break;
                row.lastObserved = now - started;
                if (exited) {
                    row.signaled = true; row.signaledObserved = now - started; row.exitCode = exitCode;
                    if (!ready.empty() && !deliveryPowerShell && row.role == "system_powershell_descendant") {
                        deliveryPowerShell = row.process.get(); deliveryBirth = row.process->created;
                        deliveryExit = now; deliveryExitCode = exitCode;
                        captureDelivery("first_retained_system_powershell_exit", L"delivery-exit.bmp");
                    }
                }
            }
            const auto now = GetTickCount64();
            if (deliveryPowerShell && !followupAttempted && now >= deliveryExit + 500 && now < deadline) {
                followupAttempted = true; captureDelivery("after_500ms", L"delivery-after-500ms.bmp");
            }
            if (now < deadline) Sleep(static_cast<DWORD>(std::min<ULONGLONG>(20, deadline - now)));
        }
    } catch (...) {
        // Partial observations are never promoted into completeness or delivery.
        reason = "observation_refused";
    }
    std::string rows = "[";
    for (const auto& row : observed) {
        if (rows.size() > 1) rows += ',';
        rows += "{\"pid\":" + std::to_string(row.process->pid) + ",\"parent_pid\":" + std::to_string(row.parent)
            + ",\"birth_filetime\":\"" + std::to_string(row.process->created) + "\",\"role\":\"" + row.role
            + "\",\"image_leaf\":\"" + row.imageLeaf + "\",\"first_seen_ms\":" + std::to_string(row.firstSeen)
            + ",\"last_handle_observed_ms\":" + std::to_string(row.lastObserved)
            + ",\"exit_signaled_observed_ms\":" + (row.signaled ? std::to_string(row.signaledObserved) : "null")
            + ",\"exit_code\":" + (row.signaled ? std::to_string(row.exitCode) : "null") + "}";
    }
    rows += ']';
    auto result = "{\"observation_only\":true,\"qualification_input\":false,\"misses_remain_unknown\":true,\"stop_reason\":\""
        + reason + "\",\"elapsed_ms\":" + std::to_string(GetTickCount64() - started) + ",\"inventory_scans\":"
        + std::to_string(scans) + ",\"missed_candidates\":" + std::to_string(missed) + ",\"processes\":" + rows
        + ",\"delivery_frames\":" + deliveryFrames + "]}";
    need(result.size() < 65536, "process_watch_receipt_bound");
    return result;
}
static std::string collectOwnedProcess(std::unique_ptr<OwnedProcess> root) {
    std::vector<std::unique_ptr<OwnedProcess>> owned; owned.push_back(std::move(root));
    // Keep every admitted process handle open. Its PID cannot be recycled while
    // descendants are discovered, rechecked and terminated through those handles.
    const auto deadline = GetTickCount64() + 3000;
    auto discover = [&]() {
        bool added = false, again = true;
        while (again) {
            again = false; const auto parents = processParents();
            for (const auto& [candidate, parent] : parents) {
                if (std::any_of(owned.begin(), owned.end(), [&](const auto& p) { return p->pid == candidate; })) continue;
                const auto ancestor = std::find_if(owned.begin(), owned.end(), [&](const auto& p) { return p->pid == parent; });
                if (ancestor == owned.end()) continue;
                const auto parentCreated = (*ancestor)->created;
                auto child = processIdentity(candidate);
                const auto current = processParents();
                if (!child || !running(child->handle)) {
                    need(std::none_of(parents.begin(), parents.end(), [&](const auto& p) { return p.second == candidate; }),
                         "owned_descendant_subtree_identity_unverified");
                    continue;
                }
                const auto entry = current.find(candidate);
                need(entry != current.end(), "owned_descendant_lineage_unavailable");
                if (entry->second != parent) {
                    need(std::none_of(parents.begin(), parents.end(), [&](const auto& p) { return p.second == candidate; }),
                         "owned_descendant_replacement_subtree_unverified");
                    continue; // The old descendant exited; never target its replacement.
                }
                need(child->created >= parentCreated, "owned_descendant_predates_parent");
                need(owned.size() < 64 && GetTickCount64() < deadline, "owned_process_cleanup_bound");
                owned.push_back(std::move(child)); added = again = true;
            }
        }
        return added;
    };
    discover();
    for (;;) {
        for (const auto& process : owned) {
            if (running(process->handle) && !process->terminationRequested) {
                // Termination is asynchronous. A process already exiting may
                // report ACCESS_DENIED before its retained object is signaled.
                const BOOL requested = TerminateProcess(process->handle, 1);
                process->terminationError = requested ? ERROR_SUCCESS : GetLastError();
                if (!requested && process->terminationError != ERROR_ACCESS_DENIED && running(process->handle))
                    throw std::runtime_error("owned_process_termination_failed:pid=" + std::to_string(process->pid)
                        + ":creation_time=" + std::to_string(process->created)
                        + ":role=" + (process.get() == owned.front().get() ? "root" : "descendant")
                        + ":image_leaf=" + utf8(fs::path(process->image).filename().wstring())
                        + ":winerror=" + std::to_string(process->terminationError) + ":handle_state=non_signaled");
                process->terminationRequested = true;
            }
        }
        const bool added = discover();
        if (GetTickCount64() >= deadline) {
            const auto pending = std::find_if(owned.begin(), owned.end(), [](const auto& p) { return running(p->handle); });
            throw std::runtime_error("owned_process_cleanup_timeout:pid=" + std::to_string(pending == owned.end() ? 0 : (*pending)->pid)
                + ":creation_time=" + std::to_string(pending == owned.end() ? 0 : (*pending)->created)
                + ":image_leaf=" + (pending == owned.end() ? std::string{} : utf8(fs::path((*pending)->image).filename().wstring()))
                + ":winerror=" + std::to_string(pending == owned.end() ? 0 : (*pending)->terminationError));
        }
        if (!added && std::none_of(owned.begin(), owned.end(), [](const auto& p) { return running(p->handle); })) break;
        pump(10);
    }
    return "{\"collected\":true,\"original_identity_changed\":false,\"descendants_collected\":"
        + std::to_string(owned.size() - 1) + "}";
}
static std::wstring selfImage() {
    wchar_t value[32768]{}; const auto length = GetModuleFileNameW(nullptr, value, 32768);
    need(length > 0 && length < 32768, "process_guard_self_image_unavailable");
    return std::wstring(value, length);
}
struct TestChild {
    PROCESS_INFORMATION process{};
    explicit TestChild(const std::wstring& arguments) {
        STARTUPINFOW startup{}; startup.cb = sizeof startup;
        auto command = L"\"" + selfImage() + L"\" " + arguments;
        // These marker/sleep fixtures need no console. CREATE_NO_WINDOW can
        // allocate a hidden console host, adding an implicit owned descendant.
        need(CreateProcessW(nullptr, command.data(), nullptr, nullptr, FALSE, DETACHED_PROCESS,
                            nullptr, nullptr, &startup, &process), "process_guard_child_start_failed");
    }
    ~TestChild() {
        if (process.hProcess) {
            if (WaitForSingleObject(process.hProcess, 0) == WAIT_TIMEOUT) {
                TerminateProcess(process.hProcess, 1); WaitForSingleObject(process.hProcess, 3000);
            }
            CloseHandle(process.hProcess);
        }
        if (process.hThread) CloseHandle(process.hThread);
    }
};
static void testToastSelection();
static int testProcessIdentity() {
    testToastSelection();
    wchar_t temporary[32768]{}, marker[MAX_PATH]{};
    const auto length = GetTempPathW(32768, temporary);
    need(length > 0 && length < MAX_PATH && GetTempFileNameW(temporary, L"TPI", 0, marker), "process_guard_temp_path_failed");
    TestChild child(L"process-test-tree \"" + std::wstring(marker) + L"\"");
    auto root = processIdentity(child.process.dwProcessId);
    need(root && sameImage(root->image, selfImage()), "process_guard_child_identity_missing");
    DWORD leafPID = 0; const auto deadline = GetTickCount64() + 5000;
    while (!leafPID && GetTickCount64() < deadline) { std::ifstream input(marker); input >> leafPID; if (!leafPID) Sleep(20); }
    auto leaf = leafPID ? processIdentity(leafPID) : nullptr;
    const auto parents = processParents();
    need(leaf && parents.contains(leafPID) && parents.at(leafPID) == root->pid
         && leaf->created >= root->created && sameImage(leaf->image, root->image), "process_guard_descendant_identity_missing");
    try {
        bool refused = false;
        try { watchOwnedProcesses(heldProcess(duplicateHeld(root->handle), root->created + 1, root->image), selfImage(), {}, {}, GetTickCount64() + 200); }
        catch (const std::runtime_error& error) { refused = std::string(error.what()) == "owned_process_birth_changed"; }
        need(refused && running(root->handle) && running(leaf->handle), "process_watch_wrong_birth_changed_actor");
        refused = false;
        try { watchOwnedProcesses(heldProcess(duplicateHeld(root->handle), root->created, L"C:\\TEST\\foreign.exe"), selfImage(), {}, {}, GetTickCount64() + 200); }
        catch (const std::runtime_error& error) { refused = std::string(error.what()) == "owned_process_image_changed"; }
        need(refused && running(root->handle) && running(leaf->handle), "process_watch_wrong_image_changed_actor");
        {
            TestChild foreign(L"process-test-child");
            HANDLE queryOnly = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, root->pid);
            need(queryOnly != nullptr, "process_watch_query_handle_missing");
            const auto timeline = watchOwnedProcesses(heldProcess(queryOnly, root->created, root->image), selfImage(), {}, {}, GetTickCount64() + 200);
            need(timeline.find("\"pid\":" + std::to_string(leaf->pid) + ",") != std::string::npos
                 && timeline.find("\"pid\":" + std::to_string(foreign.process.dwProcessId) + ",") == std::string::npos
                 && timeline.find("\"role\":\"direct_candidate\"") != std::string::npos
                 && timeline.find("\"stop_reason\":\"deadline\"") != std::string::npos
                 && running(root->handle) && running(leaf->handle) && WaitForSingleObject(foreign.process.hProcess, 0) == WAIT_TIMEOUT,
                 "process_watch_readonly_descendant_guard_failed");
            std::cout << "{\"scope\":\"native_readonly_descendant_watch_guard\",\"query_only_handles\":true,\"foreign_process_excluded\":true,"
                         "\"wrong_birth_and_image_refused\":true,\"all_actors_still_live\":true,\"desktop_qualified\":false}\n" << std::flush;
        }
        refused = false;
        try { collectOwnedProcess(heldProcess(duplicateHeld(root->handle), root->created + 1, root->image)); }
        catch (const std::runtime_error& error) { refused = std::string(error.what()) == "owned_process_birth_changed"; }
        need(refused && running(root->handle) && running(leaf->handle), "mismatched_process_birth_was_signalled");
        refused = false;
        try { collectOwnedProcess(heldProcess(duplicateHeld(root->handle), root->created, L"C:\\TEST\\foreign.exe")); }
        catch (const std::runtime_error& error) { refused = std::string(error.what()) == "owned_process_image_changed"; }
        need(refused && running(root->handle) && running(leaf->handle), "mismatched_process_image_was_signalled");
        need(TerminateProcess(root->handle, 0) && WaitForSingleObject(root->handle, 3000) == WAIT_OBJECT_0
             && running(leaf->handle), "process_guard_root_exit_with_live_leaf_missing");
        const auto afterExit = processParents();
        std::cout << "{\"scope\":\"native_owned_process_cleanup_guard\",\"phase\":\"root_exited_leaf_live\",\"root_pid\":"
                  << root->pid << ",\"leaf_pid\":" << leaf->pid << ",\"leaf_snapshot_present\":"
                  << (afterExit.contains(leaf->pid) ? "true" : "false") << ",\"leaf_parent_pid\":"
                  << (afterExit.contains(leaf->pid) ? afterExit.at(leaf->pid) : 0) << "}\n" << std::flush;
        const auto result = collectOwnedProcess(heldProcess(duplicateHeld(root->handle), root->created, root->image));
        const bool rootRunning = running(root->handle), leafRunning = running(leaf->handle);
        std::cout << "{\"scope\":\"native_owned_process_cleanup_guard\",\"phase\":\"collection_returned\",\"result\":"
                  << result << ",\"root_running\":" << (rootRunning ? "true" : "false")
                  << ",\"leaf_running\":" << (leafRunning ? "true" : "false") << "}\n" << std::flush;
        need(result.find("\"descendants_collected\":1") != std::string::npos
             && !rootRunning && !leafRunning, "owned_process_tree_not_collected");
        const auto reaped = collectOwnedProcess(heldProcess(duplicateHeld(root->handle), root->created, root->image));
        need(reaped.find("\"descendants_collected\":0") != std::string::npos, "reaped_process_tree_still_running");
    } catch (...) {
        if (running(leaf->handle)) { TerminateProcess(leaf->handle, 1); WaitForSingleObject(leaf->handle, 3000); }
        DeleteFileW(marker); throw;
    }
    DeleteFileW(marker);
    {
        TestChild denied(L"process-test-child");
        auto original = processIdentity(denied.process.dwProcessId);
        need(original != nullptr, "process_guard_denied_identity_missing");
        HANDLE queryOnly = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, original->pid);
        need(queryOnly != nullptr, "process_guard_query_handle_missing");
        bool refused = false;
        try { collectOwnedProcess(heldProcess(queryOnly, original->created, original->image)); }
        catch (const std::runtime_error& error) {
            refused = std::string(error.what()).rfind("owned_process_cleanup_timeout:pid=", 0) == 0;
        }
        need(refused && running(original->handle), "live_denied_process_falsely_collected");
    }
    std::cout << "{\"scope\":\"native_owned_process_cleanup_only\",\"mismatched_birth_rejected\":true,"
                 "\"mismatched_image_rejected\":true,\"reaped_identity_rejected\":true,"
                 "\"root_exited_before_live_descendant_collection\":true,"
                 "\"actual_descendant_collected\":true,\"live_denied_process_rejected\":true,\"desktop_qualified\":false}\n";
    return 0;
}
static std::wstring title(HWND window) {
    wchar_t value[1024]{}; GetWindowTextW(window, value, 1024); return value;
}
static DWORD owner(HWND window) { DWORD pid = 0; GetWindowThreadProcessId(window, &pid); return pid; }
static bool testTitle(const std::wstring& value) { return value.rfind(L"TEST-Pi-", 0) == 0 && value.size() <= 256; }
static bool terminalClass(HWND window) {
    wchar_t value[128]{}; GetClassNameW(window, value, 128);
    return std::wstring(value) == L"CASCADIA_HOSTING_WINDOW_CLASS" || std::wstring(value) == L"TEST.Pi.Product.Neutral";
}
static HWND boundWindow(const wchar_t* raw, const wchar_t* pid, const std::wstring& expected) {
    const auto window = reinterpret_cast<HWND>(static_cast<uintptr_t>(std::stoull(raw)));
    need(testTitle(expected) && IsWindow(window) && IsWindowVisible(window) && owner(window) == std::stoul(pid)
         && terminalClass(window) && title(window).rfind(expected, 0) == 0, "owned_terminal_identity_changed");
    return window;
}
static HWND keyboardRoot() {
    GUITHREADINFO info{}; info.cbSize = sizeof info;
    const DWORD thread = GetWindowThreadProcessId(GetForegroundWindow(), nullptr);
    return thread && GetGUIThreadInfo(thread, &info) && info.hwndFocus ? GetAncestor(info.hwndFocus, GA_ROOT) : nullptr;
}
static bool mouse(POINT point) {
    INPUT values[3]{};
    for (auto& value : values) value.type = INPUT_MOUSE;
    values[0].mi.dx = (point.x - GetSystemMetrics(SM_XVIRTUALSCREEN)) * 65535 / (GetSystemMetrics(SM_CXVIRTUALSCREEN) - 1);
    values[0].mi.dy = (point.y - GetSystemMetrics(SM_YVIRTUALSCREEN)) * 65535 / (GetSystemMetrics(SM_CYVIRTUALSCREEN) - 1);
    values[0].mi.dwFlags = MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK;
    values[1].mi.dwFlags = MOUSEEVENTF_LEFTDOWN; values[2].mi.dwFlags = MOUSEEVENTF_LEFTUP;
    return SendInput(3, values, sizeof(INPUT)) == 3;
}
static void foreground(HWND window) {
    ShowWindow(window, SW_RESTORE); SetForegroundWindow(window); pump(100);
    if (GetForegroundWindow() != window) {
        RECT rect{}; need(GetWindowRect(window, &rect), "terminal_rectangle_missing");
        POINT point{rect.left + 100, rect.top + 12};
        need(GetAncestor(WindowFromPoint(point), GA_ROOT) == window && mouse(point), "owned_terminal_occluded_or_input_denied");
    }
    const auto until = GetTickCount64() + 2000;
    while ((GetForegroundWindow() != window || keyboardRoot() != window) && GetTickCount64() < until) pump(20);
    need(GetForegroundWindow() == window && keyboardRoot() == window, "native_terminal_focus_not_observed");
}
static void key(WORD vk, bool up) {
    INPUT value{}; value.type = INPUT_KEYBOARD; value.ki.wVk = vk; value.ki.dwFlags = up ? KEYEVENTF_KEYUP : 0;
    need(SendInput(1, &value, sizeof value) == 1, "native_keyboard_input_denied");
}
static void typeLine(HWND window, const std::wstring& value) {
    need(value.size() <= 1024 && GetForegroundWindow() == window && keyboardRoot() == window, "typing_requires_owned_foreground");
    key(VK_CONTROL, false); key('U', false); key('U', true); key(VK_CONTROL, true);
    for (wchar_t ch : value) {
        INPUT pair[2]{};
        for (auto& item : pair) { item.type = INPUT_KEYBOARD; item.ki.wScan = ch; item.ki.dwFlags = KEYEVENTF_UNICODE; }
        pair[1].ki.dwFlags |= KEYEVENTF_KEYUP;
        need(SendInput(2, pair, sizeof(INPUT)) == 2, "native_unicode_input_denied"); pump(25);
    }
    key(VK_RETURN, false); key(VK_RETURN, true);
}
static void screenshot(const fs::path& path) {
    const int width = GetSystemMetrics(SM_CXSCREEN), height = GetSystemMetrics(SM_CYSCREEN);
    need(width > 0 && height > 0 && static_cast<uint64_t>(width) * height <= 16000000, "screen_capture_bound");
    HDC screen = GetDC(nullptr), memory = CreateCompatibleDC(screen);
    BITMAPINFO info{}; info.bmiHeader.biSize = sizeof(BITMAPINFOHEADER); info.bmiHeader.biWidth = width;
    info.bmiHeader.biHeight = -height; info.bmiHeader.biPlanes = 1; info.bmiHeader.biBitCount = 32; info.bmiHeader.biCompression = BI_RGB;
    void* bits = nullptr; HBITMAP bitmap = CreateDIBSection(screen, &info, DIB_RGB_COLORS, &bits, nullptr, 0);
    need(screen && memory && bitmap && bits, "screen_capture_unavailable");
    const auto previous = SelectObject(memory, bitmap);
    need(BitBlt(memory, 0, 0, width, height, screen, 0, 0, SRCCOPY | CAPTUREBLT), "screen_capture_failed");
    BITMAPFILEHEADER header{}; header.bfType = 0x4d42; header.bfOffBits = sizeof header + sizeof(BITMAPINFOHEADER);
    header.bfSize = header.bfOffBits + width * height * 4;
    std::ofstream file(path, std::ios::binary); file.write(reinterpret_cast<char*>(&header), sizeof header);
    file.write(reinterpret_cast<char*>(&info.bmiHeader), sizeof info.bmiHeader);
    file.write(static_cast<char*>(bits), static_cast<std::streamsize>(width) * height * 4);
    SelectObject(memory, previous); DeleteObject(bitmap); DeleteDC(memory); ReleaseDC(nullptr, screen);
    need(file.good(), "screen_capture_file_failed");
}
struct ToastUnavailable {};
struct ToastDeadline {};
static void toastBudget(ULONGLONG until) { if (GetTickCount64() >= until) throw ToastDeadline{}; }
static void toastcheck(HRESULT value) {
    if (value == UIA_E_ELEMENTNOTAVAILABLE) throw ToastUnavailable{};
    hrcheck(value);
}
struct ToastShell {
    std::unique_ptr<OwnedProcess> process;
    std::unique_ptr<PiWindowsShellImageTEST::InspectionImage> image;
    ULONGLONG birth = 0;
    std::wstring path;
    explicit ToastShell(DWORD pid) {
        process = processIdentity(pid, PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE);
        need(process != nullptr, "product_toast_shell_process_unavailable");
        birth = process->created; path = process->image;
        auto leaf = fs::path(path).filename().wstring();
        std::transform(leaf.begin(), leaf.end(), leaf.begin(), towlower);
        need(leaf == L"shellexperiencehost.exe" || leaf == L"shellhost.exe" || leaf == L"explorer.exe", "product_toast_shell_image_not_admitted");
        try {
            image = std::make_unique<PiWindowsShellImageTEST::InspectionImage>(path); image->verifyMicrosoft();
            need(image->verifiedStable, "product_toast_shell_signature_unverified");
        } catch (const char*) { throw std::runtime_error("product_toast_shell_image_proof_refused"); }
        validate();
    }
    void validate(bool bytes = false) {
        need(running(process->handle), "product_toast_original_shell_exited"); processFacts(*process);
        need(process->created == birth && sameImage(process->image, path), "product_toast_original_shell_changed");
        if (bytes) {
            try { need(image->currentHash() == image->hash, "product_toast_original_shell_image_changed"); }
            catch (const char*) { throw std::runtime_error("product_toast_shell_image_revalidation_refused"); }
        }
    }
};
static std::wstring elementName(IUIAutomationElement* element) {
    BSTR raw = nullptr; toastcheck(element->get_CurrentName(&raw));
    std::wstring value = raw ? raw : L""; if (raw) SysFreeString(raw);
    need(value.size() <= 4096, "native_ui_text_bound"); return value;
}
static bool toastCardType(CONTROLTYPEID type) {
    return type == UIA_ButtonControlTypeId || type == UIA_ListItemControlTypeId || type == UIA_WindowControlTypeId;
}
static bool toastBody(const std::wstring& value, const std::wstring& expected, const std::wstring& body) {
    if (value == body) return true;
    const std::wstring prefix = L"TEST-Pi-toast-";
    return expected.rfind(prefix, 0) == 0 && value == L"TEST-Pi-origin-" + expected.substr(prefix.size()) + L"\n" + body;
}
static bool toastGeometry(const RECT& box, const RECT& title, int width, int height) {
    return box.left >= 0 && box.top >= 0 && box.right <= width && box.bottom <= height
        && box.right > box.left && box.bottom > box.top && title.right > title.left && title.bottom > title.top
        && title.left >= box.left && title.top >= box.top && title.right <= box.right && title.bottom <= box.bottom
        && static_cast<uint64_t>(box.right - box.left) * (box.bottom - box.top) < static_cast<uint64_t>(width) * height;
}
static std::string toastRuntime(IUIAutomationElement* element) {
    SAFEARRAY* raw = nullptr; toastcheck(element->GetRuntimeId(&raw)); need(raw != nullptr, "product_toast_runtime_missing");
    std::string result = "[";
    try {
        LONG low = 0, high = -1; VARTYPE type = VT_EMPTY;
        need(SafeArrayGetDim(raw) == 1 && SUCCEEDED(SafeArrayGetVartype(raw, &type)) && type == VT_I4
            && SUCCEEDED(SafeArrayGetLBound(raw, 1, &low)) && SUCCEEDED(SafeArrayGetUBound(raw, 1, &high))
            && high >= low && high - low < 32, "product_toast_runtime_bound");
        for (LONG i = low; i <= high; ++i) { LONG value = 0; hrcheck(SafeArrayGetElement(raw, &i, &value));
            if (i != low) result += ','; result += std::to_string(value); }
    } catch (...) { SafeArrayDestroy(raw); throw; }
    SafeArrayDestroy(raw); return result + ']';
}
struct ToastTrace {
    int maxTitleMatches = 0, staleElements = 0;
    bool shellProviderSeen = false;
    std::string stage = "not_started";
    std::vector<int> ancestorTypes;
};
static std::string toastTrace(const ToastTrace& trace) {
    std::string types = "[";
    for (const auto type : trace.ancestorTypes) { if (types.size() > 1) types += ','; types += std::to_string(type); }
    return "\"max_exact_title_matches\":" + std::to_string(trace.maxTitleMatches)
        + ",\"shell_provider_seen\":" + jsonBool(trace.shellProviderSeen) + ",\"stale_element_reacquisitions\":" + std::to_string(trace.staleElements)
        + ",\"last_stage\":\"" + trace.stage + "\",\"ancestor_control_types\":" + types + "]";
}
struct ToastCard {
    ComPtr<IUIAutomationElement> element, title;
    std::shared_ptr<ToastShell> shell;
    RECT box{}, titleBox{};
    std::string runtime, titleRuntime;
    CONTROLTYPEID type = 0;
};
static std::unique_ptr<ToastCard> card(IUIAutomation* automation, const std::wstring& expected, const std::wstring& body,
        ToastTrace& trace, std::map<DWORD, std::shared_ptr<ToastShell>>& shells, ULONGLONG until) {
    auto budget = [&]() { toastBudget(until); };
    budget(); ComPtr<IUIAutomationElement> root, text; toastcheck(automation->GetRootElement(&root));
    VARIANT name{}; name.vt = VT_BSTR; name.bstrVal = SysAllocString(expected.c_str());
    ComPtr<IUIAutomationCondition> match; const auto condition = automation->CreatePropertyCondition(UIA_NamePropertyId, name, &match); VariantClear(&name); toastcheck(condition);
    ComPtr<IUIAutomationElementArray> all; toastcheck(root->FindAll(TreeScope_Descendants, match.Get(), &all));
    int total = 0; toastcheck(all->get_Length(&total)); need(total <= 1, "product_toast_title_ambiguous");
    trace.maxTitleMatches = std::max(trace.maxTitleMatches, total); trace.stage = "exact_title_missing";
    budget(); if (!total) return nullptr;
    toastcheck(all->GetElement(0, &text));
    int pid = 0; toastcheck(text->get_CurrentProcessId(&pid)); need(pid > 0, "product_toast_provider_missing");
    trace.stage = "exact_title_provider_not_shell";
    if (!shells.contains(pid)) { need(shells.size() < 8, "product_toast_shell_owner_bound"); shells.emplace(pid, std::make_shared<ToastShell>(pid)); }
    auto shell = shells.at(pid); shell->validate(); budget();
    trace.shellProviderSeen = true; trace.ancestorTypes.clear(); trace.stage = "exact_card_ancestor_missing";
    ComPtr<IUIAutomationTreeWalker> walker; toastcheck(automation->get_ControlViewWalker(&walker));
    RECT titleBox{}; BOOL offscreen = TRUE; toastcheck(text->get_CurrentBoundingRectangle(&titleBox)); toastcheck(text->get_CurrentIsOffscreen(&offscreen));
    if (offscreen) return nullptr;
    auto current = text;
    for (int depth = 0; depth < 6; depth++) {
        budget(); CONTROLTYPEID type = 0; int currentPID = 0;
        toastcheck(current->get_CurrentControlType(&type)); toastcheck(current->get_CurrentProcessId(&currentPID)); trace.ancestorTypes.push_back(type);
        if (currentPID != pid) return nullptr;
        if (type == UIA_WindowControlTypeId && depth != 1) return nullptr;
        if (toastCardType(type)) {
            RECT box{}; BOOL enabled = FALSE;
            toastcheck(current->get_CurrentBoundingRectangle(&box)); toastcheck(current->get_CurrentIsOffscreen(&offscreen)); toastcheck(current->get_CurrentIsEnabled(&enabled));
            if (offscreen || !enabled || !toastGeometry(box, titleBox, GetSystemMetrics(SM_CXSCREEN), GetSystemMetrics(SM_CYSCREEN))) return nullptr;
            ComPtr<IUIAutomationCondition> yes; toastcheck(automation->CreateTrueCondition(&yes));
            ComPtr<IUIAutomationElementArray> children; toastcheck(current->FindAll(TreeScope_Descendants, yes.Get(), &children));
            int count = 0; toastcheck(children->get_Length(&count)); need(count <= 64, "product_toast_group_bound");
            int titleSeen = 0, bodySeen = 0;
            for (int index = 0; index < count; index++) {
                budget(); ComPtr<IUIAutomationElement> child; toastcheck(children->GetElement(index, &child));
                int childPID = 0; toastcheck(child->get_CurrentProcessId(&childPID)); need(childPID == pid, "product_toast_child_provider_changed");
                const auto value = elementName(child.Get()); titleSeen += value == expected; bodySeen += toastBody(value, expected, body);
            }
            need(titleSeen <= 1 && bodySeen <= 1, "product_toast_card_content_ambiguous");
            if (titleSeen == 1 && bodySeen == 1) {
                auto result = std::make_unique<ToastCard>(); result->element = current; result->title = text; result->shell = shell;
                result->box = box; result->titleBox = titleBox; result->type = type;
                result->runtime = toastRuntime(current.Get()); result->titleRuntime = toastRuntime(text.Get());
                shell->validate(); budget(); trace.stage = "exact_card_selected"; return result;
            }
            trace.stage = "exact_card_title_or_body_missing";
            if (type == UIA_WindowControlTypeId) return nullptr; // Never promote an unrelated outer Window.
        }
        ComPtr<IUIAutomationElement> parent; toastcheck(walker->GetParentElement(current.Get(), &parent));
        if (!parent) break; current = parent;
    }
    return nullptr;
}
static void testToastSelection() {
    need(toastCardType(UIA_WindowControlTypeId) && !toastCardType(UIA_PaneControlTypeId), "toast_guard_measured_window_type");
    const std::wstring title = L"TEST-Pi-toast-owned", body = L"Pi completed a task";
    need(toastBody(body, title, body) && toastBody(L"TEST-Pi-origin-owned\n" + body, title, body)
        && !toastBody(L"foreign " + body, title, body) && !toastBody(L"TEST-Pi-origin-foreign\n" + body, title, body), "toast_guard_exact_controlled_body");
    need(toastGeometry({10, 10, 300, 200}, {30, 50, 200, 80}, 1024, 768)
        && !toastGeometry({0, 0, 1024, 768}, {30, 50, 200, 80}, 1024, 768)
        && !toastGeometry({10, 10, 300, 200}, {301, 50, 400, 80}, 1024, 768), "toast_guard_card_geometry");
    bool refused = false;
    try { ToastShell foreign(GetCurrentProcessId()); } catch (const std::runtime_error&) { refused = true; }
    need(refused, "toast_guard_foreign_actor_admitted");
}
static int toast(const std::wstring& expected, const std::wstring& body, const fs::path& image, bool click, const fs::path& ready = {}, ULONGLONG originalUntil = 0) {
    need(testTitle(expected) && !body.empty() && body.size() <= 256, "owned_product_toast_selector_required");
    const auto started = GetTickCount64();
    const auto until = originalUntil ? originalUntil : started + (ready.empty() ? 15000 : 25000);
    need(until > started && until - started <= 25000, "original_product_toast_deadline_required");
    ComPtr<IUIAutomation> automation; hrcheck(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)));
    if (!ready.empty()) {
        need(!click && !fs::exists(ready), "observation_only_toast_ready_path_required");
        auto pending = ready; pending += L".pending"; need(!fs::exists(pending), "observation_only_toast_pending_path_required");
        { std::ofstream file(pending); file << "{\"ready\":true,\"observation_only\":true,\"budget_ms\":25000}\n"; need(file.good(), "toast_observer_ready_write_failed"); }
        fs::rename(pending, ready);
    }
    ToastTrace trace; std::map<DWORD, std::shared_ptr<ToastShell>> shells; bool inputAttempted = false;
    while (GetTickCount64() < until) {
        try {
            auto selected = card(automation.Get(), expected, body, trace, shells, until);
            if (selected) {
                screenshot(image); toastBudget(until);
                auto fresh = card(automation.Get(), expected, body, trace, shells, until);
                BOOL same = FALSE;
                if (!fresh || !EqualRect(&selected->box, &fresh->box) || !EqualRect(&selected->titleBox, &fresh->titleBox)
                    || selected->runtime != fresh->runtime || selected->titleRuntime != fresh->titleRuntime
                    || selected->shell != fresh->shell) { trace.stage = "card_changed_before_handoff"; pump(100); continue; }
                toastcheck(automation->CompareElements(selected->element.Get(), fresh->element.Get(), &same));
                if (!same) { trace.stage = "card_changed_before_handoff"; pump(100); continue; }
                const POINT point{(fresh->titleBox.left + fresh->titleBox.right) / 2, (fresh->titleBox.top + fresh->titleBox.bottom) / 2};
                if (click) {
                    fresh->shell->validate(true);
                    need(!(GetAsyncKeyState(VK_LBUTTON) & 0x8000) && SetCursorPos(point.x, point.y), "product_toast_pointer_unavailable");
                    ComPtr<IUIAutomationElement> hit; toastcheck(automation->ElementFromPoint(point, &hit)); need(hit != nullptr, "product_toast_topmost_hit_missing");
                    ComPtr<IUIAutomationTreeWalker> walker; toastcheck(automation->get_ControlViewWalker(&walker)); bool matched = false;
                    for (unsigned depth = 0; hit && depth < 6; ++depth) {
                        int pid = 0; CONTROLTYPEID type = 0; toastcheck(hit->get_CurrentProcessId(&pid));
                        need(pid == static_cast<int>(fresh->shell->process->pid), "product_toast_hit_provider_changed");
                        toastcheck(automation->CompareElements(hit.Get(), fresh->element.Get(), &same)); if (same) { matched = true; break; }
                        toastcheck(hit->get_CurrentControlType(&type)); need(type == UIA_TextControlTypeId || type == UIA_ImageControlTypeId, "product_toast_hit_is_not_controlled_text");
                        ComPtr<IUIAutomationElement> parent; toastcheck(walker->GetParentElement(hit.Get(), &parent)); hit = parent;
                    }
                    RECT latestTitle{}; toastcheck(fresh->title->get_CurrentBoundingRectangle(&latestTitle));
                    need(elementName(fresh->title.Get()) == expected && toastRuntime(fresh->title.Get()) == fresh->titleRuntime
                        && toastRuntime(fresh->element.Get()) == fresh->runtime && EqualRect(&latestTitle, &fresh->titleBox), "product_toast_controlled_text_changed");
                    fresh->shell->validate(); POINT actual{};
                    need(matched && owner(GetAncestor(WindowFromPoint(point), GA_ROOT)) == fresh->shell->process->pid
                        && GetCursorPos(&actual) && actual.x == point.x && actual.y == point.y && !(GetAsyncKeyState(VK_LBUTTON) & 0x8000),
                        "product_toast_exact_topmost_handoff_refused");
                    toastBudget(until);
                    inputAttempted = true; INPUT events[2]{}; events[0].type = events[1].type = INPUT_MOUSE;
                    events[0].mi.dwFlags = MOUSEEVENTF_LEFTDOWN; events[1].mi.dwFlags = MOUSEEVENTF_LEFTUP;
                    const auto accepted = SendInput(2, events, sizeof(INPUT));
                    if (accepted == 1) { INPUT release{}; release.type = INPUT_MOUSE; release.mi.dwFlags = MOUSEEVENTF_LEFTUP; SendInput(1, &release, sizeof release); }
                    need(accepted == 2, "product_toast_native_mouse_uncertain");
                }
                std::cout << "{\"found\":true,\"native_shell_pid\":" << fresh->shell->process->pid << ",\"shell_birth\":\"" << fresh->shell->birth
                          << "\",\"signed_shell_image_sha256\":\"" << fresh->shell->image->hash << "\",\"card_control_type\":" << fresh->type
                          << ",\"card_bounds\":[" << fresh->box.left << ',' << fresh->box.top << ',' << fresh->box.right << ',' << fresh->box.bottom
                          << "],\"controlled_title_bounds\":[" << fresh->titleBox.left << ',' << fresh->titleBox.top << ',' << fresh->titleBox.right << ',' << fresh->titleBox.bottom
                          << "],\"card_runtime_id\":" << fresh->runtime << ",\"controlled_title_runtime_id\":" << fresh->titleRuntime
                          << ",\"exact_title_and_body\":true,\"real_mouse_click\":" << jsonBool(click)
                          << ",\"accepted_mouse_events\":" << (click ? 2 : 0) << ",\"original_deadline_tick_ms\":" << until
                          << ",\"budget_remaining_ms_at_start\":" << until - started
                          << ",\"elapsed_ms\":" << GetTickCount64() - started << ',' << toastTrace(trace) << "}\n"; return 0;
            }
        } catch (const ToastDeadline&) { trace.stage = "original_toast_deadline_expired"; break; }
        catch (const ToastUnavailable&) {
            need(!inputAttempted, "product_toast_stale_after_input_unknown"); ++trace.staleElements; trace.stage = "stale_element_reacquiring";
        }
        pump(100);
    }
    bool captured = false; if (!ready.empty()) { try { screenshot(image); captured = true; } catch (...) {} }
    std::cout << "{\"found\":false,\"observation_end_screenshot\":" << jsonBool(captured)
              << ",\"original_deadline_tick_ms\":" << until << ",\"budget_remaining_ms_at_start\":" << until - started
              << ",\"elapsed_ms\":" << GetTickCount64() - started << ',' << toastTrace(trace) << "}\n"; return 3;
}
static int capture(const fs::path& path, const fs::path& stop, int routeCount, wchar_t** routeArgs) {
    ComPtr<IMMDeviceEnumerator> enumerator; ComPtr<IMMDevice> endpoint; ComPtr<IAudioClient> client; ComPtr<IAudioCaptureClient> reader;
    hrcheck(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator)));
    const auto route = PiWindowsAudioFixture::select(enumerator.Get(), routeCount, routeArgs);
    endpoint = route.capture;
    hrcheck(endpoint->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &client));
    WAVEFORMATEX* format = nullptr; hrcheck(client->GetMixFormat(&format));
    bool floating = format->wFormatTag == WAVE_FORMAT_IEEE_FLOAT;
    if (format->wFormatTag == WAVE_FORMAT_EXTENSIBLE && format->cbSize >= 22)
        floating = IsEqualGUID(reinterpret_cast<WAVEFORMATEXTENSIBLE*>(format)->SubFormat, KSDATAFORMAT_SUBTYPE_IEEE_FLOAT);
    need((floating && format->wBitsPerSample == 32) || (!floating && format->wBitsPerSample == 16), "unsupported_native_loopback_format");
    hrcheck(client->Initialize(AUDCLNT_SHAREMODE_SHARED, route.flags, 10000000, 0, format, nullptr));
    hrcheck(client->GetService(IID_PPV_ARGS(&reader))); hrcheck(client->Start());
    std::ofstream file(path, std::ios::binary); need(file.good() && !fs::exists(stop), "native_capture_path_invalid");
    std::cout << "{\"ready\":true,\"sample_rate\":" << format->nSamplesPerSec << ",\"audio_route\":" << route.json << "}\n" << std::flush;
    const auto started = GetTickCount64(); uint64_t samples = 0, nonzero = 0; double peak = 0;
    while (!fs::exists(stop) && GetTickCount64() - started < observationSeconds * 1000) {
        UINT32 packets = 0; hrcheck(reader->GetNextPacketSize(&packets));
        while (packets) {
            BYTE* bytes = nullptr; UINT32 frames = 0; DWORD flags = 0;
            hrcheck(reader->GetBuffer(&bytes, &frames, &flags, nullptr, nullptr));
            for (UINT32 index = 0; index < frames; index++) {
                double value = 0;
                if (!(flags & AUDCLNT_BUFFERFLAGS_SILENT) && bytes) {
                    for (UINT32 channel = 0; channel < format->nChannels; channel++)
                        value += floating ? reinterpret_cast<float*>(bytes)[index * format->nChannels + channel]
                                          : reinterpret_cast<int16_t*>(bytes)[index * format->nChannels + channel] / 32768.0;
                    value /= format->nChannels;
                }
                need(std::isfinite(value), "native_audio_not_finite");
                value = std::clamp(value, -1.0, 1.0); peak = std::max(peak, std::abs(value));
                int16_t pcm = static_cast<int16_t>(value * 32767); file.write(reinterpret_cast<char*>(&pcm), sizeof pcm);
                samples++; if (std::abs(static_cast<int>(pcm)) > 128) nonzero++;
            }
            hrcheck(reader->ReleaseBuffer(frames)); hrcheck(reader->GetNextPacketSize(&packets));
            need(samples <= static_cast<uint64_t>(format->nSamplesPerSec) * (observationSeconds + 1), "native_audio_capture_bound");
        }
        pump(5);
    }
    hrcheck(client->Stop()); DWORD state = 0; hrcheck(endpoint->GetState(&state));
    const auto rate = format->nSamplesPerSec; CoTaskMemFree(format);
    need(state == DEVICE_STATE_ACTIVE && file.good() && fs::exists(stop), "native_audio_capture_incomplete");
    std::cout << "{\"complete\":true,\"sample_rate\":" << rate << ",\"samples\":" << samples
              << ",\"peak\":" << peak << ",\"nonzero_samples\":" << nonzero
              << ",\"elapsed_ms\":" << GetTickCount64() - started << ",\"audio_route\":" << route.json << "}\n"; return 0;
}
static int decode(const fs::path& source, const fs::path& output) {
    // Decode the actual configured asset for comparison only; never render it.
    hrcheck(MFStartup(MF_VERSION, MFSTARTUP_NOSOCKET));
    ComPtr<IMFSourceReader> reader; hrcheck(MFCreateSourceReaderFromURL(source.c_str(), nullptr, &reader));
    ComPtr<IMFMediaType> type; hrcheck(MFCreateMediaType(&type));
    hrcheck(type->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Audio)); hrcheck(type->SetGUID(MF_MT_SUBTYPE, MFAudioFormat_PCM));
    hrcheck(type->SetUINT32(MF_MT_AUDIO_BITS_PER_SAMPLE, 16));
    hrcheck(reader->SetCurrentMediaType(MF_SOURCE_READER_FIRST_AUDIO_STREAM, nullptr, type.Get()));
    ComPtr<IMFMediaType> actual; hrcheck(reader->GetCurrentMediaType(MF_SOURCE_READER_FIRST_AUDIO_STREAM, &actual));
    UINT32 sampleRate = 0, channelCount = 0, bitCount = 0;
    hrcheck(actual->GetUINT32(MF_MT_AUDIO_SAMPLES_PER_SECOND, &sampleRate));
    hrcheck(actual->GetUINT32(MF_MT_AUDIO_NUM_CHANNELS, &channelCount));
    hrcheck(actual->GetUINT32(MF_MT_AUDIO_BITS_PER_SAMPLE, &bitCount));
    need(bitCount == 16 && (channelCount == 1 || channelCount == 2) && sampleRate >= 8000 && sampleRate <= 192000,
         "configured_cue_pcm_format");
    std::ofstream file(output, std::ios::binary); char empty[44]{}; file.write(empty, sizeof empty);
    uint32_t bytes = 0;
    for (;;) {
        DWORD flags = 0; ComPtr<IMFSample> sample;
        hrcheck(reader->ReadSample(MF_SOURCE_READER_FIRST_AUDIO_STREAM, 0, nullptr, &flags, nullptr, &sample));
        if (sample) {
            ComPtr<IMFMediaBuffer> buffer; hrcheck(sample->ConvertToContiguousBuffer(&buffer));
            BYTE* data = nullptr; DWORD size = 0; hrcheck(buffer->Lock(&data, nullptr, &size));
            need(bytes + size <= 30 * sampleRate * channelCount * 2, "configured_cue_decode_bound");
            file.write(reinterpret_cast<char*>(data), size); bytes += size; hrcheck(buffer->Unlock());
        }
        if (flags & MF_SOURCE_READERF_ENDOFSTREAM) break;
    }
    const uint32_t riff = bytes + 36, sixteen = 16, rate = sampleRate, average = sampleRate * channelCount * 2;
    const uint16_t pcm = 1, channels = static_cast<uint16_t>(channelCount), align = static_cast<uint16_t>(channelCount * 2), bits = 16;
    file.seekp(0); file.write("RIFF", 4); file.write(reinterpret_cast<const char*>(&riff), 4); file.write("WAVEfmt ", 8);
    file.write(reinterpret_cast<const char*>(&sixteen), 4); file.write(reinterpret_cast<const char*>(&pcm), 2);
    file.write(reinterpret_cast<const char*>(&channels), 2); file.write(reinterpret_cast<const char*>(&rate), 4);
    file.write(reinterpret_cast<const char*>(&average), 4); file.write(reinterpret_cast<const char*>(&align), 2);
    file.write(reinterpret_cast<const char*>(&bits), 2); file.write("data", 4); file.write(reinterpret_cast<const char*>(&bytes), 4);
    need(bytes && file.good(), "configured_cue_decode_failed");
    std::cout << "{\"decoded\":true,\"bytes\":" << bytes << "}\n"; return 0;
}
int wmain(int argc, wchar_t** argv) {
    try {
        need(argc >= 2, "native_observer_command_required"); const std::wstring mode = argv[1];
        if (mode == L"preflight" && (argc == 3 || argc == 9)) return piPrerequisiteEntry(argc - 1, argv + 1);
        hrcheck(CoInitializeEx(nullptr, COINIT_MULTITHREADED));
        if (mode == L"process-test-child" && argc == 2) { Sleep(20000); return 0; }
        if (mode == L"process-test-tree" && argc == 3) {
            TestChild child(L"process-test-child");
            { std::ofstream ready(argv[2]); ready << child.process.dwProcessId; need(ready.good(), "process_guard_ready_file_failed"); }
            Sleep(20000); return 0;
        }
        if (mode == L"test-process-identity" && argc == 2) return testProcessIdentity();
        if ((mode == L"process-identity-handle" && argc == 4) || (mode == L"collect-owned-handle" && argc == 5)) {
            const auto raw = std::stoull(argv[2]); need(raw > 0 && raw <= UINTPTR_MAX, "owned_process_handle_required");
            const auto handle = reinterpret_cast<HANDLE>(static_cast<uintptr_t>(raw));
            if (mode == L"collect-owned-handle") {
                const auto created = std::stoull(argv[3]); need(created > 0, "owned_process_birth_required");
                std::cout << collectOwnedProcess(heldProcess(handle, created, argv[4])) << '\n'; return 0;
            }
            auto process = heldProcess(handle, 0, argv[3]);
            std::cout << "{\"present\":true,\"pid\":" << process->pid << ",\"creation_time\":\"" << process->created
                      << "\",\"executable\":" << NavigationTokenTEST::quoted(utf8(process->image)) << "}\n"; return 0;
        }
        if (mode == L"process-watch-owned-handle" && argc == 10) {
            const auto raw = std::stoull(argv[2]), birth = std::stoull(argv[3]), deadline = std::stoull(argv[8]);
            need(raw > 0 && raw <= UINTPTR_MAX && birth > 0, "process_watch_original_handle_required");
            const fs::path receipt = argv[9]; auto pending = receipt; pending += L".pending";
            need(!fs::exists(fs::symlink_status(receipt)) && !fs::exists(fs::symlink_status(pending))
                 && receipt.parent_path() == fs::path(argv[6]).parent_path() && receipt.parent_path() == fs::path(argv[7]).parent_path(),
                 "process_watch_fresh_receipt_required");
            const auto value = watchOwnedProcesses(heldProcess(reinterpret_cast<HANDLE>(static_cast<uintptr_t>(raw)), birth, argv[4]),
                                                   argv[5], argv[6], argv[7], deadline);
            {
                std::ofstream file(pending); file << value << '\n';
                need(file.good(), "process_watch_receipt_write_failed");
            }
            fs::rename(pending, receipt);
            // The closed file avoids bulk stdout backpressure retaining the
            // watcher past its original cutoff during a longer negative case.
            std::cout << "{\"receipt_written\":true}\n"; return 0;
        }
        if (mode == L"screenshot" && argc == 3) { screenshot(argv[2]); std::cout << "{\"captured\":true}\n"; return 0; }
        if (mode == L"capture" && (argc == 4 || argc == 10)) return capture(argv[2], argv[3], argc - 4, argv + 4);
        if (mode == L"decode" && argc == 4) return decode(argv[2], argv[3]);
        if (mode == L"neutral" && argc == 4) {
            const std::wstring name = argv[2]; need(testTitle(name) && !fs::exists(argv[3]), "owned_neutral_window_required");
            WNDCLASSW klass{}; klass.lpfnWndProc = windowProc; klass.hInstance = GetModuleHandleW(nullptr);
            klass.lpszClassName = L"TEST.Pi.Product.Neutral"; RegisterClassW(&klass);
            HWND window = CreateWindowExW(0, klass.lpszClassName, name.c_str(), WS_OVERLAPPEDWINDOW | WS_VISIBLE,
                                          40, 160, 500, 250, nullptr, nullptr, klass.hInstance, nullptr);
            need(window != nullptr, "neutral_window_unavailable");
            std::cout << "{\"hwnd\":" << reinterpret_cast<uintptr_t>(window) << ",\"pid\":" << GetCurrentProcessId() << "}\n" << std::flush;
            const auto until = GetTickCount64() + 45000;
            while (IsWindow(window) && !fs::exists(argv[3]) && GetTickCount64() < until) pump(10);
            if (IsWindow(window)) DestroyWindow(window); return 0;
        }
        if (mode == L"foreground") {
            HWND window = GetForegroundWindow();
            std::cout << "{\"hwnd\":" << reinterpret_cast<uintptr_t>(window) << ",\"pid\":" << owner(window)
                      << ",\"keyboard_hwnd\":" << reinterpret_cast<uintptr_t>(keyboardRoot()) << "}\n"; return 0;
        }
        if (mode == L"window" && argc == 3) {
            struct Search { std::wstring expected; std::vector<HWND> found; } search{argv[2], {}};
            need(testTitle(search.expected), "owned_window_title_required");
            EnumWindows([](HWND window, LPARAM value) -> BOOL {
                auto& search = *reinterpret_cast<Search*>(value);
                if (IsWindowVisible(window) && terminalClass(window) && title(window).rfind(search.expected, 0) == 0) search.found.push_back(window);
                return TRUE;
            }, reinterpret_cast<LPARAM>(&search));
            need(search.found.size() <= 1, "owned_terminal_window_ambiguous");
            const HWND window = search.found.empty() ? nullptr : search.found[0];
            std::cout << "{\"hwnd\":" << reinterpret_cast<uintptr_t>(window) << ",\"pid\":" << owner(window) << "}\n"; return 0;
        }
        if ((mode == L"activate" || mode == L"close" || mode == L"type") && argc >= 5) {
            HWND window = nullptr;
            try { window = boundWindow(argv[2], argv[3], argv[4]); }
            catch (const std::runtime_error&) {
                // The owned shell can exit after lookup, while its Pi child is
                // collected. Only an actually absent original HWND is complete.
                if (mode != L"close" || !testTitle(argv[4])) throw;
                const auto original = reinterpret_cast<HWND>(static_cast<uintptr_t>(std::stoull(argv[2])));
                if (IsWindow(original)) throw;
                std::cout << "{\"ok\":true,\"already_absent\":true}\n"; return 0;
            }
            if (mode == L"close") PostMessageW(window, WM_CLOSE, 0, 0);
            else { foreground(window); if (mode == L"type") { need(argc == 6, "native_text_required"); typeLine(window, argv[5]); } }
            std::cout << "{\"ok\":true}\n"; return 0;
        }
        if ((mode == L"toast" || mode == L"toast-click") && argc == 5) return toast(argv[2], argv[3], argv[4], mode == L"toast-click");
        if (mode == L"toast-click-until" && argc == 6) return toast(argv[2], argv[3], argv[4], true, {}, std::stoull(argv[5]));
        if (mode == L"toast-watch" && argc == 6) return toast(argv[2], argv[3], argv[4], false, argv[5]);
        throw std::runtime_error("unsupported_native_observer_command");
    } catch (const NavigationTokenTEST::Failure& error) {
        std::cerr << error.query << ':' << error.error; return 2;
    } catch (const std::exception& error) { std::cerr << error.what(); return 2; }
}
