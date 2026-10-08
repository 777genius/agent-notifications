// Disposable CI-only Windows client toast lifecycle probe. No production adapter.
#define NOMINMAX
#include <windows.h>
#include <wtsapi32.h>
#pragma comment(lib, "Wtsapi32.lib")
#include <shlobj.h>
#include <sddl.h>
#include <propkey.h>
#include <propvarutil.h>
#include <notificationactivationcallback.h>
#include <UIAutomation.h>
#include <wrl/client.h>
#include <winrt/Windows.Data.Xml.Dom.h>
#include <winrt/Windows.Data.Json.h>
#include <winrt/Windows.UI.Notifications.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Management.Deployment.h>
#include <winrt/Windows.ApplicationModel.h>
#include <winrt/Windows.System.h>
#include "navigation_windows_vendor_sdk_test.h"
#include "navigation_windows_token_queries.h"
using namespace NavigationTokenTEST;
#include <bcrypt.h>
#pragma comment(lib, "Bcrypt.lib")
#include <wincodec.h>
#pragma comment(lib, "Windowscodecs.lib")
#pragma comment(lib, "Gdi32.lib")
#include <filesystem>
#include <fstream>
#include <iostream>
#include <string>
#include <atomic>
#include <cwchar>
#include <algorithm>
#include <cwctype>
#include <vector>
#include <utility>
#include <optional>
#include <exception>
#include <memory>
#include <cstring>
#include <cstdint>
#include <map>
#include <appxpackaging.h>
#include <shlwapi.h>
#pragma comment(lib, "Shlwapi.lib")
using Microsoft::WRL::ComPtr;
namespace fs = std::filesystem;
using namespace winrt::Windows::UI::Notifications;
static fs::path root;
static std::wstring uuid, aumid, action;
static GUID clsid;
static std::atomic<bool> activated{false};
static std::wstring activeMode;
static bool ownedRootValidated = false, showCallEntered = false, showCallReturned = false;
static std::string currentSendStage = "not_started";
static unsigned sendStageIndex = 0;
static unsigned long long processStartedAt() {
    FILETIME created{}, exited{}, kernel{}, user{};
    if (!GetProcessTimes(GetCurrentProcess(), &created, &exited, &kernel, &user))
        throw std::runtime_error("process creation time unavailable");
    ULARGE_INTEGER time{}; time.LowPart = created.dwLowDateTime; time.HighPart = created.dwHighDateTime;
    return time.QuadPart / 10000ULL - 11644473600000ULL;
}
static void check(HRESULT hr) { winrt::check_hresult(hr); }
static std::string narrow(const std::wstring& s) { return winrt::to_string(s); }
static std::string jsonQuote(const std::wstring& s) {
    std::string out = "\"";
    for (char c : narrow(s)) {
        if (c == '\\' || c == '"') out += '\\';
        if (static_cast<unsigned char>(c) < 32) throw std::runtime_error("control character");
        out += c;
    }
    return out + "\"";
}
static void report(const char* name, const std::string& content) {
    fs::path path = root / name;
    fs::path temporary = path; temporary += L".tmp";
    HANDLE h = CreateFileW(temporary.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_NEW,
                          FILE_ATTRIBUTE_NORMAL, nullptr);
    if (h == INVALID_HANDLE_VALUE) throw std::runtime_error("exclusive report creation failed");
    DWORD written = 0;
    bool ok = WriteFile(h, content.data(), static_cast<DWORD>(content.size()), &written, nullptr)
              && written == content.size();
    FlushFileBuffers(h); CloseHandle(h);
    if (!ok || !MoveFileExW(temporary.c_str(), path.c_str(), MOVEFILE_WRITE_THROUGH)) {
        DeleteFileW(temporary.c_str()); throw std::runtime_error("atomic exclusive report write failed");
    }
}
static void sendStage(const char* phase) {
    currentSendStage = phase;
    std::string filename = "sender-stage-" + std::to_string(++sendStageIndex) + ".json";
    report(filename.c_str(), "{\"phase\":" + jsonQuote(winrt::to_hstring(phase).c_str())
        + ",\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid) + "}\n");
}
static void sendFailure(HRESULT hr) noexcept {
    if (!ownedRootValidated || activeMode != L"send") return;
    try {
        report("sender-failure.json", "{\"phase\":" + jsonQuote(winrt::to_hstring(currentSendStage).c_str())
            + ",\"hresult\":" + std::to_string(hr) + ",\"pid\":" + std::to_string(GetCurrentProcessId())
            + ",\"nonce\":" + jsonQuote(uuid) + ",\"showCallEntered\":" + (showCallEntered ? "true" : "false")
            + ",\"showCallReturned\":" + (showCallReturned ? "true" : "false") + "}\n");
    } catch (...) { /* Controller retains partial stages and treats missing final evidence as uncertain. */ }
}
static std::wstring objectName(HANDLE h) {
    wchar_t name[256]{}; DWORD needed = 0;
    if (!GetUserObjectInformationW(h, UOI_NAME, name, sizeof(name), &needed)) return L"";
    return name;
}
struct CenterPolicyObservation { std::string json; bool inspected; bool enabled; };
static CenterPolicyObservation centerPolicyValue(HKEY hive) {
    constexpr auto keyPath = L"Software\\Policies\\Microsoft\\Windows\\Explorer";
    HKEY key{};
    LSTATUS status = RegOpenKeyExW(hive, keyPath, 0, KEY_QUERY_VALUE | KEY_WOW64_64KEY, &key);
    if (status == ERROR_FILE_NOT_FOUND) return {"{\"state\":\"not_present\"}", true, false};
    if (status != ERROR_SUCCESS) return {"{\"state\":\"error\",\"status\":" + std::to_string(status) + "}", false, false};
    DWORD type = 0, value = 0, bytes = sizeof(value);
    status = RegQueryValueExW(key, L"DisableNotificationCenter", nullptr, &type,
        reinterpret_cast<BYTE*>(&value), &bytes);
    RegCloseKey(key);
    if (status == ERROR_FILE_NOT_FOUND) return {"{\"state\":\"not_present\"}", true, false};
    const auto metadata = ",\"type\":" + std::to_string(type) + ",\"bytes\":" + std::to_string(bytes);
    if (status != ERROR_SUCCESS || type != REG_DWORD || bytes != sizeof(value) || value > 1) {
        if (status == ERROR_SUCCESS) status = ERROR_INVALID_DATA;
        return {"{\"state\":\"error\",\"status\":" + std::to_string(status) + metadata + "}", false, false};
    }
    return {"{\"state\":\"present\",\"enabled\":" + std::string(value == 1 ? "true" : "false")
        + metadata + "}", true, value == 1};
}
static bool centerPolicy() {
    const auto user = centerPolicyValue(HKEY_CURRENT_USER), machine = centerPolicyValue(HKEY_LOCAL_MACHINE);
    const bool complete = user.inspected && machine.inspected;
    report("center-policy.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"diagnosticOnly\":true,\"registryView\":\"native64\",\"effectiveShellPolicyProved\":false"
        + ",\"hkcu\":" + user.json + ",\"hklm\":" + machine.json
        + ",\"lookupComplete\":" + (complete ? "true" : "false")
        + ",\"configuredDisabled\":" + (user.enabled || machine.enabled ? "true" : "false") + "}\n");
    return complete;
}
static bool preflight(const char* reportName = "preflight.json") {
    OSVERSIONINFOEXW v{}; v.dwOSVersionInfoSize = sizeof(v);
    auto rtl = reinterpret_cast<LONG(WINAPI*)(OSVERSIONINFOEXW*)>(
        GetProcAddress(GetModuleHandleW(L"ntdll.dll"), "RtlGetVersion"));
    if (!rtl || rtl(&v) != 0) throw std::runtime_error("OS version unavailable");
    DWORD session = 0;
    bool sessionKnown = ProcessIdToSessionId(GetCurrentProcessId(), &session) != FALSE;
    LPWSTR stateBuffer = nullptr; DWORD stateBytes = 0;
    BOOL stateQuery = sessionKnown && WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, session,
        WTSConnectState, &stateBuffer, &stateBytes);
    DWORD stateError = stateQuery ? ERROR_SUCCESS : sessionKnown ? GetLastError() : ERROR_INVALID_PARAMETER;
    int state = -1;
    static_assert(sizeof(state) == sizeof(WTS_CONNECTSTATE_CLASS));
    bool stateShape = stateQuery && stateBuffer && stateBytes == sizeof(state);
    if (stateShape) std::memcpy(&state, stateBuffer, sizeof(state));
    if (stateBuffer) WTSFreeMemory(stateBuffer);
    bool stateKnown = stateShape && state >= WTSActive && state <= WTSInit;
    if (stateQuery && !stateKnown) stateError = ERROR_INVALID_DATA;
    HWINSTA station = GetProcessWindowStation(); USEROBJECTFLAGS flags{}; DWORD needed = 0;
    bool flagsKnown = GetUserObjectInformationW(station, UOI_FLAGS, &flags, sizeof(flags), &needed) != FALSE;
    HDESK input = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS);
    std::wstring stationName = objectName(station), inputName = input ? objectName(input) : L"";
    std::wstring threadName = objectName(GetThreadDesktop(GetCurrentThreadId()));
    if (input) CloseDesktop(input);
    DWORD shellPid = 0; HWND shell = GetShellWindow();
    if (shell) GetWindowThreadProcessId(shell, &shellPid);
    bool client = v.wProductType == VER_NT_WORKSTATION && v.dwMajorVersion == 10 && v.dwBuildNumber >= 22000;
    bool ready = client && sessionKnown && session != 0 && flagsKnown && (flags.dwFlags & WSF_VISIBLE)
        && stationName == L"WinSta0" && !inputName.empty() && inputName == threadName && shellPid != 0
        && stateKnown && state == WTSActive;
    report(reportName, "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"client\":" + std::string(client ? "true" : "false")
        + ",\"build\":" + std::to_string(v.dwBuildNumber) + ",\"productType\":" + std::to_string(v.wProductType)
        + ",\"sessionKnown\":" + (sessionKnown ? "true" : "false") + ",\"session\":" + std::to_string(session)
        + ",\"connectionStateQuerySucceeded\":" + (stateQuery ? "true" : "false")
        + ",\"connectionStateKnown\":" + (stateKnown ? "true" : "false")
        + ",\"connectionStateBytes\":" + std::to_string(stateBytes)
        + ",\"connectionState\":" + (stateShape ? std::to_string(static_cast<int>(state)) : "null")
        + ",\"connectionStateError\":" + std::to_string(stateError)
        + ",\"remoteSession\":" + (GetSystemMetrics(SM_REMOTESESSION) ? "true" : "false")
        + ",\"screenWidth\":" + std::to_string(GetSystemMetrics(SM_CXSCREEN))
        + ",\"screenHeight\":" + std::to_string(GetSystemMetrics(SM_CYSCREEN))
        + ",\"foregroundPresent\":" + (GetForegroundWindow() ? "true" : "false")
        + ",\"station\":" + jsonQuote(stationName) + ",\"stationVisible\":" + ((flags.dwFlags & WSF_VISIBLE) ? "true" : "false")
        + ",\"inputDesktop\":" + jsonQuote(inputName) + ",\"threadDesktop\":" + jsonQuote(threadName)
        + ",\"shellPID\":" + std::to_string(shellPid) + ",\"ready\":" + (ready ? "true" : "false") + "}\n");
    return ready;
}
static fs::path shortcut() {
    PWSTR programs = nullptr; check(SHGetKnownFolderPath(FOLDERID_Programs, 0, nullptr, &programs));
    fs::path result = fs::path(programs) / (L"Navigation TEST " + uuid + L".lnk");
    CoTaskMemFree(programs); return result;
}
static std::wstring registryKey() { return L"Software\\Classes\\CLSID\\{" + uuid + L"}"; }
static std::wstring appIdentityKey() { return L"Software\\Classes\\AppUserModelId\\" + aumid; }
static std::wstring appIdentityDisplayName() { return L"Navigation TEST " + uuid; }
static bool ownAppIdentityProof() {
    std::ifstream proof(root / ".aumid-owned"); std::string text; std::getline(proof, text);
    return text == narrow(appIdentityKey());
}
static std::optional<std::wstring> appIdentityValue(const wchar_t* name) {
    HKEY key{};
    LSTATUS status = RegOpenKeyExW(HKEY_CURRENT_USER, appIdentityKey().c_str(), 0, KEY_QUERY_VALUE, &key);
    if (status == ERROR_FILE_NOT_FOUND) return std::nullopt;
    check(HRESULT_FROM_WIN32(status));
    wchar_t value[512]{}; DWORD size = sizeof(value), type{};
    status = RegQueryValueExW(key, name, nullptr, &type, reinterpret_cast<BYTE*>(value), &size);
    RegCloseKey(key);
    if (status == ERROR_FILE_NOT_FOUND) return std::nullopt;
    check(HRESULT_FROM_WIN32(status));
    if (type != REG_SZ || size < sizeof(wchar_t) || size > sizeof(value) || size % sizeof(wchar_t) != 0)
        throw std::runtime_error("TEST AUMID value type/size changed; preserved");
    size_t characters = size / sizeof(wchar_t);
    if (value[characters - 1] != L'\0') throw std::runtime_error("unterminated TEST AUMID value; preserved");
    std::wstring content(value, characters - 1);
    if (content.find(L'\0') != std::wstring::npos) throw std::runtime_error("embedded NUL in TEST AUMID value; preserved");
    return content;
}
static bool appIdentityMatches(bool partial = false) {
    auto display = appIdentityValue(L"DisplayName"), activator = appIdentityValue(L"CustomActivator");
    return ((display && *display == appIdentityDisplayName()) || (partial && !display))
           && ((activator && *activator == L"{" + uuid + L"}") || (partial && !activator));
}
static bool appIdentityAbsent() {
    HKEY key{};
    LSTATUS status = RegOpenKeyExW(HKEY_CURRENT_USER, appIdentityKey().c_str(), 0, KEY_READ, &key);
    if (status == ERROR_FILE_NOT_FOUND) return true;
    check(HRESULT_FROM_WIN32(status)); RegCloseKey(key); return false;
}
static bool appIdentityContainsOnlyOwnValues() {
    HKEY key{};
    LSTATUS status = RegOpenKeyExW(HKEY_CURRENT_USER, appIdentityKey().c_str(), 0, KEY_READ, &key);
    if (status == ERROR_FILE_NOT_FOUND) return true;
    check(HRESULT_FROM_WIN32(status));
    try {
        DWORD subkeys{}, values{};
        check(HRESULT_FROM_WIN32(RegQueryInfoKeyW(key, nullptr, nullptr, nullptr, &subkeys, nullptr, nullptr,
                                                &values, nullptr, nullptr, nullptr, nullptr)));
        bool own = subkeys == 0 && values <= 2;
        for (DWORD i = 0; own && i < values; ++i) {
            wchar_t name[128]{}; DWORD size = 128, type{};
            check(HRESULT_FROM_WIN32(RegEnumValueW(key, i, name, &size, nullptr, &type, nullptr, nullptr)));
            own = type == REG_SZ && (std::wstring(name, size) == L"DisplayName" || std::wstring(name, size) == L"CustomActivator");
        }
        RegCloseKey(key); return own;
    } catch (...) { RegCloseKey(key); throw; }
}
static void installAppIdentity() {
    // Current C++/WinRT compatibility registration uses this per-user identity,
    // separately from the shortcut and the COM LocalServer32 registration.
    sendStage("aumid_identity_create");
    HKEY key{}; DWORD disposition{};
    check(HRESULT_FROM_WIN32(RegCreateKeyExW(HKEY_CURRENT_USER, appIdentityKey().c_str(), 0, nullptr, 0,
                                            KEY_SET_VALUE | KEY_QUERY_VALUE | KEY_ENUMERATE_SUB_KEYS, nullptr, &key, &disposition)));
    if (disposition != REG_CREATED_NEW_KEY) { RegCloseKey(key); throw std::runtime_error("unique TEST AUMID already exists"); }
    bool markerPublished = false;
    try {
        report(".aumid-owned", narrow(appIdentityKey()) + "\n");
        markerPublished = true;
        for (const auto& item : {std::pair<std::wstring, std::wstring>{L"DisplayName", appIdentityDisplayName()},
                                {L"CustomActivator", L"{" + uuid + L"}"}}) {
            check(HRESULT_FROM_WIN32(RegSetValueExW(key, item.first.c_str(), 0, REG_SZ,
                reinterpret_cast<const BYTE*>(item.second.c_str()), static_cast<DWORD>((item.second.size() + 1) * sizeof(wchar_t)))));
        }
        RegCloseKey(key); key = nullptr;
        if (!appIdentityMatches()) throw std::runtime_error("TEST AUMID registry readback mismatch");
        report("aumid-identity.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
            + ",\"aumid\":" + jsonQuote(aumid) + ",\"displayNameMatches\":true,\"customActivatorMatches\":true,\"newKey\":true}\n");
    } catch (...) {
        if (key && !markerPublished) {
            // Retained NEW handle, before any value write. Preserve anything unexpected.
            DWORD subkeys{}, values{};
            LSTATUS inspected = RegQueryInfoKeyW(key, nullptr, nullptr, nullptr, &subkeys, nullptr, nullptr,
                                                 &values, nullptr, nullptr, nullptr, nullptr);
            LSTATUS removed = ERROR_ACCESS_DENIED;
            if (inspected == ERROR_SUCCESS && subkeys == 0 && values == 0)
                removed = RegDeleteKeyW(HKEY_CURRENT_USER, appIdentityKey().c_str());
            RegCloseKey(key); key = nullptr;
            std::cerr << "TEST new-empty AUMID rollback query=" << inspected << " delete=" << removed << '\n';
            try {
                report("aumid-empty-rollback.json", "{\"newHandle\":true,\"queryStatus\":" + std::to_string(inspected)
                    + ",\"deleteStatus\":" + std::to_string(removed)
                    + ",\"absenceVerified\":" + (removed == ERROR_SUCCESS && appIdentityAbsent() ? "true" : "false") + "}\n");
            } catch (...) { /* Original publication failure stays failed; never assert missing evidence. */ }
        }
        if (key) RegCloseKey(key);
        throw;
    }
}
static std::wstring serverCommand() {
    return L"\"" + (root / L"navigation-native-probe.exe").wstring() + L"\" callback \"" + root.wstring() + L"\" " + uuid;
}
static bool ownRegistryProof() {
    std::ifstream proof(root / ".registry-owned"); std::string text; std::getline(proof, text);
    return text == narrow(serverCommand());
}
static std::wstring registeredCommand() {
    wchar_t value[32768]{}; DWORD size = sizeof(value);
    LSTATUS status = RegGetValueW(HKEY_CURRENT_USER, (registryKey() + L"\\LocalServer32").c_str(), nullptr,
                                 RRF_RT_REG_SZ, nullptr, value, &size);
    if (status == ERROR_FILE_NOT_FOUND) return L"";
    check(HRESULT_FROM_WIN32(status)); return value;
}
static bool ownShortcutProof() {
    std::ifstream proof(root / ".shortcut-owned"); std::string text; std::getline(proof, text);
    return text == narrow(shortcut().wstring());
}
static bool shortcutMatches(bool emitReadback = false) {
    ComPtr<IShellLinkW> link; check(CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&link)));
    ComPtr<IPersistFile> file; check(link.As(&file)); check(file->Load(shortcut().c_str(), STGM_READ));
    wchar_t target[32768]{}, args[256]{};
    check(link->GetPath(target, 32768, nullptr, SLGP_RAWPATH)); check(link->GetArguments(args, 256));
    bool targetMatches = fs::path(target) == root / L"navigation-native-probe.exe" && std::wstring(args) == L"inert";
    ComPtr<IPropertyStore> props; check(link.As(&props)); PROPVARIANT id{}, activator{};
    check(props->GetValue(PKEY_AppUserModel_ID, &id));
    check(props->GetValue(PKEY_AppUserModel_ToastActivatorCLSID, &activator));
    bool matches = targetMatches && id.vt == VT_LPWSTR && id.pwszVal && id.pwszVal == aumid
        && activator.vt == VT_CLSID && activator.puuid && *activator.puuid == clsid;
    if (emitReadback) {
        wchar_t actualCLSID[40]{};
        if (activator.vt == VT_CLSID && activator.puuid) StringFromGUID2(*activator.puuid, actualCLSID, 40);
        report("shortcut-readback.json", "{\"target\":" + jsonQuote(target) + ",\"arguments\":" + jsonQuote(args)
            + ",\"aumid\":" + jsonQuote(id.vt == VT_LPWSTR && id.pwszVal ? id.pwszVal : L"")
            + ",\"aumidVariantType\":" + std::to_string(id.vt) + ",\"activator\":" + jsonQuote(actualCLSID)
            + ",\"activatorVariantType\":" + std::to_string(activator.vt)
            + ",\"matches\":" + (matches ? "true" : "false") + "}\n");
    }
    PropVariantClear(&id); PropVariantClear(&activator); return matches;
}
static std::vector<BYTE> tokenUser(HANDLE process) {
    HANDLE token = nullptr;
    if (!OpenProcessToken(process, TOKEN_QUERY, &token)) check(HRESULT_FROM_WIN32(GetLastError()));
    DWORD size = 0; GetTokenInformation(token, TokenUser, nullptr, 0, &size);
    if (!size || size > 16384) { CloseHandle(token); throw std::runtime_error("token user size unavailable"); }
    std::vector<BYTE> user(size);
    bool ok = GetTokenInformation(token, TokenUser, user.data(), size, &size) != FALSE;
    DWORD error = GetLastError(); CloseHandle(token);
    if (!ok) check(HRESULT_FROM_WIN32(error)); return user;
}
// Diagnostic metadata only. Held kernel handles bracket HWND/PID observations;
// these checks do not qualify a signed client or prove Center visibility.
struct SurfaceOwner {
    HANDLE process;
    std::wstring imageLeaf;
    explicit SurfaceOwner(HANDLE value) : process(value) {}
    ~SurfaceOwner() { CloseHandle(process); }
    bool live() const { return WaitForSingleObject(process, 0) == WAIT_TIMEOUT; }
};
struct SurfaceScan {
    std::map<DWORD, std::unique_ptr<SurfaceOwner>> owners;
    DWORD session = 0;
    std::vector<BYTE> user;
    std::wstring windows;
    std::vector<std::wstring> imageNames{L"explorer.exe", L"shellhost.exe", L"shellexperiencehost.exe"};
    bool exportImageLeaf = false, allowAnyWindowsImage = false;
    std::string projection = "visible_or_foreground_owned_shell";
    unsigned visited = 0, errors = 0, count = 0;
    bool truncated = false, enumerationCompleted = false;
    ULONGLONG deadline = 0;
    std::string rows;
    SurfaceOwner* owner(DWORD pid) {
        auto found = owners.find(pid);
        if (found != owners.end()) return found->second->live() ? found->second.get() : nullptr;
        if (owners.size() >= 16) { truncated = true; return nullptr; }
        HANDLE handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, pid);
        if (!handle) { ++errors; return nullptr; }
        auto held = std::make_unique<SurfaceOwner>(handle);
        wchar_t image[32768]{}; DWORD size = 32768, observedSession = 0;
        if (!held->live() || !QueryFullProcessImageNameW(handle, 0, image, &size)
            || !ProcessIdToSessionId(pid, &observedSession) || observedSession != session) return nullptr;
        std::wstring path(image, size);
        std::transform(path.begin(), path.end(), path.begin(), [](wchar_t c) { return std::towlower(c); });
        const auto leaf = fs::path(path).filename().wstring();
        if (path.rfind(windows + L"\\", 0) != 0
            || (!allowAnyWindowsImage && std::find(imageNames.begin(), imageNames.end(), leaf) == imageNames.end())) return nullptr;
        const auto candidate = tokenUser(handle);
        if (!EqualSid(reinterpret_cast<TOKEN_USER*>(user.data())->User.Sid,
            reinterpret_cast<const TOKEN_USER*>(candidate.data())->User.Sid) || !held->live()) return nullptr;
        held->imageLeaf = leaf;
        auto result = held.get(); owners.emplace(pid, std::move(held)); return result;
    }
    static BOOL CALLBACK visit(HWND window, LPARAM context) noexcept {
        auto& scan = *reinterpret_cast<SurfaceScan*>(context);
        if (scan.visited >= 128 || GetTickCount64() >= scan.deadline) {
            scan.truncated = true; return FALSE;
        }
        ++scan.visited;
        try {
            DWORD pid = 0; GetWindowThreadProcessId(window, &pid);
            auto held = pid ? scan.owner(pid) : nullptr;
            if (!held || !held->live()) return TRUE;
            const bool visible = IsWindowVisible(window) != FALSE, foreground = GetForegroundWindow() == window;
            if (!visible && !foreground) return TRUE;
            wchar_t name[64]{}; int length = GetClassNameW(window, name, 64);
            if (length <= 0 || length >= 63) { ++scan.errors; return TRUE; }
            DWORD after = 0; GetWindowThreadProcessId(window, &after);
            if (after != pid || !IsWindow(window) || !held->live()) { ++scan.errors; return TRUE; }
            const auto row = "{\"hwnd\":" + std::to_string(reinterpret_cast<uintptr_t>(window))
                + ",\"pid\":" + std::to_string(pid) + ",\"class\":" + jsonQuote(name)
                + (scan.exportImageLeaf ? ",\"verifiedImageLeaf\":" + jsonQuote(held->imageLeaf) : "")
                + ",\"visible\":" + (visible ? "true" : "false")
                + ",\"foreground\":" + (foreground ? "true" : "false") + "}";
            if (scan.count >= 32 || scan.rows.size() + (scan.count ? 1 : 0) + row.size() > 6000) {
                scan.truncated = true; return FALSE;
            }
            if (scan.count++) scan.rows += ',';
            scan.rows += row;
        } catch (...) { ++scan.errors; }
        return TRUE;
    }
    std::string snapshot() {
        visited = errors = count = 0; truncated = false; rows.clear(); deadline = GetTickCount64() + 2000;
        enumerationCompleted = EnumWindows(visit, reinterpret_cast<LPARAM>(this)) != FALSE;
        return "{\"projection\":" + jsonQuote(winrt::to_hstring(projection).c_str()) + ",\"enumerationCompleted\":" + std::string(enumerationCompleted ? "true" : "false")
            + ",\"truncated\":" + (truncated ? "true" : "false") + ",\"errors\":" + std::to_string(errors)
            + ",\"visited\":" + std::to_string(visited) + ",\"windows\":[" + rows + "]}";
    }
};
// Read-only discriminator: identifiers of a held Shell taskbar subtree, never UI text.
// Completing this bounded projection does not identify or authorize a Center action.
static bool taskbarUI() {
    SurfaceScan owners;
    unsigned visited = 0, count = 0, errors = 0, providerSkips = 0;
    bool truncated = false, available = false, rootStable = false, completed = false;
    HRESULT lastError = S_OK;
    std::string rows;
    const ULONGLONG deadline = GetTickCount64() + 2000;
    auto budget = [&]() { return GetTickCount64() < deadline; };
    auto call = [&](auto operation) {
        if (!budget()) { truncated = true; winrt::throw_hresult(E_ABORT); }
        const HRESULT hr = operation(); if (!budget()) truncated = true;
        return hr;
    };
    auto observed = [&](HRESULT hr) { if (FAILED(hr)) { ++errors; lastError = hr; } return SUCCEEDED(hr); };
    auto identifier = [&](HRESULT hr, BSTR value) {
        std::unique_ptr<OLECHAR, decltype(&SysFreeString)> owned(value, &SysFreeString);
        std::wstring text;
        bool valid = SUCCEEDED(hr) && (!value || SysStringLen(value) <= 128);
        if (valid && value) text.assign(value, SysStringLen(value));
        if (!observed(hr) || !valid) { if (SUCCEEDED(hr)) ++errors; return std::string("null"); }
        try { return jsonQuote(text); } catch (...) { ++errors; return std::string("null"); }
    };
    try {
        wchar_t windows[32768]{}; const UINT length = GetWindowsDirectoryW(windows, 32768);
        if (!length || length >= 32768 || !ProcessIdToSessionId(GetCurrentProcessId(), &owners.session))
            throw std::runtime_error("taskbar prerequisites unavailable");
        owners.windows.assign(windows, length); owners.user = tokenUser(GetCurrentProcess());
        std::transform(owners.windows.begin(), owners.windows.end(), owners.windows.begin(), [](wchar_t c) { return std::towlower(c); });
        const HWND window = FindWindowExW(nullptr, nullptr, L"Shell_TrayWnd", nullptr);
        DWORD pid = 0; if (window) GetWindowThreadProcessId(window, &pid);
        auto held = pid ? owners.owner(pid) : nullptr;
        auto stable = [&]() {
            DWORD current = 0; wchar_t name[64]{};
            return window && held && held->live() && IsWindow(window)
                && GetWindowThreadProcessId(window, &current) && current == pid
                && GetClassNameW(window, name, 64) == 13 && std::wstring(name) == L"Shell_TrayWnd"
                && FindWindowExW(nullptr, nullptr, L"Shell_TrayWnd", nullptr) == window
                && ([&]() { SetLastError(ERROR_SUCCESS);
                    return !FindWindowExW(nullptr, window, L"Shell_TrayWnd", nullptr) && GetLastError() == ERROR_SUCCESS; })()
                && held->live();
        };
        if (stable() && preflight("taskbar-preflight.json") && budget()) {
            ComPtr<IUIAutomation> automation;
            check(call([&]() { return CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)); }));
            ComPtr<IUIAutomationElement> element; check(call([&]() { return automation->ElementFromHandle(window, &element); }));
            ComPtr<IUIAutomationTreeWalker> walker; check(call([&]() { return automation->get_RawViewWalker(&walker); }));
            int rootPid = 0; UIA_HWND rootWindow{};
            if (!element || !observed(call([&]() { return element->get_CurrentProcessId(&rootPid); })) || rootPid != static_cast<int>(pid)
                || !observed(call([&]() { return element->get_CurrentNativeWindowHandle(&rootWindow); }))
                || reinterpret_cast<HWND>(rootWindow) != window || !stable())
                throw std::runtime_error("taskbar UIA root binding unavailable");
            available = true;
            struct Node { ComPtr<IUIAutomationElement> element; unsigned depth; int parent; };
            std::vector<Node> pending{{element, 0, -1}};
            while (!pending.empty() && budget() && visited < 128 && stable()) {
                Node node = std::move(pending.back()); pending.pop_back(); ++visited;
                int providerPid = 0; const HRESULT pidHR = call([&]() { return node.element->get_CurrentProcessId(&providerPid); });
                if (!observed(pidHR) || providerPid <= 0) { ++providerSkips; continue; }
                auto peer = owners.owner(static_cast<DWORD>(providerPid));
                if (!peer || !peer->live()) { ++providerSkips; continue; }
                BSTR id = nullptr, className = nullptr;
                const HRESULT idHR = call([&]() { return node.element->get_CurrentAutomationId(&id); });
                const auto idJSON = identifier(idHR, id);
                const HRESULT classHR = call([&]() { return node.element->get_CurrentClassName(&className); });
                const auto classJSON = identifier(classHR, className);
                CONTROLTYPEID type = 0; BOOL offscreen = TRUE;
                const HRESULT typeHR = call([&]() { return node.element->get_CurrentControlType(&type); });
                const HRESULT offscreenHR = call([&]() { return node.element->get_CurrentIsOffscreen(&offscreen); });
                observed(typeHR); observed(offscreenHR);
                int afterPid = 0; const HRESULT afterHR = call([&]() { return node.element->get_CurrentProcessId(&afterPid); });
                if (!observed(afterHR) || afterPid != providerPid || !peer->live() || !stable() || !budget()) { ++errors; continue; }
                const auto row = "{\"index\":" + std::to_string(count) + ",\"parentIndex\":" + std::to_string(node.parent)
                    + ",\"depth\":" + std::to_string(node.depth) + ",\"providerPID\":" + std::to_string(providerPid)
                    + ",\"automationId\":" + idJSON + ",\"className\":" + classJSON
                    + ",\"controlType\":" + (SUCCEEDED(typeHR) ? std::to_string(type) : "null")
                    + ",\"offscreen\":" + (FAILED(offscreenHR) ? "null" : offscreen ? "true" : "false")
                    + ",\"propertyHRESULTs\":[" + std::to_string(pidHR) + ',' + std::to_string(idHR) + ','
                    + std::to_string(classHR) + ',' + std::to_string(typeHR) + ',' + std::to_string(offscreenHR) + ',' + std::to_string(afterHR) + "]}";
                if (rows.size() + (count ? 1 : 0) + row.size() > 10000) { truncated = true; break; }
                const int index = static_cast<int>(count++); if (index) rows += ','; rows += row;
                ComPtr<IUIAutomationElement> child;
                if (!observed(call([&]() { return walker->GetFirstChildElement(node.element.Get(), &child); }))) continue;
                if (child && node.depth >= 16) { truncated = true; continue; }
                while (child && budget()) {
                    if (pending.size() + visited >= 128) { truncated = true; break; }
                    pending.push_back({child, node.depth + 1, index});
                    ComPtr<IUIAutomationElement> next;
                    if (!observed(call([&]() { return walker->GetNextSiblingElement(child.Get(), &next); }))) break;
                    child = next;
                }
                if (child && !budget()) truncated = true;
            }
            rootStable = stable();
            if (!pending.empty() || !budget() || owners.truncated) truncated = true;
            completed = rootStable && !truncated && !errors && !owners.errors && !providerSkips;
        }
    } catch (const winrt::hresult_error& e) { ++errors; lastError = e.code().value; }
      catch (...) { ++errors; lastError = E_FAIL; }
    const bool expired = !budget();
    if (expired) { truncated = true; completed = false; }
    report("taskbar-uia.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"diagnosticOnly\":true,\"readOnly\":true,\"showAttempts\":0,\"inputAttempted\":false"
        + ",\"centerActionQualified\":false,\"negativeIsAbsenceProof\":false,\"projection\":\"owned_taskbar_identifiers\""
        + ",\"available\":" + (available ? "true" : "false") + ",\"rootStable\":" + (rootStable ? "true" : "false")
        + ",\"walkCompleted\":" + (completed ? "true" : "false") + ",\"truncated\":" + (truncated ? "true" : "false")
        + ",\"deadlineExpired\":" + (expired ? "true" : "false")
        + ",\"visited\":" + std::to_string(visited) + ",\"count\":" + std::to_string(count)
        + ",\"verifiedOwners\":" + std::to_string(owners.owners.size()) + ",\"providerSkips\":" + std::to_string(providerSkips)
        + ",\"errors\":" + std::to_string(errors + owners.errors) + ",\"lastErrorHRESULT\":" + std::to_string(lastError)
        + ",\"rows\":[" + rows + "]}\n");
    return available;
}
static bool centerSurface() {
    SurfaceScan scan;
    wchar_t windows[32768]{}; UINT size = GetWindowsDirectoryW(windows, 32768);
    if (!size || size >= 32768 || !ProcessIdToSessionId(GetCurrentProcessId(), &scan.session))
        throw std::runtime_error("surface prerequisites unavailable");
    scan.windows.assign(windows, size); scan.user = tokenUser(GetCurrentProcess());
    std::transform(scan.windows.begin(), scan.windows.end(), scan.windows.begin(), [](wchar_t c) { return std::towlower(c); });
    const auto before = scan.snapshot(), beforeRows = scan.rows;
    report("center-surface-before.json", "{\"pid\":" + std::to_string(GetCurrentProcessId())
        + ",\"nonce\":" + jsonQuote(uuid) + ",\"showAttempts\":0,\"snapshot\":" + before + "}\n");
    DWORD shellPid = 0; GetWindowThreadProcessId(GetShellWindow(), &shellPid);
    auto shell = shellPid ? scan.owner(shellPid) : nullptr;
    const bool shellLive = shell && shell->live(), scanComplete = scan.enumerationCompleted && !scan.truncated;
    const bool desktopReady = preflight("surface-preflight.json");
    // Metadata completeness limits the observations, not input authority. The
    // independently held Shell identity and fresh desktop remain mandatory.
    if (!shellLive || !desktopReady) {
        report("center-surface-rejected.json", "{\"pid\":" + std::to_string(GetCurrentProcessId())
            + ",\"nonce\":" + jsonQuote(uuid) + ",\"showAttempts\":0,\"inputAttempted\":false"
            + ",\"shellLive\":" + (shellLive ? "true" : "false") + ",\"scanComplete\":" + (scanComplete ? "true" : "false")
            + ",\"desktopReady\":" + (desktopReady ? "true" : "false") + "}\n");
        return false;
    }
    for (int key : {VK_LWIN, VK_RWIN, static_cast<int>('N'), VK_SHIFT, VK_CONTROL, VK_MENU}) {
        if (GetAsyncKeyState(key) & 0x8000) throw std::runtime_error("existing key press; no input injected");
    }
    report("center-surface-intent.json", "{\"pid\":" + std::to_string(GetCurrentProcessId())
        + ",\"nonce\":" + jsonQuote(uuid) + ",\"showAttempts\":0,\"chordIntent\":1}\n");
    // Each successful down is owned by this attempt. Cleanup only releases owned
    // unreleased keys, and failed release stays unknown rather than claiming safety.
    struct Keys {
        bool win = false, n = false, releaseUnknown = false;
        unsigned accepted = 0, releaseAttempts = 0;
        DWORD error = 0;
        bool send(WORD key, bool up) {
            INPUT event{}; event.type = INPUT_KEYBOARD; event.ki.wVk = key;
            event.ki.dwFlags = up ? KEYEVENTF_KEYUP : 0;
            SetLastError(ERROR_SUCCESS); UINT sent = SendInput(1, &event, sizeof(event));
            if (sent != 1) { error = GetLastError(); return false; }
            ++accepted; (key == VK_LWIN ? win : n) = !up; return true;
        }
        void release() noexcept {
            if (n) { ++releaseAttempts; if (!send('N', true)) releaseUnknown = true; n = false; }
            if (win) { ++releaseAttempts; if (!send(VK_LWIN, true)) releaseUnknown = true; win = false; }
        }
        ~Keys() { release(); }
    } keys;
    bool chord = shell->live() && keys.send(VK_LWIN, false) && keys.send('N', false)
        && keys.send('N', true) && keys.send(VK_LWIN, true);
    keys.release(); Sleep(500);
    const auto after = scan.snapshot();
    report("center-surface.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"diagnosticOnly\":true,\"showAttempts\":0,\"centerOpenedProved\":false,\"chordAttempts\":1"
        + ",\"chordAccepted\":" + (chord ? "true" : "false") + ",\"acceptedKeyEvents\":" + std::to_string(keys.accepted)
        + ",\"releaseAttempts\":" + std::to_string(keys.releaseAttempts) + ",\"keyReleaseUnknown\":" + (keys.releaseUnknown ? "true" : "false")
        + ",\"inputError\":" + std::to_string(keys.error) + ",\"shellStillLive\":" + (shell->live() ? "true" : "false")
        + ",\"surfaceChangeObserved\":" + (beforeRows != scan.rows ? "true" : "false")
        + ",\"surfaceChangeMeaning\":\"sampled metadata rows differ; completeness and Center visibility unproved\""
        + ",\"before\":" + before + ",\"after\":" + after + "}\n");
    return chord && !keys.releaseUnknown;
}
static void verifyShellIdentity() {
    DWORD shellPID = 0, ownSession = 0, shellSession = 0;
    GetWindowThreadProcessId(GetShellWindow(), &shellPID);
    if (!shellPID || !ProcessIdToSessionId(GetCurrentProcessId(), &ownSession)
        || !ProcessIdToSessionId(shellPID, &shellSession)) throw std::runtime_error("Shell session unavailable");
    HANDLE shell = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, shellPID);
    if (!shell) check(HRESULT_FROM_WIN32(GetLastError()));
    std::vector<BYTE> shellUser;
    try { shellUser = tokenUser(shell); } catch (...) { CloseHandle(shell); throw; }
    CloseHandle(shell); auto ownUser = tokenUser(GetCurrentProcess());
    bool sameUser = EqualSid(reinterpret_cast<TOKEN_USER*>(ownUser.data())->User.Sid,
                             reinterpret_cast<TOKEN_USER*>(shellUser.data())->User.Sid) != FALSE;
    report("shell-identity.json", "{\"shellPID\":" + std::to_string(shellPID)
        + ",\"ownSession\":" + std::to_string(ownSession) + ",\"shellSession\":" + std::to_string(shellSession)
        + ",\"sameUser\":" + (sameUser ? "true" : "false") + "}\n");
    if (!sameUser || ownSession == 0 || ownSession != shellSession) throw std::runtime_error("Shell user/session mismatch");
}
static NotificationSetting measuredReadiness(ToastNotifier& notifier) {
    // Experiment: explicit shortcut notification may let Shell recognize this new TEST identity.
    // Neither SHCNF_FLUSH nor the shortcut's existence proves AppResolver recognition.
    ULONGLONG started = GetTickCount64(), deadline = started + 3000; unsigned attempt = 0;
    ToastNotifier fresh{nullptr}; bool freshAttempted = false; HRESULT freshCreateHR = E_PENDING;
    for (;;) {
        ComPtr<IShellItem2> item; std::wstring parsingName = L"shell:AppsFolder\\" + aumid;
        HRESULT parseHR = SHCreateItemFromParsingName(parsingName.c_str(), nullptr, IID_PPV_ARGS(&item));
        HRESULT appIDHR = E_PENDING; PWSTR actualID = nullptr;
        if (SUCCEEDED(parseHR)) appIDHR = item->GetString(PKEY_AppUserModel_ID, &actualID);
        std::wstring observedID = actualID ? actualID : L""; CoTaskMemFree(actualID);
        bool recognized = SUCCEEDED(parseHR) && SUCCEEDED(appIDHR) && observedID == aumid;
        HRESULT settingHR = S_OK; NotificationSetting setting = NotificationSetting::Enabled;
        try { setting = notifier.Setting(); } catch (const winrt::hresult_error& error) { settingHR = error.code().value; }
        if (recognized && !freshAttempted) {
            freshAttempted = true;
            try { fresh = ToastNotificationManager::CreateToastNotifier(aumid); freshCreateHR = S_OK; }
            catch (const winrt::hresult_error& error) { freshCreateHR = error.code().value; }
        }
        HRESULT freshSettingHR = E_PENDING; NotificationSetting freshSetting = NotificationSetting::Enabled;
        if (fresh) {
            try { freshSetting = fresh.Setting(); freshSettingHR = S_OK; }
            catch (const winrt::hresult_error& error) { freshSettingHR = error.code().value; }
        }
        ULONGLONG now = GetTickCount64();
        std::string filename = "shell-readiness-" + std::to_string(++attempt) + ".json";
        report(filename.c_str(), "{\"aumid\":" + jsonQuote(aumid) + ",\"observedAppID\":" + jsonQuote(observedID)
            + ",\"parseHRESULT\":" + std::to_string(parseHR) + ",\"appIDHRESULT\":" + std::to_string(appIDHR)
            + ",\"recognized\":" + (recognized ? "true" : "false") + ",\"settingHRESULT\":" + std::to_string(settingHR)
            + ",\"freshAttempted\":" + (freshAttempted ? "true" : "false")
            + ",\"freshCreateHRESULT\":" + std::to_string(freshCreateHR)
            + ",\"freshSettingHRESULT\":" + std::to_string(freshSettingHR)
            + ",\"freshSetting\":" + std::to_string(SUCCEEDED(freshSettingHR) ? static_cast<int>(freshSetting) : -1)
            + ",\"elapsedMS\":" + std::to_string(now - started)
            + ",\"setting\":" + std::to_string(SUCCEEDED(settingHR) ? static_cast<int>(setting) : -1) + "}\n");
        if (FAILED(settingHR) && settingHR != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) check(settingHR);
        if (freshAttempted && FAILED(freshCreateHR)) check(freshCreateHR);
        if (fresh && FAILED(freshSettingHR) && freshSettingHR != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) check(freshSettingHR);
        // A new TEST identity may be absent from this read. Bounded polling does not
        // establish an AppsFolder recognition guarantee; Show still requires a match.
        if (FAILED(parseHR) && parseHR != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)
            && parseHR != HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND)) check(parseHR);
        if (now >= deadline) throw std::runtime_error("TEST Shell readiness budget expired; no Show");
        if (SUCCEEDED(settingHR) && setting != NotificationSetting::Enabled) return setting;
        // One new public-API instance after exact recognition tests a stale-object
        // hypothesis. Neither recognition nor the old instance permits Show.
        if (recognized && fresh && SUCCEEDED(freshSettingHR)) { notifier = fresh; return freshSetting; }
        if (attempt >= 16) throw std::runtime_error("TEST Shell AUMID/Setting readiness limit; no Show");
        // Predicate polling with a bounded message wait, rather than a fixed startup sleep.
        DWORD wait = static_cast<DWORD>(std::min<ULONGLONG>(200, deadline - now));
        if (MsgWaitForMultipleObjectsEx(0, nullptr, wait, QS_ALLINPUT, MWMO_INPUTAVAILABLE) == WAIT_FAILED)
            check(HRESULT_FROM_WIN32(GetLastError()));
        MSG message{}; unsigned messages = 0;
        while (messages++ < 64 && GetTickCount64() < deadline && PeekMessageW(&message, nullptr, 0, 0, PM_REMOVE)) {
            TranslateMessage(&message); DispatchMessageW(&message);
        }
    }
}
static void install() {
    // Root is supplied by the CI-only controller and has a unique immutable UUID marker.
    wchar_t exe[MAX_PATH]{};
    DWORD exeLength = GetModuleFileNameW(nullptr, exe, MAX_PATH);
    if (!exeLength || exeLength >= MAX_PATH) throw std::runtime_error("executable path unavailable or too long");
    HKEY key = nullptr; DWORD disposition = 0;
    sendStage("registry_create");
    LSTATUS status = RegCreateKeyExW(HKEY_CURRENT_USER, registryKey().c_str(), 0, nullptr, 0,
                                     KEY_WRITE, nullptr, &key, &disposition);
    if (status != ERROR_SUCCESS) throw std::runtime_error("COM registration failed");
    RegCloseKey(key);
    if (disposition != REG_CREATED_NEW_KEY) throw std::runtime_error("unique COM key already exists");
    std::wstring command = serverCommand();
    report(".registry-owned", narrow(command) + "\n");
    sendStage("registry_local_server_create");
    check(HRESULT_FROM_WIN32(RegCreateKeyExW(HKEY_CURRENT_USER, (registryKey() + L"\\LocalServer32").c_str(),
        0, nullptr, 0, KEY_SET_VALUE, nullptr, &key, nullptr)));
    sendStage("registry_local_server_value");
    status = RegSetValueExW(key, nullptr, 0, REG_SZ, reinterpret_cast<const BYTE*>(command.c_str()),
                           static_cast<DWORD>((command.size() + 1) * sizeof(wchar_t)));
    RegCloseKey(key); check(HRESULT_FROM_WIN32(status));
    installAppIdentity();
    sendStage("shortcut_locate");
    fs::path linkPath = shortcut();
    report("shortcut-location.json", "{\"path\":" + jsonQuote(linkPath.wstring()) + ",\"aumid\":" + jsonQuote(aumid) + "}\n");
    if (fs::exists(linkPath)) throw std::runtime_error("unique shortcut already exists");
    sendStage("shortcut_create");
    ComPtr<IShellLinkW> link; check(CoCreateInstance(CLSID_ShellLink, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&link)));
    sendStage("shortcut_target");
    check(link->SetPath(exe)); check(link->SetArguments(L"inert"));
    ComPtr<IPropertyStore> props; check(link.As(&props));
    sendStage("shortcut_aumid");
    PROPVARIANT value{}; check(InitPropVariantFromString(aumid.c_str(), &value));
    check(props->SetValue(PKEY_AppUserModel_ID, value)); PropVariantClear(&value);
    sendStage("shortcut_activator");
    check(InitPropVariantFromCLSID(clsid, &value));
    check(props->SetValue(PKEY_AppUserModel_ToastActivatorCLSID, value)); PropVariantClear(&value);
    sendStage("shortcut_commit");
    check(props->Commit()); ComPtr<IPersistFile> file; check(link.As(&file));
    sendStage("shortcut_save");
    check(file->Save(linkPath.c_str(), TRUE));
    report(".shortcut-owned", narrow(linkPath.wstring()) + "\n");
    sendStage("install_complete");
}
static int send() {
    install();
    sendStage("shortcut_readback");
    if (!shortcutMatches(true)) throw std::runtime_error("saved TEST shortcut binding mismatch");
    sendStage("shell_identity"); verifyShellIdentity();
    sendStage("shell_shortcut_notify");
    fs::path linkPath = shortcut();
    if (linkPath.wstring().size() >= MAX_PATH) throw std::runtime_error("Shell notification path too long");
    SHChangeNotify(SHCNE_CREATE, SHCNF_PATHW | SHCNF_FLUSH, linkPath.c_str(), nullptr);
    report("shell-notify.json", "{\"event\":\"SHCNE_CREATE\",\"flags\":\"SHCNF_PATHW|SHCNF_FLUSH\",\"path\":"
        + jsonQuote(linkPath.wstring()) + ",\"returned\":true}\n");
    sendStage("notifier_create");
    auto notifier = ToastNotificationManager::CreateToastNotifier(aumid);
    sendStage("notifier_readiness");
    auto setting = measuredReadiness(notifier);
    sendStage("sender_receipt");
    report("sender.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"aumid\":" + jsonQuote(aumid)
        + ",\"nonce\":" + jsonQuote(uuid) + ",\"notificationSetting\":" + std::to_string(static_cast<int>(setting))
        + ",\"showCalledAtReceipt\":false}\n");
    if (setting != NotificationSetting::Enabled) return 3;
    std::wstring xml = L"<toast launch='" + uuid + L"'><visual><binding template='ToastGeneric'><text>Navigation TEST "
        + uuid + L"</text><text>Synthetic CI lifecycle probe</text></binding></visual><actions><action content='"
        + action + L"' arguments='" + uuid + L"' activationType='foreground'/></actions><audio silent='true'/></toast>";
    sendStage("xml_load");
    winrt::Windows::Data::Xml::Dom::XmlDocument document; document.LoadXml(xml);
    sendStage("toast_create");
    ToastNotification toast(document);
    sendStage("toast_tag"); toast.Tag(uuid.substr(0, 16));
    sendStage("toast_group"); toast.Group(L"NavigationTEST");
    sendStage("show_boundary"); // Durable intent is not proof the following call was reached.
    showCallEntered = true;
    notifier.Show(toast); // Exactly one native Show; no resubmission on uncertainty.
    showCallReturned = true;
    report("show-outcome.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"showCallEntered\":true,\"showCallReturned\":true}\n");
    sendStage("show_returned");
    report("submitted.json", "{\"showReturned\":true,\"pid\":" + std::to_string(GetCurrentProcessId()) + "}\n");
    return 0;
}
class Callback final : public INotificationActivationCallback {
    std::atomic<ULONG> refs{1};
public:
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID iid, void** out) override {
        if (!out) return E_POINTER; *out = nullptr;
        if (iid == IID_IUnknown || iid == __uuidof(INotificationActivationCallback)) {
            *out = static_cast<INotificationActivationCallback*>(this); AddRef(); return S_OK;
        }
        return E_NOINTERFACE;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { ULONG n = --refs; if (!n) delete this; return n; }
    HRESULT STDMETHODCALLTYPE Activate(LPCWSTR app, LPCWSTR args, const NOTIFICATION_USER_INPUT_DATA*, ULONG count) override {
        try {
            // Reject unbounded OS arguments before reading or serializing them.
            if (!app || !args || wcsnlen_s(app, 129) > 128 || wcsnlen_s(args, 37) > 36) return E_INVALIDARG;
            bool matches = app && args && app == aumid && args == uuid && count == 0;
            report("callback.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"aumid\":"
                + jsonQuote(app ? app : L"") + ",\"nonce\":" + jsonQuote(args ? args : L"")
                + ",\"startedAt\":" + std::to_string(processStartedAt())
                + ",\"inputCount\":" + std::to_string(count) + ",\"matches\":" + (matches ? "true" : "false") + "}\n");
            activated = true; return matches ? S_OK : E_INVALIDARG;
        } catch (...) { activated = true; return E_FAIL; }
    }
};
class Factory final : public IClassFactory {
    std::atomic<ULONG> refs{1};
public:
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID iid, void** out) override {
        if (!out) return E_POINTER; *out = nullptr;
        if (iid == IID_IUnknown || iid == IID_IClassFactory) { *out = static_cast<IClassFactory*>(this); AddRef(); return S_OK; }
        return E_NOINTERFACE;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { ULONG n = --refs; if (!n) delete this; return n; }
    HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown* outer, REFIID iid, void** out) override {
        if (outer) return CLASS_E_NOAGGREGATION;
        Callback* cb = new Callback; HRESULT hr = cb->QueryInterface(iid, out); cb->Release(); return hr;
    }
    HRESULT STDMETHODCALLTYPE LockServer(BOOL) override { return S_OK; }
};
static int callback() {
    report("callback-started.json", "{\"pid\":" + std::to_string(GetCurrentProcessId())
        + ",\"nonce\":" + jsonQuote(uuid) + ",\"startedAt\":" + std::to_string(processStartedAt()) + "}\n");
    Factory* factory = new Factory; DWORD cookie = 0;
    HRESULT hr = CoRegisterClassObject(clsid, factory, CLSCTX_LOCAL_SERVER, REGCLS_MULTIPLEUSE, &cookie);
    factory->Release(); check(hr);
    // COM uses the MTA. This is only the OS-created LocalServer32 entrypoint.
    for (int i = 0; i < 300 && !activated; ++i) Sleep(100);
    CoRevokeClassObject(cookie); return activated ? 0 : 4;
}
// Failure diagnostics only. Never exports unrelated UI text or performs an action.
// Child walking keeps this independent of the action finder's exact Name + Button predicate.
static void snapshotOwnedShellUI(IUIAutomation* automation) noexcept {
    unsigned rootsRead = 0, shellRoots = 0, nodesRead = 0, errors = 0, providerSkips = 0;
    bool capped = false; std::string matches = "[";
    const ULONGLONG deadline = GetTickCount64() + 2000;
    auto budget = [&]() { return GetTickCount64() < deadline && nodesRead < 512; };
    try {
        DWORD session = 0; if (!ProcessIdToSessionId(GetCurrentProcessId(), &session)) throw std::runtime_error("session unavailable");
        wchar_t windows[32768]{}; if (!GetWindowsDirectoryW(windows, 32768)) throw std::runtime_error("Windows directory unavailable");
        std::wstring windowsPrefix = std::wstring(windows) + L"\\";
        std::transform(windowsPrefix.begin(), windowsPrefix.end(), windowsPrefix.begin(), towlower);
        ComPtr<IUIAutomationTreeWalker> walker; check(automation->get_RawViewWalker(&walker));
        ComPtr<IUIAutomationElement> desktop, child;
        check(automation->GetRootElement(&desktop)); check(walker->GetFirstChildElement(desktop.Get(), &child));
        while (child && rootsRead < 64 && shellRoots < 16 && budget()) {
            ++rootsRead;
            int pid = 0; HRESULT pidResult = child->get_CurrentProcessId(&pid);
            DWORD peerSession = 0;
            std::unique_ptr<void, decltype(&CloseHandle)> peer(SUCCEEDED(pidResult) && pid > 0 && pid != static_cast<int>(GetCurrentProcessId())
                ? OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, static_cast<DWORD>(pid)) : nullptr, &CloseHandle);
            wchar_t image[32768]{}; DWORD length = 32768;
            bool verified = peer && WaitForSingleObject(peer.get(), 0) == WAIT_TIMEOUT
                && ProcessIdToSessionId(static_cast<DWORD>(pid), &peerSession) && peerSession == session
                && WaitForSingleObject(peer.get(), 0) == WAIT_TIMEOUT
                && QueryFullProcessImageNameW(peer.get(), 0, image, &length);
            std::wstring imageLower = image;
            std::transform(imageLower.begin(), imageLower.end(), imageLower.begin(), towlower);
            std::wstring base = fs::path(imageLower).filename().wstring();
            verified = verified && imageLower.compare(0, windowsPrefix.size(), windowsPrefix) == 0
                && (base == L"shellexperiencehost.exe" || base == L"shellhost.exe" || base == L"explorer.exe");
            // Retain the kernel process handle while reading that provider's subtree.
            if (verified) {
                ++shellRoots;
                struct Node { ComPtr<IUIAutomationElement> element; unsigned depth; };
                std::vector<Node> pending{{child, 0}};
                unsigned containerNodes = 0;
                while (!pending.empty() && containerNodes < 128 && budget() && WaitForSingleObject(peer.get(), 0) == WAIT_TIMEOUT) {
                    Node node = std::move(pending.back()); pending.pop_back(); ++nodesRead; ++containerNodes;
                    int nodePid = 0;
                    if (FAILED(node.element->get_CurrentProcessId(&nodePid)) || nodePid != pid) { ++providerSkips; continue; }
                    BSTR name = nullptr;
                    HRESULT nameResult = node.element->get_CurrentName(&name);
                    bool exactTitle = false, exactAction = false, ownNonce = false;
                    if (SUCCEEDED(nameResult) && name && SysStringLen(name) <= 4096) {
                        std::wstring text(name, SysStringLen(name));
                        exactTitle = text == L"Navigation TEST " + uuid; exactAction = text == action;
                        ownNonce = text.find(uuid) != std::wstring::npos;
                    } else if (FAILED(nameResult)) ++errors;
                    SysFreeString(name);
                    if (ownNonce) {
                        CONTROLTYPEID type = 0; BOOL offscreen = TRUE, enabled = FALSE;
                        HRESULT typeResult = node.element->get_CurrentControlType(&type);
                        HRESULT offscreenResult = node.element->get_CurrentIsOffscreen(&offscreen);
                        HRESULT enabledResult = node.element->get_CurrentIsEnabled(&enabled);
                        VARIANT available{};
                        HRESULT patternResult = node.element->GetCurrentPropertyValue(UIA_IsInvokePatternAvailablePropertyId, &available);
                        bool invokable = SUCCEEDED(patternResult) && available.vt == VT_BOOL && available.boolVal == VARIANT_TRUE;
                        VariantClear(&available);
                        if (matches.size() > 1) matches += ',';
                        matches += "{\"depth\":" + std::to_string(node.depth) + ",\"providerPID\":" + std::to_string(pid)
                            + ",\"exactTitle\":" + (exactTitle ? "true" : "false") + ",\"exactAction\":" + (exactAction ? "true" : "false")
                            + ",\"controlType\":" + (SUCCEEDED(typeResult) ? std::to_string(type) : "null")
                            + ",\"offscreen\":" + (FAILED(offscreenResult) ? "null" : offscreen ? "true" : "false")
                            + ",\"enabled\":" + (FAILED(enabledResult) ? "null" : enabled ? "true" : "false")
                            + ",\"invokePatternAvailable\":" + (FAILED(patternResult) ? "null" : invokable ? "true" : "false") + '}';
                    }
                    if (node.depth >= 16) { capped = true; continue; }
                    ComPtr<IUIAutomationElement> descendant;
                    if (FAILED(walker->GetFirstChildElement(node.element.Get(), &descendant))) { ++errors; continue; }
                    while (descendant && budget()) {
                        if (pending.size() + nodesRead >= 512 || pending.size() + containerNodes >= 128) { capped = true; break; }
                        pending.push_back({descendant, node.depth + 1});
                        ComPtr<IUIAutomationElement> next;
                        if (FAILED(walker->GetNextSiblingElement(descendant.Get(), &next))) { ++errors; break; }
                        descendant = next;
                    }
                }
                if (!pending.empty()) capped = true;
            }
            ComPtr<IUIAutomationElement> next;
            check(walker->GetNextSiblingElement(child.Get(), &next)); child = next;
        }
        if (child) capped = true;
    } catch (...) { ++errors; }
    matches += ']';
    try {
        report("ui-owned-snapshot.json", "{\"diagnosticOnly\":true,\"negativeIsAbsenceProof\":false,\"desktopChildrenRead\":" + std::to_string(rootsRead)
            + ",\"verifiedShellContainers\":" + std::to_string(shellRoots) + ",\"nodesRead\":" + std::to_string(nodesRead)
            + ",\"providerSkips\":" + std::to_string(providerSkips) + ",\"errors\":" + std::to_string(errors)
            + ",\"truncated\":" + (capped || !budget() ? "true" : "false") + ",\"ownedNonceMatches\":" + matches + "}\n");
    } catch (...) { /* Diagnostic failure never qualifies a callback or changes the failed Invoke. */ }
}
// Packaged TEST default-body activation is distinct from the unpackaged action finder.
static std::string oobeStateHash(const std::string& state);
static void oobeDurableIntent(const char* name, const std::string& content);
struct ToastInput {
    HANDLE file = INVALID_HANDLE_VALUE;
    std::string bytes;
    winrt::Windows::Data::Json::JsonObject json{nullptr};
    explicit ToastInput(const char* name) {
        const auto path = root / name;
        if (fs::canonical(path) != path) throw std::runtime_error("toast input link refused");
        file = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING,
            FILE_FLAG_OPEN_REPARSE_POINT, nullptr);
        if (file == INVALID_HANDLE_VALUE) throw std::runtime_error("toast input custody missing");
        try {
            BY_HANDLE_FILE_INFORMATION info{}; LARGE_INTEGER size{}; DWORD count{};
            if (!GetFileInformationByHandle(file, &info) || info.nNumberOfLinks != 1
                || (info.dwFileAttributes & (FILE_ATTRIBUTE_REPARSE_POINT | FILE_ATTRIBUTE_DIRECTORY))
                || !GetFileSizeEx(file, &size) || size.QuadPart < 1 || size.QuadPart > 16384)
                throw std::runtime_error("toast input bound invalid");
            bytes.resize(static_cast<size_t>(size.QuadPart));
            if (!ReadFile(file, bytes.data(), static_cast<DWORD>(bytes.size()), &count, nullptr) || count != bytes.size())
                throw std::runtime_error("toast input incomplete");
            json = winrt::Windows::Data::Json::JsonObject::Parse(winrt::to_hstring(bytes));
        } catch (...) { CloseHandle(file); file = INVALID_HANDLE_VALUE; throw; }
    }
    ~ToastInput() { if (file != INVALID_HANDLE_VALUE) CloseHandle(file); }
    ToastInput(const ToastInput&) = delete;
};
static std::string toastRuntime(IUIAutomationElement* element) {
    SAFEARRAY* raw = nullptr; check(element->GetRuntimeId(&raw));
    std::unique_ptr<SAFEARRAY, decltype(&SafeArrayDestroy)> held(raw, &SafeArrayDestroy);
    LONG low{}, high{};
    if (!raw || SafeArrayGetDim(raw) != 1 || SafeArrayGetElemsize(raw) != sizeof(int))
        throw std::runtime_error("toast runtime ID invalid");
    check(SafeArrayGetLBound(raw, 1, &low)); check(SafeArrayGetUBound(raw, 1, &high));
    if (high < low || static_cast<long long>(high) - low > 63) throw std::runtime_error("toast runtime ID oversized");
    std::string value = "[";
    for (long long offset = 0; offset <= static_cast<long long>(high) - low; ++offset) {
        LONG i = static_cast<LONG>(low + offset); int part{}; check(SafeArrayGetElement(raw, &i, &part));
        if (offset) value += ','; value += std::to_string(part); }
    return value + ']';
}
struct ToastRow {
    ComPtr<IUIAutomationElement> row, title;
    int pid{};
    std::string runtime, titleRuntime, containerRuntime;
};
static int invokeToastDefault() {
    const ULONGLONG deadline = GetTickCount64() + 30000;
    ToastInput spec("msix-spec.json"), ready("identity-ready.json"), show("show-outcome.json"), exited("sender-exit.json");
    auto demand = [](bool ok) { if (!ok) throw std::runtime_error("packaged toast authority unavailable"); };
    auto field = [](auto& object, const wchar_t* key) { return std::wstring(object.GetNamedString(key)); };
    DWORD session{}; demand(ProcessIdToSessionId(GetCurrentProcessId(), &session) && session != 0);
    const auto user = tokenUser(GetCurrentProcess()); LPWSTR rawSid = nullptr;
    demand(ConvertSidToStringSidW(reinterpret_cast<const TOKEN_USER*>(user.data())->User.Sid, &rawSid) != FALSE);
    std::wstring sid(rawSid); LocalFree(rawSid);
    std::wstring packageStem = L"NavigationTest." + uuid; packageStem.erase(std::remove(packageStem.begin(), packageStem.end(), L'-'), packageStem.end());
    const std::wstring package = field(spec.json, L"packageFullName"), app = field(spec.json, L"aumid");
    demand(field(spec.json, L"nonce") == uuid && field(spec.json, L"userSid") == sid
        && spec.json.GetNamedNumber(L"session") == session && package.rfind(packageStem + L"_1.0.0.0_arm64__", 0) == 0
        && app == packageStem + L"_" + package.substr(package.find_last_of(L'_') + 1) + L"!TestSender");
    for (auto* record : {&ready.json, &exited.json}) {
        for (auto key : {L"nonce", L"aumid", L"packageFullName", L"userSid", L"executableSHA256"})
            demand(field(*record, key) == field(spec.json, key));
        demand(record->GetNamedNumber(L"session") == session);
    }
    const double senderPID = ready.json.GetNamedNumber(L"pid");
    demand(senderPID >= 1 && senderPID <= MAXDWORD && senderPID == static_cast<DWORD>(senderPID));
    demand(field(show.json, L"nonce") == uuid && show.json.GetNamedNumber(L"pid") == ready.json.GetNamedNumber(L"pid")
        && exited.json.GetNamedNumber(L"pid") == ready.json.GetNamedNumber(L"pid")
        && field(exited.json, L"creationTicks") == field(ready.json, L"creationTicks")
        && show.json.GetNamedBoolean(L"showCallEntered") && show.json.GetNamedBoolean(L"showCallReturned")
        && show.json.GetNamedNumber(L"hresult") == 0 && exited.json.GetNamedBoolean(L"collected")
        && exited.json.GetNamedNumber(L"exitCode") == 0 && preflight("toast-default-preflight.json"));
    SurfaceScan custody; custody.session = session; custody.user = user;
    wchar_t windows[32768]{}; demand(GetWindowsDirectoryW(windows, 32768) != 0);
    custody.windows = windows; std::transform(custody.windows.begin(), custody.windows.end(), custody.windows.begin(), towlower);
    // Closed TEST effect authority, unlike the broader read-only diagnostic Windows-prefix census.
    const std::map<std::wstring, std::string> shellPaths{
        {custody.windows + L"\\explorer.exe", "windows_explorer"},
        {custody.windows + L"\\system32\\shellhost.exe", "system32_shellhost"},
        {custody.windows + L"\\systemapps\\shellexperiencehost_cw5n1h2txyewy\\shellexperiencehost.exe", "systemapps_shellexperiencehost"},
        {custody.windows + L"\\systemapps\\microsoftwindows.client.cbs_cw5n1h2txyewy\\shellhost.exe", "systemapps_cbs_shellhost"}};
    std::map<DWORD, std::wstring> heldPaths;
    auto shellPath = [&](SurfaceOwner* owner, DWORD pid) {
        wchar_t image[32768]{}; DWORD length = 32768;
        demand(owner && owner->live() && QueryFullProcessImageNameW(owner->process, 0, image, &length));
        std::wstring path(image, length), canonical = fs::canonical(path).wstring();
        std::transform(path.begin(), path.end(), path.begin(), towlower);
        std::transform(canonical.begin(), canonical.end(), canonical.begin(), towlower);
        demand(path == canonical && shellPaths.contains(path));
        auto found = heldPaths.find(pid); demand(found == heldPaths.end() || found->second == path);
        heldPaths.emplace(pid, path); return path;
    };
    ComPtr<IUIAutomation> automation; check(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)));
    ComPtr<IUIAutomationTreeWalker> walker; check(automation->get_RawViewWalker(&walker));
    auto census = [&](unsigned index) {
        std::map<std::string, ToastRow> rows; unsigned nodes = 0, roots = 0, titles = 0;
        auto budget = [&] { demand(nodes <= 512 && roots <= 64 && GetTickCount64() < deadline); };
        ComPtr<IUIAutomationElement> desktop, child;
        check(automation->GetRootElement(&desktop)); check(walker->GetFirstChildElement(desktop.Get(), &child));
        while (child) {
            ++roots; budget(); int pid{}; check(child->get_CurrentProcessId(&pid));
            auto owner = pid > 0 ? custody.owner(static_cast<DWORD>(pid)) : nullptr;
            if (custody.errors || custody.truncated) throw std::runtime_error("toast provider census incomplete");
            if (owner) {
                shellPath(owner, static_cast<DWORD>(pid));
                struct Node { ComPtr<IUIAutomationElement> element; unsigned depth; };
                std::vector<Node> pending{{child, 0}};
                while (!pending.empty()) {
                    auto node = std::move(pending.back()); pending.pop_back(); ++nodes; budget(); demand(owner->live());
                    int provider{}; CONTROLTYPEID type{}; check(node.element->get_CurrentProcessId(&provider));
                    demand(provider == pid); check(node.element->get_CurrentControlType(&type));
                    BSTR name = nullptr; check(node.element->get_CurrentName(&name));
                    const bool exact = name && SysStringLen(name) == (L"Navigation TEST " + uuid).size()
                        && std::wstring(name, SysStringLen(name)) == L"Navigation TEST " + uuid;
                    SysFreeString(name);
                    if (exact) {
                        ++titles; demand(type == UIA_TextControlTypeId && titles == 1);
                        ToastRow found; found.title = node.element; found.pid = pid;
                        found.titleRuntime = toastRuntime(node.element.Get()); found.containerRuntime = toastRuntime(child.Get());
                        ComPtr<IUIAutomationElement> parent = node.element;
                        for (unsigned depth = 0; depth < std::min(8U, node.depth); ++depth) {
                            ComPtr<IUIAutomationElement> next; check(walker->GetParentElement(parent.Get(), &next)); demand(next != nullptr);
                            check(next->get_CurrentProcessId(&provider)); demand(provider == pid);
                            check(next->get_CurrentControlType(&type)); parent = next;
                            if (type == UIA_ListItemControlTypeId) { found.row = next; break; }
                        }
                        demand(found.row != nullptr); BOOL offscreen = TRUE, enabled = FALSE;
                        check(found.title->get_CurrentIsOffscreen(&offscreen)); demand(!offscreen);
                        check(found.row->get_CurrentIsOffscreen(&offscreen)); check(found.row->get_CurrentIsEnabled(&enabled));
                        demand(!offscreen && enabled); ComPtr<IUIAutomationInvokePattern> pattern;
                        check(found.row->GetCurrentPatternAs(UIA_InvokePatternId, IID_PPV_ARGS(&pattern)));
                        found.runtime = toastRuntime(found.row.Get()); const auto key = found.runtime; rows.emplace(key, std::move(found));
                    }
                    ComPtr<IUIAutomationElement> next; check(walker->GetFirstChildElement(node.element.Get(), &next));
                    if (next && node.depth >= 16) throw std::runtime_error("toast subtree depth incomplete");
                    while (next) { demand(pending.size() + nodes < 512); pending.push_back({next, node.depth + 1});
                        ComPtr<IUIAutomationElement> sibling; check(walker->GetNextSiblingElement(next.Get(), &sibling)); next = sibling; }
                }
            }
            ComPtr<IUIAutomationElement> next; check(walker->GetNextSiblingElement(child.Get(), &next)); child = next;
        }
        budget(); demand(rows.size() <= 1);
        const auto name = "toast-census-" + std::to_string(index) + ".json";
        report(name.c_str(), "{\"nonce\":" + jsonQuote(uuid) + ",\"pid\":" + std::to_string(GetCurrentProcessId())
            + ",\"nodes\":" + std::to_string(nodes) + ",\"roots\":" + std::to_string(roots)
            + ",\"complete\":true,\"exactTitles\":" + std::to_string(titles) + ",\"eligibleRows\":" + std::to_string(rows.size()) + "}\n");
        return rows;
    };
    for (unsigned attempt = 0; attempt < 40 && GetTickCount64() < deadline; ++attempt) {
        auto rows = census(attempt);
        if (!rows.empty()) {
            const auto selected = rows.begin()->second; auto fresh = census(40);
            demand(fresh.size() == 1 && fresh.begin()->second.runtime == selected.runtime
                && fresh.begin()->second.titleRuntime == selected.titleRuntime && fresh.begin()->second.pid == selected.pid
                && fresh.begin()->second.containerRuntime == selected.containerRuntime);
            BOOL same = FALSE; check(automation->CompareElements(selected.row.Get(), fresh.begin()->second.row.Get(), &same)); demand(same);
            auto owner = custody.owner(selected.pid); demand(owner && owner->live());
            FILETIME birth{}, exit{}, kernel{}, used{}; ULARGE_INTEGER ticks{};
            demand(GetProcessTimes(owner->process, &birth, &exit, &kernel, &used) != FALSE);
            ticks.LowPart = birth.dwLowDateTime; ticks.HighPart = birth.dwHighDateTime;
            demand(preflight("toast-default-immediate-preflight.json") && owner->live() && GetTickCount64() < deadline);
            // Re-read the exact title/nearest row and retained kernel authority after desktop preflight.
            auto immediate = [&] {
                demand(owner->live() && GetProcessId(owner->process) == static_cast<DWORD>(selected.pid));
                DWORD nowSession{}, length = 32768; wchar_t image[32768]{};
                demand(ProcessIdToSessionId(selected.pid, &nowSession) && nowSession == session
                    && QueryFullProcessImageNameW(owner->process, 0, image, &length));
                demand(shellPath(owner, selected.pid) == heldPaths.at(selected.pid));
                const auto nowUser = tokenUser(owner->process);
                demand(EqualSid(reinterpret_cast<const TOKEN_USER*>(nowUser.data())->User.Sid,
                    reinterpret_cast<const TOKEN_USER*>(user.data())->User.Sid) != FALSE);
                FILETIME nowBirth{}; demand(GetProcessTimes(owner->process, &nowBirth, &exit, &kernel, &used) != FALSE
                    && CompareFileTime(&nowBirth, &birth) == 0);
                auto& current = fresh.begin()->second; int pid{}; CONTROLTYPEID type{}; BOOL offscreen = TRUE, enabled = FALSE;
                BSTR name = nullptr; check(current.title->get_CurrentName(&name));
                const bool exact = name && std::wstring(name, SysStringLen(name)) == L"Navigation TEST " + uuid;
                SysFreeString(name); demand(exact); check(current.title->get_CurrentControlType(&type)); demand(type == UIA_TextControlTypeId);
                check(current.title->get_CurrentProcessId(&pid)); demand(pid == selected.pid);
                check(current.title->get_CurrentIsOffscreen(&offscreen)); demand(!offscreen);
                ComPtr<IUIAutomationElement> parent = current.title; bool nearest = false;
                for (unsigned depth = 0; depth < 8; ++depth) { ComPtr<IUIAutomationElement> next;
                    check(walker->GetParentElement(parent.Get(), &next)); demand(next != nullptr);
                    check(next->get_CurrentProcessId(&pid)); demand(pid == selected.pid);
                    check(next->get_CurrentControlType(&type)); parent = next;
                    if (type == UIA_ListItemControlTypeId) { check(automation->CompareElements(next.Get(), current.row.Get(), &same)); nearest = same; break; } }
                demand(nearest && toastRuntime(current.title.Get()) == selected.titleRuntime && toastRuntime(current.row.Get()) == selected.runtime);
                check(current.row->get_CurrentProcessId(&pid)); demand(pid == selected.pid);
                check(current.row->get_CurrentIsOffscreen(&offscreen)); check(current.row->get_CurrentIsEnabled(&enabled));
                demand(!offscreen && enabled && owner->live() && GetTickCount64() < deadline);
            };
            immediate(); ComPtr<IUIAutomationInvokePattern> pattern;
            check(fresh.begin()->second.row->GetCurrentPatternAs(UIA_InvokePatternId, IID_PPV_ARGS(&pattern)));
            const std::string binding = "\"nonce\":" + jsonQuote(uuid) + ",\"pid\":" + std::to_string(GetCurrentProcessId())
                + ",\"selectionKind\":\"toast_default\",\"controlType\":50007,\"providerPID\":" + std::to_string(selected.pid)
                + ",\"providerCreationTicks\":\"" + std::to_string(ticks.QuadPart) + "\",\"providerImagePathKind\":"
                + jsonQuote(winrt::to_hstring(shellPaths.at(heldPaths.at(selected.pid))).c_str()) + ",\"runtimeID\":" + selected.runtime
                + ",\"titleRuntimeID\":" + selected.titleRuntime + ",\"containerRuntimeID\":" + selected.containerRuntime + ",\"exactTitleVerified\":true,\"offscreen\":false,\"enabled\":true"
                + ",\"specSHA256\":\"" + oobeStateHash(spec.bytes) + "\"";
            oobeDurableIntent("ui-invoke-intent.json", "{" + binding + ",\"invokeBoundaryArmed\":true,\"invokeCallEntered\":false}\n");
            immediate();
            // Exactly one public Shell UI Invoke. Missing terminal after arming is unknown, never replayable.
            HRESULT result = pattern->Invoke();
            report("ui-invoke.json", "{" + binding + ",\"invokeCallEntered\":true,\"invokeCallReturned\":true,\"invokeHRESULT\":" + std::to_string(result) + "}\n");
            check(result); return 0;
        }
        if (attempt == 10) {
            INPUT keys[4]{}; for (auto& key : keys) key.type = INPUT_KEYBOARD;
            keys[0].ki.wVk = VK_LWIN; keys[1].ki.wVk = 'N'; keys[2].ki.wVk = 'N'; keys[2].ki.dwFlags = KEYEVENTF_KEYUP;
            keys[3].ki.wVk = VK_LWIN; keys[3].ki.dwFlags = KEYEVENTF_KEYUP;
            report("center-open.json", "{\"keysAccepted\":" + std::to_string(SendInput(4, keys, sizeof(INPUT))) + "}\n");
        }
        Sleep(250);
    }
    report("ui-invoke.json", "{\"selectionKind\":\"toast_default\",\"found\":false}\n");
    snapshotOwnedShellUI(automation.Get()); return 5;
}
static int invoke() {
    if (fs::exists(root / "msix-spec.json")) return invokeToastDefault();
    ComPtr<IUIAutomation> automation;
    check(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)));
    VARIANT name{}; name.vt = VT_BSTR; name.bstrVal = SysAllocString(action.c_str());
    ComPtr<IUIAutomationCondition> named; check(automation->CreatePropertyCondition(UIA_NamePropertyId, name, &named));
    VariantClear(&name);
    VARIANT type{}; type.vt = VT_I4; type.lVal = UIA_ButtonControlTypeId;
    ComPtr<IUIAutomationCondition> button, condition;
    check(automation->CreatePropertyCondition(UIA_ControlTypePropertyId, type, &button));
    check(automation->CreateAndCondition(named.Get(), button.Get(), &condition));
    // Only a synthetic UUID-named action is eligible. No global settings or unrelated clicks.
    for (int attempt = 0; attempt < 40; ++attempt) {
        ComPtr<IUIAutomationElement> desktop, element; check(automation->GetRootElement(&desktop));
        check(desktop->FindFirst(TreeScope_Subtree, condition.Get(), &element));
        if (element) {
            int providerPid = 0; BOOL offscreen = TRUE;
            check(element->get_CurrentProcessId(&providerPid)); check(element->get_CurrentIsOffscreen(&offscreen));
            if (providerPid == static_cast<int>(GetCurrentProcessId()) || offscreen) throw std::runtime_error("action not visible native provider");
            HANDLE provider = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, providerPid);
            wchar_t image[32768]{}; DWORD length = 32768;
            bool imageKnown = provider && QueryFullProcessImageNameW(provider, 0, image, &length);
            if (provider) CloseHandle(provider);
            std::wstring base = fs::path(image).filename().wstring();
            wchar_t windows[32768]{};
            if (!GetWindowsDirectoryW(windows, 32768)) throw std::runtime_error("Windows directory unavailable");
            std::wstring imageLower = image, windowsLower = std::wstring(windows) + L"\\";
            std::transform(imageLower.begin(), imageLower.end(), imageLower.begin(), towlower);
            std::transform(windowsLower.begin(), windowsLower.end(), windowsLower.begin(), towlower);
            report("ui-candidate.json", "{\"actionName\":" + jsonQuote(action) + ",\"providerPID\":" + std::to_string(providerPid)
                + ",\"providerImage\":" + jsonQuote(image) + ",\"offscreen\":" + (offscreen ? "true" : "false") + "}\n");
            if (!imageKnown || imageLower.compare(0, windowsLower.size(), windowsLower) != 0
                || (base != L"ShellExperienceHost.exe" && base != L"ShellHost.exe" && base != L"explorer.exe"))
                throw std::runtime_error("action provider is not Windows Shell");
            // Find the exact TEST title in the nearest same-provider ancestor, never at the desktop root.
            std::wstring title = L"Navigation TEST " + uuid;
            VARIANT titleValue{}; titleValue.vt = VT_BSTR; titleValue.bstrVal = SysAllocString(title.c_str());
            ComPtr<IUIAutomationCondition> titleCondition;
            check(automation->CreatePropertyCondition(UIA_NamePropertyId, titleValue, &titleCondition));
            VariantClear(&titleValue);
            ComPtr<IUIAutomationTreeWalker> walker; check(automation->get_ControlViewWalker(&walker));
            ComPtr<IUIAutomationElement> parent = element; bool titleVerified = false;
            for (int depth = 0; depth < 8; ++depth) {
                ComPtr<IUIAutomationElement> next, matchingTitle;
                check(walker->GetParentElement(parent.Get(), &next));
                if (!next) break;
                int ancestorPid = 0; check(next->get_CurrentProcessId(&ancestorPid));
                if (ancestorPid != providerPid) break;
                check(next->FindFirst(TreeScope_Descendants, titleCondition.Get(), &matchingTitle));
                if (matchingTitle) { titleVerified = true; break; }
                parent = next;
            }
            if (!titleVerified) throw std::runtime_error("exact TEST toast title missing from action ancestry");
            ComPtr<IUIAutomationInvokePattern> pattern;
            check(element->GetCurrentPatternAs(UIA_InvokePatternId, IID_PPV_ARGS(&pattern)));
            SAFEARRAY* runtimeId = nullptr; check(element->GetRuntimeId(&runtimeId));
            std::string runtime = "["; LONG lower = 0, upper = -1;
            if (runtimeId && SafeArrayGetDim(runtimeId) == 1) {
                check(SafeArrayGetLBound(runtimeId, 1, &lower)); check(SafeArrayGetUBound(runtimeId, 1, &upper));
                if (upper - lower > 63) { SafeArrayDestroy(runtimeId); throw std::runtime_error("UI runtime ID oversized"); }
                for (LONG index = lower; index <= upper; ++index) {
                    int part = 0; check(SafeArrayGetElement(runtimeId, &index, &part));
                    if (index != lower) runtime += ','; runtime += std::to_string(part);
                }
            }
            if (runtimeId) SafeArrayDestroy(runtimeId); runtime += ']';
            // This invokes the Shell UI provider, never INotificationActivationCallback directly.
            HRESULT result = pattern->Invoke();
            report("ui-invoke.json", "{\"actionName\":" + jsonQuote(action) + ",\"providerPID\":" + std::to_string(providerPid)
                + ",\"providerImage\":" + jsonQuote(image) + ",\"toastTitle\":" + jsonQuote(title)
                + ",\"runtimeID\":" + runtime
                + ",\"exactTitleVerified\":true,\"controlType\":50000,\"offscreen\":false,\"invokeHRESULT\":" + std::to_string(result) + "}\n");
            check(result); return 0;
        }
        if (attempt == 10) {
            // Open the notification center on the disposable CI desktop; no toast is sent again.
            INPUT keys[4]{};
            for (auto& key : keys) key.type = INPUT_KEYBOARD;
            keys[0].ki.wVk = VK_LWIN; keys[1].ki.wVk = 'N';
            keys[2].ki.wVk = 'N'; keys[2].ki.dwFlags = KEYEVENTF_KEYUP;
            keys[3].ki.wVk = VK_LWIN; keys[3].ki.dwFlags = KEYEVENTF_KEYUP;
            UINT sent = SendInput(4, keys, sizeof(INPUT));
            report("center-open.json", "{\"keysAccepted\":" + std::to_string(sent) + "}\n");
        }
        Sleep(250);
    }
    report("ui-invoke.json", "{\"actionName\":" + jsonQuote(action) + ",\"found\":false}\n");
    snapshotOwnedShellUI(automation.Get());
    return 5;
}
static void cleanup() {
    // UUID marker + exact paths constrain removal; no process killing or shared registrations.
    bool registryOwned = ownRegistryProof(), shortcutOwned = ownShortcutProof(), appIdentityOwned = ownAppIdentityProof();
    // Check every owned artifact before any cleanup side effect.
    if (appIdentityOwned && (!appIdentityMatches(true) || !appIdentityContainsOnlyOwnValues()))
        throw std::runtime_error("AUMID identity ownership changed; preserved");
    if (registryOwned) {
        std::wstring registered = registeredCommand();
        if (!registered.empty() && registered != serverCommand()) throw std::runtime_error("COM ownership changed; preserved");
    }
    if (shortcutOwned && fs::exists(shortcut()) && !shortcutMatches())
        throw std::runtime_error("shortcut ownership changed; preserved");
    // The identity still exists while history is addressed. Retain a Clear failure,
    // but finish proven-owned deletions so that it cannot leak TEST registrations.
    std::exception_ptr historyFailure;
    bool historyAttempted = registryOwned || shortcutOwned || appIdentityOwned;
    if (historyAttempted) {
        try { ToastNotificationManager::History().Clear(aumid); }
        catch (...) { historyFailure = std::current_exception(); }
    }
    if (appIdentityOwned) {
        if (!appIdentityMatches(true) || !appIdentityContainsOnlyOwnValues())
            throw std::runtime_error("AUMID identity ownership changed; preserved");
        LSTATUS status = RegDeleteTreeW(HKEY_CURRENT_USER, appIdentityKey().c_str());
        if (status != ERROR_SUCCESS && status != ERROR_FILE_NOT_FOUND) check(HRESULT_FROM_WIN32(status));
        if (!appIdentityAbsent()) throw std::runtime_error("owned AUMID registry key remains");
    }
    if (registryOwned) {
        std::wstring registered = registeredCommand();
        if (!registered.empty() && registered != serverCommand()) throw std::runtime_error("COM ownership changed; preserved");
        LSTATUS status = RegDeleteTreeW(HKEY_CURRENT_USER, registryKey().c_str());
        if (status != ERROR_SUCCESS && status != ERROR_FILE_NOT_FOUND) check(HRESULT_FROM_WIN32(status));
    }
    if (shortcutOwned && fs::exists(shortcut())) {
        if (!shortcutMatches()) throw std::runtime_error("shortcut ownership changed; preserved");
        std::error_code ec; fs::remove(shortcut(), ec);
        if (ec) throw std::runtime_error("own shortcut cleanup failed");
    }
    report("cleanup.json", "{\"ownRegistrationRemoved\":" + std::string(registryOwned ? "true" : "false")
        + ",\"ownShortcutRemoved\":" + (shortcutOwned ? "true" : "false")
        + ",\"ownAumidIdentityRemoved\":" + (appIdentityOwned ? "true" : "false")
        + ",\"historyClearAttempted\":" + (historyAttempted ? "true" : "false")
        + ",\"historyClearReturned\":" + (historyAttempted && !historyFailure ? "true" : "false") + "}\n");
    if (historyFailure) std::rethrow_exception(historyFailure);
}
// Store-signed vendor bytes are checked by the controller first. This inspects
// package identity through the public SDK, without installation or activation.
static void packageMetadata() {
    const fs::path package = root / L"client.msix";
    if (!fs::is_regular_file(package) || fs::canonical(package) != package || fs::file_size(package) > 1024ULL * 1024 * 1024)
        throw std::runtime_error("package input unavailable or outside bound");
    ComPtr<IStream> stream;
    check(SHCreateStreamOnFileEx(package.c_str(), STGM_READ | STGM_SHARE_DENY_WRITE, 0, FALSE, nullptr, &stream));
    ComPtr<IAppxFactory> factory;
    check(CoCreateInstance(CLSID_AppxFactory, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&factory)));
    ComPtr<IAppxPackageReader> reader; check(factory->CreatePackageReader(stream.Get(), &reader));
    ComPtr<IAppxManifestReader> manifest; check(reader->GetManifest(&manifest));
    ComPtr<IAppxManifestPackageId> identity; check(manifest->GetPackageId(&identity));
    auto stringValue = [&](auto operation) {
        LPWSTR value = nullptr; const HRESULT result = operation(&value);
        std::unique_ptr<wchar_t, decltype(&CoTaskMemFree)> owned(value, &CoTaskMemFree);
        check(result);
        if (!value || wcsnlen_s(value, 513) > 512) throw std::runtime_error("package string unavailable or oversized");
        return jsonQuote(value);
    };
    const auto name = stringValue([&](LPWSTR* p) { return identity->GetName(p); });
    const auto publisher = stringValue([&](LPWSTR* p) { return identity->GetPublisher(p); });
    const auto family = stringValue([&](LPWSTR* p) { return identity->GetPackageFamilyName(p); });
    const auto full = stringValue([&](LPWSTR* p) { return identity->GetPackageFullName(p); });
    UINT64 version = 0; APPX_PACKAGE_ARCHITECTURE architecture{};
    check(identity->GetVersion(&version)); check(identity->GetArchitecture(&architecture));
    ComPtr<IStream> xml; check(manifest->GetStream(&xml));
    STATSTG info{}; check(xml->Stat(&info, STATFLAG_NONAME));
    if (!info.cbSize.QuadPart || info.cbSize.QuadPart > 2 * 1024 * 1024) throw std::runtime_error("manifest size outside bound");
    std::string bytes(static_cast<size_t>(info.cbSize.QuadPart), '\0');
    LARGE_INTEGER start{}; check(xml->Seek(start, STREAM_SEEK_SET, nullptr));
    ULONG read = 0; check(xml->Read(bytes.data(), static_cast<ULONG>(bytes.size()), &read));
    if (read != bytes.size()) throw std::runtime_error("manifest read incomplete");
    report("vendor-manifest.xml", bytes);
    const auto versionText = std::to_string((version >> 48) & 65535) + '.' + std::to_string((version >> 32) & 65535)
        + '.' + std::to_string((version >> 16) & 65535) + '.' + std::to_string(version & 65535);
    report("package-metadata.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"readOnly\":true,\"installed\":false,\"launchAttempted\":false,\"showAttempts\":0"
        + ",\"name\":" + name + ",\"publisher\":" + publisher + ",\"familyName\":" + family
        + ",\"fullName\":" + full + ",\"architecture\":" + std::to_string(architecture)
        + ",\"version\":" + jsonQuote(winrt::to_hstring(versionText).c_str()) + "}\n");
}
// Separate disposable vendor lane; no toast/UI/COM callback composition.
static constexpr auto vendorName = L"OpenAI.Codex";
static constexpr auto vendorPublisher = L"CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B";
static constexpr auto vendorFamily = L"OpenAI.Codex_2p2nqsd0c76g0";
static constexpr auto vendorFull = L"OpenAI.Codex_26.930.7945.0_arm64__2p2nqsd0c76g0";
static bool vendorAPIEntered = false;
static ULONGLONG vendorDeadline = 0;
static void vendorRemaining() { if (GetTickCount64() >= vendorDeadline) throw std::runtime_error("vendor overall deadline"); }
static std::string vendorPhase = "guard";
static std::string vendorRecord(const std::string& fields) {
    return "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"familyName\":" + jsonQuote(vendorFamily) + ",\"fullName\":" + jsonQuote(vendorFull)
        + ",\"toastCallbackQualified\":false,\"targetConfirmed\":false,\"showAttempts\":0," + fields + "}\n";
}
static std::string vendorProof(const char* name) {
    const fs::path path = root / name;
    if (!fs::is_regular_file(path) || fs::canonical(path) != path || fs::file_size(path) > 1024)
        throw std::runtime_error("bounded owned vendor proof required");
    std::ifstream stream(path, std::ios::binary);
    std::string text((std::istreambuf_iterator<char>(stream)), {});
    if (!stream.eof() && stream.fail()) throw std::runtime_error("vendor proof unreadable");
    return text;
}
struct VendorHashFile {
    HANDLE file = INVALID_HANDLE_VALUE;
    std::string digest;
    explicit VendorHashFile(const fs::path& path, ULONGLONG cap) {
        if (fs::canonical(path) != path) throw std::runtime_error("vendor input link refused");
        file = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING,
            FILE_FLAG_SEQUENTIAL_SCAN | FILE_FLAG_OPEN_REPARSE_POINT, nullptr);
        if (file == INVALID_HANDLE_VALUE) throw std::runtime_error("vendor custody open failed");
        BCRYPT_ALG_HANDLE algorithm{}; BCRYPT_HASH_HANDLE hash{};
        try {
            FILE_ATTRIBUTE_TAG_INFO attributes{}; LARGE_INTEGER size{};
            if (!GetFileInformationByHandleEx(file, FileAttributeTagInfo, &attributes, sizeof(attributes))
                || (attributes.FileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) || !GetFileSizeEx(file, &size)
                || size.QuadPart <= 0 || static_cast<ULONGLONG>(size.QuadPart) > cap)
                throw std::runtime_error("vendor custody input invalid");
            auto ok = [](NTSTATUS status) { if (status < 0) throw std::runtime_error("vendor SHA256 failed"); };
            ok(BCryptOpenAlgorithmProvider(&algorithm, BCRYPT_SHA256_ALGORITHM, nullptr, 0));
            ok(BCryptCreateHash(algorithm, &hash, nullptr, 0, nullptr, 0, 0));
            BYTE buffer[65536]{}, result[32]{}; DWORD count{}; ULONGLONG total = 0;
            const ULONGLONG deadline = std::min(vendorDeadline, GetTickCount64() + 120000);
            while (true) {
                if (GetTickCount64() >= deadline) throw std::runtime_error("vendor hash deadline");
                if (!ReadFile(file, buffer, sizeof(buffer), &count, nullptr)) throw std::runtime_error("vendor hash read failed");
                if (!count) break;
                total += count; if (total > cap) throw std::runtime_error("vendor hash bound exceeded");
                ok(BCryptHashData(hash, buffer, count, 0));
            }
            if (total != static_cast<ULONGLONG>(size.QuadPart)) throw std::runtime_error("vendor hash size changed");
            ok(BCryptFinishHash(hash, result, sizeof(result), 0));
            constexpr char hex[] = "0123456789abcdef";
            for (BYTE byte : result) { digest += hex[byte >> 4]; digest += hex[byte & 15]; }
            BCryptDestroyHash(hash); BCryptCloseAlgorithmProvider(algorithm, 0);
        } catch (...) {
            if (hash) BCryptDestroyHash(hash);
            if (algorithm) BCryptCloseAlgorithmProvider(algorithm, 0);
            CloseHandle(file); file = INVALID_HANDLE_VALUE; throw;
        }
    }
    ~VendorHashFile() { if (file != INVALID_HANDLE_VALUE) CloseHandle(file); }
    VendorHashFile(const VendorHashFile&) = delete;
};
// Public GDI/WIC only, one read-only primary-desktop snapshot in a fresh CI job.
static bool desktopCapture() {
    auto env = [](const wchar_t* name) {
        wchar_t value[128]{}; DWORD size = GetEnvironmentVariableW(name, value, 128);
        if (!size || size >= 128) throw std::runtime_error("capture authority absent");
        return std::wstring(value, size);
    };
    if (env(L"NAVIGATION_WINDOWS_DESKTOP_CAPTURE_TEST") != L"1"
        || env(L"GITHUB_REPOSITORY") != L"777genius/agent-notifications"
        || env(L"GITHUB_EVENT_NAME") != L"workflow_dispatch" || env(L"GITHUB_RUN_ATTEMPT") != L"1"
        || env(L"NAVIGATION_WINDOWS_RUNNER") != L"windows-11-vs2026-arm")
        throw std::runtime_error("fresh manual CI capture required");
    const auto source = env(L"NAVIGATION_SOURCE_SHA");
    if (source.size() != 40 || source.find_first_not_of(L"0123456789abcdef") != std::wstring::npos)
        throw std::runtime_error("capture source SHA required");
    const ULONGLONG started = GetTickCount64(), deadline = started + 5000;
    Token captureToken; require(OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &captureToken.handle) != FALSE, "CaptureOwnToken");
    const auto tokenBefore = facts(captureToken.handle);
    FILETIME created{}, exited{}, kernel{}, user{};
    require(GetProcessTimes(GetCurrentProcess(), &created, &exited, &kernel, &user) != FALSE, "CaptureOwnBirth");
    ULARGE_INTEGER birth{}; birth.LowPart = created.dwLowDateTime; birth.HighPart = created.dwHighDateTime;
    auto defaultDesktop = []() {
        HDESK input = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS);
        const bool valid = input && objectName(input) == L"Default" && objectName(GetThreadDesktop(GetCurrentThreadId())) == L"Default";
        if (input) CloseDesktop(input);
        if (!valid) throw std::runtime_error("capture requires current Default input desktop");
    };
    defaultDesktop();
    auto budget = [&]() { if (GetTickCount64() >= deadline) throw std::runtime_error("capture cooperative deadline"); };
    USHORT processMachine = 0, nativeMachine = 0;
    if (!IsWow64Process2(GetCurrentProcess(), &processMachine, &nativeMachine)
        || processMachine != IMAGE_FILE_MACHINE_UNKNOWN || nativeMachine != IMAGE_FILE_MACHINE_ARM64)
        throw std::runtime_error("capture requires native ARM64 process");
    vendorDeadline = deadline; VendorHashFile executable(root / L"navigation-native-probe.exe", 67108864);
    struct DPIContext {
        DPI_AWARENESS_CONTEXT previous = SetThreadDpiAwarenessContext(DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2);
        ~DPIContext() { if (previous) SetThreadDpiAwarenessContext(previous); }
    } dpi;
    auto dpiAware = []() { return AreDpiAwarenessContextsEqual(GetThreadDpiAwarenessContext(),
        DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2) != FALSE; };
    if (!dpi.previous || !dpiAware()) throw std::runtime_error("capture physical coordinate context unavailable");
    auto primaryRect = []() {
        const HMONITOR monitor = MonitorFromPoint(POINT{0, 0}, MONITOR_DEFAULTTOPRIMARY);
        MONITORINFO info{}; info.cbSize = sizeof(info);
        if (!monitor || !GetMonitorInfoW(monitor, &info) || !(info.dwFlags & MONITORINFOF_PRIMARY))
            throw std::runtime_error("capture primary monitor unavailable");
        return std::pair<HMONITOR, RECT>{monitor, info.rcMonitor};
    };
    const auto primary = primaryRect(); const RECT rectangle = primary.second;
    if (!preflight("capture-preflight.json")) return false;
    SurfaceScan scan; wchar_t windows[32768]{}; UINT length = GetWindowsDirectoryW(windows, 32768);
    if (!length || length >= 32768 || !ProcessIdToSessionId(GetCurrentProcessId(), &scan.session))
        throw std::runtime_error("capture desktop identity unavailable");
    scan.windows.assign(windows, length); scan.user = tokenUser(GetCurrentProcess());
    std::transform(scan.windows.begin(), scan.windows.end(), scan.windows.begin(), [](wchar_t c) { return std::towlower(c); });
    scan.imageNames.insert(scan.imageNames.end(), {L"wwahost.exe", L"startmenuexperiencehost.exe",
        L"systempropertiesperformance.exe", L"systempropertiesadvanced.exe", L"useroobebroker.exe",
        L"cloudexperiencehost.exe", L"applicationframehost.exe"});
    scan.exportImageLeaf = true; scan.projection = "visible_or_foreground_same_user_windows_oobe_or_shell";
    DWORD shellPID = 0; const HWND shellWindow = GetShellWindow(); GetWindowThreadProcessId(shellWindow, &shellPID);
    auto shell = shellPID ? scan.owner(shellPID) : nullptr;
    if (!shell || !shell->live()) throw std::runtime_error("capture held Shell unavailable");
    const auto metadata = scan.snapshot(); budget();
    const std::int64_t wide = static_cast<std::int64_t>(rectangle.right) - rectangle.left;
    const std::int64_t high = static_cast<std::int64_t>(rectangle.bottom) - rectangle.top;
    if (wide <= 0 || high <= 0 || wide > 2048 || high > 2048)
        throw std::runtime_error("capture primary screen dimension outside bound");
    const int width = static_cast<int>(wide), height = static_cast<int>(high);
    report("desktop-capture-intent.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"sourceSHA\":" + jsonQuote(source) + ",\"captureAttempts\":1,\"showAttempts\":0,\"inputAttempted\":false}\n");
    struct GDI {
        HDC screen = nullptr, memory = nullptr; HBITMAP bitmap = nullptr; HGDIOBJ old = nullptr;
        ~GDI() { if (old && old != HGDI_ERROR) SelectObject(memory, old); if (bitmap) DeleteObject(bitmap);
            if (memory) DeleteDC(memory); if (screen) ReleaseDC(nullptr, screen); }
    } gdi;
    gdi.screen = GetDC(nullptr); if (!gdi.screen) throw std::runtime_error("screen DC unavailable");
    gdi.memory = CreateCompatibleDC(gdi.screen); if (!gdi.memory) throw std::runtime_error("capture DC unavailable");
    for (HDC dc : {gdi.screen, gdi.memory}) {
        POINT window{}, viewport{};
        if (GetMapMode(dc) != MM_TEXT || !GetWindowOrgEx(dc, &window) || !GetViewportOrgEx(dc, &viewport)
            || window.x || window.y || viewport.x || viewport.y || GetGraphicsMode(dc) != GM_COMPATIBLE)
            throw std::runtime_error("capture pixel DC mapping unavailable");
    }
    gdi.bitmap = CreateCompatibleBitmap(gdi.screen, width, height); if (!gdi.bitmap) throw std::runtime_error("capture bitmap unavailable");
    gdi.old = SelectObject(gdi.memory, gdi.bitmap); if (!gdi.old || gdi.old == HGDI_ERROR) throw std::runtime_error("capture bitmap selection failed");
    budget();
    if (!BitBlt(gdi.memory, 0, 0, width, height, gdi.screen, rectangle.left, rectangle.top, SRCCOPY | CAPTUREBLT))
        throw std::runtime_error("capture BitBlt failed");
    if (!GdiFlush()) throw std::runtime_error("capture GDI flush failed");
    if (SelectObject(gdi.memory, gdi.old) != gdi.bitmap) throw std::runtime_error("capture bitmap deselection failed");
    gdi.old = nullptr; budget();
    ComPtr<IWICImagingFactory> factory; check(CoCreateInstance(CLSID_WICImagingFactory, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&factory)));
    ComPtr<IWICBitmap> bitmap; check(factory->CreateBitmapFromHBITMAP(gdi.bitmap, nullptr, WICBitmapIgnoreAlpha, &bitmap));
    ComPtr<IWICFormatConverter> converter; check(factory->CreateFormatConverter(&converter));
    check(converter->Initialize(bitmap.Get(), GUID_WICPixelFormat24bppBGR, WICBitmapDitherTypeNone, nullptr, 0, WICBitmapPaletteTypeCustom));
    ComPtr<IStream> stream; check(CreateStreamOnHGlobal(nullptr, TRUE, &stream));
    ComPtr<IWICBitmapEncoder> encoder; check(factory->CreateEncoder(GUID_ContainerFormatPng, nullptr, &encoder));
    check(encoder->Initialize(stream.Get(), WICBitmapEncoderNoCache));
    ComPtr<IWICBitmapFrameEncode> frame; check(encoder->CreateNewFrame(&frame, nullptr)); check(frame->Initialize(nullptr));
    check(frame->SetSize(width, height)); WICPixelFormatGUID format = GUID_WICPixelFormat24bppBGR;
    check(frame->SetPixelFormat(&format)); if (!IsEqualGUID(format, GUID_WICPixelFormat24bppBGR)) throw std::runtime_error("capture PNG pixel format mismatch");
    check(frame->WriteSource(converter.Get(), nullptr)); check(frame->Commit()); check(encoder->Commit()); budget();
    STATSTG st{}; check(stream->Stat(&st, STATFLAG_NONAME));
    if (st.cbSize.QuadPart < 33 || st.cbSize.QuadPart > 8388608) throw std::runtime_error("capture encoded PNG outside 8MiB bound");
    LARGE_INTEGER zero{}; check(stream->Seek(zero, STREAM_SEEK_SET, nullptr));
    std::string png(static_cast<size_t>(st.cbSize.QuadPart), '\0'); ULONG read = 0;
    check(stream->Read(png.data(), static_cast<ULONG>(png.size()), &read));
    if (read != png.size()) throw std::runtime_error("capture PNG incomplete");
    const auto afterPrimary = primaryRect(); const RECT afterRect = afterPrimary.second;
    if (!shell->live() || GetShellWindow() != shellWindow || !dpiAware() || afterPrimary.first != primary.first
        || afterRect.left != rectangle.left || afterRect.top != rectangle.top
        || afterRect.right != rectangle.right || afterRect.bottom != rectangle.bottom
        || !preflight("capture-after-preflight.json"))
        throw std::runtime_error("capture desktop changed across snapshot");
    budget();
    // Final filename is published only after complete bounded bytes and disk flush.
    const auto temporary = root / L"desktop-capture.png.tmp", final = root / L"desktop-capture.png";
    HANDLE file = CreateFileW(temporary.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) throw std::runtime_error("exclusive PNG creation failed");
    DWORD written = 0; bool saved = WriteFile(file, png.data(), static_cast<DWORD>(png.size()), &written, nullptr) && written == png.size();
    const bool flushed = FlushFileBuffers(file) != FALSE; CloseHandle(file);
    if (!saved || !flushed || !MoveFileExW(temporary.c_str(), final.c_str(), MOVEFILE_WRITE_THROUGH)) {
        DeleteFileW(temporary.c_str()); throw std::runtime_error("atomic PNG publication failed");
    }
    vendorDeadline = deadline; VendorHashFile image(final, 8388608); budget();
    defaultDesktop(); const auto tokenAfter = facts(captureToken.handle);
    if (factsJson(tokenBefore) != factsJson(tokenAfter)) throw std::runtime_error("capture token changed");
    report("desktop-capture.json", "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"birth\":\"" + std::to_string(birth.QuadPart) + "\",\"startedBootMs\":" + std::to_string(started)
        + ",\"endBootMs\":" + std::to_string(GetTickCount64()) + ",\"tokenBefore\":" + factsJson(tokenBefore) + ",\"tokenAfter\":" + factsJson(tokenAfter)
        + ",\"sourceSHA\":" + jsonQuote(source) + ",\"diagnosticOnly\":true,\"readOnly\":true,\"captureAttempts\":1"
        + ",\"binarySHA256\":\"" + executable.digest + "\",\"architecture\":\"ARM64\""
        + ",\"showAttempts\":0,\"inputAttempted\":false,\"launchAttempted\":false,\"installAttempted\":false"
        + ",\"centerOpenedProved\":false,\"navigationQualified\":false,\"nativeCallbackQualified\":false,\"retryAllowed\":false"
        + ",\"pixelFile\":\"desktop-capture.png\",\"pixelSHA256\":\"" + image.digest + "\",\"pixelBytes\":" + std::to_string(png.size())
        + ",\"width\":" + std::to_string(width) + ",\"height\":" + std::to_string(height) + ",\"session\":" + std::to_string(scan.session)
        + ",\"elapsedMs\":" + std::to_string(GetTickCount64() - started) + ",\"shellStillLive\":true"
        + ",\"originX\":" + std::to_string(rectangle.left) + ",\"originY\":" + std::to_string(rectangle.top)
        + ",\"coordinateContract\":\"per_monitor_aware_v2_MM_TEXT\",\"primaryMonitorStable\":true"
        + ",\"captureScope\":\"primary_monitor_physical_GDI_pixels_non_atomic_metadata\",\"metadata\":" + metadata + "}\n");
    return true;
}
// Source-bound disposable guest setup state; never used by default diagnostic modes.
static ULONGLONG oobeSetupDeadline = 0;
static unsigned oobeSetupInvokeEntered = 0;
static bool oobeExactSystemImage(SurfaceOwner* peer, const std::wstring& expected) {
    if (!peer || !peer->live() || expected.empty()) return false;
    wchar_t image[32768]{}; DWORD size = 32768;
    if (!QueryFullProcessImageNameW(peer->process, 0, image, &size) || !size || size >= 32768) return false;
    try {
        auto actual = fs::canonical(fs::path(std::wstring(image, size))).wstring();
        std::transform(actual.begin(), actual.end(), actual.begin(), [](wchar_t c) { return std::towlower(c); });
        return actual == expected && peer->live();
    } catch (...) { return false; }
}
struct OOBESelection {
    SurfaceScan owners;
    HWND window{}; DWORD pid{}; std::string created, state, stateSHA;
    ComPtr<IUIAutomationElement> element, pane, button;
    ComPtr<IUIAutomation> automation; ComPtr<IUIAutomationTreeWalker> walker;
    std::wstring buttonName, exactSystemImage;
    bool eligible = false, unambiguous = false;
    bool stable() const {
        auto found = owners.owners.find(pid); DWORD current{}; wchar_t name[121]{};
        if (found == owners.owners.end() || !oobeExactSystemImage(found->second.get(), exactSystemImage)
            || GetProcessId(found->second->process) != pid
            || GetForegroundWindow() != window || !IsWindowVisible(window)
            || !GetWindowThreadProcessId(window, &current) || current != pid
            || GetClassNameW(window, name, 121) != 26 || std::wstring(name) != L"Windows.UI.Core.CoreWindow") return false;
        FILETIME birth{}, exit{}, kernel{}, user{}; ULARGE_INTEGER ticks{};
        if (!GetProcessTimes(found->second->process, &birth, &exit, &kernel, &user)) return false;
        ticks.LowPart = birth.dwLowDateTime; ticks.HighPart = birth.dwHighDateTime;
        return std::to_string(ticks.QuadPart) == created;
    }
};
static std::string oobeStateHash(const std::string& state) {
    BCRYPT_ALG_HANDLE algorithm{}; BCRYPT_HASH_HANDLE hash{}; BYTE result[32]{}; std::string digest;
    auto ok = [](NTSTATUS status) { if (status < 0) throw std::runtime_error("OOBE state SHA256 failed"); };
    try {
        ok(BCryptOpenAlgorithmProvider(&algorithm, BCRYPT_SHA256_ALGORITHM, nullptr, 0));
        ok(BCryptCreateHash(algorithm, &hash, nullptr, 0, nullptr, 0, 0));
        ok(BCryptHashData(hash, reinterpret_cast<PUCHAR>(const_cast<char*>(state.data())), static_cast<ULONG>(state.size()), 0));
        ok(BCryptFinishHash(hash, result, sizeof(result), 0));
        for (BYTE byte : result) { constexpr char hex[] = "0123456789abcdef"; digest += hex[byte >> 4]; digest += hex[byte & 15]; }
        BCryptDestroyHash(hash); BCryptCloseAlgorithmProvider(algorithm, 0); return digest;
    } catch (...) { if (hash) BCryptDestroyHash(hash); if (algorithm) BCryptCloseAlgorithmProvider(algorithm, 0); throw; }
}
static void oobeDurableIntent(const char* name, const std::string& content) {
    report(name, content);
    HANDLE file = CreateFileW((root / name).c_str(), GENERIC_WRITE, FILE_SHARE_READ, nullptr, OPEN_EXISTING,
        FILE_ATTRIBUTE_NORMAL | FILE_FLAG_OPEN_REPARSE_POINT, nullptr);
    if (file == INVALID_HANDLE_VALUE) throw std::runtime_error("OOBE intent flush open failed");
    const BOOL flushed = FlushFileBuffers(file); CloseHandle(file);
    if (!flushed) throw std::runtime_error("OOBE intent flush failed; no action");
}
static std::wstring oobeAuthority(bool setup) {
    auto env = [](const wchar_t* name) {
        wchar_t value[128]{}; DWORD length = GetEnvironmentVariableW(name, value, 128);
        if (!length || length >= 128) throw std::runtime_error("OOBE TEST authority absent");
        return std::wstring(value, length);
    };
    if (env(setup ? L"NAVIGATION_WINDOWS_OOBE_SETUP_TEST" : L"NAVIGATION_WINDOWS_OOBE_PREFLIGHT_TEST") != L"1"
        || env(L"GITHUB_REPOSITORY") != L"777genius/agent-notifications"
        || env(L"GITHUB_EVENT_NAME") != L"workflow_dispatch" || env(L"GITHUB_RUN_ATTEMPT") != L"1"
        || env(L"NAVIGATION_WINDOWS_RUNNER") != L"windows-11-vs2026-arm")
        throw std::runtime_error("fresh manual OOBE TEST required");
    const auto source = env(L"NAVIGATION_SOURCE_SHA"); USHORT processMachine{}, nativeMachine{};
    if (source.size() != 40 || source.find_first_not_of(L"0123456789abcdef") != std::wstring::npos
        || !IsWow64Process2(GetCurrentProcess(), &processMachine, &nativeMachine)
        || processMachine != IMAGE_FILE_MACHINE_UNKNOWN || nativeMachine != IMAGE_FILE_MACHINE_ARM64)
        throw std::runtime_error("OOBE exact source/native ARM64 required");
    return source;
}
// One selected foreground subtree, property reads only; never Invoke or UI input.
static bool oobePreflight(const std::string& prefix = "oobe", OOBESelection* selection = nullptr, ULONGLONG observationDeadline = 0) {
    const auto source = oobeAuthority(selection != nullptr);
    const ULONGLONG started = GetTickCount64(), deadline = std::min(std::min(started + 5000,
        oobeSetupDeadline ? oobeSetupDeadline : started + 5000), observationDeadline ? observationDeadline : started + 5000);
    vendorDeadline = deadline; VendorHashFile executable(root / L"navigation-native-probe.exe", 67108864);
    if (!preflight((prefix + "-preflight.json").c_str())) return false;
    SurfaceScan owners; owners.allowAnyWindowsImage = true; wchar_t windows[32768]{};
    UINT length = GetWindowsDirectoryW(windows, 32768);
    if (!length || length >= 32768 || !ProcessIdToSessionId(GetCurrentProcessId(), &owners.session))
        throw std::runtime_error("OOBE desktop identity unavailable");
    owners.windows.assign(windows, length); owners.user = tokenUser(GetCurrentProcess());
    std::transform(owners.windows.begin(), owners.windows.end(), owners.windows.begin(), [](wchar_t c) { return std::towlower(c); });
    const HWND window = GetForegroundWindow(); DWORD pid{}; GetWindowThreadProcessId(window, &pid);
    auto held = pid ? owners.owner(pid) : nullptr; wchar_t windowClass[121]{};
    const int classLength = window ? GetClassNameW(window, windowClass, 121) : 0;
    auto birth = [](SurfaceOwner* peer) {
        FILETIME created{}, exited{}, kernel{}, user{}; ULARGE_INTEGER ticks{};
        if (!peer || !peer->live() || !GetProcessTimes(peer->process, &created, &exited, &kernel, &user))
            throw std::runtime_error("OOBE kernel incarnation unavailable");
        ticks.LowPart = created.dwLowDateTime; ticks.HighPart = created.dwHighDateTime;
        return std::to_string(ticks.QuadPart);
    };
    if (!held || !held->live() || held->imageLeaf.empty() || held->imageLeaf.size() > 120 || classLength <= 0 || classLength >= 120)
        throw std::runtime_error("OOBE selected Windows foreground unavailable");
    const auto created = birth(held); const std::wstring selectedClass(windowClass, classLength);
    std::wstring exactSystemImage;
    if (selection) {
        exactSystemImage = fs::canonical(fs::path(owners.windows) / L"System32" / L"WWAHost.exe").wstring();
        std::transform(exactSystemImage.begin(), exactSystemImage.end(), exactSystemImage.begin(), [](wchar_t c) { return std::towlower(c); });
        if (!oobeExactSystemImage(held, exactSystemImage)) throw std::runtime_error("setup requires exact System32 WWAHost kernel image");
    }
    auto stable = [&]() {
        DWORD current{}; wchar_t name[121]{};
        return held->live() && GetProcessId(held->process) == pid && GetForegroundWindow() == window
            && IsWindow(window) && IsWindowVisible(window) && GetWindowThreadProcessId(window, &current)
            && current == pid && GetClassNameW(window, name, 121) == classLength
            && std::wstring(name) == selectedClass && birth(held) == created
            && (!selection || oobeExactSystemImage(held, exactSystemImage));
    };
    if (!stable()) throw std::runtime_error("OOBE foreground changed before census");
    report((prefix + "-intent.json").c_str(), "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"sourceSHA\":" + jsonQuote(source) + ",\"binarySHA256\":\"" + executable.digest + "\",\"architecture\":\"ARM64\""
        + ",\"foregroundPID\":" + std::to_string(pid)
        + ",\"foregroundCreatedUtcTicks\":\"" + created + "\",\"censusAttempts\":1,\"showAttempts\":0,\"inputAttempted\":false}\n");
    unsigned visited{}, count{}, errors{}, providerSkips{}; bool truncated{}, available{}, rootStable{}, completed{};
    HRESULT lastError = S_OK; std::string rows, semanticRows; unsigned panes{}, buttons{}, matchingButtons{}, semanticCount{}; bool rootRole{};
    ComPtr<IUIAutomationElement> rootElement, privacyPane, actionButton;
    ComPtr<IUIAutomation> rootAutomation; ComPtr<IUIAutomationTreeWalker> rootWalker; std::wstring buttonName;
    auto budget = [&]() { return GetTickCount64() < deadline; };
    auto call = [&](auto operation) {
        if (!budget()) { truncated = true; winrt::throw_hresult(E_ABORT); }
        HRESULT hr = operation(); if (!budget()) truncated = true;
        return hr;
    };
    auto observed = [&](HRESULT hr) { const bool valid = selection ? hr == S_OK : SUCCEEDED(hr);
        if (!valid) { ++errors; lastError = hr; } return valid; };
    auto text = [&](HRESULT hr, BSTR value, bool& clipped) {
        std::unique_ptr<OLECHAR, decltype(&SysFreeString)> owned(value, &SysFreeString);
        if (!observed(hr)) return std::string("null");
        const UINT size = value ? SysStringLen(value) : 0; UINT take = (std::min)(size, 120U); clipped = size > take;
        if (take && value[take - 1] >= 0xd800 && value[take - 1] <= 0xdbff) { --take; clipped = true; }
        try { return jsonQuote(value ? std::wstring(value, take) : L""); }
        catch (...) { ++errors; return std::string("null"); }
    };
    try {
        ComPtr<IUIAutomation> automation;
        check(call([&]() { return CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)); }));
        ComPtr<IUIAutomationElement> element;
        check(call([&]() { return automation->ElementFromHandle(window, &element); }));
        ComPtr<IUIAutomationTreeWalker> walker; check(call([&]() { return automation->get_RawViewWalker(&walker); }));
        auto rootBound = [&]() {
            int provider{}; UIA_HWND hwnd{};
            return element && observed(call([&]() { return element->get_CurrentProcessId(&provider); }))
                && provider == static_cast<int>(pid)
                && observed(call([&]() { return element->get_CurrentNativeWindowHandle(&hwnd); }))
                && reinterpret_cast<HWND>(hwnd) == window && stable() && budget();
        };
        if (!rootBound()) throw std::runtime_error("OOBE UIA foreground binding unavailable");
        available = true; rootElement = element; rootAutomation = automation; rootWalker = walker;
        struct Node { ComPtr<IUIAutomationElement> element; unsigned depth; int parent; bool privacy = false; int privacyParent = -1; };
        std::vector<Node> pending{{element, 0, -1}};
        while (!pending.empty() && budget() && visited < 512 && stable()) {
            Node node = std::move(pending.back()); pending.pop_back(); ++visited;
            int provider{}; const HRESULT pidHR = call([&]() { return node.element->get_CurrentProcessId(&provider); });
            if (!observed(pidHR) || provider <= 0) { ++providerSkips; continue; }
            auto peer = owners.owner(static_cast<DWORD>(provider));
            if (!peer || !peer->live() || peer->imageLeaf.empty() || peer->imageLeaf.size() > 120) { ++providerSkips; continue; }
            const auto providerBirth = birth(peer);
            BSTR name{}, id{}, cls{}; bool nameClipped{}, idClipped{}, classClipped{};
            const HRESULT nameHR = call([&]() { return node.element->get_CurrentName(&name); }); const auto nameJSON = text(nameHR, name, nameClipped);
            const HRESULT idHR = call([&]() { return node.element->get_CurrentAutomationId(&id); }); const auto idJSON = text(idHR, id, idClipped);
            const HRESULT clsHR = call([&]() { return node.element->get_CurrentClassName(&cls); }); const auto classJSON = text(clsHR, cls, classClipped);
            CONTROLTYPEID type{}; BOOL enabled{}, offscreen{}; RECT rectangle{};
            const HRESULT typeHR = call([&]() { return node.element->get_CurrentControlType(&type); }); observed(typeHR);
            const HRESULT enabledHR = call([&]() { return node.element->get_CurrentIsEnabled(&enabled); }); observed(enabledHR);
            const HRESULT offscreenHR = call([&]() { return node.element->get_CurrentIsOffscreen(&offscreen); }); observed(offscreenHR);
            const HRESULT rectHR = call([&]() { return node.element->get_CurrentBoundingRectangle(&rectangle); });
            bool rectValid = observed(rectHR) && rectangle.left <= rectangle.right && rectangle.top <= rectangle.bottom;
            for (LONG coordinate : {rectangle.left, rectangle.top, rectangle.right, rectangle.bottom})
                if (coordinate < -1048576 || coordinate > 1048576) rectValid = false;
            if (SUCCEEDED(rectHR) && !rectValid) ++errors;
            VARIANT pattern{}; const HRESULT patternHR = call([&]() {
                return node.element->GetCurrentPropertyValue(UIA_IsInvokePatternAvailablePropertyId, &pattern); });
            const bool patternValid = observed(patternHR) && pattern.vt == VT_BOOL;
            const bool invokeAvailable = patternValid && pattern.boolVal != VARIANT_FALSE; VariantClear(&pattern);
            if (SUCCEEDED(patternHR) && !patternValid) ++errors;
            int after{}; const HRESULT afterHR = call([&]() { return node.element->get_CurrentProcessId(&after); });
            if (!observed(afterHR) || after != provider || !peer->live() || birth(peer) != providerBirth || !stable() || !budget()) {
                ++errors; continue;
            }
            const auto row = "{\"index\":" + std::to_string(count) + ",\"parentIndex\":" + std::to_string(node.parent)
                + ",\"depth\":" + std::to_string(node.depth) + ",\"providerPID\":" + std::to_string(provider)
                + ",\"providerCreatedUtcTicks\":\"" + providerBirth + "\",\"verifiedImageLeaf\":" + jsonQuote(peer->imageLeaf)
                + ",\"elementName\":" + nameJSON + ",\"automationId\":" + idJSON + ",\"className\":" + classJSON
                + ",\"nameTruncated\":" + (nameClipped ? "true" : "false") + ",\"automationIdTruncated\":" + (idClipped ? "true" : "false")
                + ",\"classNameTruncated\":" + (classClipped ? "true" : "false")
                + ",\"controlType\":" + (SUCCEEDED(typeHR) ? std::to_string(type) : "null")
                + ",\"enabled\":" + (FAILED(enabledHR) ? "null" : enabled ? "true" : "false")
                + ",\"offscreen\":" + (FAILED(offscreenHR) ? "null" : offscreen ? "true" : "false")
                + ",\"invokePatternAvailable\":" + (!patternValid ? "null" : invokeAvailable ? "true" : "false")
                + ",\"rectangle\":" + (!rectValid ? "null" : "[" + std::to_string(rectangle.left) + ',' + std::to_string(rectangle.top)
                    + ',' + std::to_string(rectangle.right) + ',' + std::to_string(rectangle.bottom) + ']')
                + ",\"propertyHRESULTs\":[" + std::to_string(pidHR) + ',' + std::to_string(nameHR) + ',' + std::to_string(idHR)
                    + ',' + std::to_string(clsHR) + ',' + std::to_string(typeHR) + ',' + std::to_string(enabledHR) + ','
                    + std::to_string(offscreenHR) + ',' + std::to_string(rectHR) + ',' + std::to_string(patternHR) + ',' + std::to_string(afterHR) + "]}";
            if (rows.size() + row.size() + (count ? 1 : 0) > 60000) { truncated = true; break; }
            const int index = static_cast<int>(count++); if (index) rows += ','; rows += row;
            if (index == 0) rootRole = nameJSON == jsonQuote(L"Microsoft account") && type == UIA_WindowControlTypeId
                && !nameClipped && held->imageLeaf == L"wwahost.exe" && selectedClass == L"Windows.UI.Core.CoreWindow";
            const bool isPrivacyPane = nameJSON == jsonQuote(L"Choose privacy settings for your device")
                && classJSON == jsonQuote(L"Internet Explorer_Server") && type == UIA_PaneControlTypeId
                && !nameClipped && !classClipped && enabled && !offscreen;
            if (isPrivacyPane) { ++panes; privacyPane = node.element; }
            int privacyIndex = node.privacyParent;
            if (node.privacy || isPrivacyPane) {
                privacyIndex = static_cast<int>(semanticCount++); if (privacyIndex) semanticRows += ',';
                semanticRows += "[" + std::to_string(isPrivacyPane ? -1 : node.privacyParent) + ',' + nameJSON + ',' + idJSON + ',' + classJSON
                    + ',' + std::to_string(type) + ',' + (enabled ? "true" : "false") + ',' + (offscreen ? "true" : "false")
                    + ",[" + std::to_string(rectangle.left) + ',' + std::to_string(rectangle.top) + ','
                    + std::to_string(rectangle.right) + ',' + std::to_string(rectangle.bottom) + "],"
                    + (nameClipped ? "true" : "false") + ',' + (idClipped ? "true" : "false") + ',' + (classClipped ? "true" : "false")
                    + ',' + (invokeAvailable ? "true" : "false") + "]";
                if (!nameClipped && !idClipped && idJSON == jsonQuote(L"OobeSettingsAcceptButton") && type == UIA_ButtonControlTypeId
                    && !offscreen && patternValid && invokeAvailable && rectValid && rectangle.left < rectangle.right
                    && rectangle.top < rectangle.bottom && (nameJSON == jsonQuote(L"Next, tab through all privacy settings to continue")
                        || nameJSON == jsonQuote(L"Accept") || nameJSON == jsonQuote(L"Accept these privacy settings"))) {
                    ++matchingButtons; if (enabled) ++buttons;
                    actionButton = node.element; buttonName = nameJSON == jsonQuote(L"Accept") ? L"Accept"
                        : nameJSON == jsonQuote(L"Accept these privacy settings") ? L"Accept these privacy settings"
                        : L"Next, tab through all privacy settings to continue";
                }
            }
            ComPtr<IUIAutomationElement> child;
            if (!observed(call([&]() { return walker->GetFirstChildElement(node.element.Get(), &child); }))) continue;
            if (child && node.depth >= 12) { truncated = true; continue; }
            while (child && budget()) {
                if (pending.size() + visited >= 512) { truncated = true; break; }
                pending.push_back({child, node.depth + 1, index, node.privacy || isPrivacyPane, privacyIndex}); ComPtr<IUIAutomationElement> next;
                if (!observed(call([&]() { return walker->GetNextSiblingElement(child.Get(), &next); }))) break;
                child = next;
            }
        }
        if (!pending.empty() || !budget() || owners.truncated) truncated = true;
        rootStable = rootBound() && preflight((prefix + "-after-preflight.json").c_str()) && stable();
        completed = rootStable && budget() && count && !truncated && !errors && !owners.errors && !providerSkips;
    } catch (const winrt::hresult_error& error) { ++errors; lastError = error.code().value; }
      catch (...) { ++errors; lastError = E_FAIL; }
    const bool expired = !budget(); if (expired) { truncated = true; completed = false; }
    const auto semanticState = "[" + semanticRows + "]", semanticSHA = oobeStateHash(semanticState);
    const auto result = "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"sourceSHA\":" + jsonQuote(source) + ",\"binarySHA256\":\"" + executable.digest + "\",\"architecture\":\"ARM64\""
        + ",\"diagnosticOnly\":" + (selection ? "false" : "true") + ",\"readOnly\":" + (selection ? "false" : "true")
        + ",\"snapshotReadOnly\":true,\"actorInvocationsBeforeSnapshot\":" + std::to_string(oobeSetupInvokeEntered)
        + ",\"censusAttempts\":1,\"showAttempts\":0,\"inputAttempted\":false"
        + ",\"invokeAttempted\":" + (oobeSetupInvokeEntered ? "true" : "false") + ",\"launchAttempted\":false,\"installAttempted\":false,\"retryAllowed\":false"
        + ",\"selectorActionQualified\":false,\"negativeIsAbsenceProof\":false,\"sameUserSession\":true"
        + ",\"centerOpenedProved\":false,\"nativeCallbackQualified\":false,\"navigationQualified\":false"
        + ",\"projection\":\"selected_foreground_UIA_properties\",\"imageAuthority\":\"kernel_image_under_Windows_not_signature_qualification\""
        + ",\"setupImagePathKind\":" + (selection ? "\"exact_Windows_System32_WWAHost_kernel_image\"" : "null")
        + ",\"foregroundHWND\":" + std::to_string(reinterpret_cast<uintptr_t>(window)) + ",\"foregroundPID\":" + std::to_string(pid)
        + ",\"foregroundCreatedUtcTicks\":\"" + created + "\",\"verifiedImageLeaf\":" + jsonQuote(held->imageLeaf)
        + ",\"windowClass\":" + jsonQuote(selectedClass) + ",\"session\":" + std::to_string(owners.session)
        + ",\"available\":" + (available ? "true" : "false") + ",\"rootStable\":" + (rootStable ? "true" : "false")
        + ",\"walkCompleted\":" + (completed ? "true" : "false") + ",\"truncated\":" + (truncated ? "true" : "false")
        + ",\"deadlineExpired\":" + (expired ? "true" : "false") + ",\"elapsedMs\":" + std::to_string(GetTickCount64() - started)
        + ",\"startedBootMs\":" + std::to_string(started) + ",\"observationDeadlineBootMs\":" + std::to_string(observationDeadline)
        + ",\"visited\":" + std::to_string(visited) + ",\"count\":" + std::to_string(count) + ",\"providerSkips\":" + std::to_string(providerSkips)
        + ",\"verifiedOwners\":" + std::to_string(owners.owners.size()) + ",\"errors\":" + std::to_string(errors + owners.errors)
        + ",\"semanticStateSHA256\":\"" + semanticSHA + "\",\"privacyPanes\":" + std::to_string(panes)
        + ",\"eligiblePrivacyButtons\":" + std::to_string(buttons)
        + ",\"matchingPrivacyButtons\":" + std::to_string(matchingButtons)
        + ",\"lastErrorHRESULT\":" + std::to_string(lastError) + ",\"rows\":[" + rows + "]}\n";
    if (result.size() > 65536) throw std::runtime_error("OOBE serialized report exceeds 64KiB");
    report((prefix + "-uia.json").c_str(), result);
    if (selection) {
        selection->window = window; selection->pid = pid; selection->created = created;
        selection->element = rootElement; selection->pane = privacyPane; selection->button = actionButton;
        selection->automation = rootAutomation; selection->walker = rootWalker;
        selection->buttonName = buttonName; selection->exactSystemImage = exactSystemImage;
        selection->state = semanticState; selection->stateSHA = semanticSHA;
        selection->unambiguous = completed && rootRole && panes == 1 && matchingButtons == 1 && owners.owners.size() == 1;
        selection->eligible = selection->unambiguous && buttons == 1;
        selection->owners = std::move(owners);
    }
    return available && rootStable;
}
// Exactly one finite fresh-guest flow. Armed boundary without a terminal receipt is unknown.
static bool oobeSetup() {
    const auto source = oobeAuthority(true); const ULONGLONG started = GetTickCount64();
    oobeSetupDeadline = started + 60000; vendorDeadline = oobeSetupDeadline;
    VendorHashFile executable(root / L"navigation-native-probe.exe", 67108864);
    unsigned nextCalls{}, acceptCalls{}, returned{}, progressed{}, afterCensuses{}; bool armed{}, uncertain{}, accepted{}, windowGone{};
    HRESULT lastError = S_OK; std::string phase = "initial", previousState, previousBirth;
    HWND previousWindow{}; DWORD previousPID{};
    const auto header = "{\"pid\":" + std::to_string(GetCurrentProcessId()) + ",\"nonce\":" + jsonQuote(uuid)
        + ",\"sourceSHA\":" + jsonQuote(source) + ",\"binarySHA256\":\"" + executable.digest + "\",\"architecture\":\"ARM64\"";
    oobeDurableIntent("oobe-setup-intent.json", header + ",\"scope\":\"disposable_guest_privacy_OOBE_setup_only\",\"readOnly\":false"
        + ",\"maxNextCalls\":4,\"maxAcceptCalls\":1,\"helperBudgetMs\":60000,\"cooperativeUIBudgetMs\":5000"
        + ",\"maxAfterObservations\":8,\"afterObservationBudgetMs\":5000"
        + ",\"showAttempts\":0,\"keyboardOrPointerInputAttempted\":false,\"registryWriteAPIAttempted\":false,\"retryAllowed\":false}\n");
    try {
        for (unsigned step = 0; step < 5; ++step) {
            const auto prefix = "oobe-setup-" + std::to_string(step);
            phase = prefix + "-before"; OOBESelection selected;
            if (!oobePreflight(phase, &selected) || !selected.eligible || !selected.stable())
                throw std::runtime_error("fresh exact OOBE privacy selection unavailable");
            if (!previousState.empty() && (selected.state != previousState || selected.pid != previousPID
                || selected.created != previousBirth || selected.window != previousWindow))
                throw std::runtime_error("OOBE state changed outside confirmed progression");
            const bool accept = selected.buttonName == L"Accept" || selected.buttonName == L"Accept these privacy settings";
            if (accept ? acceptCalls >= 1 : nextCalls >= 4) throw std::runtime_error("finite OOBE action limit reached");
            const ULONGLONG invokeDeadline = std::min(oobeSetupDeadline, GetTickCount64() + 5000);
            auto budget = [&]() { if (GetTickCount64() >= invokeDeadline || !selected.stable())
                throw std::runtime_error("OOBE immediate binding/deadline unavailable"); };
            auto call = [&](auto operation) { budget(); HRESULT hr = operation();
                if (hr != S_OK) winrt::throw_hresult(FAILED(hr) ? hr : E_FAIL); };
            auto exactText = [&](IUIAutomationElement* element, unsigned property, const wchar_t* expected) {
                BSTR value{}; budget(); HRESULT hr = property == 1 ? element->get_CurrentAutomationId(&value)
                    : property == 2 ? element->get_CurrentClassName(&value) : element->get_CurrentName(&value);
                std::unique_ptr<OLECHAR, decltype(&SysFreeString)> owned(value, &SysFreeString); if (hr != S_OK) winrt::throw_hresult(FAILED(hr) ? hr : E_FAIL);
                if (!value || SysStringLen(value) > 120 || std::wstring(value, SysStringLen(value)) != expected)
                    throw std::runtime_error("OOBE selector changed before effect");
            };
            int rootPID{}; UIA_HWND hwnd{};
            call([&]() { return selected.element->get_CurrentProcessId(&rootPID); });
            call([&]() { return selected.element->get_CurrentNativeWindowHandle(&hwnd); });
            if (rootPID != static_cast<int>(selected.pid) || reinterpret_cast<HWND>(hwnd) != selected.window)
                throw std::runtime_error("OOBE UIA root changed before effect");
            exactText(selected.element.Get(), 0, L"Microsoft account");
            exactText(selected.pane.Get(), 0, L"Choose privacy settings for your device");
            exactText(selected.pane.Get(), 2, L"Internet Explorer_Server");
            exactText(selected.button.Get(), 1, L"OobeSettingsAcceptButton");
            exactText(selected.button.Get(), 0, selected.buttonName.c_str());
            CONTROLTYPEID type{}; BOOL enabled{}, offscreen{}; int buttonPID{}, panePID{};
            call([&]() { return selected.pane->get_CurrentProcessId(&panePID); });
            call([&]() { return selected.pane->get_CurrentControlType(&type); });
            call([&]() { return selected.pane->get_CurrentIsOffscreen(&offscreen); });
            if (panePID != rootPID || type != UIA_PaneControlTypeId || offscreen)
                throw std::runtime_error("OOBE privacy pane changed before effect");
            call([&]() { return selected.button->get_CurrentProcessId(&buttonPID); });
            call([&]() { return selected.button->get_CurrentControlType(&type); });
            call([&]() { return selected.button->get_CurrentIsEnabled(&enabled); });
            call([&]() { return selected.button->get_CurrentIsOffscreen(&offscreen); });
            if (buttonPID != rootPID || type != UIA_ButtonControlTypeId || !enabled || offscreen)
                throw std::runtime_error("OOBE button no longer eligible");
            // Fresh ancestry read of this exact element, not a desktop-wide selector/action.
            ComPtr<IUIAutomationElement> cursor = selected.button; bool paneSeen{}, rootSeen{};
            for (unsigned depth = 0; cursor && depth <= 12; ++depth) {
                BOOL same{}; call([&]() { return selected.automation->CompareElements(cursor.Get(), selected.pane.Get(), &same); });
                if (same) paneSeen = true;
                same = FALSE;
                call([&]() { return selected.automation->CompareElements(cursor.Get(), selected.element.Get(), &same); });
                if (same) { rootSeen = true; break; }
                ComPtr<IUIAutomationElement> parent;
                call([&]() { return selected.walker->GetParentElement(cursor.Get(), &parent); }); cursor = parent;
            }
            if (!paneSeen || !rootSeen) throw std::runtime_error("OOBE button left the bound privacy subtree");
            ComPtr<IUIAutomationInvokePattern> pattern;
            call([&]() { return selected.button->GetCurrentPatternAs(UIA_InvokePatternId, IID_PPV_ARGS(&pattern)); });
            if (!pattern) throw std::runtime_error("OOBE Invoke pattern unavailable");
            budget(); phase = prefix + "-invoke";
            const auto action = accept ? L"Accept" : L"Next";
            oobeDurableIntent((prefix + "-armed.json").c_str(), header + ",\"step\":" + std::to_string(step)
                + ",\"action\":" + jsonQuote(action) + ",\"invokeBoundaryArmed\":true,\"invokeCallEntered\":false"
                + ",\"foregroundPID\":" + std::to_string(selected.pid) + ",\"foregroundCreatedUtcTicks\":\"" + selected.created
                + "\",\"foregroundHWND\":" + std::to_string(reinterpret_cast<uintptr_t>(selected.window))
                + ",\"beforeStateSHA256\":\"" + selected.stateSHA + "\",\"targetAutomationId\":\"OobeSettingsAcceptButton\""
                + ",\"targetName\":" + jsonQuote(selected.buttonName)
                + ",\"setupImagePathKind\":\"exact_Windows_System32_WWAHost_kernel_image\""
                + ",\"atomicUIBindingQualified\":false,\"retryAllowed\":false}\n");
            armed = true; budget(); uncertain = true; ++oobeSetupInvokeEntered;
            if (accept) ++acceptCalls; else ++nextCalls;
            const HRESULT hr = pattern->Invoke(); ++returned;
            report((prefix + "-returned.json").c_str(), header + ",\"step\":" + std::to_string(step)
                + ",\"action\":" + jsonQuote(action) + ",\"invokeCallEntered\":true,\"invokeCallReturned\":true,\"hresult\":"
                + std::to_string(hr) + ",\"progressionObserved\":false}\n");
            // S_OK is API acceptance only. A late/error/same-state result never permits another action.
            if (hr != S_OK || GetTickCount64() >= invokeDeadline) { lastError = hr == S_OK ? E_ABORT : hr;
                throw std::runtime_error("OOBE Invoke failed or exceeded cooperative budget"); }
            std::string afterSHA, afterPrefix; unsigned afterObservations{}; ULONGLONG afterElapsed{};
            if (accept) {
                phase = prefix + "-window-disappearance";
                const ULONGLONG afterDeadline = std::min(oobeSetupDeadline, GetTickCount64() + 5000);
                while (IsWindow(selected.window) && GetTickCount64() < afterDeadline) Sleep(20);
                if (IsWindow(selected.window) || GetTickCount64() >= afterDeadline
                    || !preflight("oobe-setup-after-preflight.json") || GetTickCount64() >= oobeSetupDeadline)
                    throw std::runtime_error("accepted OOBE window disappearance unproved");
                windowGone = true; accepted = true;
            } else {
                // Read-only observations of a changed, sole disabled button, never another Invoke.
                // One absolute post-Invoke deadline covers every census; it never resets.
                const ULONGLONG afterStarted = GetTickCount64(), afterDeadline = std::min(oobeSetupDeadline, afterStarted + 5000);
                for (unsigned observation = 0; observation < 8 && GetTickCount64() < afterDeadline; ++observation) {
                    phase = prefix + "-after-" + std::to_string(observation); OOBESelection after;
                    ++afterObservations; ++afterCensuses;
                    if (!oobePreflight(phase, &after, afterDeadline) || !after.unambiguous || !after.stable()
                        || GetTickCount64() >= afterDeadline || after.state == selected.state || after.pid != selected.pid
                        || after.created != selected.created || after.window != selected.window)
                        throw std::runtime_error("fresh OOBE transition observation unproved; no repeat");
                    if (!after.eligible) continue; // Complete sole exact button, temporarily disabled.
                    afterSHA = after.stateSHA; afterPrefix = phase; previousState = after.state; previousPID = after.pid;
                    previousBirth = after.created; previousWindow = after.window; break;
                }
                if (afterSHA.empty() || GetTickCount64() >= afterDeadline)
                    throw std::runtime_error("bounded OOBE transition not ready; no repeat");
                afterElapsed = GetTickCount64() - afterStarted;
            }
            report((prefix + "-progress.json").c_str(), header + ",\"step\":" + std::to_string(step)
                + ",\"action\":" + jsonQuote(action) + ",\"beforeStateSHA256\":\"" + selected.stateSHA
                + "\",\"afterStateSHA256\":" + (accept ? "null" : "\"" + afterSHA + "\"")
                + ",\"afterObservationCount\":" + std::to_string(afterObservations)
                + ",\"afterObservationElapsedMs\":" + std::to_string(afterElapsed)
                + ",\"afterSnapshotPrefix\":" + (accept ? "null" : jsonQuote(winrt::to_hstring(afterPrefix).c_str()))
                + ",\"progressionObserved\":true,\"ownedOOBEWindowGone\":" + (windowGone ? "true" : "false") + "}\n");
            ++progressed; uncertain = false; if (accepted) break;
        }
        if (!accepted || GetTickCount64() >= oobeSetupDeadline) throw std::runtime_error("OOBE Accept not reached within finite budget");
        phase = "complete";
    } catch (const winrt::hresult_error& error) { lastError = error.code().value; accepted = false; }
      catch (const std::exception& error) { if (lastError == S_OK) lastError = E_FAIL; accepted = false; std::cerr << error.what() << '\n'; }
    report("oobe-setup-result.json", header + ",\"scope\":\"disposable_guest_privacy_OOBE_setup_only\",\"readOnly\":false"
        + ",\"setupQualified\":" + (accepted ? "true" : "false") + ",\"phase\":" + jsonQuote(winrt::to_hstring(phase).c_str())
        + ",\"nextCallsEntered\":" + std::to_string(nextCalls) + ",\"acceptCallsEntered\":" + std::to_string(acceptCalls)
        + ",\"invokeCallsEntered\":" + std::to_string(oobeSetupInvokeEntered) + ",\"invokeCallsReturned\":" + std::to_string(returned)
        + ",\"progressionsObserved\":" + std::to_string(progressed) + ",\"invokeBoundaryArmed\":" + (armed ? "true" : "false")
        + ",\"afterCensusesAttempted\":" + std::to_string(afterCensuses)
        + ",\"invokeEffectUncertain\":" + (uncertain ? "true" : "false") + ",\"ownedOOBEWindowGone\":" + (windowGone ? "true" : "false")
        + ",\"elapsedMs\":" + std::to_string(GetTickCount64() - started) + ",\"lastErrorHRESULT\":" + std::to_string(lastError)
        + ",\"showAttempts\":0,\"keyboardOrPointerInputAttempted\":false,\"registryWriteAPIAttempted\":false"
        + ",\"installAttempted\":false,\"launchAttempted\":false,\"retryAllowed\":false,\"atomicUIBindingQualified\":false"
        + ",\"centerOpenedProved\":false,\"nativeCallbackQualified\":false,\"navigationQualified\":false,\"processQuiescenceQualified\":false}\n");
    return accepted;
}
static void vendorArchiveIdentity() {
    ComPtr<IStream> stream;
    check(SHCreateStreamOnFileEx((root / L"client.msix").c_str(), STGM_READ | STGM_SHARE_DENY_WRITE, FILE_ATTRIBUTE_NORMAL, FALSE, nullptr, &stream));
    ComPtr<IAppxFactory> factory; check(CoCreateInstance(CLSID_AppxFactory, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&factory)));
    ComPtr<IAppxPackageReader> reader; check(factory->CreatePackageReader(stream.Get(), &reader));
    ComPtr<IAppxManifestReader> manifest; check(reader->GetManifest(&manifest));
    ComPtr<IAppxManifestPackageDependenciesEnumerator> dependencies;
    check(manifest->GetPackageDependencies(&dependencies));
    BOOL hasDependency = FALSE; check(dependencies->GetHasCurrent(&hasDependency));
    if (hasDependency) throw std::runtime_error("pinned vendor manifest dependencies forbidden");
    ComPtr<IAppxManifestPackageId> id; check(manifest->GetPackageId(&id));
    auto equal = [](auto get, const wchar_t* expected) {
        LPWSTR value{}; check(get(&value)); std::unique_ptr<wchar_t, decltype(&CoTaskMemFree)> owned(value, &CoTaskMemFree);
        return value && wcsnlen_s(value, 513) <= 512 && std::wstring(value) == expected;
    };
    if (!equal([&](LPWSTR* p) { return id->GetName(p); }, vendorName)
        || !equal([&](LPWSTR* p) { return id->GetPublisher(p); }, vendorPublisher)
        || !equal([&](LPWSTR* p) { return id->GetPackageFamilyName(p); }, vendorFamily)
        || !equal([&](LPWSTR* p) { return id->GetPackageFullName(p); }, vendorFull))
        throw std::runtime_error("vendor archive SDK identity mismatch");
}
static auto vendorInstalled() { return NavigationVendorTEST::installed(); }
template<typename Operation> static auto vendorAwait(const Operation& operation, DWORD milliseconds) {
    return NavigationVendorTEST::await(operation, milliseconds, vendorDeadline);
}
static bool vendorMode(const std::wstring& mode) {
    return mode == L"vendor-state-before" || mode == L"vendor-state-after" || mode == L"vendor-install"
        || mode == L"vendor-query-settings" || mode == L"vendor-query-thread"
        || mode == L"vendor-launch-settings" || mode == L"vendor-launch-thread" || mode == L"vendor-remove";
}
static int vendorNative(const std::wstring& mode) {
    using namespace winrt::Windows::Management::Deployment;
    using namespace winrt::Windows::System;
    vendorDeadline = GetTickCount64() + 150000;
    vendorPhase = "current_user_query";
    auto installed = vendorInstalled();
    auto user = tokenUser(GetCurrentProcess());
    PSID sid = reinterpret_cast<TOKEN_USER*>(user.data())->User.Sid;
    if (!IsValidSid(sid) || GetLengthSid(sid) > SECURITY_MAX_SID_SIZE) throw std::runtime_error("current user SID unavailable");
    std::string userBinding;
    constexpr char hex[] = "0123456789abcdef";
    const BYTE* sidBytes = static_cast<const BYTE*>(sid);
    for (DWORD i = 0; i < GetLengthSid(sid); ++i) { userBinding += hex[sidBytes[i] >> 4]; userBinding += hex[sidBytes[i] & 15]; }
    const std::string absence = narrow(uuid) + "\n" + userBinding + "\n";
    vendorRemaining();
    if (mode == L"vendor-state-before" || mode == L"vendor-state-after") {
        report((mode == L"vendor-state-before" ? "vendor-before.json" : "vendor-after.json"),
            vendorRecord("\"readOnly\":true,\"installedCount\":" + std::to_string(installed.size())
                + ",\"exactFullName\":" + (installed.size() == 1 && installed[0].Id().FullName() == vendorFull ? "true" : "false")));
        if (mode == L"vendor-state-before" && installed.empty()) report("vendor-absent.proof", absence);
        return 0;
    }
    // The future controller writes this only after actual signtool /pa /all success
    // and full streaming hashes. No arbitrary package/PFN/URI arguments are accepted.
    vendorPhase = "custody";
    VendorHashFile archive(root / L"client.msix", 1073741824ULL);
    VendorHashFile executable(root / L"navigation-native-probe.exe", 67108864ULL);
    const std::string custody = "TEST vendor custody " + narrow(uuid) + "\nsigntool-pa-all-success\n"
        + archive.digest + "\n" + executable.digest + "\n";
    if (vendorProof("vendor-custody.proof") != custody) throw std::runtime_error("controller custody binding mismatch");
    vendorArchiveIdentity();
    const std::string intent = custody + "absent-before-install\n" + userBinding + "\n";
    if (vendorProof("vendor-absent.proof") != absence) throw std::runtime_error("fresh current-user absence proof required");
    PackageManager manager;
    installed = vendorInstalled(); vendorRemaining(); // Fresh read after custody hashing, before any mutation.
    if (mode == L"vendor-install") {
        if (!installed.empty()) throw std::runtime_error("preexisting vendor family; install refused");
        report("vendor-install-intent.proof", intent); // Exclusive before Add, never retry this attempt.
        wchar_t url[32768]{}; DWORD length = 32768;
        check(UrlCreateFromPathW((root / L"client.msix").c_str(), url, &length, 0));
        vendorRemaining(); vendorPhase = "add_package"; vendorAPIEntered = true;
        auto result = vendorAwait(manager.AddPackageAsync(winrt::Windows::Foundation::Uri(url), nullptr, DeploymentOptions::None), 120000);
        report("vendor-install-result.json", vendorRecord("\"operationCompleted\":true,\"extendedError\":" + std::to_string(result.ExtendedErrorCode().value)));
        check(result.ExtendedErrorCode());
        report("vendor-install-completed.proof", intent);
        return 0;
    }
    if (vendorProof("vendor-install-intent.proof") != intent || vendorProof("vendor-install-completed.proof") != intent
        || installed.size() != 1 || installed[0].Id().FullName() != vendorFull)
        throw std::runtime_error("exact owned installed vendor package required");
    if (mode == L"vendor-remove") {
        report("vendor-remove-intent.proof", intent);
        vendorRemaining(); vendorPhase = "remove_package"; vendorAPIEntered = true;
        auto result = vendorAwait(manager.RemovePackageAsync(vendorFull), 120000);
        report("vendor-remove-result.json", vendorRecord("\"operationCompleted\":true,\"extendedError\":" + std::to_string(result.ExtendedErrorCode().value)));
        check(result.ExtendedErrorCode());
        if (!vendorInstalled().empty()) throw std::runtime_error("vendor family remains after removal");
        report("vendor-removed.json", vendorRecord("\"currentUserFamilyAbsent\":true")); return 0;
    }
    const bool thread = mode == L"vendor-query-thread" || mode == L"vendor-launch-thread";
    const bool launching = mode == L"vendor-launch-thread" || mode == L"vendor-launch-settings";
    const winrt::Windows::Foundation::Uri uri(thread ? L"codex://threads/" + uuid : L"codex://settings");
    vendorRemaining(); vendorPhase = "targeted_uri_query";
    const auto support = vendorAwait(Launcher::QueryUriSupportAsync(uri, LaunchQuerySupportType::Uri, vendorFamily), 10000);
    const std::string suffix = thread ? "thread" : "settings";
    report(("vendor-query-" + suffix + (launching ? "-prelaunch.json" : ".json")).c_str(),
        vendorRecord("\"readOnly\":true,\"uriSupport\":" + std::to_string(static_cast<int>(support))));
    if (!launching) return support == LaunchQuerySupportStatus::Available ? 0 : 3;
    if (support != LaunchQuerySupportStatus::Available) throw std::runtime_error("targeted URI support unavailable; no launch");
    // One launch total, including across settings/thread modes. No default or fallback route.
    installed = vendorInstalled(); vendorRemaining();
    if (installed.size() != 1 || installed[0].Id().FullName() != vendorFull) throw std::runtime_error("selected package changed before launch");
    report("vendor-launch-intent.proof", intent + suffix + "\n");
    LauncherOptions options; options.TargetApplicationPackageFamilyName(vendorFamily); options.FallbackUri(nullptr);
    vendorRemaining(); vendorPhase = "launch_uri"; vendorAPIEntered = true;
    const bool accepted = vendorAwait(Launcher::LaunchUriAsync(uri, options), 15000);
    report("vendor-launch.json", vendorRecord("\"launchReturned\":true,\"handoffAccepted\":" + std::string(accepted ? "true" : "false")
        + ",\"launchAttempts\":1,\"retryAllowed\":false"));
    return accepted ? 0 : 3;
}
static void vendorFailure(HRESULT hr) noexcept {
    if (!ownedRootValidated || !vendorMode(activeMode)) return;
    try {
        report(("vendor-failure-" + narrow(activeMode) + ".json").c_str(), vendorRecord("\"phase\":"
            + jsonQuote(winrt::to_hstring(vendorPhase).c_str()) + ",\"hresult\":" + std::to_string(hr)
            + ",\"effectAPIEntered\":" + (vendorAPIEntered ? "true" : "false") + ",\"outcomeUnknown\":"
            + (vendorAPIEntered ? "true" : "false") + ",\"retryAllowed\":false"));
    } catch (...) { /* Outer controller retains partial reports and must not infer clean/no-effect. */ }
}
int wmain(int argc, wchar_t** argv) {
    try {
        if (argc < 4) return 2;
        std::wstring mode = argv[1]; activeMode = mode;
        wchar_t gate[8]{}, ci[8]{}, actions[8]{};
        // The OS-created COM process need not inherit the runner's environment.
        // Its authority is the exact owned root/binary/UUID installed by the opt-in controller.
        if (mode != L"callback" && (!GetEnvironmentVariableW(vendorMode(mode) ? L"NAVIGATION_WINDOWS_VENDOR_NATIVE_TEST" : L"AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E", gate, 8)
            || std::wstring(gate) != L"1" || !GetEnvironmentVariableW(L"CI", ci, 8) || std::wstring(ci) != L"true"
            || !GetEnvironmentVariableW(L"GITHUB_ACTIONS", actions, 8) || std::wstring(actions) != L"true")) return 2;
        root = fs::canonical(argv[2]); uuid = argv[3];
        if (uuid.size() != 36 || uuid.find_first_not_of(L"0123456789abcdef-") != std::wstring::npos) return 2;
        check(CLSIDFromString((L"{" + uuid + L"}").c_str(), &clsid));
        if (root.filename().wstring() != L"navigation-windows-test-" + uuid) return 2;
        std::ifstream marker(root / ".owned-test-root"); std::string text; std::getline(marker, text);
        if (text != "TEST navigation Windows " + narrow(uuid)) return 2;
        wchar_t ownExe[32768]{};
        if (!GetModuleFileNameW(nullptr, ownExe, 32768)
            || fs::canonical(ownExe) != root / L"navigation-native-probe.exe") return 2;
        ownedRootValidated = true;
        aumid = L"AgentNotify.Navigation.TEST." + uuid; action = L"TEST open " + uuid;
        if (mode == L"callback" && (!ownRegistryProof() || registeredCommand() != serverCommand()
            || !ownAppIdentityProof() || !appIdentityMatches())) return 2;
        winrt::init_apartment(winrt::apartment_type::multi_threaded);
        if (vendorMode(mode)) { if (argc != 4) return 2; return vendorNative(mode); }
        if (mode == L"package-metadata") { packageMetadata(); return 0; }
        if (mode == L"center-policy") return centerPolicy() ? 0 : 3;
        if (mode == L"preflight") return preflight() ? 0 : 3;
        if (mode == L"desktop-capture") { if (argc != 4) return 2; return desktopCapture() ? 0 : 3; }
        if (mode == L"oobe-preflight") { if (argc != 4) return 2; return oobePreflight() ? 0 : 3; }
        if (mode == L"oobe-setup") { if (argc != 4) return 2; return oobeSetup() ? 0 : 1; }
        if (mode == L"center-surface") return centerSurface() ? 0 : 3;
        if (mode == L"taskbar-uia") return taskbarUI() ? 0 : 3;
        if (mode == L"send") return send();
        if (mode == L"callback") return callback();
        if (mode == L"invoke") return invoke();
        if (mode == L"cleanup") { cleanup(); return 0; }
        return 2;
    } catch (const winrt::hresult_error& e) {
        vendorFailure(e.code().value); sendFailure(e.code().value);
        std::cerr << "HRESULT " << e.code().value << ": " << winrt::to_string(e.message()) << '\n'; return 1;
    } catch (const Failure& e) {
        std::cerr << "capture token query " << e.query << ": " << e.error << '\n'; return 1;
    } catch (const std::exception& e) { vendorFailure(E_FAIL); sendFailure(E_FAIL); std::cerr << e.what() << '\n'; return 1; }
}
