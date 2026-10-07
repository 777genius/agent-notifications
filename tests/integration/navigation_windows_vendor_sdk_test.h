// Shared TEST SDK primitives only. No registration, process or default handler effects.
#pragma once
#include <windows.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.Management.Deployment.h>
#include <winrt/Windows.ApplicationModel.h>
#include <winrt/Windows.System.h>
#include <vector>
#include <algorithm>
#include <stdexcept>
namespace NavigationVendorTEST {
inline constexpr auto vendorName = L"OpenAI.Codex";
inline constexpr auto vendorPublisher = L"CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B";
inline constexpr auto vendorFamily = L"OpenAI.Codex_2p2nqsd0c76g0";
inline constexpr auto vendorFull = L"OpenAI.Codex_26.930.7945.0_arm64__2p2nqsd0c76g0";
inline std::vector<winrt::Windows::ApplicationModel::Package> installed() {
    winrt::Windows::Management::Deployment::PackageManager manager;
    std::vector<winrt::Windows::ApplicationModel::Package> packages;
    // Empty SID means current user, not all users. Export no unrelated package inventory.
    for (const auto& package : manager.FindPackagesForUser(L"", vendorFamily)) {
        if (packages.size() == 8) throw std::runtime_error("vendor family enumeration bound");
        const auto id = package.Id();
        if (id.Name() != vendorName || id.Publisher() != vendorPublisher || id.FamilyName() != vendorFamily)
            throw std::runtime_error("vendor installed identity mismatch");
        packages.push_back(package);
    }
    return packages;
}
inline void exactInstalled() {
    auto packages = installed();
    if (packages.size() != 1 || packages[0].Id().FullName() != vendorFull
        || packages[0].Id().Architecture() != winrt::Windows::System::ProcessorArchitecture::Arm64
        || packages[0].Id().Version().Major != 26 || packages[0].Id().Version().Minor != 930
        || packages[0].Id().Version().Build != 7945 || packages[0].Id().Version().Revision != 0)
        throw std::runtime_error("exact selected vendor changed");
}
template<typename Operation> inline auto await(const Operation& operation, DWORD milliseconds, ULONGLONG absolute) {
    const ULONGLONG deadline = std::min(absolute, GetTickCount64() + milliseconds);
    while (operation.Status() == winrt::Windows::Foundation::AsyncStatus::Started) {
        if (GetTickCount64() >= deadline) {
            operation.Cancel(); // No guarantee of external cancellation/quiescence.
            throw std::runtime_error("vendor async deadline; outcome unknown");
        }
        Sleep(10);
    }
    auto error = operation.ErrorCode(); winrt::check_hresult(error);
    if (operation.Status() != winrt::Windows::Foundation::AsyncStatus::Completed || error.value != S_OK)
        throw std::runtime_error("vendor async incomplete");
    if (GetTickCount64() >= deadline) throw std::runtime_error("vendor phase completion deadline");
    auto result = operation.GetResults();
    if (GetTickCount64() >= deadline) throw std::runtime_error("vendor overall deadline");
    return result;
}
}
