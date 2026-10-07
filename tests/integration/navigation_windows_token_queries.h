#pragma once
// Narrow TEST-only token query helpers; no token mutation or SDK calls.
#include <windows.h>
#include <bcrypt.h>
#include <cstring>
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

struct Facts {
    std::string sid, auth;
    DWORD session = 0, rid = 0;
    DWORD elevated = 0;
    TOKEN_ELEVATION_TYPE type{};
};
static Facts facts(HANDLE token) {
    Facts f;
    auto user = queryToken(token, TokenUser, "TokenUser", sizeof(TOKEN_USER));
    const auto sid = reinterpret_cast<TOKEN_USER*>(user.data())->User.Sid;
    f.sid = digest(reinterpret_cast<const BYTE*>(sid), boundedSidSize(user, sid, "TokenUserSID"));
    const auto statistics = queryFixedToken<TOKEN_STATISTICS>(token, TokenStatistics, "TokenStatistics");
    f.auth = digest(reinterpret_cast<const BYTE*>(&statistics.AuthenticationId), sizeof(LUID));
    f.session = queryFixedToken<DWORD>(token, TokenSessionId, "TokenSessionId");
    f.elevated = queryFixedToken<TOKEN_ELEVATION>(token, TokenElevation, "TokenElevation").TokenIsElevated;
    f.type = queryFixedToken<TOKEN_ELEVATION_TYPE>(token, TokenElevationType, "TokenElevationType");
    auto integrity = queryToken(token, TokenIntegrityLevel, "TokenIntegrityLevel", sizeof(TOKEN_MANDATORY_LABEL));
    const auto level = reinterpret_cast<TOKEN_MANDATORY_LABEL*>(integrity.data())->Label.Sid;
    boundedSidSize(integrity, level, "IntegritySID");
    const BYTE count = *GetSidSubAuthorityCount(level);
    if (!count || f.elevated > 1 || f.type < TokenElevationTypeDefault || f.type > TokenElevationTypeLimited)
        throw Failure{"TokenValue", ERROR_INVALID_DATA};
    f.rid = *GetSidSubAuthority(level, count - 1);
    return f;
}
static std::string factsJson(const Facts& f) {
    return "{\"sidSHA256\":\"" + f.sid + "\",\"authLUIDSHA256\":\"" + f.auth +
        "\",\"session\":" + std::to_string(f.session) + ",\"elevated\":" + (f.elevated ? "true" : "false") +
        ",\"elevationType\":" + std::to_string(f.type) + ",\"integrityRID\":" + std::to_string(f.rid) + "}";
}
static std::string quoted(const std::string& input) {
    std::string out = "\"";
    for (unsigned char c : input) {
        if (c == '"' || c == '\\') { out += '\\'; out += static_cast<char>(c); }
        else if (c < 32) {
            out += "\\u00"; out += "0123456789abcdef"[c >> 4]; out += "0123456789abcdef"[c & 15];
        } else out += static_cast<char>(c);
    }
    return out + "\"";
}
static bool enabledAdmins(HANDLE token) {
    Token duplicate;
    require(DuplicateTokenEx(token, TOKEN_QUERY, nullptr, SecurityIdentification, TokenImpersonation,
                             &duplicate.handle) != FALSE, "QueryOnlyDuplicate");
    BYTE sid[SECURITY_MAX_SID_SIZE]{};
    DWORD bytes = sizeof(sid);
    require(CreateWellKnownSid(WinBuiltinAdministratorsSid, nullptr, sid, &bytes) != FALSE, "AdministratorsSID");
    BOOL member = FALSE;
    require(CheckTokenMembership(duplicate.handle, sid, &member) != FALSE, "CheckTokenMembership");
    return member != FALSE;
}
