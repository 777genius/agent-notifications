// Explicit disposable Windows client CI fixture. No production route or registration.
#define NOMINMAX
#include <windows.h>
#include <appmodel.h>
#include <shobjidl.h>
#include <sddl.h>
#include <wincrypt.h>
#include <notificationactivationcallback.h>
#include <wrl/client.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Data.Json.h>
#include <winrt/Windows.Data.Xml.Dom.h>
#include <winrt/Windows.UI.Notifications.h>
#include <filesystem>
#include <fstream>
#include <string>
#include <vector>
#include <atomic>
#include <algorithm>
#include <iostream>

namespace fs = std::filesystem;
using Microsoft::WRL::ComPtr;
using namespace winrt::Windows::Data::Json;
using namespace winrt::Windows::UI::Notifications;
static fs::path root;
static std::wstring nonce, aumid, packageName, sid, executableHash;
static JsonObject spec{nullptr};
static GUID clsid{};
static std::atomic<bool> callbackSeen{false}, callbackDone{false}, callbackValid{false}, duplicateCallback{false};
static bool authorityReady = false, showArmed = false, showEntered = false, showReturned = false;

struct Handle {
    HANDLE value = nullptr;
    explicit Handle(HANDLE h) : value(h) {}
    ~Handle() { if (value && value != INVALID_HANDLE_VALUE) CloseHandle(value); }
    Handle(const Handle&) = delete;
    Handle& operator=(const Handle&) = delete;
};
static void require(bool condition, const char* reason) {
    if (!condition) throw std::runtime_error(reason);
}
static void check(HRESULT hr) { winrt::check_hresult(hr); }
static void put(JsonObject& obj, const wchar_t* key, const std::wstring& value) {
    obj.SetNamedValue(key, JsonValue::CreateStringValue(value));
}
static void put(JsonObject& obj, const wchar_t* key, bool value) {
    obj.SetNamedValue(key, JsonValue::CreateBooleanValue(value));
}
static void number(JsonObject& obj, const wchar_t* key, double value) {
    obj.SetNamedValue(key, JsonValue::CreateNumberValue(value));
}
static std::wstring text(const JsonObject& obj, const wchar_t* key) {
    auto value = obj.GetNamedString(key);
    require(value.size() > 0 && value.size() < 32768, "bounded string missing");
    return std::wstring(value);
}
static unsigned long long creation(HANDLE process) {
    FILETIME c{}, e{}, k{}, u{};
    require(GetProcessTimes(process, &c, &e, &k, &u) != FALSE, "process birth unavailable");
    ULARGE_INTEGER n{}; n.LowPart = c.dwLowDateTime; n.HighPart = c.dwHighDateTime;
    return n.QuadPart;
}
static std::wstring userSid(HANDLE process) {
    HANDLE raw{};
    require(OpenProcessToken(process, TOKEN_QUERY, &raw) != FALSE, "process user unavailable");
    Handle token(raw); DWORD length{};
    GetTokenInformation(token.value, TokenUser, nullptr, 0, &length);
    require(length > 0 && length < 65536, "bounded user token unavailable");
    std::vector<BYTE> buffer(length);
    require(GetTokenInformation(token.value, TokenUser, buffer.data(), length, &length) != FALSE,
            "user token read failed");
    LPWSTR converted{};
    require(ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(buffer.data())->User.Sid, &converted) != FALSE,
            "user SID conversion failed");
    std::wstring result(converted); LocalFree(converted); return result;
}
static DWORD session(DWORD pid) {
    DWORD value{}; require(ProcessIdToSessionId(pid, &value) != FALSE, "session unavailable"); return value;
}
static std::wstring hashFile(const fs::path& path) {
    require(fs::is_regular_file(path) && fs::file_size(path) <= 67108864, "bounded file unavailable");
    HCRYPTPROV provider{}; HCRYPTHASH hash{};
    require(CryptAcquireContextW(&provider, nullptr, nullptr, PROV_RSA_AES, CRYPT_VERIFYCONTEXT) != FALSE,
            "SHA256 provider unavailable");
    try {
        require(CryptCreateHash(provider, CALG_SHA_256, 0, 0, &hash) != FALSE, "SHA256 unavailable");
        std::ifstream input(path, std::ios::binary); require(input.good(), "hash input unavailable");
        char buffer[65536];
        while (input.read(buffer, sizeof buffer) || input.gcount()) {
            require(CryptHashData(hash, reinterpret_cast<const BYTE*>(buffer), static_cast<DWORD>(input.gcount()), 0)
                    != FALSE, "SHA256 update failed");
        }
        require(input.eof() && !input.bad(), "hash input read failed");
        BYTE digest[32]{}; DWORD size = sizeof digest;
        require(CryptGetHashParam(hash, HP_HASHVAL, digest, &size, 0) != FALSE && size == 32,
                "SHA256 finish failed");
        std::wstring result; const wchar_t* digits = L"0123456789abcdef";
        for (BYTE byte : digest) { result += digits[byte >> 4]; result += digits[byte & 15]; }
        CryptDestroyHash(hash); CryptReleaseContext(provider, 0); return result;
    } catch (...) { if (hash) CryptDestroyHash(hash); CryptReleaseContext(provider, 0); throw; }
}
static fs::path processImage(HANDLE process) {
    wchar_t buffer[32768]{}; DWORD size = 32768;
    require(QueryFullProcessImageNameW(process, 0, buffer, &size) != FALSE, "process executable unavailable");
    return fs::canonical(buffer);
}
static JsonObject load(const char* name) {
    fs::path path = root / name;
    require(fs::is_regular_file(path) && fs::file_size(path) < 16384, "bounded TEST record missing");
    require((GetFileAttributesW(path.c_str()) & FILE_ATTRIBUTE_REPARSE_POINT) == 0, "reparsed TEST record");
    std::ifstream input(path, std::ios::binary);
    std::string data((std::istreambuf_iterator<char>(input)), {});
    require(!input.bad(), "TEST record read failed");
    return JsonObject::Parse(winrt::to_hstring(data));
}
static void publish(const char* name, const JsonObject& record) {
    auto bytes = winrt::to_string(record.Stringify());
    fs::path final = root / name, temporary = final; temporary += L".tmp";
    Handle file(CreateFileW(temporary.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr));
    require(file.value != INVALID_HANDLE_VALUE, "exclusive record creation failed");
    DWORD written{};
    bool complete = WriteFile(file.value, bytes.data(), static_cast<DWORD>(bytes.size()), &written, nullptr)
                    && written == bytes.size() && FlushFileBuffers(file.value);
    CloseHandle(file.value); file.value = nullptr;
    if (!complete || !MoveFileExW(temporary.c_str(), final.c_str(), MOVEFILE_WRITE_THROUGH)) {
        DeleteFileW(temporary.c_str()); throw std::runtime_error("atomic exclusive record publish failed");
    }
}
static std::wstring packageOf(HANDLE process, bool current) {
    UINT32 length{};
    LONG rc = current ? GetCurrentPackageFullName(&length, nullptr) : GetPackageFullName(process, &length, nullptr);
    require(rc == ERROR_INSUFFICIENT_BUFFER && length > 1 && length < 1024, "package identity absent");
    std::vector<wchar_t> buffer(length);
    rc = current ? GetCurrentPackageFullName(&length, buffer.data()) : GetPackageFullName(process, &length, buffer.data());
    require(rc == ERROR_SUCCESS, "package identity read failed"); return buffer.data();
}
static std::wstring applicationOf(HANDLE process, bool current) {
    UINT32 length{};
    LONG rc = current ? GetCurrentApplicationUserModelId(&length, nullptr)
                      : GetApplicationUserModelId(process, &length, nullptr);
    require(rc == ERROR_INSUFFICIENT_BUFFER && length > 1 && length < 1024, "application identity absent");
    std::vector<wchar_t> buffer(length);
    rc = current ? GetCurrentApplicationUserModelId(&length, buffer.data())
                 : GetApplicationUserModelId(process, &length, buffer.data());
    require(rc == ERROR_SUCCESS, "application identity read failed"); return buffer.data();
}
static JsonObject identity(HANDLE process, DWORD pid, bool current) {
    require(packageOf(process, current) == packageName && applicationOf(process, current) == aumid,
            "runtime package/application identity mismatch");
    require(userSid(process) == sid && session(pid) == static_cast<DWORD>(spec.GetNamedNumber(L"session")),
            "runtime user/session mismatch");
    auto image = processImage(process);
    require(image == fs::canonical(text(spec, L"installedExecutable")) && hashFile(image) == executableHash,
            "runtime installed executable mismatch");
    JsonObject record; number(record, L"pid", pid); put(record, L"nonce", nonce);
    put(record, L"creationTicks", std::to_wstring(creation(process)));
    put(record, L"packageFullName", packageName); put(record, L"aumid", aumid);
    put(record, L"userSid", sid); number(record, L"session", session(pid));
    put(record, L"executableSHA256", executableHash); return record;
}
static JsonObject ownIdentity() { return identity(GetCurrentProcess(), GetCurrentProcessId(), true); }
struct OwnedProcessScope {
    HANDLE process;
    const char* evidenceName;
    JsonObject owner;
    bool complete = false;
    // Construct only after package, executable, user, session and incarnation checks.
    OwnedProcessScope(HANDLE handle, const char* name, const JsonObject& record)
        : process(handle), evidenceName(name), owner(record) {}
    ~OwnedProcessScope() noexcept {
        if (complete) return;
        try {
            DWORD state = WaitForSingleObject(process, 0);
            bool terminated = false;
            if (state == WAIT_TIMEOUT) {
                terminated = TerminateProcess(process, 125) != FALSE;
                state = WaitForSingleObject(process, 3000);
            }
            DWORD code = STILL_ACTIVE;
            bool collected = state == WAIT_OBJECT_0 && GetExitCodeProcess(process, &code) != FALSE;
            put(owner, L"failureCleanup", true); put(owner, L"terminateReturned", terminated);
            put(owner, L"collected", collected); number(owner, L"waitResult", state);
            number(owner, L"exitCode", code); publish(evidenceName, owner);
        } catch (...) { /* Missing cleanup evidence remains failure, never a pass or resend. */ }
    }
};
static void showState(const char* name, HRESULT hr = S_OK) {
    JsonObject record; number(record, L"pid", GetCurrentProcessId()); put(record, L"nonce", nonce);
    put(record, L"showCallEntered", showEntered); put(record, L"showCallReturned", showReturned);
    put(record, L"showBoundaryArmed", showArmed);
    number(record, L"hresult", hr); publish(name, record);
}
static int sendToast() {
    auto ready = ownIdentity(); publish("identity-ready.json", ready);
    auto end = GetTickCount64() + 15000;
    while (!fs::exists(root / "show-permit.json") && GetTickCount64() < end) Sleep(25);
    require(GetTickCount64() < end, "sender Show permit expired");
    auto permit = load("show-permit.json");
    require(text(permit, L"nonce") == nonce && permit.GetNamedNumber(L"pid") == GetCurrentProcessId()
            && text(permit, L"creationTicks") == std::to_wstring(creation(GetCurrentProcess())),
            "Show permit identity mismatch");
    ownIdentity();
    JsonObject factoryBefore; put(factoryBefore, L"createBoundaryArmed", true);
    publish("factory-before.json", factoryBefore);
    auto notifier = ToastNotificationManager::CreateToastNotifier(aumid);
    JsonObject factoryAfter; put(factoryAfter, L"createReturned", true); publish("factory-after.json", factoryAfter);
    auto started = GetTickCount64(); JsonArray reads; bool enabled = false;
    for (unsigned i = 0; i < 16 && GetTickCount64() - started < 3000; ++i) {
        JsonObject item; HRESULT hr = S_OK; int setting = -1;
        try { setting = static_cast<int>(notifier.Setting()); } catch (const winrt::hresult_error& error) { hr = error.code(); }
        auto elapsed = GetTickCount64() - started;
        number(item, L"elapsedMs", static_cast<double>(elapsed)); number(item, L"setting", setting);
        number(item, L"hresult", hr); reads.Append(item);
        std::string readName = "readiness-read-" + std::to_string(i) + ".json";
        publish(readName.c_str(), item);
        if (elapsed >= 3000) break;
        if (SUCCEEDED(hr) && setting == static_cast<int>(NotificationSetting::Enabled)) { enabled = true; break; }
        Sleep(static_cast<DWORD>((std::min)(200ULL, 3000ULL - elapsed)));
    }
    JsonObject readiness; readiness.SetNamedValue(L"reads", reads); put(readiness, L"enabled", enabled);
    publish("readiness.json", readiness);
    require(enabled, "native Setting is not Enabled within readiness budget");
    std::wstring xml = L"<toast launch='" + nonce + L"'><visual><binding template='ToastGeneric'><text>Navigation TEST "
        + nonce + L"</text><text>Synthetic packaged CI lifecycle probe</text></binding></visual><actions><action content='TEST open "
        + nonce + L"' arguments='" + nonce + L"' activationType='foreground'/></actions><audio silent='true'/></toast>";
    winrt::Windows::Data::Xml::Dom::XmlDocument document; document.LoadXml(xml);
    ToastNotification toast(document); toast.Tag(nonce.substr(0, 16)); toast.Group(L"NavigationTEST");
    // An armed boundary is conservative effect uncertainty, not proof Show was entered.
    // The controller must never infer Show0 from an armed boundary without a terminal outcome.
    showArmed = true; showState("show-boundary.json"); showEntered = true;
    notifier.Show(toast); showReturned = true; showState("show-outcome.json"); return 0;
}
static int activateSender() {
    ComPtr<IApplicationActivationManager> manager;
    check(CoCreateInstance(CLSID_ApplicationActivationManager, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&manager)));
    DWORD pid{}; std::wstring args = L"send \"" + root.wstring() + L"\" " + nonce;
    HRESULT hr = manager->ActivateApplication(aumid.c_str(), args.c_str(), AO_NOERRORUI, &pid);
    // Diagnostic only: no authority to terminate this raw PID. Flush before ownership checks.
    std::cerr << "activation returned HRESULT " << hr << " PID " << pid << std::endl;
    JsonObject invocation; number(invocation, L"hresult", hr); number(invocation, L"pid", pid);
    if (FAILED(hr) || pid == 0) { publish("activation-api.json", invocation); check(hr); throw std::runtime_error("activation PID absent"); }
    Handle process(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE | PROCESS_TERMINATE, FALSE, pid));
    require(process.value != nullptr, "activated sender handle unavailable");
    auto retained = identity(process.value, pid, false);
    OwnedProcessScope cleanup(process.value, "sender-failure-cleanup.json", retained);
    publish("activation-api.json", invocation); publish("sender-retained.json", retained);
    auto end = GetTickCount64() + 12000;
    while (!fs::exists(root / "identity-ready.json") && GetTickCount64() < end) {
        require(WaitForSingleObject(process.value, 0) == WAIT_TIMEOUT, "sender exited before permit"); Sleep(25);
    }
    require(GetTickCount64() < end, "sender identity-ready expired");
    auto ready = load("identity-ready.json");
    require(ready.GetNamedNumber(L"pid") == retained.GetNamedNumber(L"pid")
            && ready.GetNamedNumber(L"session") == retained.GetNamedNumber(L"session"),
            "sender identity-ready numeric fields disagree with retained process");
    for (const wchar_t* key : {L"nonce", L"creationTicks", L"packageFullName", L"aumid", L"userSid", L"executableSHA256"}) {
        require(text(ready, key) == text(retained, key), "sender identity-ready disagrees with retained process");
    }
    identity(process.value, pid, false); publish("show-permit.json", retained);
    require(WaitForSingleObject(process.value, 20000) == WAIT_OBJECT_0, "sender exit not collected");
    FILETIME collectedUtc{}; GetSystemTimePreciseAsFileTime(&collectedUtc);
    auto collectedBoot = GetTickCount64();
    DWORD code{}; require(GetExitCodeProcess(process.value, &code) != FALSE, "sender exit code unavailable");
    FILETIME c{}, exited{}, k{}, u{};
    require(GetProcessTimes(process.value, &c, &exited, &k, &u) != FALSE, "sender exit time unavailable");
    ULARGE_INTEGER ticks{}; ticks.LowPart = exited.dwLowDateTime; ticks.HighPart = exited.dwHighDateTime;
    ULARGE_INTEGER collected{}; collected.LowPart = collectedUtc.dwLowDateTime; collected.HighPart = collectedUtc.dwHighDateTime;
    number(retained, L"exitCode", code); put(retained, L"exitTicks", std::to_wstring(ticks.QuadPart));
    put(retained, L"collectedUtcTicks", std::to_wstring(collected.QuadPart));
    number(retained, L"collectedBootMs", static_cast<double>(collectedBoot));
    put(retained, L"collected", true); publish("sender-exit.json", retained);
    require(code == 0, "activated sender failed"); cleanup.complete = true; return 0;
}
class Callback final : public INotificationActivationCallback {
    std::atomic<ULONG> refs{1};
public:
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID iid, void** out) override {
        if (!out) return E_POINTER;
        *out = nullptr;
        if (iid != IID_IUnknown && iid != __uuidof(INotificationActivationCallback)) return E_NOINTERFACE;
        *out = static_cast<INotificationActivationCallback*>(this); AddRef(); return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { ULONG n = --refs; if (!n) delete this; return n; }
    HRESULT STDMETHODCALLTYPE Activate(LPCWSTR app, LPCWSTR args, const NOTIFICATION_USER_INPUT_DATA*, ULONG count) override {
        if (callbackSeen.exchange(true)) {
            duplicateCallback = true;
            try { JsonObject rejection; put(rejection, L"duplicate", true); publish("callback-duplicate.json", rejection); } catch (...) {}
            return E_UNEXPECTED;
        }
        try {
            require(app && args && wcsnlen_s(app, 1024) < 1024 && wcsnlen_s(args, 37) == 36
                    && app == aumid && args == nonce && count == 0, "native callback target mismatch");
            auto record = ownIdentity(); put(record, L"targetMatches", true);
            publish("callback.json", record); publish("effect.json", record);
            callbackValid = true; callbackDone = true; return S_OK;
        } catch (...) { callbackDone = true; return E_FAIL; }
    }
};
class Factory final : public IClassFactory {
    std::atomic<ULONG> refs{1};
public:
    HRESULT STDMETHODCALLTYPE QueryInterface(REFIID iid, void** out) override {
        if (!out) return E_POINTER;
        *out = nullptr;
        if (iid != IID_IUnknown && iid != IID_IClassFactory) return E_NOINTERFACE;
        *out = static_cast<IClassFactory*>(this); AddRef(); return S_OK;
    }
    ULONG STDMETHODCALLTYPE AddRef() override { return ++refs; }
    ULONG STDMETHODCALLTYPE Release() override { ULONG n = --refs; if (!n) delete this; return n; }
    HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown* outer, REFIID iid, void** out) override {
        if (outer) return CLASS_E_NOAGGREGATION;
        Callback* object = new Callback; HRESULT hr = object->QueryInterface(iid, out); object->Release(); return hr;
    }
    HRESULT STDMETHODCALLTYPE LockServer(BOOL) override { return S_OK; }
};
static int callbackServer() {
    auto started = ownIdentity(); number(started, L"observedBootMs", static_cast<double>(GetTickCount64()));
    publish("callback-started.json", started);
    Factory* factory = new Factory; DWORD cookie{};
    HRESULT hr = CoRegisterClassObject(clsid, factory, CLSCTX_LOCAL_SERVER, REGCLS_MULTIPLEUSE, &cookie);
    factory->Release(); check(hr);
    auto end = GetTickCount64() + 30000;
    while (!callbackDone && GetTickCount64() < end) Sleep(25);
    // Keep the genuine OS-created process available for the controller to retain its exact handle.
    while (callbackValid && !fs::exists(root / "callback-exit-permit.json") && GetTickCount64() < end) Sleep(25);
    bool permitted = false;
    if (callbackValid && fs::exists(root / "callback-exit-permit.json")) {
        auto permit = load("callback-exit-permit.json");
        permitted = text(permit, L"nonce") == nonce && permit.GetNamedNumber(L"pid") == GetCurrentProcessId()
                    && text(permit, L"creationTicks") == std::to_wstring(creation(GetCurrentProcess()));
    }
    CoRevokeClassObject(cookie);
    JsonObject exit; put(exit, L"valid", callbackValid.load() && !duplicateCallback.load() && permitted);
    put(exit, L"exitPermitValidated", permitted);
    put(exit, L"duplicate", duplicateCallback.load()); publish("callback-terminal.json", exit);
    return callbackValid && !duplicateCallback && permitted ? 0 : 4;
}
static unsigned long long ticks(const JsonObject& obj, const wchar_t* key) {
    auto value = text(obj, key);
    require(value.size() <= 20 && value.find_first_not_of(L"0123456789") == std::wstring::npos,
            "invalid bounded process ticks");
    return std::stoull(value);
}
static int collectCallback() {
    auto started = load("callback-started.json"), sender = load("sender-exit.json");
    double numericPid = started.GetNamedNumber(L"pid");
    require(numericPid > 0 && numericPid <= MAXDWORD && numericPid == static_cast<DWORD>(numericPid),
            "invalid callback PID");
    DWORD pid = static_cast<DWORD>(numericPid);
    require(pid != sender.GetNamedNumber(L"pid") && pid != GetCurrentProcessId()
            && sender.GetNamedBoolean(L"collected") && sender.GetNamedNumber(L"exitCode") == 0,
            "cold callback sender-death evidence missing");
    Handle process(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE | PROCESS_TERMINATE, FALSE, pid));
    require(process.value != nullptr, "callback handle unavailable");
    auto retained = identity(process.value, pid, false);
    require(ticks(retained, L"creationTicks") == ticks(started, L"creationTicks"), "callback incarnation mismatch");
    OwnedProcessScope cleanup(process.value, "callback-failure-cleanup.json", retained);
    require(ticks(retained, L"creationTicks") >= ticks(sender, L"collectedUtcTicks")
            && ticks(retained, L"creationTicks") > ticks(sender, L"exitTicks")
            && started.GetNamedNumber(L"observedBootMs") >= sender.GetNamedNumber(L"collectedBootMs"),
            "callback incarnation is not cold after collected sender exit");
    publish("callback-retained.json", retained);
    auto callback = load("callback.json"), effect = load("effect.json");
    require(callback.GetNamedBoolean(L"targetMatches") && effect.GetNamedBoolean(L"targetMatches")
            && callback.GetNamedNumber(L"pid") == pid && effect.GetNamedNumber(L"pid") == pid,
            "callback effect identity mismatch");
    for (const wchar_t* key : {L"nonce", L"creationTicks", L"packageFullName", L"aumid", L"userSid", L"executableSHA256"}) {
        require(text(callback, key) == text(retained, key) && text(effect, key) == text(retained, key),
                "callback effect disagrees with retained process");
    }
    require(WaitForSingleObject(process.value, 0) == WAIT_TIMEOUT, "callback exited before handle retention");
    publish("callback-exit-permit.json", retained);
    require(WaitForSingleObject(process.value, 5000) == WAIT_OBJECT_0, "callback exit not collected");
    DWORD code{}; require(GetExitCodeProcess(process.value, &code) != FALSE, "callback exit code unavailable");
    number(retained, L"exitCode", code); put(retained, L"collected", true); publish("callback-exit.json", retained);
    auto terminal = load("callback-terminal.json");
    require(code == 0 && terminal.GetNamedBoolean(L"valid") && terminal.GetNamedBoolean(L"exitPermitValidated")
            && !terminal.GetNamedBoolean(L"duplicate") && !fs::exists(root / "callback-duplicate.json"),
            "callback terminal outcome failed");
    cleanup.complete = true; return 0;
}
static void stopOwned(const char* inputName, const char* outputName) {
    JsonObject result; put(result, L"recordPresent", fs::exists(root / inputName));
    if (!fs::exists(root / inputName)) { publish(outputName, result); return; }
    auto saved = load(inputName);
    require(text(saved, L"nonce") == nonce && text(saved, L"packageFullName") == packageName
            && text(saved, L"aumid") == aumid && text(saved, L"userSid") == sid
            && text(saved, L"executableSHA256") == executableHash, "cleanup ownership record mismatch");
    double numericPid = saved.GetNamedNumber(L"pid");
    require(numericPid > 0 && numericPid <= MAXDWORD && numericPid == static_cast<DWORD>(numericPid)
            && numericPid != GetCurrentProcessId(), "cleanup PID invalid");
    DWORD pid = static_cast<DWORD>(numericPid);
    Handle process(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE | PROCESS_TERMINATE, FALSE, pid));
    if (!process.value) {
        require(GetLastError() == ERROR_INVALID_PARAMETER, "cleanup process visibility unknown");
        put(result, L"originalAbsent", true); publish(outputName, result); return;
    }
    if (creation(process.value) != ticks(saved, L"creationTicks")) {
        // A reused PID is not our original process and must never be terminated.
        put(result, L"originalAbsent", true); put(result, L"pidReused", true);
        publish(outputName, result); return;
    }
    DWORD state = WaitForSingleObject(process.value, 0);
    auto retained = state == WAIT_OBJECT_0 ? saved : identity(process.value, pid, false);
    bool terminated = false;
    if (state == WAIT_TIMEOUT) {
        require(TerminateProcess(process.value, 125) != FALSE, "owned process termination failed");
        terminated = true; state = WaitForSingleObject(process.value, 3000);
    }
    DWORD code{};
    require(state == WAIT_OBJECT_0 && GetExitCodeProcess(process.value, &code) != FALSE,
            "owned process cleanup not collected");
    put(retained, L"collected", true); put(retained, L"terminateReturned", terminated);
    number(retained, L"exitCode", code); publish(outputName, retained);
}
int wmain(int argc, wchar_t** argv) {
    std::wstring mode;
    try {
        require(argc == 4 || (argc == 5 && std::wstring(argv[4]) == L"-Embedding"), "invalid bounded TEST arguments");
        mode = argv[1]; require(mode == L"send" || mode == L"callback" || mode == L"activate" || mode == L"cleanup" || mode == L"collect" || mode == L"history",
                               "unknown TEST mode");
        // OS-created packaged processes need not inherit the CI controller's environment.
        // Sender authority comes from package identity and the retained-process Show permit.
        if (mode != L"callback" && mode != L"send") {
            wchar_t gate[8]{}, ci[8]{}, actions[8]{};
            require(GetEnvironmentVariableW(L"AGENT_NOTIFY_NAVIGATION_WINDOWS_E2E", gate, 8) && std::wstring(gate) == L"1"
                    && GetEnvironmentVariableW(L"CI", ci, 8) && std::wstring(ci) == L"true"
                    && GetEnvironmentVariableW(L"GITHUB_ACTIONS", actions, 8) && std::wstring(actions) == L"true",
                    "explicit disposable CI opt-in missing");
        }
        nonce = argv[3];
        require(nonce.size() == 36 && nonce.find_first_not_of(L"0123456789abcdef-") == std::wstring::npos,
                "invalid TEST nonce");
        check(CLSIDFromString((L"{" + nonce + L"}").c_str(), &clsid));
        require((GetFileAttributesW(argv[2]) & FILE_ATTRIBUTE_REPARSE_POINT) == 0, "reparsed TEST root");
        root = fs::canonical(argv[2]); require(root.filename() == L"navigation-windows-test-" + nonce, "TEST root mismatch");
        std::ifstream marker(root / ".owned-test-root"); std::string markerText; std::getline(marker, markerText);
        require(markerText == "TEST navigation Windows " + winrt::to_string(nonce), "TEST root marker mismatch");
        winrt::init_apartment(winrt::apartment_type::multi_threaded);
        spec = load("msix-spec.json"); require(text(spec, L"nonce") == nonce, "immutable spec mismatch");
        aumid = text(spec, L"aumid"); packageName = text(spec, L"packageFullName"); sid = text(spec, L"userSid");
        executableHash = text(spec, L"executableSHA256");
        require(userSid(GetCurrentProcess()) == sid && session(GetCurrentProcessId()) == spec.GetNamedNumber(L"session"),
                "controller user/session mismatch");
        if (mode == L"send" || mode == L"callback") ownIdentity();
        else require(processImage(GetCurrentProcess()) == root / "navigation-msix-probe.exe"
                     && hashFile(processImage(GetCurrentProcess())) == executableHash, "controller binary mismatch");
        authorityReady = true;
        if (mode == L"send") return sendToast();
        if (mode == L"activate") return activateSender();
        if (mode == L"callback") return callbackServer();
        if (mode == L"collect") return collectCallback();
        if (mode == L"history") {
            JsonObject observation; put(observation, L"diagnosticOnly", true);
            try {
                auto entries = ToastNotificationManager::History().GetHistory(aumid);
                number(observation, L"entryCount", entries.Size());
                require(entries.Size() <= 64, "bounded owned notification history exceeded");
                unsigned matches = 0;
                for (const auto& toast : entries) {
                    if (toast.Tag() == nonce.substr(0, 16) && toast.Group() == L"NavigationTEST") ++matches;
                }
                number(observation, L"ownedTagGroupMatches", matches);
                number(observation, L"HRESULT", 0);
            } catch (const winrt::hresult_error& error) {
                number(observation, L"HRESULT", error.code().value);
            } catch (const std::exception&) {
                put(observation, L"boundedObservationFailed", true);
            }
            publish("notification-history.json", observation);
            return 0;
        }
        bool failed = false;
        // Cleanup branches are independent. A failure never skips another owned branch.
        try { stopOwned("sender-retained.json", "sender-cleanup.json"); }
        catch (...) { failed = true; std::cerr << "sender cleanup failed" << std::endl; }
        try { stopOwned("callback-started.json", "callback-cleanup.json"); }
        catch (...) { failed = true; std::cerr << "callback cleanup failed" << std::endl; }
        try {
            ToastNotificationManager::History().Remove(nonce.substr(0, 16), L"NavigationTEST", aumid);
            JsonObject cleanup; put(cleanup, L"returned", true); publish("notification-cleanup.json", cleanup);
        } catch (...) { failed = true; std::cerr << "notification cleanup failed" << std::endl; }
        return failed ? 1 : 0;
    } catch (const winrt::hresult_error& error) {
        if (authorityReady && mode == L"send") { try { showState("sender-failure.json", error.code()); } catch (...) {} }
        std::cerr << "HRESULT " << error.code().value << '\n'; return 1;
    } catch (const std::exception& error) {
        if (authorityReady && mode == L"send") { try { showState("sender-failure.json", E_FAIL); } catch (...) {} }
        std::cerr << error.what() << '\n'; return 1;
    }
}
