// Finite guest preparation and explicit endpoint discovery for CI TEST fixtures.
#define NOMINMAX
#include <windows.h>
#include <mmdeviceapi.h>
#include <endpointvolume.h>
#include <audioclient.h>
#include <UIAutomation.h>
#include <wtsapi32.h>
#include <dwmapi.h>
#include <wintrust.h>
#include <softpub.h>
#include <wincrypt.h>
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
#include <sstream>
#pragma comment(lib, "Uiautomationcore.lib")
#pragma comment(lib, "Bcrypt.lib")
#pragma comment(lib, "Wtsapi32.lib")
#pragma comment(lib, "Gdi32.lib")
#pragma comment(lib, "Dwmapi.lib")
#pragma comment(lib, "Wintrust.lib")
#pragma comment(lib, "Crypt32.lib")

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
static std::wstring inspectionFinalPath(HANDLE file) {
    wchar_t path[32768]{};
    const auto count = GetFinalPathNameByHandleW(file, path, 32768, FILE_NAME_NORMALIZED | VOLUME_NAME_DOS);
    demand(count > 0 && count < 32768, "diagnostic canonical image unavailable");
    std::wstring result(path, count);
    if (result.starts_with(L"\\\\?\\")) result.erase(0, 4);
    demand(result.size() <= 512, "diagnostic canonical image length bound");
    return result;
}
static std::string inspectionUTF8(const std::wstring& value) {
    const int size = WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    demand(size > 0 && size <= 2048, "diagnostic image encoding bound");
    std::string result(size, 0);
    demand(WideCharToMultiByte(CP_UTF8, WC_ERR_INVALID_CHARS, value.data(), static_cast<int>(value.size()), result.data(), size, nullptr, nullptr) == size,
           "diagnostic image encoding unavailable");
    return result;
}
struct InspectionImage {
    HANDLE file = INVALID_HANDLE_VALUE;
    std::wstring path;
    std::string rootKind = "redacted", hash;
    LONG trustStatus = TRUST_E_NOSIGNATURE;
    bool microsoft = false, verifiedStable = false;
    explicit InspectionImage(const std::wstring& input) {
        file = CreateFileW(input.c_str(), GENERIC_READ, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, nullptr, OPEN_EXISTING, FILE_ATTRIBUTE_NORMAL, nullptr);
        demand(file != INVALID_HANDLE_VALUE, "diagnostic image open unavailable");
        try {
            path = inspectionFinalPath(file);
            wchar_t windows[32768]{}; const auto length = GetWindowsDirectoryW(windows, 32768);
            demand(length > 0 && length < 32768, "diagnostic Windows root unavailable");
            HANDLE directory = CreateFileW(windows, FILE_READ_ATTRIBUTES, FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE, nullptr, OPEN_EXISTING,
                                            FILE_FLAG_BACKUP_SEMANTICS, nullptr);
            demand(directory != INVALID_HANDLE_VALUE, "diagnostic Windows root open unavailable");
            std::wstring base;
            try { base = inspectionFinalPath(directory); } catch (...) { CloseHandle(directory); throw; }
            CloseHandle(directory);
            for (const auto& item : std::vector<std::pair<std::wstring, std::string>>{{base + L"\\System32\\", "system32"}, {base + L"\\SystemApps\\", "systemapps"}})
                if (_wcsnicmp(path.c_str(), item.first.c_str(), item.first.size()) == 0) rootKind = item.second;
            if (_wcsicmp(path.c_str(), (base + L"\\explorer.exe").c_str()) == 0) rootKind = "fixed_explorer";
            demand(rootKind != "redacted", "diagnostic image outside protected Windows roots");
            hash = currentHash();
        } catch (...) { CloseHandle(file); file = INVALID_HANDLE_VALUE; throw; }
    }
    ~InspectionImage() { if (file != INVALID_HANDLE_VALUE) CloseHandle(file); }
    std::string currentHash() {
        LARGE_INTEGER size{}, zero{};
        demand(GetFileSizeEx(file, &size) && size.QuadPart > 0 && size.QuadPart <= 64 * 1024 * 1024
               && SetFilePointerEx(file, zero, nullptr, FILE_BEGIN), "diagnostic image size or seek unavailable");
        std::vector<BYTE> bytes(static_cast<size_t>(size.QuadPart)); size_t offset = 0;
        while (offset < bytes.size()) {
            DWORD read = 0; const auto count = static_cast<DWORD>(std::min<size_t>(65536, bytes.size() - offset));
            demand(ReadFile(file, bytes.data() + offset, count, &read, nullptr) && read == count, "diagnostic image read unavailable");
            offset += read;
        }
        demand(inspectionFinalPath(file) == path, "diagnostic image canonical path changed");
        return NavigationTokenTEST::digest(bytes.data(), static_cast<DWORD>(bytes.size()));
    }
    void verifyMicrosoft() {
        WINTRUST_FILE_INFO info{}; info.cbStruct = sizeof info; info.pcwszFilePath = path.c_str(); info.hFile = file;
        WINTRUST_DATA trust{}; trust.cbStruct = sizeof trust; trust.dwUIChoice = WTD_UI_NONE; trust.fdwRevocationChecks = WTD_REVOKE_NONE;
        trust.dwUnionChoice = WTD_CHOICE_FILE; trust.pFile = &info; trust.dwStateAction = WTD_STATEACTION_VERIFY;
        trust.dwProvFlags = WTD_CACHE_ONLY_URL_RETRIEVAL;
        GUID action = WINTRUST_ACTION_GENERIC_VERIFY_V2;
        const auto noInteractiveUser = reinterpret_cast<HWND>(INVALID_HANDLE_VALUE);
        trustStatus = WinVerifyTrust(noInteractiveUser, &action, &trust);
        // Signer O is obtained from the chain of this exact verified file, not
        // from an unrelated certificate. No UI or network retrieval is allowed.
        if (trustStatus == ERROR_SUCCESS && trust.hWVTStateData) {
            const HMODULE module = GetModuleHandleW(L"wintrust.dll");
            const auto provider = reinterpret_cast<decltype(&WTHelperProvDataFromStateData)>(GetProcAddress(module, "WTHelperProvDataFromStateData"));
            const auto signer = reinterpret_cast<decltype(&WTHelperGetProvSignerFromChain)>(GetProcAddress(module, "WTHelperGetProvSignerFromChain"));
            auto* data = provider ? provider(trust.hWVTStateData) : nullptr;
            auto* chain = data && signer ? signer(data, 0, FALSE, 0) : nullptr;
            if (chain && chain->csCertChain && chain->pasCertChain && chain->pasCertChain[0].pCert) {
                wchar_t organization[256]{};
                const auto count = CertGetNameStringW(chain->pasCertChain[0].pCert, CERT_NAME_ATTR_TYPE, 0,
                    const_cast<char*>(szOID_ORGANIZATION_NAME), organization, 256);
                microsoft = count > 1 && count < 256 && wcscmp(organization, L"Microsoft Corporation") == 0;
            }
        }
        trust.dwStateAction = WTD_STATEACTION_CLOSE;
        demand(WinVerifyTrust(noInteractiveUser, &action, &trust) == ERROR_SUCCESS, "diagnostic trust state close failed");
        demand(currentHash() == hash, "diagnostic image changed during signature verification");
        verifiedStable = trustStatus == ERROR_SUCCESS && microsoft;
    }
};
struct InspectionActorFacts {
    DWORD pid = 0, win32Error = 0, session = 0; ULONGLONG birth = 0;
    bool running = false, tokenKnown = false, sameSID = false, sameAuth = false, sameSession = false, fixedName = false;
    std::string refusal = "unknown", imageError = "none", imageRoot = "redacted", imageJSON = "null", basenameJSON = "null", hash;
    LONG trustStatus = TRUST_E_NOSIGNATURE; bool microsoft = false, imageStable = false;
};
struct InspectionOwner {
    HANDLE process = nullptr; DWORD pid = 0; ULONGLONG birth = 0; std::string imageKind;
    std::wstring image;
    std::shared_ptr<InspectionActorFacts> observation;
    std::unique_ptr<InspectionImage> diagnosticImage;
    explicit InspectionOwner(DWORD value, std::shared_ptr<InspectionActorFacts> facts = {}) : pid(value), observation(std::move(facts)) {
        if (observation) observation->pid = pid;
        process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, pid);
        if (!process) { if (observation) { observation->win32Error = GetLastError(); observation->refusal = "process_open_failed"; } throw "Shell provider process unavailable"; }
        try { validate(true); if (observation) observation->refusal = "none"; }
        catch (const char* reason) { if (observation) observation->refusal = reason; CloseHandle(process); process = nullptr; throw; }
        catch (const NavigationTokenTEST::Failure& failure) {
            if (observation) { observation->refusal = "token_query_failed"; observation->win32Error = failure.error; }
            CloseHandle(process); process = nullptr; throw;
        } catch (...) { if (observation) observation->refusal = "typed_query_exception"; CloseHandle(process); process = nullptr; throw; }
    }
    ~InspectionOwner() { if (process) CloseHandle(process); }
    void validate(bool initial = false) {
        demand(WaitForSingleObject(process, 0) == WAIT_TIMEOUT, "Shell provider exited");
        if (observation) observation->running = true;
        FILETIME created{}, exited{}, kernel{}, used{};
        if (!GetProcessTimes(process, &created, &exited, &kernel, &used)) {
            if (observation) observation->win32Error = GetLastError(); throw "Shell birth unavailable";
        }
        const auto actualBirth = (static_cast<ULONGLONG>(created.dwHighDateTime) << 32) | created.dwLowDateTime;
        if (observation) observation->birth = actualBirth;
        demand(actualBirth != 0 && (initial || actualBirth == birth), "Shell birth changed");
        wchar_t path[32768]{}; DWORD count = 32768;
        if (!QueryFullProcessImageNameW(process, 0, path, &count)) {
            if (observation) observation->win32Error = GetLastError(); throw "Shell image unavailable";
        }
        demand(count > 0 && count < 32768, "Shell image length unavailable");
        auto known = shellImages(); const auto match = std::find_if(known.begin(), known.end(), [&](const auto& item) {
            return _wcsicmp(path, item.first.c_str()) == 0;
        });
        if (initial && observation) {
            observation->fixedName = match != known.end();
            try {
                diagnosticImage = std::make_unique<InspectionImage>(path);
                observation->imageRoot = diagnosticImage->rootKind;
                observation->imageJSON = NavigationTokenTEST::quoted(inspectionUTF8(diagnosticImage->path));
                observation->basenameJSON = NavigationTokenTEST::quoted(inspectionUTF8(fs::path(diagnosticImage->path).filename().wstring()));
                observation->hash = diagnosticImage->hash;
                diagnosticImage->verifyMicrosoft();
                observation->trustStatus = diagnosticImage->trustStatus; observation->microsoft = diagnosticImage->microsoft;
            } catch (const char* reason) { observation->imageError = reason; }
            if (diagnosticImage) { observation->trustStatus = diagnosticImage->trustStatus; observation->microsoft = diagnosticImage->microsoft;
                observation->imageStable = diagnosticImage->verifiedStable; }
        }
        NavigationTokenTEST::Token actual, own;
        if (!OpenProcessToken(process, TOKEN_QUERY, &actual.handle) || !OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &own.handle)) {
            if (observation) observation->win32Error = GetLastError(); throw "Shell token unavailable";
        }
        const auto a = NavigationTokenTEST::facts(actual.handle), b = NavigationTokenTEST::facts(own.handle);
        if (observation) { observation->tokenKnown = true; observation->sameSID = a.sid == b.sid;
            observation->sameAuth = a.auth == b.auth; observation->sameSession = a.session == b.session && a.session != 0; observation->session = a.session; }
        demand(a.sid == b.sid && a.auth == b.auth && a.session == b.session && a.session != 0, "Shell user authentication or session changed");
        // This fallback exists only for the explicitly nonqualifying read-only
        // inspection. It grants no Shell mutation or product authorization.
        const bool diagnostic = observation && diagnosticImage && diagnosticImage->verifiedStable
            && (diagnosticImage->rootKind == "system32" || diagnosticImage->rootKind == "systemapps");
        demand((match != known.end() || diagnostic) && (initial || _wcsicmp(path, image.c_str()) == 0), "Shell provider image not admitted");
        if (initial) { birth = actualBirth; image = path; imageKind = match != known.end() ? match->second : "observed_signed_windows_diagnostic"; }
    }
    void verifyImageUnchanged() { if (diagnosticImage) demand(diagnosticImage->currentHash() == diagnosticImage->hash, "diagnostic image bytes changed"); }
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
struct InspectionWindows { std::vector<HWND> windows; unsigned visited = 0, unknown = 0; bool capped = false; };
static InspectionWindows inspectionVisibleWindows() {
    InspectionWindows inventory;
    const BOOL enumerated = EnumWindows([](HWND window, LPARAM value) -> BOOL {
        auto& result = *reinterpret_cast<InspectionWindows*>(value);
        if (++result.visited > 256) { result.capped = true; return FALSE; }
        if (IsWindowVisible(window)) {
            DWORD cloaked = 0;
            if (FAILED(DwmGetWindowAttribute(window, DWMWA_CLOAKED, &cloaked, sizeof cloaked))) ++result.unknown;
            else if (!cloaked) result.windows.push_back(window);
        }
        return TRUE;
    }, reinterpret_cast<LPARAM>(&inventory));
    demand(enumerated && !inventory.capped, "inspection visible window inventory bound or error");
    return inventory;
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
    const auto baseline = inspectionVisibleWindows();
    const HWND foregroundBefore = GetForegroundWindow();
    ComPtr<IUIAutomation> automation; demand(SUCCEEDED(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation))), "inspection UIA unavailable");
    ComPtr<IUIAutomationTreeWalker> walker; demand(SUCCEEDED(automation->get_ControlViewWalker(&walker)), "inspection walker unavailable");
    unsigned uiaDiscoveryVisited = 0, uiaDiscoveryErrors = 0;
    auto visibleUIARoots = [&]() {
        std::vector<ComPtr<IUIAutomationElement>> result;
        ComPtr<IUIAutomationElement> desktop, child;
        demand(SUCCEEDED(automation->GetRootElement(&desktop)) && desktop
               && SUCCEEDED(walker->GetFirstChildElement(desktop.Get(), &child)), "inspection UIA desktop roots unavailable");
        unsigned thisPass = 0;
        while (child) {
            budget();
            if (thisPass == 32 || uiaDiscoveryVisited == 256) { ++uiaDiscoveryErrors; break; }
            ++thisPass; ++uiaDiscoveryVisited;
            BOOL offscreen = TRUE; RECT box{};
            if (FAILED(child->get_CurrentIsOffscreen(&offscreen))) ++uiaDiscoveryErrors;
            else if (!offscreen) {
                if (FAILED(child->get_CurrentBoundingRectangle(&box))) ++uiaDiscoveryErrors;
                else if (box.right > box.left && box.bottom > box.top && box.right > 0 && box.bottom > 0
                    && box.left < GetSystemMetrics(SM_CXSCREEN) && box.top < GetSystemMetrics(SM_CYSCREEN)) result.push_back(child);
            }
            ComPtr<IUIAutomationElement> next;
            if (FAILED(walker->GetNextSiblingElement(child.Get(), &next))) { ++uiaDiscoveryErrors; break; }
            child = next;
        }
        return result;
    };
    const auto baselineUIA = visibleUIARoots();
    auto sameElement = [&](IUIAutomationElement* a, IUIAutomationElement* b) {
        BOOL same = FALSE; demand(SUCCEEDED(automation->CompareElements(a, b, &same)), "inspection UIA identity unavailable"); return same != FALSE;
    };
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
    std::vector<std::shared_ptr<InspectionActorFacts>> actors;
    std::vector<std::unique_ptr<InspectionOwner>> owners;
    auto owner = [&](DWORD pid) -> InspectionOwner* {
        for (const auto& held : owners) if (held->pid == pid) { held->validate(); return held.get(); }
        demand(owners.size() < 8, "inspection provider bound");
        demand(actors.size() < 16, "inspection actor facts bound");
        auto facts = std::make_shared<InspectionActorFacts>(); actors.push_back(facts);
        owners.push_back(std::make_unique<InspectionOwner>(pid, facts)); return owners.back().get();
    };
    struct Surface { HWND window; DWORD pid; ULONGLONG birth, firstSeen, lastSeen; unsigned samples; ComPtr<IUIAutomationElement> element; bool uia; };
    std::vector<Surface> surfaces;
    std::vector<DWORD> foreignPIDs;
    unsigned discoveryScans = 0, discoveryErrors = 0, visibilityUnknown = baseline.unknown;
    bool discoveryComplete = baseline.unknown == 0;
    const auto discoveryDeadline = std::min(deadline, started + 12000);
    ULONGLONG nextUIAPass = GetTickCount64(); unsigned uiaPasses = 0;
    std::string discoveryFailure = "none";
    auto observe = [&](ComPtr<IUIAutomationElement> element, HWND window, DWORD pid, bool uia) {
        if (!pid || std::find(foreignPIDs.begin(), foreignPIDs.end(), pid) != foreignPIDs.end()) return;
        InspectionOwner* held = nullptr;
        try { held = owner(pid); } catch (...) {
            demand(foreignPIDs.size() < 256, "inspection rejected provider bound"); foreignPIDs.push_back(pid); return;
        }
        const auto now = GetTickCount64(); budget();
        const auto found = std::find_if(surfaces.begin(), surfaces.end(), [&](const auto& value) { return sameElement(value.element.Get(), element.Get()); });
        if (found == surfaces.end()) {
            if (surfaces.size() == 4) { discoveryComplete = false; ++discoveryErrors; return; }
            surfaces.push_back({window, pid, held->birth, now - started, now - started, 1, element, uia});
        } else {
            demand(found->pid == pid && found->birth == held->birth, "inspection surface identity changed");
            found->lastSeen = now - started; ++found->samples; found->uia = found->uia || uia;
        }
    };
    // SendInput acceptance is not a Shell handoff. Observe newly visible real
    // Shell surfaces instead of treating a transient Progman foreground as the
    // panel. All roots share the original deadline and census/output caps.
    try { while (GetTickCount64() < discoveryDeadline && discoveryScans < 120) {
        ++discoveryScans; budget(); explorer.validate(); inspectionDesktop();
        demand(GetShellWindow() == shell, "Explorer window changed during arrival");
        const auto visible = inspectionVisibleWindows();
        visibilityUnknown += visible.unknown;
        if (visible.unknown) discoveryComplete = false;
        for (const auto window : visible.windows) {
            if (window == shell || std::find(baseline.windows.begin(), baseline.windows.end(), window) != baseline.windows.end()) continue;
            DWORD pid = 0; GetWindowThreadProcessId(window, &pid);
            ComPtr<IUIAutomationElement> element;
            if (SUCCEEDED(automation->ElementFromHandle(window, &element)) && element) observe(element, window, pid, false);
            else { ++discoveryErrors; discoveryComplete = false; }
        }
        if (GetTickCount64() >= nextUIAPass && uiaPasses < 24 && uiaDiscoveryVisited < 256) {
            ++uiaPasses;
            // EnumWindows excludes immersive application roots on Windows 8+.
            // A bounded UIA desktop-child inventory observes those actual roots.
            for (const auto& element : visibleUIARoots()) {
                if (std::any_of(baselineUIA.begin(), baselineUIA.end(), [&](const auto& before) { return sameElement(before.Get(), element.Get()); })) continue;
                UIA_HWND handle{}; int pid = 0;
                if (SUCCEEDED(element->get_CurrentNativeWindowHandle(&handle)) && SUCCEEDED(element->get_CurrentProcessId(&pid)) && pid > 0
                    && reinterpret_cast<HWND>(handle) != shell) observe(element, reinterpret_cast<HWND>(handle), static_cast<DWORD>(pid), true);
                else { ++discoveryErrors; discoveryComplete = false; }
            }
            nextUIAPass = GetTickCount64() + 500;
        }
        Sleep(100);
    } } catch (const char* reason) { discoveryFailure = reason; ++discoveryErrors; discoveryComplete = false; }
    catch (...) { discoveryFailure = "typed_discovery_exception"; ++discoveryErrors; discoveryComplete = false; }
    bool afterScreenshot = false;
    if (GetTickCount64() < deadline) {
        try { inspectionScreenshot(root / L"notification-ui-after.bmp"); afterScreenshot = true; }
        catch (...) { ++discoveryErrors; discoveryComplete = false; }
    }
    const HWND foregroundAfterArrival = GetForegroundWindow();
    DWORD foregroundPID = 0; GetWindowThreadProcessId(foregroundAfterArrival, &foregroundPID);
    if (GetTickCount64() < deadline && foregroundPID && std::find(foreignPIDs.begin(), foreignPIDs.end(), foregroundPID) == foreignPIDs.end()) {
        try { owner(foregroundPID); } catch (...) { discoveryComplete = false; }
    }
    unsigned visited = 0, totalVisited = uiaDiscoveryVisited, skipped = 0, errors = discoveryErrors + uiaDiscoveryErrors;
    bool complete = discoveryComplete && !uiaDiscoveryErrors && surfaces.size() == 1; std::string rows, roots;
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
        auto automationID = inspectionText(id);
        demand(SUCCEEDED(node->get_CurrentClassName(&klass)), "inspection class unavailable");
        auto className = inspectionText(klass);
        demand(SUCCEEDED(node->get_CurrentName(&name)), "inspection name query unavailable");
        const std::wstring nameValue = name && SysStringLen(name) <= 128 ? name : L""; if (name) SysFreeString(name);
        std::string publicName = "null";
        for (const auto label : {L"Notifications", L"Notification Center", L"Do not disturb", L"Do not disturb on", L"Do not disturb off",
                 L"Do not disturb is on", L"Do not disturb is off", L"Turn on do not disturb", L"Turn off do not disturb"})
            if (nameValue == label) { BSTR copy = SysAllocString(label); publicName = inspectionText(copy); break; }
        if (held->imageKind == "observed_signed_windows_diagnostic" && publicName == "null") { automationID = "null"; className = "null"; }
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
        demand(rows.size() + row.size() <= 45000, "inspection output bound");
        if (!rows.empty()) rows += ','; rows += row;
        held->validate(); budget();
        ComPtr<IUIAutomationElement> child; demand(SUCCEEDED(walker->GetFirstChildElement(node.Get(), &child)), "inspection child walk failed");
        while (child) { walk(child, depth + 1, static_cast<int>(index)); ComPtr<IUIAutomationElement> next;
            demand(SUCCEEDED(walker->GetNextSiblingElement(child.Get(), &next)), "inspection sibling walk failed"); child = next; }
    };
    for (const auto& surface : surfaces) {
        bool current = false;
        try {
            budget(); DWORD actualPID = 0; if (surface.window) GetWindowThreadProcessId(surface.window, &actualPID);
            auto* held = owner(surface.pid);
            DWORD cloaked = 0;
            BOOL offscreen = TRUE; int rootPID = 0; UIA_HWND actualHandle{};
            current = SUCCEEDED(surface.element->get_CurrentProcessId(&rootPID)) && rootPID == static_cast<int>(surface.pid)
                && SUCCEEDED(surface.element->get_CurrentIsOffscreen(&offscreen)) && !offscreen
                && SUCCEEDED(surface.element->get_CurrentNativeWindowHandle(&actualHandle)) && reinterpret_cast<HWND>(actualHandle) == surface.window
                && (!surface.window || (IsWindow(surface.window) && IsWindowVisible(surface.window) && actualPID == surface.pid
                    && SUCCEEDED(DwmGetWindowAttribute(surface.window, DWMWA_CLOAKED, &cloaked, sizeof cloaked)) && !cloaked))
                && held->birth == surface.birth && surface.samples >= 2;
            demand(current, "inspection surface no longer stable");
            held->verifyImageUnchanged(); budget();
            walk(surface.element, 0, -1);
            DWORD finalPID = 0; if (surface.window) GetWindowThreadProcessId(surface.window, &finalPID);
            held->validate(); held->verifyImageUnchanged(); budget();
            current = SUCCEEDED(surface.element->get_CurrentProcessId(&rootPID)) && rootPID == static_cast<int>(surface.pid)
                && SUCCEEDED(surface.element->get_CurrentIsOffscreen(&offscreen)) && !offscreen
                && SUCCEEDED(surface.element->get_CurrentNativeWindowHandle(&actualHandle)) && reinterpret_cast<HWND>(actualHandle) == surface.window
                && (!surface.window || (IsWindowVisible(surface.window) && finalPID == surface.pid
                    && SUCCEEDED(DwmGetWindowAttribute(surface.window, DWMWA_CLOAKED, &cloaked, sizeof cloaked)) && !cloaked));
            demand(current, "inspection surface disappeared during census");
        } catch (...) { ++errors; complete = false; current = false; }
        if (!roots.empty()) roots += ',';
        roots += "{\"hwnd\":" + std::to_string(reinterpret_cast<uintptr_t>(surface.window)) + ",\"provider_pid\":" + std::to_string(surface.pid)
            + ",\"provider_birth\":\"" + std::to_string(surface.birth) + "\",\"visible_before_chord\":false,\"first_seen_ms\":" + std::to_string(surface.firstSeen)
            + ",\"last_seen_ms\":" + std::to_string(surface.lastSeen) + ",\"observed_samples\":" + std::to_string(surface.samples)
            + ",\"discovered_via_uia\":" + (surface.uia ? "true" : "false")
            + ",\"foreground_at_arrival_end\":" + (foregroundAfterArrival == surface.window ? "true" : "false")
            + ",\"stable_through_census\":" + (current ? "true" : "false") + "}";
    }
    bool finalActorsAndDesktopValidated = false;
    try { explorer.validate(); inspectionDesktop(); for (const auto& held : owners) held->validate(); finalActorsAndDesktopValidated = true; }
    catch (...) { ++errors; complete = false; }
    if (GetTickCount64() >= deadline) complete = false;
    std::string actorJSON; bool actorFactsCapped = false;
    for (const auto& actor : actors) {
        const std::string row = "{\"provider_pid\":" + std::to_string(actor->pid) + ",\"provider_birth\":\"" + std::to_string(actor->birth)
            + "\",\"running_observed\":" + (actor->running ? "true" : "false") + ",\"fixed_name_admitted\":" + (actor->fixedName ? "true" : "false")
            + ",\"refusal\":" + NavigationTokenTEST::quoted(actor->refusal) + ",\"query_win32_error\":" + std::to_string(actor->win32Error)
            + ",\"token_facts_known\":" + (actor->tokenKnown ? "true" : "false") + ",\"same_user_sid\":" + (actor->sameSID ? "true" : "false")
            + ",\"same_authentication\":" + (actor->sameAuth ? "true" : "false") + ",\"same_session\":" + (actor->sameSession ? "true" : "false")
            + ",\"token_session\":" + std::to_string(actor->session) + ",\"canonical_image_root\":" + NavigationTokenTEST::quoted(actor->imageRoot)
            + ",\"canonical_windows_image\":" + actor->imageJSON + ",\"image_basename\":" + actor->basenameJSON + ",\"image_sha256\":" + NavigationTokenTEST::quoted(actor->hash)
            + ",\"image_query_error\":" + NavigationTokenTEST::quoted(actor->imageError) + ",\"winverifytrust_status\":" + std::to_string(actor->trustStatus)
            + ",\"verified_signer_microsoft_organization\":" + (actor->microsoft ? "true" : "false")
            + ",\"signature_image_stable\":" + (actor->imageStable ? "true" : "false") + ",\"diagnostic_only\":true,\"mutation_authorized\":false}";
        if (actorJSON.size() + row.size() > 12000) { actorFactsCapped = true; complete = false; break; }
        if (!actorJSON.empty()) actorJSON += ','; actorJSON += row;
    }
    std::ostringstream receipt;
    receipt << prefix << ",\"status\":\"unqualified_selector_inventory_only\",\"walk_completed\":" << (complete ? "true" : "false")
        << ",\"visited\":" << visited << ",\"total_visited\":" << totalVisited << ",\"skipped_providers\":" << skipped << ",\"errors\":" << errors
        << ",\"elapsed_ms\":" << GetTickCount64() - started << ",\"baseline_visible_windows\":" << baseline.windows.size()
        << ",\"visibility_unknown_samples\":" << visibilityUnknown << ",\"rejected_provider_count\":" << foreignPIDs.size()
        << ",\"uia_discovery_visited\":" << uiaDiscoveryVisited << ",\"uia_discovery_passes\":" << uiaPasses
        << ",\"discovery_failure\":" << NavigationTokenTEST::quoted(discoveryFailure) << ",\"after_screenshot_captured\":" << (afterScreenshot ? "true" : "false")
        << ",\"initially_validated_input_desktop\":\"Default\",\"initially_validated_station\":\"WinSta0\""
        << ",\"final_actors_and_desktop_validated\":" << (finalActorsAndDesktopValidated ? "true" : "false") << ",\"actor_facts_capped\":" << (actorFactsCapped ? "true" : "false")
        << ",\"discovery_scans\":" << discoveryScans << ",\"surface_identity_is_not_control_identity\":true,\"surfaces_ambiguous\":" << (surfaces.size() > 1 ? "true" : "false")
        << ",\"foreground_before_hwnd\":" << reinterpret_cast<uintptr_t>(foregroundBefore)
        << ",\"foreground_after_arrival_hwnd\":" << reinterpret_cast<uintptr_t>(foregroundAfterArrival)
        << ",\"foreground_final_hwnd\":" << reinterpret_cast<uintptr_t>(GetForegroundWindow())
        << ",\"baseline_hwnds\":[";
    for (size_t index = 0; index < baseline.windows.size(); ++index) {
        if (index) receipt << ','; receipt << reinterpret_cast<uintptr_t>(baseline.windows[index]);
    }
    receipt << "],\"surfaces\":[" << roots << "],\"observed_providers\":[" << actorJSON << "],\"rows\":[" << rows << "]}";
    demand(receipt.str().size() <= 65536, "inspection total receipt bound");
    std::cout << receipt.str() << '\n';
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
