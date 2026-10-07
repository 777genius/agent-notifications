// Disposable-CI prerequisite only: current-user deployment, bootstrap and static IsSupported.
#include "../navigation_windows_token_queries.h"
#include <WindowsAppSDK-VersionInfo.h>
#include <MddBootstrap.h>
#include <appmodel.h>
#include <softpub.h>
#include <wintrust.h>
#include <winver.h>
#include <winrt/base.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Management.Deployment.h>
#include <winrt/Windows.ApplicationModel.h>
#include <winrt/Windows.System.h>
#include <winrt/Microsoft.Windows.AppNotifications.h>
#include <array>
#include <cstdio>

struct File {
    HANDLE h = INVALID_HANDLE_VALUE;
    ~File() { if (h != INVALID_HANDLE_VALUE) CloseHandle(h); }
};
struct Bootstrap {
    bool initialized = false;
    bool& observed;
    explicit Bootstrap(bool& shutdownObserved) : observed(shutdownObserved) {}
    ~Bootstrap() { shutdown(); }
    void shutdown() { if (initialized) { MddBootstrapShutdown(); initialized = false; observed = true; } }
};
struct Apartment {
    bool initialized = false;
    ~Apartment() { if (initialized) winrt::uninit_apartment(); }
};
struct PackagePin { const wchar_t* file; const wchar_t* name; const wchar_t* version; const char* hash; const char* manifestHash; };
static constexpr PackagePin packages[] = {
    {L"Microsoft.WindowsAppRuntime.2.msix", L"Microsoft.WindowsAppRuntime.2", L"2.5.1.0", "cbd61bfa868269a57cab6725c046e2f1661bd00d2f185f62cc5ae35642c5d3c1", "758d1fefcce51be2d7949303642cb731f47ad2810748d52dc658992607adc7e7"},
    {L"Microsoft.WindowsAppRuntime.DDLM.2.msix", L"Microsoft.WinAppRuntime.DDLM.2.5.1.0-a6", L"2.5.1.0", "82e733691c811d333b595e3d15991c9235cb92451dadaff376527c2421d7e30d", "f91e23f562deaf17c16f5541df94ca7db343fd45ce8e15def76738c5b14665a6"},
    {L"Microsoft.WindowsAppRuntime.Main.2.msix", L"MicrosoftCorporationII.WinAppRuntime.Main.2", L"2.5.1.0", "c9bbacff630dc7567ab23350d4400477e38b0b2299b9b8802f9af4e6887b5f0d", "7e965b348b9952c98db9ac187eb94bcf4cb3ca2a6a5437956e25a5aa40ef3edb"},
    {L"Microsoft.WindowsAppRuntime.Singleton.2.msix", L"MicrosoftCorporationII.WinAppRuntime.Singleton", L"8002.5.1.0", "00bb4edb6324943d624004f3d0fae073f403d83af6650f23cba681c6e71fdd87", "3f28fdc1e8176e9f732d3570d0987da25328048801db835b260c2b891fcb11c2"},
};
static constexpr wchar_t publisher[] = L"CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US";
static std::wstring fullName(const PackagePin& p) { return std::wstring(p.name) + L"_" + p.version + L"_arm64__8wekyb3d8bbwe"; }
static void regular(const std::wstring& path) {
    const DWORD flags = GetFileAttributesW(path.c_str());
    if (flags == INVALID_FILE_ATTRIBUTES || (flags & (FILE_ATTRIBUTE_DIRECTORY | FILE_ATTRIBUTE_REPARSE_POINT)))
        throw Failure{"RegularPinnedFile", ERROR_INVALID_DATA};
}
static std::string hashFile(const std::wstring& path) {
    regular(path); File file;
    file.h = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING, FILE_FLAG_SEQUENTIAL_SCAN, nullptr);
    require(file.h != INVALID_HANDLE_VALUE, "OpenPinnedBytes");
    LARGE_INTEGER length{}; require(GetFileSizeEx(file.h, &length) != FALSE, "PinnedBytesSize");
    if (length.QuadPart <= 0 || length.QuadPart > 64 * 1024 * 1024) throw Failure{"PinnedBytesBound", ERROR_INVALID_DATA};
    BCRYPT_ALG_HANDLE algorithm = nullptr; BCRYPT_HASH_HANDLE hash = nullptr;
    BYTE result[32]{}, bytes[65536]{}; DWORD got = 0; ULONGLONG total = 0;
    NTSTATUS status = BCryptOpenAlgorithmProvider(&algorithm, BCRYPT_SHA256_ALGORITHM, nullptr, 0);
    if (status >= 0) status = BCryptCreateHash(algorithm, &hash, nullptr, 0, nullptr, 0, 0);
    try {
        if (status < 0) throw Failure{"FileHashInit", static_cast<DWORD>(status)};
        do {
            require(ReadFile(file.h, bytes, sizeof(bytes), &got, nullptr) != FALSE, "PinnedBytesRead");
            total += got;
            if (total > static_cast<ULONGLONG>(length.QuadPart)) throw Failure{"PinnedBytesChanged", ERROR_INVALID_DATA};
            status = BCryptHashData(hash, bytes, got, 0);
            if (status < 0) throw Failure{"FileHashData", static_cast<DWORD>(status)};
        } while (got);
        if (total != static_cast<ULONGLONG>(length.QuadPart)) throw Failure{"PinnedBytesChanged", ERROR_INVALID_DATA};
        status = BCryptFinishHash(hash, result, sizeof(result), 0);
        if (status < 0) throw Failure{"FileHashFinish", static_cast<DWORD>(status)};
    } catch (...) { if (hash) BCryptDestroyHash(hash); if (algorithm) BCryptCloseAlgorithmProvider(algorithm, 0); throw; }
    BCryptDestroyHash(hash); BCryptCloseAlgorithmProvider(algorithm, 0);
    std::string text;
    for (BYTE b : result) { text += "0123456789abcdef"[b >> 4]; text += "0123456789abcdef"[b & 15]; }
    return text;
}
static void durable(const std::wstring& path, const std::string& json) {
    if (json.size() > 8192) throw Failure{"PhaseEvidenceBound", ERROR_MORE_DATA};
    File file; file.h = CreateFileW(path.c_str(), GENERIC_WRITE, 0, nullptr, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr);
    require(file.h != INVALID_HANDLE_VALUE, "CreateOwnPhaseEvidence");
    DWORD written = 0; require(WriteFile(file.h, json.data(), static_cast<DWORD>(json.size()), &written, nullptr) != FALSE, "WritePhaseEvidence");
    if (written != json.size()) throw Failure{"PhaseEvidenceLength", ERROR_WRITE_FAULT};
    require(FlushFileBuffers(file.h) != FALSE, "FlushPhaseEvidence");
    require(CloseHandle(file.h) != FALSE, "ClosePhaseEvidence"); file.h = INVALID_HANDLE_VALUE;
}
static void signature(const std::wstring& path) {
    WINTRUST_FILE_INFO file{}; file.cbStruct = sizeof(file); file.pcwszFilePath = path.c_str();
    WINTRUST_DATA data{}; data.cbStruct = sizeof(data); data.dwUIChoice = WTD_UI_NONE;
    data.dwUnionChoice = WTD_CHOICE_FILE; data.pFile = &file; data.dwStateAction = WTD_STATEACTION_VERIFY;
    GUID action = WINTRUST_ACTION_GENERIC_VERIFY_V2;
    const LONG result = WinVerifyTrust(reinterpret_cast<HWND>(INVALID_HANDLE_VALUE), &action, &data);
    data.dwStateAction = WTD_STATEACTION_CLOSE; WinVerifyTrust(reinterpret_cast<HWND>(INVALID_HANDLE_VALUE), &action, &data);
    if (result != ERROR_SUCCESS) throw Failure{"RawMSIXWinVerifyTrust", static_cast<DWORD>(result)};
}
static void frozenPackage(const std::wstring& root, const PackagePin& p) {
    if (hashFile(root + L"\\" + p.file) != p.hash || hashFile(root + L"\\" + p.file + L".manifest.xml") != p.manifestHash)
        throw Failure{"FrozenMSIXManifestPin", ERROR_INVALID_DATA};
}
static std::string module(HMODULE loaded, const std::wstring& expected, const std::string& pin) {
    wchar_t path[32768]{}; const DWORD n = GetModuleFileNameW(loaded, path, 32768);
    if (!loaded || !n || n >= 32768 || _wcsicmp(path, expected.c_str())) throw Failure{"LoadedModulePath", ERROR_INVALID_DATA};
    const std::string actualHash = hashFile(path);
    if (actualHash != pin) throw Failure{"LoadedModulePin", ERROR_INVALID_DATA};
    DWORD unused = 0; const DWORD bytes = GetFileVersionInfoSizeW(path, &unused);
    if (!bytes || bytes > 65536) throw Failure{"ModuleVersionBound", ERROR_INVALID_DATA};
    std::vector<BYTE> info(bytes); require(GetFileVersionInfoW(path, 0, bytes, info.data()) != FALSE, "ModuleVersion");
    VS_FIXEDFILEINFO* version = nullptr; UINT length = 0;
    require(VerQueryValueW(info.data(), L"\\", reinterpret_cast<LPVOID*>(&version), &length) != FALSE, "ModuleVersionValue");
    if (length != sizeof(*version) || !version || version->dwSignature != 0xfeef04bd) throw Failure{"ModuleVersionShape", ERROR_INVALID_DATA};
    const std::string v = std::to_string(HIWORD(version->dwFileVersionMS)) + "." + std::to_string(LOWORD(version->dwFileVersionMS)) +
        "." + std::to_string(HIWORD(version->dwFileVersionLS)) + "." + std::to_string(LOWORD(version->dwFileVersionLS));
    return "{\"path\":" + quoted(winrt::to_string(std::wstring(path))) + ",\"sha256\":" + quoted(actualHash) + ",\"fileVersion\":" + quoted(v) + "}";
}
static std::array<std::string, 2> modulePins(const std::wstring& root) {
    const auto path = root + L"\\TEST-module-pins.txt"; regular(path); File file;
    file.h = CreateFileW(path.c_str(), GENERIC_READ, FILE_SHARE_READ, nullptr, OPEN_EXISTING, 0, nullptr);
    require(file.h != INVALID_HANDLE_VALUE, "ModulePinsOpen");
    char bytes[131]{}; DWORD n = 0; require(ReadFile(file.h, bytes, sizeof(bytes), &n, nullptr) != FALSE, "ModulePinsRead");
    if (n != 130 || bytes[64] != '\n' || bytes[129] != '\n') throw Failure{"ModulePinsShape", ERROR_INVALID_DATA};
    std::array<std::string, 2> pins{std::string(bytes, 64), std::string(bytes + 65, 64)};
    for (const auto& p : pins) if (p.find_first_not_of("0123456789abcdef") != std::string::npos) throw Failure{"ModulePinHex", ERROR_INVALID_DATA};
    return pins;
}
static bool installed(winrt::Windows::Management::Deployment::PackageManager const& manager, const PackagePin& expected) {
    using Architecture = winrt::Windows::System::ProcessorArchitecture;
    const bool framework = !wcscmp(expected.name, packages[0].name);
    unsigned enumerated = 0, count = 0;
    for (const auto& p : manager.FindPackagesForUser(L"", expected.name, publisher)) {
        if (++enumerated > 8) throw Failure{"CurrentUserAdmissionCountBound", ERROR_MORE_DATA};
        const auto id = p.Id();
        if (id.Name() != expected.name || id.Publisher() != publisher || (framework && !p.IsFramework()))
            throw Failure{"ConflictingCurrentUserPackage", ERROR_INVALID_DATA};
        // ARM64 supports coexisting Microsoft x86/x64 Frameworks; they never prove ARM64 presence.
        if (framework && (id.Architecture() == Architecture::X86 || id.Architecture() == Architecture::X64)) continue;
        if (++count > 1 || id.FullName() != fullName(expected) || id.Architecture() != Architecture::Arm64)
            throw Failure{"ConflictingCurrentUserPackage", ERROR_INVALID_DATA};
    }
    return count == 1;
}
static void inventory(winrt::Windows::Management::Deployment::PackageManager const& manager, std::string& observed) {
    for (const auto& expected : packages) {
        unsigned count = 0; std::string rows = "[";
        for (const auto& p : manager.FindPackagesForUser(L"", expected.name, publisher)) {
            if (++count > 8) throw Failure{"CurrentUserInventoryCountBound", ERROR_MORE_DATA};
            const auto id = p.Id(); const auto version = id.Version();
            const std::string v = std::to_string(version.Major) + "." + std::to_string(version.Minor) + "." +
                std::to_string(version.Build) + "." + std::to_string(version.Revision);
            const std::string row = "{\"name\":" + quoted(winrt::to_string(id.Name())) + ",\"fullName\":" +
                quoted(winrt::to_string(id.FullName())) + ",\"publisher\":" + quoted(winrt::to_string(id.Publisher())) +
                ",\"architecture\":" + std::to_string(static_cast<int>(id.Architecture())) + ",\"version\":" + quoted(v) + "}";
            if (observed.size() + rows.size() + row.size() + 256 > 4096) throw Failure{"CurrentUserInventoryByteBound", ERROR_MORE_DATA};
            if (count > 1) rows += ","; rows += row;
        }
        if (observed.size() > 1) observed += ",";
        observed += "{\"requestedName\":" + quoted(winrt::to_string(expected.name)) + ",\"count\":" + std::to_string(count) + ",\"packages\":" + rows + "]}";
    }
}
static std::wstring selectedFramework() {
    UINT32 bytes = 0, count = 0; const UINT32 flags = PACKAGE_FILTER_HEAD | PACKAGE_FILTER_DIRECT | PACKAGE_FILTER_DYNAMIC;
    LONG error = GetCurrentPackageInfo(flags, &bytes, nullptr, &count);
    if (error != ERROR_INSUFFICIENT_BUFFER || !bytes || bytes > 1024 * 1024) throw Failure{"SelectedGraphSize", static_cast<DWORD>(error)};
    std::vector<BYTE> buffer(bytes); error = GetCurrentPackageInfo(flags, &bytes, buffer.data(), &count);
    if (error || count > buffer.size() / sizeof(PACKAGE_INFO)) throw Failure{"SelectedGraphQuery", static_cast<DWORD>(error)};
    const auto* info = reinterpret_cast<const PACKAGE_INFO*>(buffer.data()); std::wstring path; unsigned matches = 0;
    for (UINT32 i = 0; i < count; ++i) if (info[i].packageId.name && !wcscmp(info[i].packageId.name, packages[0].name)) {
        if (++matches != 1 || !info[i].packageFullName || fullName(packages[0]) != info[i].packageFullName || !info[i].path)
            throw Failure{"SelectedFrameworkIdentity", ERROR_INVALID_DATA};
        path = info[i].path;
    }
    if (matches != 1) throw Failure{"SelectedFrameworkAbsent", ERROR_NOT_FOUND};
    return path;
}
int wmain(int argc, wchar_t** argv) {
    if (argc != 7 || !validNonce(argv[2])) return 64;
    const bool deploy = wcscmp(argv[1], L"--TEST-sdk-deploy") == 0;
    if (!deploy && wcscmp(argv[1], L"--TEST-sdk-bootstrap")) return 64;
    const std::wstring root(argv[3]), wideNonce(argv[2]);
    const std::string nonce(wideNonce.begin(), wideNonce.end());
    // Only baseline identity digests and session number are passed, never raw SID/LUID values.
    std::string current = "null", after = "null", bootstrapModule = "null", runtimeModule = "null", selected = "null", phases = "[";
    std::string observedPackages = "[";
    bool complete = false, isSupported = false, bootstrapCalled = false, bootstrapShutdown = false;
    const char* query = nullptr; DWORD error = 0; HRESULT bootstrapHR = E_PENDING;
    try {
        Token token; require(OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY | TOKEN_DUPLICATE, &token.handle) != FALSE, "OwnToken");
        const auto before = facts(token.handle); current = factsJson(before);
        DWORD processSession = 0;
        require(ProcessIdToSessionId(GetCurrentProcessId(), &processSession) != FALSE, "ActorProcessSession");
        if (processSession != before.session) throw Failure{"ActorTokenSessionMismatch", ERROR_INVALID_DATA};
        if (before.sid != winrt::to_string(argv[4]) || before.auth != winrt::to_string(argv[5]) || std::to_wstring(before.session) != argv[6])
            throw Failure{"ActorIdentitySession", ERROR_INVALID_DATA};
        if (!deploy && (before.elevated || before.rid != SECURITY_MANDATORY_MEDIUM_RID || enabledAdmins(token.handle)))
            throw Failure{"ActorMediumAdmission", ERROR_INVALID_DATA};
        const auto pins = modulePins(root);
        bootstrapModule = module(GetModuleHandleW(L"Microsoft.WindowsAppRuntime.Bootstrap.dll"), root + L"\\Microsoft.WindowsAppRuntime.Bootstrap.dll", pins[0]);
        Apartment apartment;
        winrt::init_apartment(winrt::apartment_type::multi_threaded); apartment.initialized = true;
        if (deploy) {
            for (const auto& p : packages) { frozenPackage(root, p); signature(root + L"\\" + p.file); }
            winrt::Windows::Management::Deployment::PackageManager manager;
            inventory(manager, observedPackages); // Retain actual public identities before the unchanged conflict gate.
            for (const auto& p : packages) installed(manager, p); // Conflicts stop before the first Add.
            unsigned index = 0;
            for (const auto& p : packages) {
                frozenPackage(root, p); signature(root + L"\\" + p.file);
                if (factsJson(facts(token.handle)) != current) throw Failure{"DeploymentTokenChanged", ERROR_INVALID_DATA};
                const bool present = installed(manager, p);
                const std::string phase = "{\"nonce\":" + quoted(nonce) + ",\"pid\":" + std::to_string(GetCurrentProcessId()) +
                    ",\"package\":" + quoted(winrt::to_string(fullName(p))) + ",\"sha256\":" + quoted(p.hash) + ",\"token\":" + current;
                const std::wstring prefix = root + L"\\TEST-deploy-" + std::to_wstring(index++);
                durable(prefix + L"-intent.json", phase + ",\"operation\":" + quoted(present ? "skip_exact" : "AddPackageAsync_None") + "}");
                int asyncStatus = -1; HRESULT extended = E_PENDING;
                if (!present) {
                    std::wstring uri = L"file:///" + root + L"/" + p.file;
                    for (auto& c : uri) if (c == L'\\') c = L'/';
                    const auto operation = manager.AddPackageAsync(winrt::Windows::Foundation::Uri(uri), nullptr,
                        winrt::Windows::Management::Deployment::DeploymentOptions::None);
                    const auto result = operation.get(); // Exception leaves durable intent without fabricated completion.
                    asyncStatus = static_cast<int>(operation.Status()); extended = result.ExtendedErrorCode();
                }
                const bool completed = present || (asyncStatus == static_cast<int>(winrt::Windows::Foundation::AsyncStatus::Completed) && extended == S_OK);
                const bool readback = completed && installed(manager, p);
                const std::string tokenAfter = factsJson(facts(token.handle));
                durable(prefix + L"-complete.json", phase + ",\"operation\":" + quoted(present ? "skip_exact" : "AddPackageAsync_None") +
                    ",\"asyncStatus\":" + (present ? "null" : std::to_string(asyncStatus)) +
                    ",\"extendedHRESULT\":" + (present ? "null" : std::to_string(static_cast<DWORD>(extended))) +
                    ",\"tokenAfter\":" + tokenAfter + ",\"readbackExact\":" + (readback ? "true" : "false") + ",\"completionObserved\":true}");
                if (!completed) throw Failure{"DeploymentStatusNotExactSOK", extended != S_OK ? static_cast<DWORD>(extended) : ERROR_INVALID_DATA};
                if (!readback || tokenAfter != current) throw Failure{"CurrentUserReadback", ERROR_INVALID_DATA};
                if (index > 1) phases += ",";
                phases += "{\"package\":" + quoted(winrt::to_string(fullName(p))) + ",\"skippedExact\":" + (present ? "true" : "false") + ",\"readbackExact\":true}";
            }
        } else {
            Bootstrap bootstrap(bootstrapShutdown); PACKAGE_VERSION minimum{}; minimum.Version = WINDOWSAPPSDK_RUNTIME_VERSION_UINT64;
            bootstrapCalled = true;
            bootstrapHR = MddBootstrapInitialize2(WINDOWSAPPSDK_RELEASE_MAJORMINOR, WINDOWSAPPSDK_RELEASE_VERSION_TAG_W,
                minimum, MddBootstrapInitializeOptions_None);
            winrt::check_hresult(bootstrapHR); bootstrap.initialized = true;
            const auto framework = selectedFramework();
            selected = quoted(winrt::to_string(fullName(packages[0])));
            if (hashFile(framework + L"\\Microsoft.WindowsAppRuntime.dll") != pins[1]) throw Failure{"SelectedPayloadPin", ERROR_INVALID_DATA};
            isSupported = winrt::Microsoft::Windows::AppNotifications::AppNotificationManager::IsSupported();
            runtimeModule = module(GetModuleHandleW(L"Microsoft.WindowsAppRuntime.dll"), framework + L"\\Microsoft.WindowsAppRuntime.dll", pins[1]);
            bootstrap.shutdown(); bootstrapShutdown = true;
        }
        after = factsJson(facts(token.handle));
        require(ProcessIdToSessionId(GetCurrentProcessId(), &processSession) != FALSE, "ActorProcessSessionAfter");
        if (processSession != before.session) throw Failure{"ActorProcessSessionChanged", ERROR_INVALID_DATA};
        if (after != current) throw Failure{"ActorTokenChanged", ERROR_INVALID_DATA};
        complete = true;
    } catch (const Failure& f) { query = f.query; error = f.error; }
      catch (const winrt::hresult_error& e) { query = "HRESULT"; error = static_cast<DWORD>(e.code().value); }
      catch (...) { query = "exception"; error = ERROR_NOT_ENOUGH_MEMORY; }
    phases += "]";
    observedPackages += "]";
    const std::string json = "{\"nonce\":" + quoted(nonce) + ",\"pid\":" + std::to_string(GetCurrentProcessId()) +
        ",\"stage\":" + quoted(deploy ? "deploy" : "bootstrap") + ",\"before\":" + current + ",\"after\":" + after +
        ",\"bootstrapModule\":" + bootstrapModule + ",\"runtimeModule\":" + runtimeModule + ",\"selectedFramework\":" + selected + ",\"phases\":" + phases + ",\"packageInventory\":" + observedPackages +
        ",\"bootstrapCalled\":" + (bootstrapCalled ? "true" : "false") + ",\"bootstrapHRESULT\":" + std::to_string(static_cast<DWORD>(bootstrapHR)) +
        ",\"bootstrapShutdown\":" + (bootstrapShutdown ? "true" : "false") + ",\"isSupported\":" + (complete && !deploy ? (isSupported ? "true" : "false") : "null") +
        ",\"queriesComplete\":" + (complete ? "true" : "false") + ",\"query\":" + (query ? quoted(query) : "null") +
        ",\"error\":" + (query ? std::to_string(error) : "null") + ",\"sdkNativeCallback\":false,\"notificationEffects\":0}";
    if (json.size() > 8192) return 65;
    std::puts(json.c_str()); return complete ? 0 : 1;
}
