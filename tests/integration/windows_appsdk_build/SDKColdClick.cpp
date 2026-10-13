// One disposable TEST generation; SDK registration is independent of the classic COM fixture.
#include "SDKObservationTEST.h"
#include "../navigation_windows_vendor_sdk_test.h"
#include <winrt/Windows.Data.Json.h>
#include <winrt/Microsoft.Windows.AppNotifications.h>
#include <winrt/Microsoft.Windows.AppLifecycle.h>
#include <uiautomation.h>
#include <wtsapi32.h>
#include <wrl/client.h>
#include <filesystem>
#include <atomic>
#include <mutex>
#include <algorithm>
#include <thread>
#include <memory>
using winrt::Windows::Data::Json::JsonObject;
using winrt::Windows::Data::Json::JsonValue;
using Microsoft::WRL::ComPtr;
namespace fs = std::filesystem;
static constexpr wchar_t frameworkFull[] = L"Microsoft.WindowsAppRuntime.2_2.5.1.0_arm64__8wekyb3d8bbwe";
static std::wstring root, nonce, image;
static ULONGLONG lease = 0;
static void demand(bool ok, const char* name) { if (!ok) throw Failure{name, ERROR_INVALID_DATA}; }
static void put(JsonObject& j, const wchar_t* key, const std::wstring& value) { j.Insert(key, JsonValue::CreateStringValue(value)); }
static void put(JsonObject& j, const wchar_t* key, const wchar_t* value) { put(j, key, std::wstring(value)); }
static void put(JsonObject& j, const wchar_t* key, bool value) { j.Insert(key, JsonValue::CreateBooleanValue(value)); }
static void num(JsonObject& j, const wchar_t* key, double value) { j.Insert(key, JsonValue::CreateNumberValue(value)); }
static void budget(ULONGLONG limit) { demand(GetTickCount64() < limit, "AbsoluteDeadline"); }
static std::wstring birth(HANDLE process) {
    FILETIME b{}, e{}, k{}, u{}; require(GetProcessTimes(process, &b, &e, &k, &u) != FALSE, "ProcessBirth");
    ULARGE_INTEGER n{}; n.LowPart = b.dwLowDateTime; n.HighPart = b.dwHighDateTime; return std::to_wstring(n.QuadPart);
}
static JsonObject record(const wchar_t* phase) {
    JsonObject j; put(j, L"nonce", nonce); put(j, L"phase", std::wstring(phase)); num(j, L"pid", GetCurrentProcessId());
    put(j, L"birth", birth(GetCurrentProcess())); num(j, L"bootMs", static_cast<double>(GetTickCount64())); return j;
}
static void publish(const wchar_t* leaf, const JsonObject& j) {
    const std::wstring final = root + L"\\" + leaf;
    const std::wstring pending = final + L".pending";
    // CREATE_NEW reserves this receipt; durable closes it before final becomes visible.
    durable(pending, winrt::to_string(j.Stringify()));
    // Same-directory move only: never replace, copy across volumes, retry, or remove a failed reservation.
    require(MoveFileExW(pending.c_str(), final.c_str(), 0) != FALSE, "PublishOwnPhaseEvidence");
}
static JsonObject read(const wchar_t* leaf) {
    const auto path = root + L"\\" + leaf; regular(path); File f;
    f.h = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING, FILE_FLAG_OPEN_REPARSE_POINT, nullptr);
    require(f.h != INVALID_HANDLE_VALUE, "OwnReceiptOpen");
    BY_HANDLE_FILE_INFORMATION i{}; require(GetFileInformationByHandle(f.h, &i) != FALSE, "OwnReceiptIdentity");
    demand(!(i.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) && i.nNumberOfLinks == 1 && !i.nFileSizeHigh && i.nFileSizeLow <= 8192, "OwnReceiptBound");
    std::string bytes(i.nFileSizeLow, '\0'); DWORD got = 0;
    require(ReadFile(f.h, bytes.data(), static_cast<DWORD>(bytes.size()), &got, nullptr) != FALSE, "OwnReceiptRead");
    demand(got == bytes.size(), "OwnReceiptLength"); return JsonObject::Parse(winrt::to_hstring(bytes));
}
static bool exists(const wchar_t* leaf) { return GetFileAttributesW((root + L"\\" + leaf).c_str()) != INVALID_FILE_ATTRIBUTES; }
static JsonObject ownToken(HANDLE process = GetCurrentProcess()) {
    Token t; require(OpenProcessToken(process, TOKEN_QUERY | TOKEN_DUPLICATE, &t.handle) != FALSE, "ProcessToken");
    const auto f = facts(t.handle); DWORD session = 0;
    require(ProcessIdToSessionId(GetProcessId(process), &session) != FALSE, "ProcessSession"); demand(session == f.session, "TokenSession");
    auto j = JsonObject::Parse(winrt::to_hstring(factsJson(f))); put(j, L"enabledAdmins", enabledAdmins(t.handle)); return j;
}
static void sameIdentity(const JsonObject& a, const JsonObject& b) {
    for (auto key : {L"sidSHA256", L"authLUIDSHA256"}) demand(a.GetNamedString(key) == b.GetNamedString(key), "IdentityDigest");
    demand(a.GetNamedNumber(L"session") == b.GetNamedNumber(L"session"), "IdentitySession");
}
static bool medium(const JsonObject& t) { return !t.GetNamedBoolean(L"elevated") && t.GetNamedNumber(L"integrityRID") == 0x2000 && !t.GetNamedBoolean(L"enabledAdmins"); }
struct Binding {
    File held; JsonObject value; std::string sha;
    Binding() {
        const auto path = root + L"\\TEST-cold-binding.json"; regular(path);
        held.h = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING, FILE_FLAG_OPEN_REPARSE_POINT, nullptr);
        require(held.h != INVALID_HANDLE_VALUE, "HoldImmutableBinding");
        value = read(L"TEST-cold-binding.json"); sha = hashFile(path);
        demand(value.Size() == 9 && value.GetNamedString(L"nonce") == nonce &&
            value.GetNamedString(L"exeSHA256") == winrt::to_hstring(hashFile(image)) &&
            value.GetNamedString(L"familyName") == NavigationVendorTEST::vendorFamily &&
            value.GetNamedString(L"fullName") == NavigationVendorTEST::vendorFull &&
            value.GetNamedString(L"uri") == L"codex://threads/" + nonce &&
            value.GetNamedNumber(L"leaseMs") == 65000, "FixedImmutableBinding");
        sameIdentity(value.GetNamedObject(L"identity"), ownToken());
    }
    void stable() const { demand(hashFile(root + L"\\TEST-cold-binding.json") == sha && hashFile(image) == winrt::to_string(value.GetNamedString(L"exeSHA256")), "BindingChanged"); }
};
static void bootstrap(Bootstrap& state, JsonObject& j, const Binding& binding) {
    binding.stable(); const auto pins = modulePins(root);
    demand(pins[0] == winrt::to_string(binding.value.GetNamedString(L"bootstrapSHA256")) &&
        pins[1] == winrt::to_string(binding.value.GetNamedString(L"runtimeSHA256")), "ModuleBindingPins");
    j.Insert(L"bootstrapModule", JsonObject::Parse(winrt::to_hstring(module(GetModuleHandleW(L"Microsoft.WindowsAppRuntime.Bootstrap.dll"), root + L"\\Microsoft.WindowsAppRuntime.Bootstrap.dll", pins[0]))));
    PACKAGE_VERSION minimum{}; minimum.Version = WINDOWSAPPSDK_RUNTIME_VERSION_UINT64;
    num(j, L"bootstrapCallBootMs", static_cast<double>(GetTickCount64())); budget(lease);
    const HRESULT hr = MddBootstrapInitialize2(WINDOWSAPPSDK_RELEASE_MAJORMINOR, WINDOWSAPPSDK_RELEASE_VERSION_TAG_W, minimum, MddBootstrapInitializeOptions_None);
    num(j, L"bootstrapHRESULT", static_cast<DWORD>(hr)); winrt::check_hresult(hr); state.initialized = true;
    const auto framework = selectedFramework(frameworkFull); put(j, L"selectedFramework", std::wstring(frameworkFull));
    demand(hashFile(framework + L"\\Microsoft.WindowsAppRuntime.dll") == pins[1], "SelectedPayloadPin");
    const bool supported = winrt::Microsoft::Windows::AppNotifications::AppNotificationManager::IsSupported(); put(j, L"isSupported", supported);
    j.Insert(L"runtimeModule", JsonObject::Parse(winrt::to_hstring(module(GetModuleHandleW(L"Microsoft.WindowsAppRuntime.dll"), framework + L"\\Microsoft.WindowsAppRuntime.dll", pins[1]))));
    demand(supported, "SDKIsSupportedFalse"); budget(lease);
}
// Audit reference: SDK source 13160d..., not a source-to-loaded-DLL attestation.
// Only read the existing SDK path identity; Default() itself is not a zero-effects API.
static void registryCheck(LSTATUS status, const char* name) { if (status != ERROR_SUCCESS) throw Failure{name, static_cast<DWORD>(status)}; }
static JsonObject registryIdentity() {
    std::wstring key = image; std::replace(key.begin(), key.end(), L'\\', L'.');
    key = L"Software\\Classes\\AppUserModelId\\" + key;
    HKEY handle = nullptr; registryCheck(RegOpenKeyExW(HKEY_CURRENT_USER, key.c_str(), 0, KEY_READ, &handle), "ExistingSDKPathKey");
    struct CloseKey { HKEY h; ~CloseKey() { RegCloseKey(h); } } close{handle};
    DWORD children = 0, values = 0; FILETIME before{}, after{};
    registryCheck(RegQueryInfoKeyW(handle, nullptr, nullptr, nullptr, &children, nullptr, nullptr, &values, nullptr, nullptr, nullptr, &before), "SDKPathKeyMetadata");
    demand(children == 0 && values == 1, "BoundedSDKPathIdentity");
    wchar_t value[40]{}; DWORD type = 0, bytes = sizeof(value);
    registryCheck(RegQueryValueExW(handle, L"NotificationGUID", nullptr, &type, reinterpret_cast<BYTE*>(value), &bytes), "ExistingNotificationGUID");
    demand(type == REG_SZ && (bytes == 76 || bytes == 78) && value[38] == 0, "NotificationGUIDShape");
    GUID guid{}; require(CLSIDFromString(value, &guid) == S_OK, "NotificationGUIDParse");
    registryCheck(RegQueryInfoKeyW(handle, nullptr, nullptr, nullptr, nullptr, nullptr, nullptr, nullptr, nullptr, nullptr, nullptr, &after), "SDKPathKeyReadback");
    demand(before.dwLowDateTime == after.dwLowDateTime && before.dwHighDateTime == after.dwHighDateTime, "SDKPathIdentityChangedDuringRead");
    JsonObject result; put(result, L"pathKey", key); put(result, L"notificationGUID", value);
    ULARGE_INTEGER tick{}; tick.LowPart = after.dwLowDateTime; tick.HighPart = after.dwHighDateTime;
    put(result, L"lastWrite", std::to_wstring(tick.QuadPart)); num(result, L"valueBytes", bytes); num(result, L"values", values); num(result, L"subkeys", children);
    put(result, L"guidBytesSHA256", winrt::to_hstring(digest(reinterpret_cast<BYTE*>(value), bytes)).c_str()); return result;
}
static std::wstring payloadDigest(const winrt::hstring& payload) {
    demand(payload.size() <= 8192, "HistoryPayloadProjectionBound");
    const auto utf8 = winrt::to_string(payload); demand(utf8.size() <= 8192, "HistoryPayloadUTF8Bound");
    return std::wstring(winrt::to_hstring(digest(reinterpret_cast<const BYTE*>(utf8.data()), static_cast<DWORD>(utf8.size()))));
}
static ULONGLONG historyDeadline() {
    const auto request = read(L"TEST-history-request.json"), sender = read(L"TEST-sender-collected.json");
    demand(request.Size() == 3 && request.GetNamedString(L"nonce") == nonce && sender.GetNamedString(L"nonce") == nonce &&
        sender.GetNamedBoolean(L"collected") && sender.GetNamedNumber(L"exitCode") == 0, "HistoryCollectedSender");
    const double deadline = request.GetNamedNumber(L"deadlineBootMs");
    demand(deadline == sender.GetNamedNumber(L"collectedBootMs") + 30000 && deadline > GetTickCount64() && deadline <= GetTickCount64() + 30000 &&
        request.GetNamedBoolean(L"enabled"), "SharedHistoryUIBudget");
    return static_cast<ULONGLONG>(deadline);
}
static int history() {
    lease = historyDeadline(); Binding binding; auto result = record(L"history"); num(result, L"deadlineBootMs", static_cast<double>(lease));
    result.Insert(L"token", ownToken()); put(result, L"physicalImage", image); put(result, L"exeSHA256", binding.value.GetNamedString(L"exeSHA256").c_str());
    bool shutdown = false; Bootstrap state(shutdown);
    winrt::Microsoft::Windows::AppNotifications::AppNotificationManager manager{nullptr};
    winrt::Windows::Foundation::IAsyncOperation<winrt::Windows::Foundation::Collections::IVector<winrt::Microsoft::Windows::AppNotifications::AppNotification>> operation{nullptr};
    struct NoUnwind { bool completedPublication = false; ~NoUnwind() noexcept { if (!completedPublication) ExitProcess(1); } } noUnwind;
    bool pending = false;
    // The TEST actor self-exits if synchronous SDK entry or cleanup exceeds the immutable lease.
    std::thread([deadline = lease] { while (GetTickCount64() < deadline) Sleep(10); TerminateProcess(GetCurrentProcess(), 124); }).detach();
    try {
        const auto sender = read(L"TEST-sender.json"); demand(sender.GetNamedString(L"nonce") == nonce && sender.GetNamedBoolean(L"unregisterReturned"), "HistorySenderIdentity");
        demand(medium(result.GetNamedObject(L"token")), "HistoryMediumRequired"); sameIdentity(result.GetNamedObject(L"token"), sender.GetNamedObject(L"token"));
        num(result, L"senderID", sender.GetNamedNumber(L"nativeID")); put(result, L"expectedPayloadSHA256", sender.GetNamedString(L"payloadSHA256").c_str());
        const auto before = registryIdentity(); result.Insert(L"registryBefore", before);
        demand(before.Stringify() == sender.GetNamedObject(L"registryIdentity").Stringify(), "HistoryExistingSenderGUID");
        publish(L"TEST-history-intent.json", result); budget(lease); bootstrap(state, result, binding); budget(lease);
        manager = winrt::Microsoft::Windows::AppNotifications::AppNotificationManager::Default(); budget(lease);
        num(result, L"queryCallBootMs", static_cast<double>(GetTickCount64())); pending = true; operation = manager.GetAllAsync();
        while (operation.Status() == winrt::Windows::Foundation::AsyncStatus::Started && GetTickCount64() < lease) Sleep(10);
        budget(lease); num(result, L"asyncStatus", static_cast<int>(operation.Status()));
        demand(operation.Status() == winrt::Windows::Foundation::AsyncStatus::Completed, "HistoryAsyncNotCompleted");
        const auto notifications = operation.GetResults(); pending = false; num(result, L"queryReturnedBootMs", static_cast<double>(GetTickCount64())); budget(lease);
        demand(notifications != nullptr, "HistoryNullVectorUnknown"); const auto count = notifications.Size(); demand(count <= 32, "HistoryLocalIterationBound"); num(result, L"count", count);
        unsigned matchingID = 0; bool matched = false;
        for (unsigned i = 0; i < count; ++i) { budget(lease); const auto item = notifications.GetAt(i);
            if (item.Id() == sender.GetNamedNumber(L"nativeID")) { ++matchingID; const auto hash = payloadDigest(item.Payload()); put(result, L"observedPayloadSHA256", hash); matched = winrt::hstring{hash} == sender.GetNamedString(L"payloadSHA256"); } }
        num(result, L"matchingIDCount", matchingID); put(result, L"exactPayloadMatch", matchingID == 1 && matched);
        const auto after = registryIdentity(); result.Insert(L"registryAfter", after); demand(after.Stringify() == before.Stringify(), "HistoryRegistryChanged");
        demand(ownToken().Stringify() == result.GetNamedObject(L"token").Stringify(), "HistoryTokenChanged"); binding.stable(); budget(lease);
        state.shutdown(); put(result, L"bootstrapShutdown", shutdown); budget(lease);
        put(result, L"outcome", count == 0 ? L"completed_empty" : matchingID == 1 && matched ? L"completed_exact" : L"completed_mismatch");
        num(result, L"endBootMs", static_cast<double>(GetTickCount64())); publish(L"TEST-history.json", result); std::puts(winrt::to_string(result.Stringify()).c_str()); noUnwind.completedPublication = true; return 0;
    } catch (const Failure& e) { put(result, L"query", winrt::to_hstring(e.query).c_str()); num(result, L"error", e.error); }
      catch (const winrt::hresult_error& e) { num(result, L"error", static_cast<DWORD>(e.code().value)); }
      catch (...) { put(result, L"query", L"HistoryException"); }
    num(result, L"failureBootMs", static_cast<double>(GetTickCount64()));
    try { result.Insert(L"registryAfter", registryIdentity()); } catch (const Failure& e) { num(result, L"registryReadbackError", e.error); } catch (...) { num(result, L"registryReadbackError", ERROR_INVALID_DATA); }
    if (pending && operation) { try { operation.Cancel(); put(result, L"cancelRequested", true); } catch (...) { put(result, L"cancelRequested", false); } }
    // Cancel does not prove RPC completion. Retain manager/op until this process exits; no UI admission.
    put(result, L"rpcCompletionQualified", false); put(result, L"outcome", L"unknown"); num(result, L"endBootMs", static_cast<double>(GetTickCount64()));
    publish(L"TEST-history.json", result); std::puts(winrt::to_string(result.Stringify()).c_str()); std::fflush(stdout); ExitProcess(1);
}
static int sender(bool historyDiagnostic = false) {
    lease = GetTickCount64() + 30000; Binding binding; auto result = record(L"sender"); num(result, L"deadlineBootMs", static_cast<double>(lease)); const auto token = ownToken(); result.Insert(L"token", token);
    bool shutdown = false; Bootstrap state(shutdown); bool registered = false;
    winrt::Microsoft::Windows::AppNotifications::AppNotificationManager manager{nullptr};
    try {
        demand(medium(token), "SenderMediumRequired"); bootstrap(state, result, binding);
        manager = winrt::Microsoft::Windows::AppNotifications::AppNotificationManager::Default();
        const auto event = manager.NotificationInvoked([](auto const&, auto const&) { /* Sender must die before any click. */ });
        put(result, L"handlerBeforeRegister", true); publish(L"TEST-register-intent.json", result); budget(lease);
        manager.Register(); registered = true; put(result, L"registered", true); budget(lease); binding.stable();
        if (historyDiagnostic) result.Insert(L"registryIdentity", registryIdentity());
        const std::wstring xml = L"<toast launch=\"TEST-cold-" + nonce + L"\"><visual><binding template=\"ToastGeneric\"><text>Navigation TEST " + nonce + L"</text><text>SDK cold activation TEST</text></binding></visual></toast>";
        winrt::Microsoft::Windows::AppNotifications::AppNotification toast(xml);
        if (historyDiagnostic) put(result, L"payloadSHA256", payloadDigest(toast.Payload()));
        publish(L"TEST-show-intent.json", result); num(result, L"showCallBootMs", static_cast<double>(GetTickCount64())); budget(lease); manager.Show(toast); num(result, L"showReturnedBootMs", static_cast<double>(GetTickCount64())); num(result, L"nativeID", toast.Id());
        demand(toast.Id() != 0, "NativeIDZero"); put(result, L"showReturned", true);
        num(result, L"unregisterCallBootMs", static_cast<double>(GetTickCount64())); manager.Unregister(); num(result, L"unregisterReturnedBootMs", static_cast<double>(GetTickCount64())); registered = false; put(result, L"unregisterReturned", true); manager.NotificationInvoked(event);
        state.shutdown(); put(result, L"bootstrapShutdown", shutdown); put(result, L"outcome", std::wstring(L"shown_sender_finished"));
        demand(ownToken().Stringify() == token.Stringify(), "SenderTokenChanged"); num(result, L"endBootMs", static_cast<double>(GetTickCount64())); publish(L"TEST-sender.json", result);
        std::puts(winrt::to_string(result.Stringify()).c_str()); return 0;
    } catch (const winrt::hresult_error& e) { num(result, L"error", static_cast<DWORD>(e.code().value)); }
      catch (const Failure& e) { put(result, L"query", winrt::to_hstring(e.query).c_str()); num(result, L"error", e.error); }
    num(result, L"failureBootMs", static_cast<double>(GetTickCount64()));
    if (registered) { try { manager.Unregister(); put(result, L"unregisterReturned", true); } catch (...) { put(result, L"unregisterReturned", false); } }
    num(result, L"endBootMs", static_cast<double>(GetTickCount64())); put(result, L"outcome", std::wstring(L"unknown")); publish(L"TEST-sender.json", result); std::puts(winrt::to_string(result.Stringify()).c_str()); return 1;
}
static int receiver() {
    const ULONGLONG entered = GetTickCount64(); lease = entered + 65000;
    std::thread([deadline = lease] { while (GetTickCount64() < deadline) Sleep(10);
        // SDK/COM calls have no cancellation guarantee. Kill only this exact current incarnation.
        TerminateProcess(GetCurrentProcess(), 124);
    }).detach();
    Binding binding;
    auto ready = record(L"receiver_ready"); const auto token = ownToken(); ready.Insert(L"token", token);
    put(ready, L"bindingSHA256", winrt::to_hstring(binding.sha).c_str()); num(ready, L"leaseDeadlineBootMs", static_cast<double>(lease));
    try { publish(L"TEST-receiver-ready.json", ready); }
    catch (...) { try { publish(L"TEST-second-receiver.json", ready); } catch (...) {} return 1; } // Exclusive before SDK admission, including unsupported token.
    publish(L"TEST-receiver-lease-intent.json", ready);
    auto terminal = record(L"receiver_terminal"); terminal.Insert(L"token", token); bool shutdown = false; Bootstrap state(shutdown);
    bool registered = false; std::atomic<bool> claimed = false, finished = false; std::mutex lock;
    try {
        const ULONGLONG ackDeadline = std::min(lease - 5000, entered + 15000);
        while (!exists(L"TEST-receiver-ack.json")) { budget(ackDeadline); Sleep(10); }
        const auto ack = read(L"TEST-receiver-ack.json");
        demand(ack.GetNamedString(L"nonce") == nonce && ack.GetNamedNumber(L"receiverPID") == GetCurrentProcessId() &&
            ack.GetNamedString(L"receiverBirth") == ready.GetNamedString(L"birth") && ack.GetNamedString(L"bindingSHA256") == ready.GetNamedString(L"bindingSHA256"), "CollectorAcknowledgement");
        if (!medium(token)) { put(terminal, L"outcome", std::wstring(L"sdk_unsupported_token")); publish(L"TEST-receiver-terminal.json", terminal); return 0; }
        bootstrap(state, terminal, binding);
        auto manager = winrt::Microsoft::Windows::AppNotifications::AppNotificationManager::Default();
        auto admit = [&](winrt::Microsoft::Windows::AppNotifications::AppNotificationActivatedEventArgs const& args, const wchar_t* kind) {
            if (claimed.exchange(true)) { try { publish(L"TEST-second-callback.json", record(L"second_callback_rejected")); } catch (...) {} return; }
            // At most one admitted callback/effect, never replay.
            const ULONGLONG entry = GetTickCount64(), deadline = std::min(entry + 30000, lease - 5000);
            auto effect = record(L"callback"); put(effect, L"eventKind", std::wstring(kind)); effect.Insert(L"token", ownToken()); put(effect, L"argument", std::wstring(args.Argument()));
            num(effect, L"entryBootMs", static_cast<double>(entry)); num(effect, L"deadlineBootMs", static_cast<double>(deadline));
            bool launchEntered = false;
            try {
                budget(deadline); binding.stable(); demand(args.Argument() == L"TEST-cold-" + nonce, "ExactActivationArgument");
                demand(medium(ownToken()) && ownToken().Stringify() == token.Stringify(), "CallbackTokenChanged");
                const auto exited = read(L"TEST-sender-collected.json");
                demand(exited.GetNamedString(L"nonce") == nonce && exited.GetNamedBoolean(L"collected") && exited.GetNamedNumber(L"exitCode") == 0 &&
                    exited.GetNamedNumber(L"pid") != GetCurrentProcessId() && exited.GetNamedNumber(L"collectedBootMs") < entry, "SenderDeathBeforeCallback");
                effect.Insert(L"sender", exited); NavigationVendorTEST::exactInstalled();
                put(effect, L"uri", std::wstring(binding.value.GetNamedString(L"uri"))); put(effect, L"familyName", std::wstring(NavigationVendorTEST::vendorFamily));
                const ULONGLONG queryAdmission = GetTickCount64(), queryDeadline = std::min(deadline, queryAdmission + 10000);
                num(effect, L"queryAdmissionBootMs", static_cast<double>(queryAdmission)); num(effect, L"queryDeadlineBootMs", static_cast<double>(queryDeadline));
                put(effect, L"queryEntered", false);
                auto queryIntent = JsonObject::Parse(effect.Stringify()); put(queryIntent, L"phase", L"query_intent"); put(queryIntent, L"queryBoundaryArmed", true);
                publish(L"TEST-query-intent.json", queryIntent); budget(queryDeadline);
                using namespace winrt::Windows::System;
                const winrt::Windows::Foundation::Uri uri(binding.value.GetNamedString(L"uri"));
                budget(queryDeadline); num(effect, L"queryCallBootMs", static_cast<double>(GetTickCount64())); put(effect, L"queryEntered", true);
                const auto support = NavigationVendorTEST::await(Launcher::QueryUriSupportAsync(uri, LaunchQuerySupportType::Uri, NavigationVendorTEST::vendorFamily), 10000, queryDeadline);
                budget(queryDeadline); num(effect, L"queryReturnedBootMs", static_cast<double>(GetTickCount64())); put(effect, L"queryReturned", true);
                num(effect, L"uriSupport", static_cast<int>(support));
                if (support != LaunchQuerySupportStatus::Available) put(effect, L"outcome", std::wstring(L"unsupported_target"));
                else {
                    NavigationVendorTEST::exactInstalled(); binding.stable(); budget(deadline);
                    const ULONGLONG launchAdmission = GetTickCount64(), launchDeadline = std::min(deadline, launchAdmission + 15000);
                    num(effect, L"launchAdmissionBootMs", static_cast<double>(launchAdmission)); num(effect, L"launchDeadlineBootMs", static_cast<double>(launchDeadline));
                    put(effect, L"launchEntered", false);
                    auto launchIntent = JsonObject::Parse(effect.Stringify()); put(launchIntent, L"phase", L"launch_intent"); put(launchIntent, L"launchBoundaryArmed", true);
                    publish(L"TEST-launch-intent.json", launchIntent);
                    LauncherOptions options; options.TargetApplicationPackageFamilyName(NavigationVendorTEST::vendorFamily); options.FallbackUri(nullptr);
                    budget(launchDeadline); num(effect, L"launchCallBootMs", static_cast<double>(GetTickCount64())); launchEntered = true;
                    const bool accepted = NavigationVendorTEST::await(Launcher::LaunchUriAsync(uri, options), 15000, launchDeadline);
                    budget(launchDeadline); num(effect, L"launchReturnedBootMs", static_cast<double>(GetTickCount64()));
                    put(effect, L"launchReturned", true); put(effect, L"accepted", accepted);
                    put(effect, L"outcome", std::wstring(accepted ? L"handoff_accepted" : L"declined"));
                }
            } catch (const winrt::hresult_error& e) { num(effect, L"error", static_cast<DWORD>(e.code().value)); put(effect, L"outcome", std::wstring(L"unknown")); }
              catch (const Failure& e) { put(effect, L"query", winrt::to_hstring(e.query).c_str()); num(effect, L"error", e.error); put(effect, L"outcome", std::wstring(L"unknown")); }
              catch (...) { put(effect, L"outcome", std::wstring(L"unknown")); }
            put(effect, L"launchEntered", launchEntered); put(effect, L"targetVisibleQualified", false); put(effect, L"timely", GetTickCount64() < deadline);
            std::lock_guard guard(lock); publish(L"TEST-callback.json", effect); finished = true;
        };
        const auto event = manager.NotificationInvoked([&](auto const&, auto const& args) { admit(args, L"NotificationInvoked"); });
        put(terminal, L"handlerBeforeRegister", true); budget(lease - 5000); manager.Register(); registered = true;
        budget(lease - 7000); // SDK Deserialize has a 2s native wait; reserve collection/terminal time.
        const auto activation = winrt::Microsoft::Windows::AppLifecycle::AppInstance::GetCurrent().GetActivatedEventArgs();
        budget(lease - 5000);
        num(terminal, L"activationKind", static_cast<int>(activation.Kind()));
        if (activation.Kind() == winrt::Microsoft::Windows::AppLifecycle::ExtendedActivationKind::AppNotification)
            admit(activation.Data().as<winrt::Microsoft::Windows::AppNotifications::AppNotificationActivatedEventArgs>(), L"AppInstance_AppNotification");
        else demand(finished || activation.Kind() == winrt::Microsoft::Windows::AppLifecycle::ExtendedActivationKind::Launch, "UnexpectedActivationKind");
        while (!finished && GetTickCount64() < lease - 5000) Sleep(10);
        demand(finished, "CallbackDeadlineUnknown");
        manager.Unregister(); registered = false; manager.NotificationInvoked(event); state.shutdown();
        put(terminal, L"bootstrapShutdown", shutdown); put(terminal, L"unregisterReturned", true); put(terminal, L"outcome", std::wstring(L"callback_observed"));
    } catch (const winrt::hresult_error& e) { num(terminal, L"error", static_cast<DWORD>(e.code().value)); put(terminal, L"outcome", std::wstring(L"unknown")); }
      catch (const Failure& e) { put(terminal, L"query", winrt::to_hstring(e.query).c_str()); num(terminal, L"error", e.error); put(terminal, L"outcome", std::wstring(L"unknown")); }
    catch (...) { put(terminal, L"outcome", std::wstring(L"unknown")); }
    put(terminal, L"registrationStillOwned", registered); publish(L"TEST-receiver-terminal.json", terminal);
    // Terminate only this incarnation at its immutable self lease; no broker/global cleanup claim.
    ExitProcess(terminal.GetNamedString(L"outcome") == L"callback_observed" ? 0 : 1);
}
static int collect() {
    const ULONGLONG deadline = GetTickCount64() + 100000; const ULONGLONG readyDeadline = exists(L"TEST-history-request.json") ? historyDeadline() : deadline - 70000; Binding binding; auto j = record(L"collector"); File process;
    num(j, L"deadlineBootMs", static_cast<double>(deadline)); num(j, L"readyWaitDeadlineBootMs", static_cast<double>(readyDeadline));
    put(j, L"readyObserved", false); put(j, L"ackPublished", false); put(j, L"stage", L"ready_wait");
    try {
        while (!exists(L"TEST-receiver-ready.json")) { budget(readyDeadline); Sleep(10); }
        put(j, L"readyObserved", true); num(j, L"readyObservedBootMs", static_cast<double>(GetTickCount64())); put(j, L"stage", L"ready_validate");
        const auto ready = read(L"TEST-receiver-ready.json");
        demand(ready.GetNamedString(L"nonce") == nonce && ready.GetNamedString(L"phase") == L"receiver_ready" &&
            ready.GetNamedString(L"bindingSHA256") == winrt::to_hstring(binding.sha), "ReadyBinding");
        const double pid = ready.GetNamedNumber(L"pid"); demand(pid > 0 && pid <= MAXDWORD && pid == static_cast<DWORD>(pid) && pid != GetCurrentProcessId(), "ReceiverPID");
        process.h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, static_cast<DWORD>(pid)); require(process.h != nullptr, "HoldReceiverProcess");
        demand(birth(process.h) == ready.GetNamedString(L"birth") && WaitForSingleObject(process.h, 0) == WAIT_TIMEOUT, "LiveReceiverBirth");
        wchar_t path[32768]{}; DWORD size = 32768;
        require(QueryFullProcessImageNameW(process.h, 0, path, &size) != FALSE, "ReceiverImage");
        demand(fs::canonical(path).wstring() == image && std::wstring(path, size) == image &&
            hashFile(path) == winrt::to_string(binding.value.GetNamedString(L"exeSHA256")), "ExactReceiverPhysicalImage");
        const auto actual = ownToken(process.h); sameIdentity(actual, binding.value.GetNamedObject(L"identity"));
        demand(actual.Stringify() == ready.GetNamedObject(L"token").Stringify(), "ReceiverReadyTokenMatch");
        const double end = ready.GetNamedNumber(L"leaseDeadlineBootMs");
        demand(ready.Size() == 8 && end > GetTickCount64() && end < deadline && end - ready.GetNamedNumber(L"bootMs") <= 65000, "ReceiverLease");
        auto ack = record(L"collector_ack"); num(ack, L"receiverPID", pid); put(ack, L"receiverBirth", std::wstring(ready.GetNamedString(L"birth")));
        put(ack, L"bindingSHA256", winrt::to_hstring(binding.sha).c_str()); publish(L"TEST-receiver-ack.json", ack);
        j.Insert(L"ready", ready); j.Insert(L"heldToken", actual); put(j, L"heldPhysicalImage", image); put(j, L"ackPublished", true); num(j, L"ackPublishedBootMs", static_cast<double>(GetTickCount64())); put(j, L"stage", L"receiver_collect");
        const ULONGLONG now = GetTickCount64();
        demand(now < static_cast<ULONGLONG>(end) + 3000, "ReceiverCollectionLeaseElapsed");
        const ULONGLONG remaining = static_cast<ULONGLONG>(end) + 3000 - now;
        demand(remaining <= 68000, "ReceiverCollectionWaitBound");
        const DWORD wait = WaitForSingleObject(process.h, static_cast<DWORD>(remaining));
        num(j, L"waitResult", wait); put(j, L"collected", wait == WAIT_OBJECT_0);
        if (wait != WAIT_OBJECT_0) throw Failure{"ReceiverCollectionUnknown", wait == WAIT_FAILED ? GetLastError() : WAIT_TIMEOUT};
        DWORD exit = 0; require(GetExitCodeProcess(process.h, &exit) != FALSE, "CollectedReceiverExit"); num(j, L"exitCode", exit);
        demand(birth(process.h) == ready.GetNamedString(L"birth"), "CollectedReceiverBirth");
        num(j, L"endBootMs", static_cast<double>(GetTickCount64())); put(j, L"outcome", std::wstring(L"collected")); publish(L"TEST-collector.json", j); return 0;
    } catch (const Failure& e) { put(j, L"query", winrt::to_hstring(e.query).c_str()); num(j, L"error", e.error); }
      catch (...) { put(j, L"query", std::wstring(L"exception")); }
    num(j, L"failureBootMs", static_cast<double>(GetTickCount64())); num(j, L"endBootMs", static_cast<double>(GetTickCount64())); put(j, L"outcome", std::wstring(L"unknown")); publish(L"TEST-collector.json", j); return 1;
}
struct ShellOwner {
    File process; DWORD pid = 0; std::wstring born, path; JsonObject token;
    explicit ShellOwner(DWORD id) : pid(id) {
        process.h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, id); require(process.h != nullptr, "ShellOwnerOpen");
        wchar_t p[32768]{}, windows[32768]{}; DWORD n = 32768;
        require(QueryFullProcessImageNameW(process.h, 0, p, &n) != FALSE, "ShellImage"); require(GetWindowsDirectoryW(windows, 32768) != 0, "WindowsDirectory");
        // Exclude positively identified foreign images before filesystem work.
        // Unknown image/identity failures still invalidate the complete census.
        std::wstring normalized(p, n); std::transform(normalized.begin(), normalized.end(), normalized.begin(), towlower);
        std::wstring w(windows); std::transform(w.begin(), w.end(), w.begin(), towlower);
        const std::vector<std::wstring> allowed{w + L"\\explorer.exe", w + L"\\system32\\shellhost.exe", w + L"\\systemapps\\shellexperiencehost_cw5n1h2txyewy\\shellexperiencehost.exe", w + L"\\systemapps\\microsoftwindows.client.cbs_cw5n1h2txyewy\\shellhost.exe"};
        demand(std::find(allowed.begin(), allowed.end(), normalized) != allowed.end(), "ClosedShellProvider");
        path = fs::canonical(p).wstring(); demand(!_wcsicmp(path.c_str(), p), "ShellPhysicalPath");
        born = birth(process.h); token = ownToken(process.h); sameIdentity(token, ownToken()); live();
    }
    void live() { demand(WaitForSingleObject(process.h, 0) == WAIT_TIMEOUT && birth(process.h) == born, "ShellOwnerChanged"); }
};
static std::wstring name(IUIAutomationElement* e) {
    BSTR text = nullptr; winrt::check_hresult(e->get_CurrentName(&text)); std::wstring value;
    if (text) { demand(SysStringLen(text) <= 1024, "UIANameBound"); value.assign(text, SysStringLen(text)); SysFreeString(text); } return value;
}
static std::vector<int> runtime(IUIAutomationElement* e) {
    SAFEARRAY* raw = nullptr; winrt::check_hresult(e->GetRuntimeId(&raw)); demand(raw != nullptr, "UIARuntimeMissing");
    LONG low = 0, high = -1; const auto a = SafeArrayGetLBound(raw, 1, &low), b = SafeArrayGetUBound(raw, 1, &high);
    if (FAILED(a) || FAILED(b) || high < low || high - low > 31) { SafeArrayDestroy(raw); throw Failure{"UIARuntimeBound", ERROR_INVALID_DATA}; }
    std::vector<int> result; for (LONG i = low; i <= high; ++i) { int value = 0; const HRESULT hr = SafeArrayGetElement(raw, &i, &value);
        if (FAILED(hr)) { SafeArrayDestroy(raw); winrt::check_hresult(hr); } result.push_back(value); }
    SafeArrayDestroy(raw); return result;
}
static std::wstring objectName(HANDLE object) {
    wchar_t text[256]{}; DWORD bytes = 0;
    require(GetUserObjectInformationW(object, UOI_NAME, text, sizeof(text), &bytes) != FALSE, "DesktopObjectName");
    demand(bytes >= sizeof(wchar_t) && bytes <= sizeof(text) && text[(bytes / sizeof(wchar_t)) - 1] == 0, "DesktopNameBound"); return text;
}
static void interactive() {
    DWORD session = 0; require(ProcessIdToSessionId(GetCurrentProcessId(), &session) != FALSE, "ShellActorSession"); demand(session != 0, "InteractiveSessionRequired");
    LPWSTR raw = nullptr; DWORD bytes = 0;
    require(WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, session, WTSConnectState, &raw, &bytes) != FALSE, "ActualWTSState");
    const bool active = bytes == sizeof(WTS_CONNECTSTATE_CLASS) && *reinterpret_cast<WTS_CONNECTSTATE_CLASS*>(raw) == WTSActive;
    WTSFreeMemory(raw); demand(active, "InteractiveSessionActive");
    USEROBJECTFLAGS flags{}; DWORD got = 0; const HWINSTA station = GetProcessWindowStation();
    require(GetUserObjectInformationW(station, UOI_FLAGS, &flags, sizeof(flags), &got) != FALSE, "OwnWindowStationFlags");
    demand(got == sizeof(flags) && (flags.dwFlags & WSF_VISIBLE) && objectName(station) == L"WinSta0", "OwnVisibleWindowStation");
    const HDESK desktop = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS); require(desktop != nullptr, "HeldInputDesktop");
    try { demand(objectName(desktop) == L"Default" && objectName(GetThreadDesktop(GetCurrentThreadId())) == L"Default", "OwnInputDesktopMatch"); }
    catch (...) { CloseDesktop(desktop); throw; }
    require(CloseDesktop(desktop) != FALSE, "CloseHeldInputDesktop");
}
static void inputAuthority() {
    wchar_t authority[64]{}; const DWORD length = GetEnvironmentVariableW(L"SDK_COLDCLICK_DISPOSABLE_TEST", authority, 64);
    demand(length == nonce.size() && std::wstring(authority, length) == nonce, "DisposableInputAuthority");
}
// Explicit disposable CI shortcut authority; not a production foreground capability.
static std::wstring globalInputAuthority() {
    inputAuthority(); wchar_t authority[160]{}, runner[64]{};
    const DWORD n = GetEnvironmentVariableW(L"SDK_COLDCLICK_GLOBAL_AUTHORITY", authority, 160);
    const DWORD r = GetEnvironmentVariableW(L"SDK_COLDCLICK_DISPOSABLE_CLIENT", runner, 64);
    const std::wstring prefix = L"dispatch-attempt1:";
    demand(r == std::wstring(L"windows-11-vs2026-arm").size() && std::wstring(runner, r) == L"windows-11-vs2026-arm", "DisposableClientRunnerAuthority");
    demand(n == prefix.size() + 40 + 1 + nonce.size() && std::wstring(authority, prefix.size()) == prefix &&
        std::wstring(authority + prefix.size() + 40, 1 + nonce.size()) == L":" + nonce, "GlobalShortcutSourceNonceAuthority");
    const std::wstring source(authority + prefix.size(), 40);
    demand(source.find_first_not_of(L"0123456789abcdef") == std::wstring::npos, "GlobalShortcutSourceSHA");
    OSVERSIONINFOEXW client{}; client.dwOSVersionInfoSize = sizeof(client); client.wProductType = VER_NT_WORKSTATION;
    require(VerifyVersionInfoW(&client, VER_PRODUCT_TYPE, VerSetConditionMask(0, VER_PRODUCT_TYPE, VER_EQUAL)) != FALSE, "WindowsClientProductType");
    return source;
}
// Separate TEST GUI scenario; never an extension of the closed Shell provider list.
struct OwnedForeground {
    HWND window = nullptr; DWORD thread = GetCurrentThreadId(); bool alive = false, finished = false, mayFree = false; ATOM atom = 0;
    std::wstring className = L"NavigationColdForegroundTEST-" + nonce; JsonObject j = record(L"owned_foreground"); JsonObject token = ownToken();
    static LRESULT CALLBACK procedure(HWND w, UINT msg, WPARAM a, LPARAM b) {
        auto self = reinterpret_cast<OwnedForeground*>(GetWindowLongPtrW(w, GWLP_USERDATA));
        if (msg == WM_NCCREATE) { self = static_cast<OwnedForeground*>(reinterpret_cast<CREATESTRUCTW*>(b)->lpCreateParams);
            self->window = w; SetLastError(ERROR_SUCCESS);
            const LONG_PTR prior = SetWindowLongPtrW(w, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(self));
            if (prior != 0 || GetLastError() != ERROR_SUCCESS || GetWindowLongPtrW(w, GWLP_USERDATA) != reinterpret_cast<LONG_PTR>(self)) return FALSE;
            self->alive = true; }
        if (msg == WM_CLOSE) return 0; // The creator owns destruction, never a foreign foreground restore.
        if (msg == WM_NCDESTROY && self) { self->alive = false; SetWindowLongPtrW(w, GWLP_USERDATA, 0); }
        return DefWindowProcW(w, msg, a, b);
    }
    void live() const {
        DWORD pid = 0; wchar_t cls[128]{};
        demand(window && alive && IsWindow(window) && GetCurrentThreadId() == thread &&
            GetWindowThreadProcessId(window, &pid) == thread && pid == GetCurrentProcessId() &&
            GetClassNameW(window, cls, 128) == static_cast<int>(className.size()) && cls == className &&
            GetWindowLongPtrW(window, GWLP_USERDATA) == reinterpret_cast<LONG_PTR>(this), "OwnedForegroundCustody");
    }
    void pump() { live(); MSG msg{}; unsigned count = 0;
        while (PeekMessageW(&msg, window, 0, 0, PM_REMOVE)) { demand(++count <= 64, "OwnWindowMessageBound"); DispatchMessageW(&msg); live(); } }
    void create(const Binding& binding, ULONGLONG deadline) {
        inputAuthority(); interactive(); binding.stable(); sameIdentity(token, binding.value.GetNamedObject(L"identity"));
        num(j, L"creatorTID", thread); put(j, L"className", className); put(j, L"scenario", L"owned_test_foreground");
        put(j, L"physicalImage", image); put(j, L"exeSHA256", binding.value.GetNamedString(L"exeSHA256").c_str()); j.Insert(L"token", token);
        put(j, L"windowCreated", false); num(j, L"foregroundAttempts", 0);
        num(j, L"deadlineBootMs", static_cast<double>(deadline)); num(j, L"createIntentBootMs", static_cast<double>(GetTickCount64()));
        publish(L"TEST-owned-foreground-intent.json", j); budget(deadline);
        WNDCLASSW cls{}; cls.lpfnWndProc = procedure; cls.hInstance = GetModuleHandleW(nullptr); cls.lpszClassName = className.c_str();
        atom = RegisterClassW(&cls); require(atom != 0, "OwnWindowClass");
        const HWND created = CreateWindowExW(0, className.c_str(), className.c_str(), WS_OVERLAPPEDWINDOW, CW_USEDEFAULT, CW_USEDEFAULT, 320, 120,
            nullptr, nullptr, cls.hInstance, this); require(created != nullptr, "CreateOwnWindow");
        put(j, L"windowCreated", true); num(j, L"createdBootMs", static_cast<double>(GetTickCount64()));
        demand(window == created, "OwnCreatedWindowIdentity"); live();
        STARTUPINFOW startup{}; startup.cb = sizeof(startup); GetStartupInfoW(&startup);
        num(j, L"startupFlags", startup.dwFlags); num(j, L"startupShowWindow", startup.wShowWindow);
        budget(deadline); ShowWindow(window, SW_SHOWNOACTIVATE); put(j, L"visible", IsWindowVisible(window) != FALSE); live();
        demand(IsWindowVisible(window) != FALSE, "OwnWindowNotVisible"); binding.stable(); interactive(); budget(deadline);
        num(j, L"foregroundAttempts", 1); num(j, L"foregroundCallBootMs", static_cast<double>(GetTickCount64()));
        put(j, L"setForegroundReturned", SetForegroundWindow(window) != FALSE);
        num(j, L"foregroundReturnedBootMs", static_cast<double>(GetTickCount64())); pump();
        put(j, L"actualForeground", GetForegroundWindow() == window); demand(j.GetNamedBoolean(L"setForegroundReturned") && GetForegroundWindow() == window, "OwnForegroundRefused");
        demand(ownToken().Stringify() == token.Stringify(), "OwnForegroundTokenChanged");
    }
    bool close() {
        if (finished) return j.GetNamedBoolean(L"windowCustodyKnown", false); finished = true;
        bool known = true, destroyed = window == nullptr;
        try { if (window) { live(); num(j, L"destroyCallBootMs", static_cast<double>(GetTickCount64()));
            destroyed = DestroyWindow(window) != FALSE && !alive && !IsWindow(window); } }
        catch (...) { known = false; }
        if (atom && !UnregisterClassW(className.c_str(), GetModuleHandleW(nullptr))) known = false;
        known = known && destroyed; put(j, L"destroyed", destroyed); put(j, L"windowCustodyKnown", known);
        put(j, L"ownerLifetimeRetained", !known); num(j, L"endBootMs", static_cast<double>(GetTickCount64())); mayFree = known; return known;
    }
    void finish() { const bool known = close(); publish(L"TEST-owned-foreground.json", j); demand(known, "OwnWindowDestructionUnknown"); }
    ~OwnedForeground() { if (!finished) { try { close(); } catch (...) {} } }
};
// One owned chord: the Shell scenario changes no focus; the separate TEST GUI owns its HWND.
static void centerInput(const Binding& binding, JsonObject& proof, ULONGLONG deadline, OwnedForeground* own = nullptr, bool globalScenario = false) {
    inputAuthority();
    put(proof, L"centerAttempted", true); put(proof, L"inputEffectUnknown", true); put(proof, L"stage", L"center_input");
    auto input = record(L"center_input"); num(input, L"deadlineBootMs", static_cast<double>(deadline));
    num(input, L"firstCompletedCensusBootMs", proof.GetNamedNumber(L"firstCompletedCensusBootMs"));
    num(input, L"completedCensusAttempts", proof.GetNamedNumber(L"completedCensusAttempts")); put(input, L"inputEffectUnknown", true);
    struct Keys {
        bool win = false, n = false, releaseUnknown = false; unsigned accepted = 0, releaseAttempts = 0; DWORD error = 0;
        bool send(WORD key, bool up) {
            INPUT event{}; event.type = INPUT_KEYBOARD; event.ki.wVk = key; event.ki.dwFlags = up ? KEYEVENTF_KEYUP : 0;
            SetLastError(ERROR_SUCCESS); if (SendInput(1, &event, sizeof(event)) != 1) { error = GetLastError(); return false; }
            ++accepted; (key == VK_LWIN ? win : n) = !up; return true;
        }
        void release() noexcept {
            if (n) { ++releaseAttempts; if (!send('N', true)) releaseUnknown = true; n = false; }
            if (win) { ++releaseAttempts; if (!send(VK_LWIN, true)) releaseUnknown = true; win = false; }
        }
        ~Keys() { release(); }
    } keys;
    try {
        const auto sender = read(L"TEST-sender-collected.json");
        demand(sender.GetNamedString(L"nonce") == nonce && sender.GetNamedBoolean(L"collected") && sender.GetNamedNumber(L"exitCode") == 0, "CollectedSenderBeforeInput");
        input.Insert(L"sender", sender); put(input, L"scenario", globalScenario ? L"disposable_global_shortcut" : own ? L"owned_test_foreground" : L"shell_foreground"); const HWND foreground = globalScenario ? nullptr : GetForegroundWindow(); DWORD pid = 0;
        std::unique_ptr<ShellOwner> owner;
        if (globalScenario) { put(input, L"globalSourceSHA", globalInputAuthority()); put(input, L"windowsClient", true); sameIdentity(ownToken(), binding.value.GetNamedObject(L"identity")); }
        else { demand(foreground != nullptr && GetWindowThreadProcessId(foreground, &pid) != 0, "ForegroundShellWindow");
            if (own) { own->live(); demand(foreground == own->window && pid == GetCurrentProcessId(), "OwnActualForeground"); }
            else owner = std::make_unique<ShellOwner>(pid);
            num(input, L"foregroundPID", pid); put(input, L"foregroundBirth", own ? birth(GetCurrentProcess()) : owner->born); }
        auto immediate = [&] { interactive(); binding.stable(); if (globalScenario) { demand(winrt::hstring{globalInputAuthority()} == input.GetNamedString(L"globalSourceSHA"), "GlobalAuthorityChanged"); sameIdentity(ownToken(), binding.value.GetNamedObject(L"identity")); budget(deadline); return; } if (own) { own->live(); demand(ownToken().Stringify() == own->token.Stringify(), "OwnForegroundTokenChanged"); } else owner->live(); DWORD actual = 0;
            demand(GetForegroundWindow() == foreground && GetWindowThreadProcessId(foreground, &actual) != 0 && actual == pid, "RetainedForegroundShell"); budget(deadline); };
        auto released = [&] { for (int key : {VK_LWIN, VK_RWIN, static_cast<int>('N'), VK_SHIFT, VK_LSHIFT, VK_RSHIFT, VK_CONTROL, VK_LCONTROL, VK_RCONTROL, VK_MENU, VK_LMENU, VK_RMENU})
            demand(!(GetAsyncKeyState(key) & 0x8000), "InitiallyReleasedKeys"); };
        immediate(); released(); put(input, L"initialKeysReleased", true); put(input, L"disposableAuthority", true);
        num(input, L"intentBootMs", static_cast<double>(GetTickCount64())); publish(L"TEST-center-input-intent.json", input);
        immediate(); released(); num(input, L"inputCallBootMs", static_cast<double>(GetTickCount64())); budget(deadline);
        demand(keys.send(VK_LWIN, false), "OwnedWinDown"); immediate(); demand(keys.send('N', false), "OwnedNDown");
        // Releases of successfully owned downs are allowed even after admission expires.
        demand(keys.send('N', true), "OwnedNUp"); demand(keys.send(VK_LWIN, true), "OwnedWinUp");
        num(input, L"inputReturnedBootMs", static_cast<double>(GetTickCount64())); put(input, L"outcome", L"input_accepted");
    } catch (const Failure& e) { put(input, L"query", winrt::to_hstring(e.query).c_str()); num(input, L"error", e.error); }
      catch (const winrt::hresult_error& e) { num(input, L"error", static_cast<DWORD>(e.code().value)); }
      catch (...) { put(input, L"query", L"exception"); }
    keys.release(); const bool accepted = input.HasKey(L"outcome") && keys.accepted == 4 && !keys.releaseUnknown && keys.releaseAttempts == 0;
    num(input, L"acceptedEvents", keys.accepted); num(input, L"cleanupReleaseAttempts", keys.releaseAttempts); put(input, L"releaseUnknown", keys.releaseUnknown);
    put(input, L"ownedKeysReleased", !keys.win && !keys.n && !keys.releaseUnknown); num(input, L"sendError", keys.error);
    put(input, L"inputEffectUnknown", !accepted); put(input, L"centerOpenedProved", false);
    if (!accepted) { put(input, L"outcome", L"unknown"); num(input, L"failureBootMs", static_cast<double>(GetTickCount64())); }
    num(input, L"endBootMs", static_cast<double>(GetTickCount64()));
    publish(L"TEST-center-input.json", input); put(proof, L"inputEffectUnknown", !accepted);
    demand(accepted, "CenterInputUnknown"); budget(deadline);
}
static int invoke(bool ownScenario = false, bool globalScenario = false) {
    const ULONGLONG deadline = exists(L"TEST-history-request.json") ? historyDeadline() : GetTickCount64() + 30000; Binding binding; auto proof = record(L"shell_invoke");
    num(proof, L"deadlineBootMs", static_cast<double>(deadline)); put(proof, L"stage", L"interactive");
    put(proof, L"scenario", globalScenario ? L"disposable_global_shortcut" : ownScenario ? L"owned_test_foreground" : L"shell_foreground"); std::unique_ptr<OwnedForeground, void(*)(OwnedForeground*)> owned(nullptr, [](OwnedForeground* p) {
        if (!p->finished) { try { p->close(); } catch (...) {} }
        if (p->mayFree) delete p;
        // Unknown HWND custody retains exactly this one owner until own process exit.
    });
    put(proof, L"centerAttempted", false); put(proof, L"inputEffectUnknown", false);
    unsigned attempts = 0, completed = 0, maxRoots = 0, maxNodes = 0, maxProviders = 0, maxTitles = 0;
    for (auto key : {L"censusAttempts", L"completedCensusAttempts", L"maxRoots", L"maxNodes", L"maxAdmittedProviderRoots", L"maxOwnedTitleMatches"}) num(proof, key, 0);
    try {
        interactive();
        if (exists(L"TEST-history-request.json")) { const auto observed = read(L"TEST-history.json"); demand(observed.GetNamedString(L"nonce") == nonce && observed.GetNamedString(L"outcome") == L"completed_exact" && observed.GetNamedNumber(L"deadlineBootMs") == deadline, "HistoryRequiredBeforeUI"); }
        const auto sender = read(L"TEST-sender-collected.json"); demand(sender.GetNamedString(L"nonce") == nonce && sender.GetNamedBoolean(L"collected") && sender.GetNamedNumber(L"exitCode") == 0, "CollectedSenderBeforeClick");
        ComPtr<IUIAutomation> automation; winrt::check_hresult(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)));
        ComPtr<IUIAutomationTreeWalker> walker; winrt::check_hresult(automation->get_RawViewWalker(&walker));
        struct Selection { ComPtr<IUIAutomationElement> row, title; DWORD pid = 0; std::vector<int> rowID, titleID; };
        auto census = [&]() {
            if (owned) owned->pump(); put(proof, L"stage", L"census"); num(proof, L"censusAttempts", ++attempts);
            Selection found; unsigned roots = 0, nodes = 0, titles = 0, providers = 0;
            ComPtr<IUIAutomationElement> desktop, first; winrt::check_hresult(automation->GetRootElement(&desktop)); winrt::check_hresult(walker->GetFirstChildElement(desktop.Get(), &first));
            while (first) {
                budget(deadline); demand(++roots <= 64, "UIARootBound"); num(proof, L"maxRoots", maxRoots = std::max(maxRoots, roots)); int pid = 0; winrt::check_hresult(first->get_CurrentProcessId(&pid));
                std::unique_ptr<ShellOwner> owner;
                // Unrelated top-level providers are not traversed or invoked.
                try { if (pid > 0) owner = std::make_unique<ShellOwner>(static_cast<DWORD>(pid)); } catch (const Failure& e) { if (std::string(e.query) != "ClosedShellProvider") throw; }
                if (owner) { num(proof, L"maxAdmittedProviderRoots", maxProviders = std::max(maxProviders, ++providers));
                    struct Node { ComPtr<IUIAutomationElement> element; unsigned depth; }; std::vector<Node> pending{{first, 0}};
                    while (!pending.empty()) {
                        auto node = std::move(pending.back()); pending.pop_back(); budget(deadline); demand(++nodes <= 512 && node.depth <= 16, "UIANodeBound"); owner->live(); num(proof, L"maxNodes", maxNodes = std::max(maxNodes, nodes));
                        int provider = 0; winrt::check_hresult(node.element->get_CurrentProcessId(&provider)); demand(provider == pid, "UIAProviderChanged");
                        if (name(node.element.Get()) == L"Navigation TEST " + nonce) {
                            num(proof, L"maxOwnedTitleMatches", maxTitles = std::max(maxTitles, ++titles)); demand(titles == 1, "UniqueOwnedToastTitle"); CONTROLTYPEID type = 0; winrt::check_hresult(node.element->get_CurrentControlType(&type)); demand(type == UIA_TextControlTypeId, "OwnedTitleControl");
                            found.title = node.element; auto parent = node.element;
                            for (unsigned depth = 0; depth < 8; ++depth) { ComPtr<IUIAutomationElement> next; winrt::check_hresult(walker->GetParentElement(parent.Get(), &next)); demand(next != nullptr, "OwnedToastAncestor");
                                winrt::check_hresult(next->get_CurrentProcessId(&provider)); demand(provider == pid, "OwnedAncestorProvider"); winrt::check_hresult(next->get_CurrentControlType(&type)); parent = next;
                                if (type == UIA_ListItemControlTypeId) { found.row = next; break; } }
                            demand(found.row != nullptr, "OwnedToastRow"); found.pid = pid; found.rowID = runtime(found.row.Get()); found.titleID = runtime(found.title.Get());
                        }
                        ComPtr<IUIAutomationElement> next; winrt::check_hresult(walker->GetFirstChildElement(node.element.Get(), &next));
                        while (next) { demand(nodes + pending.size() < 512, "UIAPendingBound"); pending.push_back({next, node.depth + 1}); ComPtr<IUIAutomationElement> sibling;
                            winrt::check_hresult(walker->GetNextSiblingElement(next.Get(), &sibling)); next = sibling; }
                    }
                }
                ComPtr<IUIAutomationElement> next; winrt::check_hresult(walker->GetNextSiblingElement(first.Get(), &next)); first = next;
            }
            num(proof, L"completedCensusAttempts", ++completed);
            if (completed == 1) num(proof, L"firstCompletedCensusBootMs", static_cast<double>(GetTickCount64()));
            num(proof, L"lastCompletedCensusBootMs", static_cast<double>(GetTickCount64())); return found;
        };
        while (GetTickCount64() < deadline) {
            auto selected = census(); if (!selected.row) {
                if (completed == 1 && maxTitles == 0) {
                    // A visible owned banner needs no foreground/window/input dependency.
                    // Only this completed zero-title observation admits the separate GUI scenario.
                    if (ownScenario) { owned.reset(new OwnedForeground()); owned->create(binding, deadline); }
                    centerInput(binding, proof, deadline, owned.get(), globalScenario);
                }
                Sleep(250); continue;
            }
            auto fresh = census(); demand(fresh.row && fresh.pid == selected.pid && fresh.rowID == selected.rowID && fresh.titleID == selected.titleID, "FreshOwnedRow");
            BOOL same = FALSE; winrt::check_hresult(automation->CompareElements(selected.row.Get(), fresh.row.Get(), &same)); demand(same, "UIASameElement");
            ShellOwner owner(fresh.pid); BOOL offscreen = TRUE, enabled = FALSE;
            auto immediate = [&] { budget(deadline); if (owned) owned->live(); interactive(); owner.live(); binding.stable(); demand(name(fresh.title.Get()) == L"Navigation TEST " + nonce && runtime(fresh.row.Get()) == selected.rowID && runtime(fresh.title.Get()) == selected.titleID, "UIAFreshBinding");
                int titleProvider = 0; CONTROLTYPEID titleType = 0;
                winrt::check_hresult(fresh.title->get_CurrentProcessId(&titleProvider)); winrt::check_hresult(fresh.title->get_CurrentControlType(&titleType));
                demand(titleProvider == static_cast<int>(selected.pid) && titleType == UIA_TextControlTypeId, "FreshOwnedTitleProviderType");
                auto ancestor = fresh.title; bool nearest = false;
                for (unsigned depth = 0; depth < 8; ++depth) {
                    ComPtr<IUIAutomationElement> parent; winrt::check_hresult(walker->GetParentElement(ancestor.Get(), &parent)); demand(parent != nullptr, "FreshToastAncestor");
                    int provider = 0; CONTROLTYPEID type = 0; winrt::check_hresult(parent->get_CurrentProcessId(&provider)); demand(provider == static_cast<int>(selected.pid), "FreshAncestorProvider");
                    winrt::check_hresult(parent->get_CurrentControlType(&type)); ancestor = parent;
                    if (type == UIA_ListItemControlTypeId) { BOOL sameRow = FALSE; winrt::check_hresult(automation->CompareElements(parent.Get(), fresh.row.Get(), &sameRow)); nearest = sameRow != FALSE; break; }
                }
                demand(nearest, "FreshNearestOwnedToastRow");
                winrt::check_hresult(fresh.title->get_CurrentIsOffscreen(&offscreen)); demand(!offscreen, "VisibleOwnedTitle");
                winrt::check_hresult(fresh.row->get_CurrentIsOffscreen(&offscreen)); winrt::check_hresult(fresh.row->get_CurrentIsEnabled(&enabled)); demand(!offscreen && enabled, "VisibleOwnedRow"); owner.live(); budget(deadline); };
            put(proof, L"stage", L"invoke_admission"); immediate(); ComPtr<IUIAutomationInvokePattern> pattern; winrt::check_hresult(fresh.row->GetCurrentPatternAs(UIA_InvokePatternId, IID_PPV_ARGS(&pattern)));
            num(proof, L"providerPID", fresh.pid); put(proof, L"providerBirth", owner.born); put(proof, L"providerImage", owner.path); put(proof, L"exactOwnedTitle", true);
            publish(L"TEST-ui-invoke-intent.json", proof); immediate(); put(proof, L"stage", L"invoke"); num(proof, L"invokeCallBootMs", static_cast<double>(GetTickCount64())); budget(deadline); const HRESULT hr = pattern->Invoke();
            num(proof, L"invokeReturnedBootMs", static_cast<double>(GetTickCount64()));
            num(proof, L"invokeHRESULT", static_cast<DWORD>(hr)); put(proof, L"invokeReturned", true); winrt::check_hresult(hr);
            if (owned) owned->finish(); num(proof, L"endBootMs", static_cast<double>(GetTickCount64())); publish(L"TEST-ui-invoke.json", proof); return 0;
        }
        throw Failure{"OwnedToastUnavailable", ERROR_NOT_FOUND};
    } catch (const winrt::hresult_error& e) { num(proof, L"error", static_cast<DWORD>(e.code().value)); }
      catch (const Failure& e) { put(proof, L"query", winrt::to_hstring(e.query).c_str()); num(proof, L"error", e.error); }
      catch (...) { put(proof, L"query", std::wstring(L"exception")); }
    if (owned && !owned->finished) { try { owned->finish(); } catch (...) { put(proof, L"query", L"OwnWindowDestructionUnknown"); } }
    num(proof, L"failureBootMs", static_cast<double>(GetTickCount64())); num(proof, L"endBootMs", static_cast<double>(GetTickCount64())); put(proof, L"outcome", std::wstring(L"unknown")); publish(L"TEST-ui-invoke.json", proof); return 1;
}
int wmain(int argc, wchar_t** argv) {
    try {
        wchar_t path[32768]{}; const DWORD n = GetModuleFileNameW(nullptr, path, 32768); demand(n && n < 32768, "OwnPhysicalImage");
        image = fs::canonical(path).wstring(); demand(image == path, "OwnImageCanonical"); root = fs::path(image).parent_path().wstring();
        const auto leaf = fs::path(image).filename().wstring(); demand(leaf.size() == 49 && leaf.substr(0, 9) == L"TEST-sdk-" && leaf.substr(45) == L".exe", "PermanentGenerationEXE");
        nonce = leaf.substr(9, 36); demand(validNonce(nonce.c_str()) && fs::path(root).filename() == L"TEST-lua-preflight-" + nonce, "OwnGenerationNonce");
        Apartment apartment; winrt::init_apartment(winrt::apartment_type::multi_threaded); apartment.initialized = true;
        if (argc == 7 && !wcscmp(argv[1], L"--TEST-sdk-cold-history") && nonce == argv[2] && root == argv[3]) return history();
        if (argc == 7 && !wcscmp(argv[1], L"--TEST-sdk-cold-sender-history") && nonce == argv[2] && root == argv[3]) return sender(true);
        if (argc == 7 && !wcscmp(argv[1], L"--TEST-sdk-cold-sender") && nonce == argv[2] && root == argv[3]) return sender();
        if (argc == 3 && nonce == argv[2] && !wcscmp(argv[1], L"--TEST-sdk-cold-collect")) return collect();
        if (argc == 3 && nonce == argv[2] && !wcscmp(argv[1], L"--TEST-sdk-cold-invoke")) return invoke();
        if (argc == 3 && nonce == argv[2] && !wcscmp(argv[1], L"--TEST-sdk-cold-invoke-own-foreground")) return invoke(true);
        if (argc == 3 && nonce == argv[2] && !wcscmp(argv[1], L"--TEST-sdk-cold-invoke-global-shortcut")) return invoke(false, true);
        // SDK 2.5.1 registers this exact activation command. It selects the receiver, never admits an effect.
        if ((argc == 2 || (argc == 3 && !wcscmp(argv[2], L"-Embedding"))) && !wcscmp(argv[1], L"----AppNotificationActivated:")) return receiver();
        return 64;
    } catch (...) { return 1; }
}
