// TEST-only, read-only current-process token measurement. No SDK calls.
#include <windows.h>
#include <wtsapi32.h>
#include <bcrypt.h>
#include <cstdio>
#include <cstring>
#include <stdexcept>
#include <string>
#include <vector>

struct Failure { const char* query; DWORD error; };
struct Token {
    HANDLE handle = nullptr;
    ~Token() { if (handle) CloseHandle(handle); }
};
static void require(bool ok, const char* query) {
    if (!ok) throw Failure{query, GetLastError()};
}
template<typename T>
static T queryFixedToken(HANDLE token, TOKEN_INFORMATION_CLASS kind, const char* name) {
    T value{};
    DWORD returned = 0;
    require(GetTokenInformation(token, kind, &value, sizeof(value), &returned) != FALSE, name);
    if (returned != sizeof(value)) throw Failure{name, ERROR_INVALID_DATA};
    return value;
}
static std::vector<BYTE> queryToken(HANDLE token, TOKEN_INFORMATION_CLASS kind,
                                    const char* name, DWORD minimum) {
    DWORD needed = 0;
    SetLastError(ERROR_SUCCESS);
    const BOOL first = GetTokenInformation(token, kind, nullptr, 0, &needed);
    const DWORD error = GetLastError();
    if (first || error != ERROR_INSUFFICIENT_BUFFER || needed < minimum || needed > 65536)
        throw Failure{name, error ? error : ERROR_INVALID_DATA};
    std::vector<BYTE> data(needed);
    DWORD returned = 0;
    require(GetTokenInformation(token, kind, data.data(), needed, &returned) != FALSE, name);
    if (returned < minimum || returned > needed) throw Failure{name, ERROR_INVALID_DATA};
    data.resize(returned);
    return data;
}
static DWORD boundedSidSize(const std::vector<BYTE>& data, PSID sid, const char* name) {
    const auto begin = reinterpret_cast<ULONG_PTR>(data.data());
    const auto pointer = reinterpret_cast<ULONG_PTR>(sid);
    if (pointer < begin || pointer - begin > data.size() || data.size() - (pointer - begin) < 8)
        throw Failure{name, ERROR_INVALID_SID};
    const auto bytes = reinterpret_cast<const BYTE*>(sid);
    const DWORD length = 8 + 4 * static_cast<DWORD>(bytes[1]);
    if (length > data.size() - (pointer - begin) || !IsValidSid(sid))
        throw Failure{name, ERROR_INVALID_SID};
    return length;
}
static std::string digest(const BYTE* bytes, DWORD count) {
    BCRYPT_ALG_HANDLE algorithm = nullptr;
    NTSTATUS status = BCryptOpenAlgorithmProvider(&algorithm, BCRYPT_SHA256_ALGORITHM, nullptr, 0);
    if (status < 0) throw Failure{"BCryptOpenAlgorithmProvider", static_cast<DWORD>(status)};
    BYTE hash[32]{};
    status = BCryptHash(algorithm, nullptr, 0, const_cast<BYTE*>(bytes), count, hash, sizeof(hash));
    BCryptCloseAlgorithmProvider(algorithm, 0);
    if (status < 0) throw Failure{"BCryptHash", static_cast<DWORD>(status)};
    std::string result;
    for (BYTE value : hash) {
        result += "0123456789abcdef"[value >> 4];
        result += "0123456789abcdef"[value & 15];
    }
    return result;
}
static bool validNonce(const wchar_t* value) {
    if (wcslen(value) != 36) return false;
    for (size_t i = 0; i < 36; ++i) {
        if (i == 8 || i == 13 || i == 18 || i == 23) { if (value[i] != L'-') return false; }
        else if (!((value[i] >= L'0' && value[i] <= L'9') || (value[i] >= L'a' && value[i] <= L'f')))
            return false;
    }
    return true;
}
int wmain(int argc, wchar_t** argv) {
    if (argc != 3 || wcscmp(argv[1], L"--read-only-TEST-token-preflight") || !validNonce(argv[2]))
        return 64;
    const std::wstring wideNonce(argv[2]);
    const std::string nonce(wideNonce.begin(), wideNonce.end());
    try {
        Token token;
        require(OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token.handle) != FALSE, "OpenProcessToken");
        const auto elevation = queryFixedToken<TOKEN_ELEVATION>(token.handle, TokenElevation, "TokenElevation");
        const auto type = queryFixedToken<TOKEN_ELEVATION_TYPE>(token.handle, TokenElevationType, "TokenElevationType");
        auto integrity = queryToken(token.handle, TokenIntegrityLevel, "TokenIntegrityLevel", sizeof(TOKEN_MANDATORY_LABEL));
        auto user = queryToken(token.handle, TokenUser, "TokenUser", sizeof(TOKEN_USER));
        const auto statistics = queryFixedToken<TOKEN_STATISTICS>(token.handle, TokenStatistics, "TokenStatistics");
        const auto levelSid = reinterpret_cast<TOKEN_MANDATORY_LABEL*>(integrity.data())->Label.Sid;
        boundedSidSize(integrity, levelSid, "IntegritySID");
        const BYTE subCount = *GetSidSubAuthorityCount(levelSid);
        if (!subCount) throw Failure{"IntegrityRID", ERROR_INVALID_SID};
        const DWORD rid = *GetSidSubAuthority(levelSid, subCount - 1);
        const auto userSid = reinterpret_cast<TOKEN_USER*>(user.data())->User.Sid;
        const DWORD userSize = boundedSidSize(user, userSid, "TokenUserSID");
        const std::string sidHash = digest(reinterpret_cast<const BYTE*>(userSid), userSize);
        const auto& luid = statistics.AuthenticationId;
        const std::string authHash = digest(reinterpret_cast<const BYTE*>(&luid), sizeof(luid));
        const DWORD elevated = elevation.TokenIsElevated;
        const auto elevationType = type;
        if (elevated > 1 || elevationType < TokenElevationTypeDefault || elevationType > TokenElevationTypeLimited)
            throw Failure{"TokenValue", ERROR_INVALID_DATA};
        DWORD session = 0;
        require(ProcessIdToSessionId(GetCurrentProcessId(), &session) != FALSE, "ProcessIdToSessionId");
        LPWSTR wtsData = nullptr;
        DWORD wtsBytes = 0;
        require(WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, session, WTSConnectState,
                                            &wtsData, &wtsBytes) != FALSE, "WTSConnectState");
        if (!wtsData || wtsBytes != sizeof(WTS_CONNECTSTATE_CLASS)) {
            if (wtsData) WTSFreeMemory(wtsData);
            throw Failure{"WTSConnectStateSize", ERROR_INVALID_DATA};
        }
        const auto state = *reinterpret_cast<WTS_CONNECTSTATE_CLASS*>(wtsData);
        WTSFreeMemory(wtsData);
        if (state < WTSActive || state > WTSInit) throw Failure{"WTSConnectStateValue", ERROR_INVALID_DATA};
        const char* precondition = elevated ? "unsupported_elevated" :
            (rid >= SECURITY_MANDATORY_HIGH_RID ? "unsupported_high_integrity" : "supported_non_elevated");
        std::printf("{\"nonce\":\"%s\",\"pid\":%lu,\"session\":%lu,\"elevated\":%s,"
                    "\"elevationType\":%u,\"integrityRID\":%lu,\"sidSHA256\":\"%s\","
                    "\"authLUIDSHA256\":\"%s\",\"wtsState\":%u,\"queriesComplete\":true,"
                    "\"tokenPrecondition\":\"%s\",\"nativeEffects\":0}\n",
                    nonce.c_str(), GetCurrentProcessId(), session, elevated ? "true" : "false",
                    static_cast<unsigned>(elevationType), rid, sidHash.c_str(), authHash.c_str(),
                    static_cast<unsigned>(state), precondition);
        return 0;
    } catch (const Failure& failure) {
        std::printf("{\"nonce\":\"%s\",\"pid\":%lu,\"queriesComplete\":false,"
                    "\"tokenPrecondition\":\"unknown\",\"query\":\"%s\",\"error\":%lu,\"nativeEffects\":0}\n",
                    nonce.c_str(), GetCurrentProcessId(), failure.query, failure.error);
        return 1;
    } catch (...) {
        std::printf("{\"nonce\":\"%s\",\"pid\":%lu,\"queriesComplete\":false,"
                    "\"tokenPrecondition\":\"unknown\",\"query\":\"exception\",\"error\":8,\"nativeEffects\":0}\n",
                    nonce.c_str(), GetCurrentProcessId());
        return 1;
    }
}
