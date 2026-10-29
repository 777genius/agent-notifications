// Finite guest preparation and explicit endpoint discovery for CI TEST fixtures.
#define NOMINMAX
#include <windows.h>
#include <mmdeviceapi.h>
#include <endpointvolume.h>
#include <audioclient.h>
#include <UIAutomation.h>
#include <wtsapi32.h>
#include "pi_windows_audio_fixture.h"
#include "navigation_windows_token_queries.h"
#include <functiondiscoverykeys_devpkey.h>
#include <wrl/client.h>
#include <cstdint>
#include <iostream>
#include <string>
#include <vector>
#include <filesystem>
#include <fstream>
#include <memory>
#include <functional>
#include <algorithm>
#pragma comment(lib, "Uiautomationcore.lib")
#pragma comment(lib, "Bcrypt.lib")
#pragma comment(lib, "Wtsapi32.lib")
#pragma comment(lib, "Gdi32.lib")

using Microsoft::WRL::ComPtr;
namespace fs = std::filesystem;

static void demand(bool value, const char* reason) { if (!value) throw reason; }
static std::wstring environment(const wchar_t* key) {
    wchar_t value[256]{}; const auto size = GetEnvironmentVariableW(key, value, 256);
    return size && size < 256 ? std::wstring(value, size) : L"";
}
static std::wstring objectName(HANDLE handle) {
    wchar_t value[256]{}; DWORD bytes = 0;
    demand(GetUserObjectInformationW(handle, UOI_NAME, value, sizeof(value), &bytes), "input desktop name unavailable");
    return value;
}
static void inspectionDesktop() {
    OSVERSIONINFOEXW version{}; version.dwOSVersionInfoSize = sizeof version;
    const auto rtl = reinterpret_cast<LONG(WINAPI*)(OSVERSIONINFOEXW*)>(GetProcAddress(GetModuleHandleW(L"ntdll.dll"), "RtlGetVersion"));
    SYSTEM_INFO architecture{}; GetNativeSystemInfo(&architecture);
    demand(rtl && rtl(&version) == 0 && version.wProductType == VER_NT_WORKSTATION && version.dwBuildNumber >= 22000
           && architecture.wProcessorArchitecture == PROCESSOR_ARCHITECTURE_ARM64, "official ARM64 Client required");
    DWORD session = 0, bytes = 0; LPWSTR state = nullptr;
    demand(ProcessIdToSessionId(GetCurrentProcessId(), &session) && session != 0, "interactive session required");
    const BOOL queried = WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, session, WTSConnectState, &state, &bytes);
    const bool active = queried && state && bytes == sizeof(WTS_CONNECTSTATE_CLASS)
        && *reinterpret_cast<WTS_CONNECTSTATE_CLASS*>(state) == WTSActive;
    if (state) WTSFreeMemory(state);
    demand(active, "active console connection required");
    const auto station = GetProcessWindowStation(); USEROBJECTFLAGS flags{}; DWORD needed = 0;
    demand(GetUserObjectInformationW(station, UOI_FLAGS, &flags, sizeof flags, &needed) && (flags.dwFlags & WSF_VISIBLE)
           && objectName(station) == L"WinSta0", "visible interactive station required");
    const auto input = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS);
    demand(input != nullptr, "input desktop unavailable");
    const auto actual = objectName(input); CloseDesktop(input);
    demand(actual == L"Default" && actual == objectName(GetThreadDesktop(GetCurrentThreadId())), "current input desktop changed");
}
static std::vector<std::pair<std::wstring, std::string>> shellImages() {
    wchar_t windows[32768]{}; const auto size = GetWindowsDirectoryW(windows, 32768);
    demand(size > 0 && size < 32768, "Windows directory unavailable");
    const std::wstring base(windows);
    return {{base + L"\\explorer.exe", "explorer"},
        {base + L"\\System32\\ShellHost.exe", "system32_shell_host"},
        {base + L"\\SystemApps\\ShellExperienceHost_cw5n1h2txyewy\\ShellExperienceHost.exe", "shell_experience_host"},
        {base + L"\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\ShellHost.exe", "cbs_shell_host"},
        {base + L"\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\SearchHost.exe", "cbs_search_host"}};
}
struct InspectionOwner {
    HANDLE process = nullptr; DWORD pid = 0; ULONGLONG birth = 0; std::string imageKind;
    std::wstring image;
    explicit InspectionOwner(DWORD value) : pid(value) {
        process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, pid);
        if (!process) throw "Shell provider process unavailable";
        try { validate(true); } catch (...) { CloseHandle(process); process = nullptr; throw; }
    }
    ~InspectionOwner() { if (process) CloseHandle(process); }
    void validate(bool initial = false) {
        demand(WaitForSingleObject(process, 0) == WAIT_TIMEOUT, "Shell provider exited");
        FILETIME created{}, exited{}, kernel{}, used{};
        demand(GetProcessTimes(process, &created, &exited, &kernel, &used), "Shell birth unavailable");
        const auto actualBirth = (static_cast<ULONGLONG>(created.dwHighDateTime) << 32) | created.dwLowDateTime;
        demand(actualBirth != 0 && (initial || actualBirth == birth), "Shell birth changed");
        wchar_t path[32768]{}; DWORD count = 32768;
        demand(QueryFullProcessImageNameW(process, 0, path, &count) && count > 0 && count < 32768, "Shell image unavailable");
        auto known = shellImages(); const auto match = std::find_if(known.begin(), known.end(), [&](const auto& item) {
            return _wcsicmp(path, item.first.c_str()) == 0;
        });
        demand(match != known.end() && (initial || _wcsicmp(path, image.c_str()) == 0), "Shell provider image not admitted");
        NavigationTokenTEST::Token actual, own;
        demand(OpenProcessToken(process, TOKEN_QUERY, &actual.handle)
               && OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &own.handle), "Shell token unavailable");
        const auto a = NavigationTokenTEST::facts(actual.handle), b = NavigationTokenTEST::facts(own.handle);
        demand(a.sid == b.sid && a.auth == b.auth && a.session == b.session && a.session != 0, "Shell user authentication or session changed");
        if (initial) { birth = actualBirth; image = path; imageKind = match->second; }
    }
};
static void inspectionFile(const fs::path& path, const std::string& value) {
    demand(!fs::exists(fs::symlink_status(path)) && value.size() <= 65536, "fresh bounded inspection receipt required");
    std::ofstream output(path, std::ios::binary); output << value << '\n';
    demand(output.good(), "inspection receipt write failed");
}
static void inspectionScreenshot(const fs::path& path) {
    demand(!fs::exists(fs::symlink_status(path)), "fresh inspection image required");
    const int width = GetSystemMetrics(SM_CXSCREEN), height = GetSystemMetrics(SM_CYSCREEN);
    demand(width > 0 && height > 0 && static_cast<uint64_t>(width) * height <= 16000000, "inspection image bound");
    HDC screen = GetDC(nullptr), memory = CreateCompatibleDC(screen);
    BITMAPINFO info{}; info.bmiHeader.biSize = sizeof(BITMAPINFOHEADER); info.bmiHeader.biWidth = width;
    info.bmiHeader.biHeight = -height; info.bmiHeader.biPlanes = 1; info.bmiHeader.biBitCount = 32;
    void* pixels = nullptr; HBITMAP bitmap = CreateDIBSection(screen, &info, DIB_RGB_COLORS, &pixels, nullptr, 0);
    demand(screen && memory && bitmap && pixels, "inspection screenshot unavailable");
    const auto old = SelectObject(memory, bitmap);
    const bool copied = BitBlt(memory, 0, 0, width, height, screen, 0, 0, SRCCOPY | CAPTUREBLT) != FALSE;
    BITMAPFILEHEADER header{}; header.bfType = 0x4d42; header.bfOffBits = sizeof header + sizeof(BITMAPINFOHEADER);
    header.bfSize = header.bfOffBits + width * height * 4;
    std::ofstream file(path, std::ios::binary);
    if (copied) { file.write(reinterpret_cast<char*>(&header), sizeof header); file.write(reinterpret_cast<char*>(&info.bmiHeader), sizeof info.bmiHeader);
        file.write(static_cast<char*>(pixels), static_cast<std::streamsize>(width) * height * 4); }
    SelectObject(memory, old); DeleteObject(bitmap); DeleteDC(memory); ReleaseDC(nullptr, screen);
    demand(copied && file.good(), "inspection screenshot failed");
}
static std::string inspectionText(BSTR raw) {
    if (raw && SysStringLen(raw) > 128) { SysFreeString(raw); throw "inspection identifier length bound"; }
    const std::wstring value = raw ? raw : L""; if (raw) SysFreeString(raw);
    const auto count = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    demand(value.empty() || count > 0, "inspection identifier encoding");
    std::string result(count, 0);
    WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()), result.data(), count, nullptr, nullptr);
    return NavigationTokenTEST::quoted(result);
}
static void inspectNotificationCenter(const fs::path& root) {
    const auto source = environment(L"SOURCE_SHA");
    demand(environment(L"GITHUB_REPOSITORY") == L"777genius/agent-notifications" && environment(L"GITHUB_EVENT_NAME") == L"workflow_dispatch"
           && environment(L"GITHUB_ACTOR") == L"777genius" && environment(L"GITHUB_TRIGGERING_ACTOR") == L"777genius"
           && environment(L"GITHUB_RUN_ATTEMPT") == L"1" && environment(L"PI_WINDOWS_HOST_SETUP") == L"1"
           && environment(L"PI_WINDOWS_NOTIFICATION_UI_INSPECTION") == L"1"
           && environment(L"PI_WINDOWS_HOST_RUNNER_ENVIRONMENT") == L"github-hosted"
           && environment(L"PI_WINDOWS_HOST_RUNNER_LABEL") == L"windows-11-vs2026-arm"
           && source.size() == 40 && std::all_of(source.begin(), source.end(), [](wchar_t c) { return (c >= L'0' && c <= L'9') || (c >= L'a' && c <= L'f'); }),
           "explicit fresh owner notification inspection required");
    inspectionDesktop();
    const auto started = GetTickCount64(), deadline = started + 20000;
    auto budget = [&]() { demand(GetTickCount64() < deadline, "notification inspection deadline"); };
    const HWND shell = GetShellWindow(); DWORD shellPID = 0; GetWindowThreadProcessId(shell, &shellPID);
    InspectionOwner explorer(shellPID); demand(explorer.imageKind == "explorer", "exact Explorer required before chord");
    inspectionScreenshot(root / L"notification-ui-before.bmp");
    const auto prefix = "{\"scope\":\"public_notification_center_inspection_only\",\"product_executed\":false,\"qualification_input\":false,\"settings_writes\":0,\"native_show_attempts\":0";
    inspectionFile(root / L"notification-ui-intent.json", std::string(prefix) + ",\"explorer_pid\":" + std::to_string(shellPID)
        + ",\"explorer_birth\":\"" + std::to_string(explorer.birth) + "\",\"one_win_n_chord_intended\":true}");
    for (int key : {VK_LWIN, VK_RWIN, static_cast<int>('N'), VK_SHIFT, VK_CONTROL, VK_MENU})
        demand(!(GetAsyncKeyState(key) & 0x8000), "existing key press prevents chord");
    struct Keys {
        bool win = false, n = false; unsigned accepted = 0; bool releaseUnknown = false;
        bool send(WORD key, bool up) {
            INPUT input{}; input.type = INPUT_KEYBOARD; input.ki.wVk = key; input.ki.dwFlags = up ? KEYEVENTF_KEYUP : 0;
            if (SendInput(1, &input, sizeof input) != 1) return false;
            ++accepted; (key == VK_LWIN ? win : n) = !up; return true;
        }
        void release() { if (n) { if (!send('N', true)) releaseUnknown = true; n = false; }
            if (win) { if (!send(VK_LWIN, true)) releaseUnknown = true; win = false; } }
        ~Keys() { release(); }
    } keys;
    budget(); explorer.validate(); inspectionDesktop(); demand(GetShellWindow() == shell, "Explorer window changed");
    const bool accepted = keys.send(VK_LWIN, false) && keys.send('N', false) && keys.send('N', true) && keys.send(VK_LWIN, true);
    keys.release();
    inspectionFile(root / L"notification-ui-input.json", std::string(prefix) + ",\"win_n_chord_attempts\":1,\"accepted_key_events\":"
        + std::to_string(keys.accepted) + ",\"key_release_unknown\":" + (keys.releaseUnknown ? "true" : "false") + "}");
    demand(accepted && !keys.releaseUnknown, "single notification chord was not collected");
    Sleep(500); budget(); inspectionScreenshot(root / L"notification-ui-after.bmp");
    const HWND panel = GetForegroundWindow(); DWORD panelPID = 0; GetWindowThreadProcessId(panel, &panelPID);
    std::vector<std::unique_ptr<InspectionOwner>> owners; owners.push_back(std::make_unique<InspectionOwner>(panelPID));
    ComPtr<IUIAutomation> automation; demand(SUCCEEDED(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation))), "inspection UIA unavailable");
    ComPtr<IUIAutomationElement> element; demand(SUCCEEDED(automation->ElementFromHandle(panel, &element)) && element, "foreground panel UIA unavailable");
    ComPtr<IUIAutomationTreeWalker> walker; demand(SUCCEEDED(automation->get_ControlViewWalker(&walker)), "inspection walker unavailable");
    unsigned visited = 0, totalVisited = 0, skipped = 0, errors = 0; bool complete = true; std::string rows;
    auto owner = [&](DWORD pid) -> InspectionOwner* {
        for (const auto& held : owners) if (held->pid == pid) { held->validate(); return held.get(); }
        demand(owners.size() < 8, "inspection provider bound");
        owners.push_back(std::make_unique<InspectionOwner>(pid)); return owners.back().get();
    };
    std::function<void(ComPtr<IUIAutomationElement>, unsigned, int)> walk;
    walk = [&](ComPtr<IUIAutomationElement> node, unsigned depth, int parent) {
        budget(); demand(depth <= 12 && visited < 128 && totalVisited++ < 512, "inspection census bound");
        int pid = 0; demand(SUCCEEDED(node->get_CurrentProcessId(&pid)) && pid > 0, "inspection provider unavailable");
        InspectionOwner* held = nullptr;
        try { held = owner(static_cast<DWORD>(pid)); } catch (...) { ++skipped; complete = false; return; }
        CONTROLTYPEID type = 0; BOOL enabled = FALSE, offscreen = TRUE; RECT bounds{};
        demand(SUCCEEDED(node->get_CurrentControlType(&type)) && SUCCEEDED(node->get_CurrentIsEnabled(&enabled))
               && SUCCEEDED(node->get_CurrentIsOffscreen(&offscreen)) && SUCCEEDED(node->get_CurrentBoundingRectangle(&bounds)), "inspection fixed properties unavailable");
        BSTR id = nullptr, klass = nullptr, name = nullptr;
        demand(SUCCEEDED(node->get_CurrentAutomationId(&id)), "inspection automation id unavailable");
        const auto automationID = inspectionText(id);
        demand(SUCCEEDED(node->get_CurrentClassName(&klass)), "inspection class unavailable");
        const auto className = inspectionText(klass);
        demand(SUCCEEDED(node->get_CurrentName(&name)), "inspection name query unavailable");
        const std::wstring nameValue = name && SysStringLen(name) <= 128 ? name : L""; if (name) SysFreeString(name);
        std::string publicName = "null";
        for (const auto label : {L"Notifications", L"Do not disturb", L"Do not disturb on", L"Do not disturb off",
                 L"Do not disturb is on", L"Do not disturb is off", L"Turn on do not disturb", L"Turn off do not disturb"})
            if (nameValue == label) { BSTR copy = SysAllocString(label); publicName = inspectionText(copy); break; }
        SAFEARRAY* runtime = nullptr; demand(SUCCEEDED(node->GetRuntimeId(&runtime)) && runtime, "inspection runtime id unavailable");
        LONG lo = 0, hi = -1; VARTYPE variant = VT_EMPTY;
        const bool arrayValid = SafeArrayGetDim(runtime) == 1 && SUCCEEDED(SafeArrayGetVartype(runtime, &variant)) && variant == VT_I4
            && SUCCEEDED(SafeArrayGetLBound(runtime, 1, &lo)) && SUCCEEDED(SafeArrayGetUBound(runtime, 1, &hi)) && hi >= lo && hi - lo < 32;
        std::string runtimeJSON = "[";
        if (arrayValid) for (LONG index = lo; index <= hi; ++index) { LONG value = 0;
            if (FAILED(SafeArrayGetElement(runtime, &index, &value))) { SafeArrayDestroy(runtime); throw "inspection runtime id read failed"; }
            if (index != lo) runtimeJSON += ','; runtimeJSON += std::to_string(value); }
        SafeArrayDestroy(runtime); demand(arrayValid, "inspection runtime id shape"); runtimeJSON += ']';
        ComPtr<IUIAutomationTogglePattern> toggle; ToggleState state = ToggleState_Indeterminate;
        const HRESULT pattern = node->GetCurrentPatternAs(UIA_TogglePatternId, IID_PPV_ARGS(&toggle));
        const bool toggleKnown = SUCCEEDED(pattern) && toggle && SUCCEEDED(toggle->get_CurrentToggleState(&state))
            && state >= ToggleState_Off && state <= ToggleState_Indeterminate;
        const auto index = visited++;
        std::string row = "{\"index\":" + std::to_string(index) + ",\"parent_index\":" + std::to_string(parent) + ",\"depth\":" + std::to_string(depth)
            + ",\"provider_pid\":" + std::to_string(pid) + ",\"provider_birth\":\"" + std::to_string(held->birth) + "\",\"image_kind\":\"" + held->imageKind
            + "\",\"control_type\":" + std::to_string(type) + ",\"automation_id\":" + automationID + ",\"class_name\":" + className
            + ",\"public_name\":" + publicName + ",\"enabled\":" + (enabled ? "true" : "false") + ",\"offscreen\":" + (offscreen ? "true" : "false")
            + ",\"bounds\":[" + std::to_string(bounds.left) + ',' + std::to_string(bounds.top) + ',' + std::to_string(bounds.right) + ',' + std::to_string(bounds.bottom)
            + "],\"runtime_id\":" + runtimeJSON + ",\"toggle_state\":" + (toggleKnown ? std::to_string(state) : "null") + ",\"toggle_pattern_hresult\":" + std::to_string(pattern) + "}";
        demand(rows.size() + row.size() <= 60000, "inspection output bound");
        if (!rows.empty()) rows += ','; rows += row;
        held->validate(); budget();
        ComPtr<IUIAutomationElement> child; demand(SUCCEEDED(walker->GetFirstChildElement(node.Get(), &child)), "inspection child walk failed");
        while (child) { walk(child, depth + 1, static_cast<int>(index)); ComPtr<IUIAutomationElement> next;
            demand(SUCCEEDED(walker->GetNextSiblingElement(child.Get(), &next)), "inspection sibling walk failed"); child = next; }
    };
    try { walk(element, 0, -1); } catch (...) { ++errors; complete = false; }
    try { explorer.validate(); inspectionDesktop(); for (const auto& held : owners) held->validate(); }
    catch (...) { ++errors; complete = false; }
    if (GetForegroundWindow() != panel || GetTickCount64() >= deadline) complete = false;
    std::cout << prefix << ",\"status\":\"unqualified_selector_inventory_only\",\"walk_completed\":" << (complete ? "true" : "false")
        << ",\"visited\":" << visited << ",\"total_visited\":" << totalVisited << ",\"skipped_providers\":" << skipped << ",\"errors\":" << errors
        << ",\"elapsed_ms\":" << GetTickCount64() - started << ",\"panel_hwnd\":" << reinterpret_cast<uintptr_t>(panel)
        << ",\"panel_provider_pid\":" << panelPID << ",\"rows\":[" << rows << "]}\n";
}

