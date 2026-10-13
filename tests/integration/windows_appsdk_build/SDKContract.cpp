// Compile/link prerequisite for the unpackaged native callback canary.
// CI never executes this binary. No registration or notification is performed.
#include <windows.h>
#include <WindowsAppSDK-VersionInfo.h>
#include <MddBootstrap.h>
#include <winrt/base.h>
#include <winrt/Microsoft.Windows.AppNotifications.h>
#include <winrt/Microsoft.Windows.AppLifecycle.h>
#include <type_traits>

using namespace winrt::Microsoft::Windows::AppNotifications;
static_assert(std::is_same_v<decltype(AppNotificationManager::IsSupported()), bool>);
static_assert(std::is_same_v<decltype(AppNotificationManager::Default().Show(
    std::declval<AppNotification const&>())), void>);
static_assert(std::is_same_v<decltype(std::declval<AppNotificationActivatedEventArgs>().Argument()), winrt::hstring>);
static_assert(std::is_same_v<decltype(std::declval<AppNotificationActivatedEventArgs>().Arguments()),
    winrt::Windows::Foundation::Collections::IMap<winrt::hstring, winrt::hstring>>);
static_assert(WINDOWSAPPSDK_RELEASE_MAJORMINOR != 0);

// Export keeps the bootstrap ABI in the linked image; the function is not run.
extern "C" __declspec(dllexport) HRESULT SDKBootstrapLinkContract() noexcept {
    PACKAGE_VERSION minimum{};
    minimum.Version = WINDOWSAPPSDK_RUNTIME_VERSION_UINT64;
    const HRESULT hr = MddBootstrapInitialize2(WINDOWSAPPSDK_RELEASE_MAJORMINOR,
        WINDOWSAPPSDK_RELEASE_VERSION_TAG_W, minimum, MddBootstrapInitializeOptions_None);
    if (SUCCEEDED(hr)) MddBootstrapShutdown();
    return hr;
}

int main() { return 0; }
