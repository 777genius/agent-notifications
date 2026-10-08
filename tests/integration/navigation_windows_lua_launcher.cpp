// Own TEST child only. No SDK, UI, impersonation or explicit privilege changes.
#include "navigation_windows_token_queries.h"
#include <cstdio>
#include <cstddef>
#include <memory>

struct Handle {
    HANDLE value = nullptr;
    ~Handle() { if (value && value != INVALID_HANDLE_VALUE) CloseHandle(value); }
    Handle() = default;
    Handle(const Handle&) = delete;
    Handle& operator=(const Handle&) = delete;
};
struct Child {
    PROCESS_INFORMATION pi{};
    bool collected = false;
    ~Child() {
        if (pi.hProcess) {
            CloseHandle(pi.hProcess);
        }
        if (pi.hThread) CloseHandle(pi.hThread);
    }
};
struct Attributes {
    std::vector<BYTE> storage;
    LPPROC_THREAD_ATTRIBUTE_LIST list = nullptr;
    ~Attributes() { if (list) DeleteProcThreadAttributeList(list); }
};
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
        else if (c < 32 || c >= 127) {
            out += "\\u00"; out += "0123456789abcdef"[c >> 4]; out += "0123456789abcdef"[c & 15];
        } else out += static_cast<char>(c);
    }
    return out + "\"";
}
static void file(Handle& h, const std::wstring& path) {
    SECURITY_ATTRIBUTES sa{sizeof(sa), nullptr, TRUE};
    h.value = CreateFileW(path.c_str(), GENERIC_READ | GENERIC_WRITE, FILE_SHARE_READ,
                         &sa, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr);
    require(h.value != INVALID_HANDLE_VALUE, "CreateOwnTESTFile");
}
static std::string readOutput(HANDLE fileHandle) {
    require(FlushFileBuffers(fileHandle) != FALSE, "FlushOwnOutput");
    LARGE_INTEGER size{}, zero{};
    require(GetFileSizeEx(fileHandle, &size) != FALSE, "OwnOutputSize");
    if (size.QuadPart < 0 || size.QuadPart > 8192) throw Failure{"OwnOutputBound", ERROR_MORE_DATA};
    require(SetFilePointerEx(fileHandle, zero, nullptr, FILE_BEGIN) != FALSE, "OwnOutputSeek");
    std::string data(static_cast<size_t>(size.QuadPart), '\0');
    DWORD received = 0;
    require(ReadFile(fileHandle, data.data(), static_cast<DWORD>(data.size()), &received, nullptr) != FALSE, "OwnOutputRead");
    if (received != data.size()) throw Failure{"OwnOutputLength", ERROR_INVALID_DATA};
    return data;
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
int wmain(int argc, wchar_t** argv) {
    if (argc != 5 || !validNonce(argv[2])) return 64;
    const bool medium = wcscmp(argv[1], L"--TEST-lua-medium-token-preflight") == 0;
    if (!medium && wcscmp(argv[1], L"--TEST-lua-token-preflight")) return 64;
    const std::wstring wideNonce(argv[2]), root(argv[3]), probe(argv[4]);
    const std::string nonce(wideNonce.begin(), wideNonce.end());
    std::string baseline = "null", held = "null", output, stderrText, birth = "null";
    std::string restrictedBefore = "null", restrictedAfter = "null", parentAfter = "null";
    bool loweringAttempted = false, integrityLowered = false;
    DWORD childPid = 0, exitCode = 0;
    bool collected = false, timedOut = false, terminated = false, admin = false, adminQueried = false, exitKnown = false, identity = false, complete = false;
    const char* errorQuery = nullptr;
    DWORD errorCode = 0, cleanupError = 0;
    Handle job, out, err, input;
    Child child;
    try {
        wchar_t canonical[32768]{};
        const DWORD length = GetFullPathNameW(root.c_str(), 32768, canonical, nullptr);
        const auto basename = root.substr(root.find_last_of(L"\\/") + 1);
        const DWORD attrs = GetFileAttributesW(root.c_str());
        if (!length || length >= 32768 || root != canonical || basename.rfind(L"TEST-lua-preflight-", 0) != 0 ||
            attrs == INVALID_FILE_ATTRIBUTES || !(attrs & FILE_ATTRIBUTE_DIRECTORY) || (attrs & FILE_ATTRIBUTE_REPARSE_POINT) ||
            probe != root + L"\\TEST-token-" + wideNonce + L".exe")
            throw Failure{"OwnTESTPath", ERROR_INVALID_PARAMETER};
        const DWORD probeAttrs = GetFileAttributesW(probe.c_str());
        if (probeAttrs == INVALID_FILE_ATTRIBUTES || (probeAttrs & (FILE_ATTRIBUTE_DIRECTORY | FILE_ATTRIBUTE_REPARSE_POINT)))
            throw Failure{"OwnProbePath", ERROR_INVALID_PARAMETER};
        Token parent, restricted;
        require(OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY | TOKEN_DUPLICATE | TOKEN_ASSIGN_PRIMARY |
                                 (medium ? TOKEN_ADJUST_DEFAULT : 0),
                                 &parent.handle) != FALSE, "OwnPrimaryToken");
        const auto before = facts(parent.handle);
        DWORD ownSession = 0;
        require(ProcessIdToSessionId(GetCurrentProcessId(), &ownSession) != FALSE, "OwnProcessSession");
        if (ownSession != before.session) throw Failure{"OwnSessionMismatch", ERROR_INVALID_DATA};
        baseline = factsJson(before);
        require(CreateRestrictedToken(parent.handle, LUA_TOKEN, 0, nullptr, 0, nullptr, 0, nullptr,
                                      &restricted.handle) != FALSE, "CreateRestrictedTokenLUA");
        if (medium) {
            const auto initial = facts(restricted.handle); restrictedBefore = factsJson(initial);
            if (initial.rid < SECURITY_MANDATORY_MEDIUM_RID)
                throw Failure{"RestrictedBelowMediumNoRaise", ERROR_INVALID_DATA};
            if (initial.rid > SECURITY_MANDATORY_MEDIUM_RID) {
                struct MediumLabel { TOKEN_MANDATORY_LABEL label{}; alignas(DWORD) BYTE sid[SECURITY_MAX_SID_SIZE]{}; } buffer;
                static_assert(offsetof(MediumLabel, sid) == sizeof(TOKEN_MANDATORY_LABEL));
                DWORD sidBytes = sizeof(buffer.sid);
                require(CreateWellKnownSid(WinMediumLabelSid, nullptr, buffer.sid, &sidBytes) != FALSE, "MediumLabelSID");
                if (sidBytes < 8 || sidBytes > sizeof(buffer.sid) || !IsValidSid(buffer.sid) ||
                    GetLengthSid(buffer.sid) != sidBytes || !IsWellKnownSid(buffer.sid, WinMediumLabelSid))
                    throw Failure{"MediumLabelBound", ERROR_INVALID_SID};
                buffer.label.Label.Sid = buffer.sid; buffer.label.Label.Attributes = SE_GROUP_INTEGRITY;
                loweringAttempted = true;
                require(SetTokenInformation(restricted.handle, TokenIntegrityLevel, &buffer.label,
                    static_cast<DWORD>(sizeof(buffer.label)) + sidBytes) != FALSE, "LowerOwnRestrictedIntegrity");
            }
            const auto final = facts(restricted.handle); restrictedAfter = factsJson(final);
            integrityLowered = loweringAttempted && final.rid == SECURITY_MANDATORY_MEDIUM_RID;
            const bool restrictedAdmin = enabledAdmins(restricted.handle);
            parentAfter = factsJson(facts(parent.handle));
            if (parentAfter != baseline) throw Failure{"ParentTokenChanged", ERROR_INVALID_DATA};
            if (final.rid != SECURITY_MANDATORY_MEDIUM_RID || final.elevated || restrictedAdmin ||
                final.sid != before.sid || final.auth != before.auth || final.session != before.session)
                throw Failure{"RestrictedMediumAdmission", ERROR_INVALID_DATA};
        }
        job.value = CreateJobObjectW(nullptr, nullptr);
        require(job.value != nullptr, "OwnJob");
        JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits{};
        limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE | JOB_OBJECT_LIMIT_ACTIVE_PROCESS;
        limits.BasicLimitInformation.ActiveProcessLimit = 1;
        require(SetInformationJobObject(job.value, JobObjectExtendedLimitInformation, &limits, sizeof(limits)) != FALSE, "OwnJobLimits");
        file(out, root + L"\\TEST-child-stdout.txt"); file(err, root + L"\\TEST-child-stderr.txt");
        file(input, root + L"\\TEST-child-stdin.txt");
        Attributes attributes;
        SIZE_T size = 0;
        InitializeProcThreadAttributeList(nullptr, 2, 0, &size);
        if (!size || size > 65536) throw Failure{"HandleListSize", ERROR_INVALID_DATA};
        attributes.storage.resize(size);
        auto list = reinterpret_cast<LPPROC_THREAD_ATTRIBUTE_LIST>(attributes.storage.data());
        require(InitializeProcThreadAttributeList(list, 2, 0, &size) != FALSE, "HandleListInit");
        attributes.list = list;
        HANDLE inherited[] = {input.value, out.value, err.value};
        require(UpdateProcThreadAttribute(list, 0, PROC_THREAD_ATTRIBUTE_HANDLE_LIST, inherited, sizeof(inherited), nullptr, nullptr) != FALSE,
                "ExplicitHandleList");
        HANDLE jobs[] = {job.value};
        require(UpdateProcThreadAttribute(list, 0, PROC_THREAD_ATTRIBUTE_JOB_LIST, jobs, sizeof(jobs), nullptr, nullptr) != FALSE,
                "AtomicOwnJobList");
        STARTUPINFOEXW startup{};
        startup.StartupInfo.cb = sizeof(startup); startup.StartupInfo.dwFlags = STARTF_USESTDHANDLES;
        startup.StartupInfo.hStdInput = input.value; startup.StartupInfo.hStdOutput = out.value; startup.StartupInfo.hStdError = err.value;
        startup.lpAttributeList = list;
        wchar_t windows[32768]{};
        const UINT windowsLength = GetWindowsDirectoryW(windows, 32768);
        if (!windowsLength || windowsLength >= 32768) throw Failure{"SystemRoot", ERROR_INVALID_DATA};
        std::wstring environment = L"SystemRoot=" + std::wstring(windows) + L'\0' + L"TEMP=" + root + L'\0' + L"TMP=" + root;
        environment.push_back(L'\0'); environment.push_back(L'\0');
        std::wstring command = L"\"" + probe + L"\" --read-only-TEST-token-preflight " + wideNonce;
        require(CreateProcessAsUserW(restricted.handle, probe.c_str(), command.data(), nullptr, nullptr, TRUE,
                  CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT | EXTENDED_STARTUPINFO_PRESENT,
                  environment.data(), root.c_str(), &startup.StartupInfo, &child.pi) != FALSE, "CreateOwnRestrictedChild");
        childPid = child.pi.dwProcessId;
        BOOL inJob = FALSE;
        require(IsProcessInJob(child.pi.hProcess, job.value, &inJob) != FALSE, "VerifyAtomicOwnJob");
        if (!inJob) throw Failure{"OwnJobMissing", ERROR_INVALID_DATA};
        FILETIME created{}, exited{}, kernel{}, user{};
        require(GetProcessTimes(child.pi.hProcess, &created, &exited, &kernel, &user) != FALSE, "HeldProcessBirth");
        const ULONGLONG ticks = (static_cast<ULONGLONG>(created.dwHighDateTime) << 32) | created.dwLowDateTime;
        birth = quoted(std::to_string(ticks));
        Token heldToken;
        require(OpenProcessToken(child.pi.hProcess, TOKEN_QUERY | TOKEN_DUPLICATE, &heldToken.handle) != FALSE, "HeldChildToken");
        const auto after = facts(heldToken.handle); held = factsJson(after);
        admin = enabledAdmins(heldToken.handle);
        adminQueried = true;
        DWORD heldSession = 0;
        require(ProcessIdToSessionId(childPid, &heldSession) != FALSE, "HeldProcessSession");
        if (heldSession != after.session) throw Failure{"HeldSessionMismatch", ERROR_INVALID_DATA};
        identity = before.sid == after.sid && before.auth == after.auth && before.session == after.session;
        if (!identity) throw Failure{"SameIdentitySession", ERROR_INVALID_DATA};
        require(ResumeThread(child.pi.hThread) == 1, "ResumeOwnChildOnce");
        const ULONGLONG deadline = GetTickCount64() + 12000;
        DWORD wait = WAIT_TIMEOUT;
        while (GetTickCount64() < deadline && wait == WAIT_TIMEOUT)
            wait = WaitForSingleObject(child.pi.hProcess, static_cast<DWORD>((deadline - GetTickCount64()) > 100 ? 100 : 1));
        if (wait != WAIT_OBJECT_0) {
            timedOut = true;
            throw Failure{"ChildDeadline", ERROR_TIMEOUT};
        }
        child.collected = true;
    } catch (const Failure& f) { errorQuery = f.query; errorCode = f.error; }
      catch (...) { errorQuery = "exception"; errorCode = ERROR_NOT_ENOUGH_MEMORY; }
    // Finalize every post-create path while the atomically assigned job remains owned/live.
    if (child.pi.hProcess) {
        if (!child.collected) {
            child.collected = WaitForSingleObject(child.pi.hProcess, 0) == WAIT_OBJECT_0;
            if (!child.collected) {
                terminated = TerminateProcess(child.pi.hProcess, 124) != FALSE;
                if (!terminated) cleanupError = GetLastError();
                child.collected = WaitForSingleObject(child.pi.hProcess, 2000) == WAIT_OBJECT_0;
            }
        }
        collected = child.collected;
        if (!collected) {
            CloseHandle(job.value); job.value = nullptr; // Kill-on-close; uncollected remains unknown.
            if (!cleanupError) cleanupError = ERROR_TIMEOUT;
        } else {
            try {
                require(GetExitCodeProcess(child.pi.hProcess, &exitCode) != FALSE, "HeldChildExit");
                exitKnown = true;
                output = readOutput(out.value); stderrText = readOutput(err.value);
                if (output.size() + stderrText.size() > 8192) throw Failure{"CombinedOutputBound", ERROR_MORE_DATA};
                if (exitCode != 0 || !stderrText.empty()) throw Failure{"ChildQueryFailed", ERROR_INVALID_DATA};
                complete = !errorQuery && !timedOut && !terminated && !cleanupError;
            } catch (const Failure& f) {
                if (!errorQuery) { errorQuery = f.query; errorCode = f.error; } else cleanupError = f.error;
            } catch (...) { if (!errorQuery) { errorQuery = "collection_exception"; errorCode = ERROR_NOT_ENOUGH_MEMORY; } }
        }
    }
    const std::string record = "{\"nonce\":" + quoted(nonce) + ",\"pid\":" + std::to_string(GetCurrentProcessId()) +
        ",\"variant\":" + quoted(medium ? "lua-medium" : "lua-only") + ",\"restrictedBefore\":" + restrictedBefore +
        ",\"restrictedAfter\":" + restrictedAfter + ",\"parentAfter\":" + parentAfter +
        ",\"loweringAttempted\":" + (loweringAttempted ? "true" : "false") + ",\"integrityLowered\":" + (integrityLowered ? "true" : "false") +
        ",\"baseline\":" + baseline + ",\"held\":" + held + ",\"childPid\":" + std::to_string(childPid) +
        ",\"childBirth\":" + birth + ",\"enabledAdmins\":" + (!adminQueried ? "null" : (admin ? "true" : "false")) +
        ",\"sameIdentitySession\":" + (identity ? "true" : "false") + ",\"collected\":" + (collected ? "true" : "false") +
        ",\"timedOut\":" + (timedOut ? "true" : "false") + ",\"childExit\":" + (exitKnown ? std::to_string(exitCode) : "null") +
        ",\"childTerminated\":" + (terminated ? "true" : "false") + ",\"cleanupError\":" + (cleanupError ? std::to_string(cleanupError) : "null") +
        ",\"childRecord\":" + quoted(output) + ",\"childStderr\":" + quoted(stderrText) +
        ",\"queriesComplete\":" + (complete ? "true" : "false") + ",\"query\":" + (errorQuery ? quoted(errorQuery) : "null") +
        ",\"error\":" + (errorQuery ? std::to_string(errorCode) : "null") + ",\"sdkNativeCallback\":false,\"notificationEffects\":0}";
    if (record.size() > 16384) return 65;
    std::puts(record.c_str());
    return complete ? 0 : 1;
}
