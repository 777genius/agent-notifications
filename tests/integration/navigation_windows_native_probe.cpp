// Disposable CI-only Windows client toast lifecycle probe. No production adapter.
#define NOMINMAX
#include <windows.h>
#include <wtsapi32.h>
#pragma comment(lib, "Wtsapi32.lib")
#include <shlobj.h>
#include <propkey.h>
#include <propvarutil.h>
#include <notificationactivationcallback.h>
#include <UIAutomation.h>
#include <wrl/client.h>
#include <winrt/Windows.Data.Xml.Dom.h>
#include <winrt/Windows.UI.Notifications.h>
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
#include <map>
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
    explicit SurfaceOwner(HANDLE value) : process(value) {}
    ~SurfaceOwner() { CloseHandle(process); }
    bool live() const { return WaitForSingleObject(process, 0) == WAIT_TIMEOUT; }
};
struct SurfaceScan {
    std::map<DWORD, std::unique_ptr<SurfaceOwner>> owners;
    DWORD session = 0;
    std::vector<BYTE> user;
    std::wstring windows;
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
            || (leaf != L"explorer.exe" && leaf != L"shellhost.exe" && leaf != L"shellexperiencehost.exe")) return nullptr;
        const auto candidate = tokenUser(handle);
        if (!EqualSid(reinterpret_cast<TOKEN_USER*>(user.data())->User.Sid,
            reinterpret_cast<const TOKEN_USER*>(candidate.data())->User.Sid) || !held->live()) return nullptr;
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
        return "{\"projection\":\"visible_or_foreground_owned_shell\",\"enumerationCompleted\":" + std::string(enumerationCompleted ? "true" : "false")
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
static int invoke() {
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
int wmain(int argc, wchar_t** argv) {
    try {
        if (argc < 4) return 2;
        std::wstring mode = argv[1]; activeMode = mode;
        wchar_t gate[8]{}, ci[8]{}, actions[8]{};
        // The OS-created COM process need not inherit the runner's environment.
        // Its authority is the exact owned root/binary/UUID installed by the opt-in controller.
        if (mode != L"callback" && (!GetEnvironmentVariableW(L"AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E", gate, 8)
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
        if (mode == L"center-policy") return centerPolicy() ? 0 : 3;
        if (mode == L"preflight") return preflight() ? 0 : 3;
        if (mode == L"center-surface") return centerSurface() ? 0 : 3;
        if (mode == L"taskbar-uia") return taskbarUI() ? 0 : 3;
        if (mode == L"send") return send();
        if (mode == L"callback") return callback();
        if (mode == L"invoke") return invoke();
        if (mode == L"cleanup") { cleanup(); return 0; }
        return 2;
    } catch (const winrt::hresult_error& e) {
        sendFailure(e.code().value);
        std::cerr << "HRESULT " << e.code().value << ": " << winrt::to_string(e.message()) << '\n'; return 1;
    } catch (const std::exception& e) { sendFailure(E_FAIL); std::cerr << e.what() << '\n'; return 1; }
}
