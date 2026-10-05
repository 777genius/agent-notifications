// Disposable CI-only Windows client toast lifecycle probe. No production adapter.
#define NOMINMAX
#include <windows.h>
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
static bool preflight() {
    OSVERSIONINFOEXW v{}; v.dwOSVersionInfoSize = sizeof(v);
    auto rtl = reinterpret_cast<LONG(WINAPI*)(OSVERSIONINFOEXW*)>(
        GetProcAddress(GetModuleHandleW(L"ntdll.dll"), "RtlGetVersion"));
    if (!rtl || rtl(&v) != 0) throw std::runtime_error("OS version unavailable");
    DWORD session = 0;
    bool sessionKnown = ProcessIdToSessionId(GetCurrentProcessId(), &session) != FALSE;
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
        && stationName == L"WinSta0" && !inputName.empty() && inputName == threadName && shellPid != 0;
    report("preflight.json", "{\"client\":" + std::string(client ? "true" : "false")
        + ",\"build\":" + std::to_string(v.dwBuildNumber) + ",\"productType\":" + std::to_string(v.wProductType)
        + ",\"sessionKnown\":" + (sessionKnown ? "true" : "false") + ",\"session\":" + std::to_string(session)
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
static NotificationSetting measuredReadiness(const ToastNotifier& notifier) {
    // Experiment: explicit shortcut notification may let Shell recognize this new TEST identity.
    // Neither SHCNF_FLUSH nor the shortcut's existence proves AppResolver recognition.
    ULONGLONG started = GetTickCount64(), deadline = started + 3000; unsigned attempt = 0;
    for (;;) {
        ComPtr<IShellItem2> item; std::wstring parsingName = L"shell:AppsFolder\\" + aumid;
        HRESULT parseHR = SHCreateItemFromParsingName(parsingName.c_str(), nullptr, IID_PPV_ARGS(&item));
        HRESULT appIDHR = E_PENDING; PWSTR actualID = nullptr;
        if (SUCCEEDED(parseHR)) appIDHR = item->GetString(PKEY_AppUserModel_ID, &actualID);
        std::wstring observedID = actualID ? actualID : L""; CoTaskMemFree(actualID);
        bool recognized = SUCCEEDED(parseHR) && SUCCEEDED(appIDHR) && observedID == aumid;
        HRESULT settingHR = S_OK; NotificationSetting setting = NotificationSetting::Enabled;
        try { setting = notifier.Setting(); } catch (const winrt::hresult_error& error) { settingHR = error.code().value; }
        ULONGLONG now = GetTickCount64();
        std::string filename = "shell-readiness-" + std::to_string(++attempt) + ".json";
        report(filename.c_str(), "{\"aumid\":" + jsonQuote(aumid) + ",\"observedAppID\":" + jsonQuote(observedID)
            + ",\"parseHRESULT\":" + std::to_string(parseHR) + ",\"appIDHRESULT\":" + std::to_string(appIDHR)
            + ",\"recognized\":" + (recognized ? "true" : "false") + ",\"settingHRESULT\":" + std::to_string(settingHR)
            + ",\"elapsedMS\":" + std::to_string(now - started)
            + ",\"setting\":" + std::to_string(SUCCEEDED(settingHR) ? static_cast<int>(setting) : -1) + "}\n");
        if (FAILED(settingHR) && settingHR != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) check(settingHR);
        if (FAILED(parseHR) && parseHR != HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) check(parseHR);
        if (now >= deadline) throw std::runtime_error("TEST Shell readiness budget expired; no Show");
        if (SUCCEEDED(settingHR) && setting != NotificationSetting::Enabled) return setting;
        if (recognized && SUCCEEDED(settingHR)) return setting;
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
    return 5;
}
static void cleanup() {
    // UUID marker + exact paths constrain removal; no process killing or shared registrations.
    bool registryOwned = ownRegistryProof(), shortcutOwned = ownShortcutProof();
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
    if (registryOwned || shortcutOwned) ToastNotificationManager::History().Clear(aumid);
    report("cleanup.json", "{\"ownRegistrationRemoved\":" + std::string(registryOwned ? "true" : "false")
        + ",\"ownShortcutRemoved\":" + (shortcutOwned ? "true" : "false") + "}\n");
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
        if (mode == L"callback" && (!ownRegistryProof() || registeredCommand() != serverCommand())) return 2;
        winrt::init_apartment(winrt::apartment_type::multi_threaded);
        if (mode == L"preflight") return preflight() ? 0 : 3;
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