static std::vector<BYTE> user(HANDLE process) {
    HANDLE token{}; DWORD size{};
    if (!OpenProcessToken(process, TOKEN_QUERY, &token)) throw "token unavailable";
    GetTokenInformation(token, TokenUser, nullptr, 0, &size);
    std::vector<BYTE> buffer(size);
    const BOOL read = size && GetTokenInformation(token, TokenUser, buffer.data(), size, &size);
    CloseHandle(token);
    if (!read) throw "token user unavailable";
    return buffer;
}

static void shell() {
    const HWND window = GetForegroundWindow(); DWORD pid{}, ownSession{}, session{};
    GetWindowThreadProcessId(window, &pid);
    HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, pid);
    if (!process) throw "foreground process unavailable";
    wchar_t actual[32768]{}, windows[32768]{}; DWORD count = 32768;
    const BOOL imageRead = QueryFullProcessImageNameW(process, 0, actual, &count);
    const UINT windowsSize = GetWindowsDirectoryW(windows, 32768);
    const std::wstring expected = std::wstring(windows) + L"\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\SearchHost.exe";
    const bool search = imageRead && windowsSize > 0 && windowsSize < 32768 && _wcsicmp(actual, expected.c_str()) == 0;
    if (!search) {
        CloseHandle(process);
        std::cout << "{\"status\":\"not_applicable\",\"input_attempted\":false,\"product_executed\":false}\n";
        return;
    }
    const auto own = user(GetCurrentProcess()), other = user(process);
    const bool bound = ProcessIdToSessionId(GetCurrentProcessId(), &ownSession) && ProcessIdToSessionId(pid, &session)
        && session == ownSession && session != 0
        && EqualSid(reinterpret_cast<const TOKEN_USER*>(own.data())->User.Sid, reinterpret_cast<const TOKEN_USER*>(other.data())->User.Sid)
        && WaitForSingleObject(process, 0) == WAIT_TIMEOUT && GetForegroundWindow() == window;
    if (!bound) { CloseHandle(process); throw "foreground Shell identity changed"; }
    INPUT inputs[2]{};
    for (auto& input : inputs) { input.type = INPUT_KEYBOARD; input.ki.wVk = VK_ESCAPE; }
    inputs[1].ki.dwFlags = KEYEVENTF_KEYUP;
    const UINT sent = SendInput(2, inputs, sizeof(INPUT));
    const auto deadline = GetTickCount64() + 2000;
    while (GetTickCount64() < deadline && GetForegroundWindow() == window && IsWindowVisible(window)) Sleep(20);
    const bool dismissed = sent == 2 && (GetForegroundWindow() != window || !IsWindowVisible(window));
    CloseHandle(process);
    std::cout << "{\"status\":\"" << (dismissed ? "dismissed" : "unqualified")
              << "\",\"verified_search_overlay\":true,\"input_attempted\":true,\"sent\":" << sent
              << ",\"session\":" << session << ",\"pid\":" << pid
              << ",\"hwnd\":" << reinterpret_cast<uintptr_t>(window)
              << ",\"foreground_after\":" << reinterpret_cast<uintptr_t>(GetForegroundWindow())
              << ",\"product_executed\":false}\n";
    if (!dismissed) throw "single bound Search overlay Escape was not observed";
}

