// TEST-only, read-only current-process token measurement. No SDK calls.
#include <windows.h>
#include <wtsapi32.h>
#include <bcrypt.h>
#include <cstdio>
#include <cstring>
#include <stdexcept>
#include <string>
#include <vector>

#include "navigation_windows_token_queries.h"
using namespace NavigationTokenTEST;

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
