#pragma once
// Same fixed TEST byte, module and dynamic-graph observations for the two SDK actors.
#include "../navigation_windows_token_queries.h"
#include <WindowsAppSDK-VersionInfo.h>
#include <MddBootstrap.h>
#include <appmodel.h>
#include <winver.h>
#include <winrt/base.h>
#include <array>
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
static std::wstring selectedFramework(const std::wstring& expectedFullName) {
    UINT32 bytes = 0, count = 0; const UINT32 flags = PACKAGE_FILTER_HEAD | PACKAGE_FILTER_DIRECT | PACKAGE_FILTER_DYNAMIC;
    LONG error = GetCurrentPackageInfo(flags, &bytes, nullptr, &count);
    if (error != ERROR_INSUFFICIENT_BUFFER || !bytes || bytes > 1024 * 1024) throw Failure{"SelectedGraphSize", static_cast<DWORD>(error)};
    std::vector<BYTE> buffer(bytes); error = GetCurrentPackageInfo(flags, &bytes, buffer.data(), &count);
    if (error || count > buffer.size() / sizeof(PACKAGE_INFO)) throw Failure{"SelectedGraphQuery", static_cast<DWORD>(error)};
    const auto* info = reinterpret_cast<const PACKAGE_INFO*>(buffer.data()); std::wstring path; unsigned matches = 0;
    for (UINT32 i = 0; i < count; ++i) if (info[i].packageId.name && !wcscmp(info[i].packageId.name, L"Microsoft.WindowsAppRuntime.2")) {
        if (++matches != 1 || !info[i].packageFullName || expectedFullName != info[i].packageFullName || !info[i].path)
            throw Failure{"SelectedFrameworkIdentity", ERROR_INVALID_DATA};
        path = info[i].path;
    }
    if (matches != 1) throw Failure{"SelectedFrameworkAbsent", ERROR_NOT_FOUND};
    return path;
}
