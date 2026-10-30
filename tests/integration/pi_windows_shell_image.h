#pragma once
// TEST-only held Windows image custody shared by native observers.
#include <windows.h>
#include <wintrust.h>
#include <softpub.h>
#include <wincrypt.h>
#include <filesystem>
#include <vector>
#include <string>
#include <algorithm>
#include "navigation_windows_token_queries.h"
#pragma comment(lib, "Wintrust.lib")
#pragma comment(lib, "Crypt32.lib")
#pragma comment(lib, "Bcrypt.lib")
namespace PiWindowsShellImageTEST {
namespace fs = std::filesystem;
static void demand(bool value, const char* reason) { if (!value) throw reason; }
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
} // namespace PiWindowsShellImageTEST
