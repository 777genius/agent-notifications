// Disposable-CI prerequisite only: current-user deployment, bootstrap and static IsSupported.
#include "SDKObservationTEST.h"
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

struct PackagePin { const wchar_t* file; const wchar_t* name; const wchar_t* version; const char* hash; const char* manifestHash; };
static constexpr PackagePin packages[] = {
    {L"Microsoft.WindowsAppRuntime.2.msix", L"Microsoft.WindowsAppRuntime.2", L"2.5.1.0", "cbd61bfa868269a57cab6725c046e2f1661bd00d2f185f62cc5ae35642c5d3c1", "758d1fefcce51be2d7949303642cb731f47ad2810748d52dc658992607adc7e7"},
    {L"Microsoft.WindowsAppRuntime.DDLM.2.msix", L"Microsoft.WinAppRuntime.DDLM.2.5.1.0-a6", L"2.5.1.0", "82e733691c811d333b595e3d15991c9235cb92451dadaff376527c2421d7e30d", "f91e23f562deaf17c16f5541df94ca7db343fd45ce8e15def76738c5b14665a6"},
    {L"Microsoft.WindowsAppRuntime.Main.2.msix", L"MicrosoftCorporationII.WinAppRuntime.Main.2", L"2.5.1.0", "c9bbacff630dc7567ab23350d4400477e38b0b2299b9b8802f9af4e6887b5f0d", "7e965b348b9952c98db9ac187eb94bcf4cb3ca2a6a5437956e25a5aa40ef3edb"},
    {L"Microsoft.WindowsAppRuntime.Singleton.2.msix", L"MicrosoftCorporationII.WinAppRuntime.Singleton", L"8002.5.1.0", "00bb4edb6324943d624004f3d0fae073f403d83af6650f23cba681c6e71fdd87", "3f28fdc1e8176e9f732d3570d0987da25328048801db835b260c2b891fcb11c2"},
};
static constexpr wchar_t publisher[] = L"CN=Microsoft Corporation, O=Microsoft Corporation, L=Redmond, S=Washington, C=US";
static std::wstring fullName(const PackagePin& p) { return std::wstring(p.name) + L"_" + p.version + L"_arm64__8wekyb3d8bbwe"; }
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
            const auto framework = selectedFramework(fullName(packages[0]));
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
