// Narrow TEST observer. Real Terminal/Shell input + external WASAPI capture.
// Reuse the existing WTS/input/audio inventory and token-query primitives.
#define _WIN32_WINNT 0x0A00
#define wmain piPrerequisiteEntry
#include "pi_native_preflight_windows.cpp"
#undef wmain
#include "navigation_windows_token_queries.h"
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
static void need(bool value, const char* code) { if (!value) throw std::runtime_error(code); }
static void hrcheck(HRESULT value) { if (FAILED(value)) throw std::runtime_error("native_hresult:" + std::to_string(value)); }
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
static std::unique_ptr<OwnedProcess> processIdentity(DWORD pid) {
    need(pid != 0, "owned_process_pid_required");
    auto process = std::make_unique<OwnedProcess>(); process->pid = pid;
    process->handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_TERMINATE | SYNCHRONIZE, FALSE, pid);
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
static int testProcessIdentity() {
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
static bool shellOwner(DWORD pid) {
    HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
    if (!process) return false;
    wchar_t image[32768]{}, windows[32768]{}; DWORD size = 32768;
    const bool known = QueryFullProcessImageNameW(process, 0, image, &size) && GetWindowsDirectoryW(windows, 32768);
    NavigationTokenTEST::Token token, self;
    bool matched = false;
    if (known && OpenProcessToken(process, TOKEN_QUERY, &token.handle) && OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &self.handle)) {
        const auto a = NavigationTokenTEST::facts(token.handle), b = NavigationTokenTEST::facts(self.handle);
        std::wstring actual = image, prefix = std::wstring(windows) + L"\\";
        std::transform(actual.begin(), actual.end(), actual.begin(), towlower);
        std::transform(prefix.begin(), prefix.end(), prefix.begin(), towlower);
        const auto leaf = fs::path(actual).filename().wstring();
        DWORD code = 0;
        matched = GetExitCodeProcess(process, &code) && code == STILL_ACTIVE && a.sid == b.sid && a.session == b.session
            && actual.rfind(prefix, 0) == 0 && (leaf == L"shellexperiencehost.exe" || leaf == L"shellhost.exe" || leaf == L"explorer.exe");
    }
    CloseHandle(process); return matched;
}
static std::wstring elementName(IUIAutomationElement* element) {
    BSTR raw = nullptr; hrcheck(element->get_CurrentName(&raw));
    std::wstring value = raw ? raw : L""; if (raw) SysFreeString(raw);
    need(value.size() <= 4096, "native_ui_text_bound"); return value;
}
struct ToastTrace {
    int maxTitleMatches = 0;
    bool shellProviderSeen = false;
    std::string stage = "not_started";
    std::vector<int> ancestorTypes;
};
static std::string toastTrace(const ToastTrace& trace) {
    std::string types = "[";
    for (const auto type : trace.ancestorTypes) { if (types.size() > 1) types += ','; types += std::to_string(type); }
    return "\"max_exact_title_matches\":" + std::to_string(trace.maxTitleMatches)
        + ",\"shell_provider_seen\":" + jsonBool(trace.shellProviderSeen)
        + ",\"last_stage\":\"" + trace.stage + "\",\"ancestor_control_types\":" + types + "]";
}
static ComPtr<IUIAutomationElement> card(IUIAutomation* automation, const std::wstring& expected, const std::wstring& body, ToastTrace& trace) {
    ComPtr<IUIAutomationElement> root, text; hrcheck(automation->GetRootElement(&root));
    VARIANT name{}; name.vt = VT_BSTR; name.bstrVal = SysAllocString(expected.c_str());
    ComPtr<IUIAutomationCondition> match; hrcheck(automation->CreatePropertyCondition(UIA_NamePropertyId, name, &match)); VariantClear(&name);
    ComPtr<IUIAutomationElementArray> all; hrcheck(root->FindAll(TreeScope_Descendants, match.Get(), &all));
    int total = 0; hrcheck(all->get_Length(&total)); need(total <= 1, "product_toast_title_ambiguous");
    trace.maxTitleMatches = std::max(trace.maxTitleMatches, total);
    trace.stage = "exact_title_missing";
    if (!total) return nullptr;
    hrcheck(all->GetElement(0, &text));
    int pid = 0; hrcheck(text->get_CurrentProcessId(&pid));
    trace.stage = "exact_title_provider_not_shell";
    if (!shellOwner(pid)) return nullptr;
    trace.shellProviderSeen = true; trace.ancestorTypes.clear(); trace.stage = "exact_card_ancestor_missing";
    ComPtr<IUIAutomationTreeWalker> walker; hrcheck(automation->get_ControlViewWalker(&walker));
    auto current = text;
    for (int depth = 0; depth < 6; depth++) {
        CONTROLTYPEID type = 0; int currentPID = 0;
        hrcheck(current->get_CurrentControlType(&type)); hrcheck(current->get_CurrentProcessId(&currentPID));
        trace.ancestorTypes.push_back(type);
        if (currentPID != pid) return nullptr;
        if (type == UIA_ButtonControlTypeId || type == UIA_ListItemControlTypeId) {
            ComPtr<IUIAutomationCondition> yes; hrcheck(automation->CreateTrueCondition(&yes));
            ComPtr<IUIAutomationElementArray> children; hrcheck(current->FindAll(TreeScope_Descendants, yes.Get(), &children));
            int count = 0; hrcheck(children->get_Length(&count)); need(count <= 64, "product_toast_group_bound");
            bool titleSeen = false, bodySeen = false;
            for (int index = 0; index < count; index++) {
                ComPtr<IUIAutomationElement> child; hrcheck(children->GetElement(index, &child));
                const auto value = elementName(child.Get());
                titleSeen = titleSeen || value == expected; bodySeen = bodySeen || value.find(body) != std::wstring::npos;
            }
            if (titleSeen && bodySeen) { trace.stage = "exact_card_selected"; return current; }
            trace.stage = "exact_card_title_or_body_missing";
        }
        ComPtr<IUIAutomationElement> parent; hrcheck(walker->GetParentElement(current.Get(), &parent));
        if (!parent) break; current = parent;
    }
    return nullptr;
}
static int toast(const std::wstring& expected, const std::wstring& body, const fs::path& image, bool click, const fs::path& ready = {}) {
    need(testTitle(expected) && !body.empty() && body.size() <= 256, "owned_product_toast_selector_required");
    ComPtr<IUIAutomation> automation; hrcheck(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)));
    if (!ready.empty()) {
        need(!click && !fs::exists(ready), "observation_only_toast_ready_path_required");
        auto pending = ready; pending += L".pending";
        need(!fs::exists(pending), "observation_only_toast_pending_path_required");
        {
            std::ofstream file(pending); file << "{\"ready\":true,\"observation_only\":true,\"budget_ms\":15000}\n";
            need(file.good(), "toast_observer_ready_write_failed");
        }
        fs::rename(pending, ready);
    }
    ToastTrace trace;
    const auto started = GetTickCount64(), until = started + 15000;
    while (GetTickCount64() < until) {
        auto selected = card(automation.Get(), expected, body, trace);
        if (selected) {
            BOOL offscreen = TRUE, clickable = FALSE; POINT point{};
            hrcheck(selected->get_CurrentIsOffscreen(&offscreen)); hrcheck(selected->GetClickablePoint(&point, &clickable));
            int pid = 0; hrcheck(selected->get_CurrentProcessId(&pid));
            need(!offscreen && clickable && shellOwner(pid), "product_toast_not_visible_clickable_shell");
            screenshot(image);
            if (click) {
                need(owner(GetAncestor(WindowFromPoint(point), GA_ROOT)) == static_cast<DWORD>(pid), "product_toast_click_occluded");
                need(mouse(point), "product_toast_native_mouse_denied");
            }
            std::cout << "{\"found\":true,\"native_shell_pid\":" << pid << ",\"real_mouse_click\":" << jsonBool(click)
                      << ",\"elapsed_ms\":" << GetTickCount64() - started << ',' << toastTrace(trace) << "}\n";
            return 0;
        }
        pump(100);
    }
    std::cout << "{\"found\":false,\"elapsed_ms\":" << GetTickCount64() - started << ',' << toastTrace(trace) << "}\n"; return 3;
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
    while (!fs::exists(stop) && GetTickCount64() - started < 30000) {
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
            need(samples <= static_cast<uint64_t>(format->nSamplesPerSec) * 31, "native_audio_capture_bound");
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
            HWND window = boundWindow(argv[2], argv[3], argv[4]);
            if (mode == L"close") PostMessageW(window, WM_CLOSE, 0, 0);
            else { foreground(window); if (mode == L"type") { need(argc == 6, "native_text_required"); typeLine(window, argv[5]); } }
            std::cout << "{\"ok\":true}\n"; return 0;
        }
        if ((mode == L"toast" || mode == L"toast-click") && argc == 5) return toast(argv[2], argv[3], argv[4], mode == L"toast-click");
        if (mode == L"toast-watch" && argc == 6) return toast(argv[2], argv[3], argv[4], false, argv[5]);
        throw std::runtime_error("unsupported_native_observer_command");
    } catch (const NavigationTokenTEST::Failure& error) {
        std::cerr << error.query << ':' << error.error; return 2;
    } catch (const std::exception& error) { std::cerr << error.what(); return 2; }
}