int wmain(int argc, wchar_t** argv) {
    if (argc < 2 || FAILED(CoInitializeEx(nullptr, COINIT_MULTITHREADED))) return 2;
    try {
        const std::wstring mode(argv[1]);
        if (mode == L"notifications-inspect" && argc == 3) {
            inspectNotificationCenter(argv[2]);
        } else if (mode == L"shell" && argc == 2) {
            auto env = [](const wchar_t* key) { wchar_t value[256]{}; const DWORD size = GetEnvironmentVariableW(key, value, 256); return size && size < 256 ? std::wstring(value, size) : L""; };
            if (env(L"GITHUB_REPOSITORY") != L"777genius/agent-notifications" || env(L"GITHUB_EVENT_NAME") != L"workflow_dispatch" ||
                env(L"GITHUB_RUN_ATTEMPT") != L"1" || env(L"PI_WINDOWS_HOST_SETUP") != L"1") throw "fresh manual hosted setup required";
            shell();
        } else if (mode == L"discover" && argc == 3) {
            std::cout << PiWindowsAudioFixture::discover(argv[2]) << '\n';
        } else if (mode == L"verify" && argc == 8) {
            ComPtr<IMMDeviceEnumerator> enumerator;
            if (FAILED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator)))) throw "enumerator unavailable";
            std::cout << PiWindowsAudioFixture::select(enumerator.Get(), argc - 2, argv + 2).json << '\n';
        } else return 2;
    } catch (const char* error) { std::cerr << error << '\n'; return 2; }
      catch (const std::exception& error) { std::cerr << error.what() << '\n'; return 2; }
      catch (...) { std::cerr << "native inspection unavailable\n"; return 2; }
    return 0;
}
