// Own TEST child only. No SDK, UI, impersonation or explicit privilege changes.
#include "navigation_windows_token_queries.h"
#include <cstdio>
#include <cstddef>
#include <memory>
#include <aclapi.h>
#include <algorithm>

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
struct LocalMemory { void* value = nullptr; ~LocalMemory() { if (value) LocalFree(value); } };
static void privateDescriptor(PSID user, PSID system, bool directory, SECURITY_DESCRIPTOR& sd, LocalMemory& aclMemory) {
    EXPLICIT_ACCESSW entries[2]{};
    for (unsigned i = 0; i < 2; ++i) {
        entries[i].grfAccessPermissions = FILE_ALL_ACCESS; entries[i].grfAccessMode = SET_ACCESS;
        entries[i].grfInheritance = directory ? SUB_CONTAINERS_AND_OBJECTS_INHERIT : NO_INHERITANCE;
        entries[i].Trustee.TrusteeForm = TRUSTEE_IS_SID;
        entries[i].Trustee.ptstrName = reinterpret_cast<LPWSTR>(i ? system : user);
    }
    PACL acl = nullptr;
    DWORD error = SetEntriesInAclW(EqualSid(user, system) ? 1 : 2, entries, nullptr, &acl); aclMemory.value = acl;
    if (error) throw Failure{"PrivateACLBuild", error};
    require(InitializeSecurityDescriptor(&sd, SECURITY_DESCRIPTOR_REVISION) != FALSE, "PrivateSDInit");
    require(SetSecurityDescriptorOwner(&sd, user, FALSE) != FALSE, "ExplicitOwnOwner");
    require(SetSecurityDescriptorDacl(&sd, TRUE, acl, FALSE) != FALSE, "ExplicitOwnDACL");
    require(SetSecurityDescriptorControl(&sd, SE_DACL_PROTECTED, SE_DACL_PROTECTED) != FALSE, "ExplicitProtectedDACL");
}
static void privateAcl(HANDLE object, PSID user, PSID system, bool directory) {
    LocalMemory aclMemory, descriptor; SECURITY_DESCRIPTOR desired{};
    privateDescriptor(user, system, directory, desired, aclMemory);
    DWORD error = SetSecurityInfo(object, SE_FILE_OBJECT, DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                            nullptr, nullptr, static_cast<PACL>(aclMemory.value), nullptr);
    if (error) throw Failure{"SealOwnObject", error};
    PSECURITY_DESCRIPTOR sd = nullptr; PACL actual = nullptr;
    error = GetSecurityInfo(object, SE_FILE_OBJECT, DACL_SECURITY_INFORMATION, nullptr, nullptr, &actual, nullptr, &sd);
    descriptor.value = sd;
    if (error) throw Failure{"SealedACLReadback", error};
    SECURITY_DESCRIPTOR_CONTROL control{}; DWORD revision = 0;
    require(GetSecurityDescriptorControl(sd, &control, &revision) != FALSE, "SealedACLControl");
    const DWORD expectedEntries = EqualSid(user, system) ? 1 : 2;
    if (!(control & SE_DACL_PROTECTED) || !actual || actual->AceCount != expectedEntries) throw Failure{"SealedACLShape", ERROR_INVALID_ACL};
    bool sawUser = false, sawSystem = false;
    for (DWORD i = 0; i < expectedEntries; ++i) {
        ACCESS_ALLOWED_ACE* ace = nullptr;
        require(GetAce(actual, i, reinterpret_cast<LPVOID*>(&ace)) != FALSE, "SealedACE");
        if (!ace || ace->Header.AceType != ACCESS_ALLOWED_ACE_TYPE || ace->Mask != FILE_ALL_ACCESS ||
            ace->Header.AceFlags != (directory ? OBJECT_INHERIT_ACE | CONTAINER_INHERIT_ACE : 0))
            throw Failure{"SealedACEPolicy", ERROR_INVALID_ACL};
        PSID sid = &ace->SidStart;
        const bool isUser = EqualSid(sid, user) != FALSE, isSystem = EqualSid(sid, system) != FALSE;
        if (!isUser && !isSystem) throw Failure{"ForeignSealedACE", ERROR_INVALID_ACL};
        sawUser = sawUser || isUser; sawSystem = sawSystem || isSystem;
    }
    if (!sawUser || !sawSystem) throw Failure{"SealedPrincipalAbsent", ERROR_INVALID_ACL};
}
static void boundOwned(Handle& handle, const std::wstring& path, PSID user, bool directory) {
    handle.value = CreateFileW(path.c_str(), READ_CONTROL | WRITE_DAC | FILE_READ_ATTRIBUTES, FILE_SHARE_READ,
        nullptr, OPEN_EXISTING, FILE_FLAG_OPEN_REPARSE_POINT | FILE_FLAG_BACKUP_SEMANTICS, nullptr);
    require(handle.value != INVALID_HANDLE_VALUE, "BindOwnObject");
    BY_HANDLE_FILE_INFORMATION info{}; require(GetFileInformationByHandle(handle.value, &info) != FALSE, "OwnObjectIdentity");
    if ((info.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT) ||
        !!(info.dwFileAttributes & FILE_ATTRIBUTE_DIRECTORY) != directory || (!directory && info.nNumberOfLinks != 1))
        throw Failure{"OwnObjectRedirected", ERROR_INVALID_DATA};
    wchar_t finalPath[32768]{}; const DWORD length = GetFinalPathNameByHandleW(handle.value, finalPath, 32768, FILE_NAME_NORMALIZED);
    if (!length || length >= 32768 || _wcsicmp(finalPath, (L"\\\\?\\" + path).c_str()))
        throw Failure{"OwnObjectPhysicalPath", ERROR_INVALID_DATA};
    LocalMemory descriptor; PSECURITY_DESCRIPTOR sd = nullptr; PSID owner = nullptr;
    const DWORD error = GetSecurityInfo(handle.value, SE_FILE_OBJECT, OWNER_SECURITY_INFORMATION, &owner, nullptr, nullptr, nullptr, &sd);
    descriptor.value = sd;
    if (error) throw Failure{"OwnObjectOwner", error};
    if (!owner || !EqualSid(owner, user)) throw Failure{"ForeignObjectOwner", ERROR_ACCESS_DENIED};
}
static std::vector<std::wstring> closedLeaves(const std::wstring& nonce, bool cold = false) {
    if (cold) return {L"TEST-launcher-" + nonce + L".exe", L"TEST-sdk-" + nonce + L".exe",
        L"Microsoft.WindowsAppRuntime.Bootstrap.dll", L"TEST-module-pins.txt", L"TEST-cold-binding.json"};
    return {L"TEST-launcher-" + nonce + L".exe", L"TEST-sdk-" + nonce + L".exe",
        L"Microsoft.WindowsAppRuntime.Bootstrap.dll", L"TEST-module-pins.txt", L"Microsoft.WindowsAppRuntime.2.msix",
        L"Microsoft.WindowsAppRuntime.DDLM.2.msix", L"Microsoft.WindowsAppRuntime.Main.2.msix", L"Microsoft.WindowsAppRuntime.Singleton.2.msix",
        L"Microsoft.WindowsAppRuntime.2.msix.manifest.xml", L"Microsoft.WindowsAppRuntime.DDLM.2.msix.manifest.xml",
        L"Microsoft.WindowsAppRuntime.Main.2.msix.manifest.xml", L"Microsoft.WindowsAppRuntime.Singleton.2.msix.manifest.xml"};
}
static void freshRootPath(const std::wstring& root, const std::wstring& nonce) {
        wchar_t temp[32768]{}, canonical[32768]{};
        const DWORD n = GetEnvironmentVariableW(L"RUNNER_TEMP", temp, 32768);
        if (!n || n >= 32768) throw Failure{"PrivateRunnerTemp", ERROR_INVALID_PARAMETER};
        const DWORD length = GetFullPathNameW(temp, 32768, canonical, nullptr);
        if (!length || length >= 32768) throw Failure{"PrivateTempCanonical", ERROR_INVALID_PARAMETER};
        std::wstring parent(canonical); while (parent.size() > 3 && parent.back() == L'\\') parent.pop_back();
        if (root != parent + L"\\TEST-lua-preflight-" + nonce) throw Failure{"PrivateFreshRootNonce", ERROR_INVALID_PARAMETER};
}
static int createPrivateRoot(int argc, wchar_t** argv) {
    if (argc != 5 || !validNonce(argv[2])) return 64;
    const bool deployment = !wcscmp(argv[4], L"deploy"), cold = !wcscmp(argv[4], L"coldclick");
    if (!deployment && !cold && wcscmp(argv[4], L"bootstrap")) return 64;
    const std::wstring root(argv[3]), wideNonce(argv[2]); const std::string nonce(wideNonce.begin(), wideNonce.end());
    try {
        freshRootPath(root, wideNonce);
        Token token; require(OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token.handle) != FALSE, "PrivateOwnToken");
        auto userData = queryToken(token.handle, TokenUser, "PrivateTokenUser", sizeof(TOKEN_USER));
        const PSID user = reinterpret_cast<TOKEN_USER*>(userData.data())->User.Sid; boundedSidSize(userData, user, "PrivateUserSID");
        BYTE system[SECURITY_MAX_SID_SIZE]{}; DWORD bytes = sizeof(system);
        require(CreateWellKnownSid(WinLocalSystemSid, nullptr, system, &bytes) != FALSE, "PrivateSystemSID");
        LocalMemory rootAcl, fileAcl; SECURITY_DESCRIPTOR rootSD{}, fileSD{};
        privateDescriptor(user, system, true, rootSD, rootAcl); privateDescriptor(user, system, false, fileSD, fileAcl);
        SECURITY_ATTRIBUTES rootSA{sizeof(rootSA), &rootSD, FALSE}, fileSA{sizeof(fileSA), &fileSD, FALSE};
        require(CreateDirectoryW(root.c_str(), &rootSA) != FALSE, "CreateFreshPrivateRoot");
        Handle directory; boundOwned(directory, root, user, true); privateAcl(directory.value, user, system, true);
        const auto leaves = closedLeaves(wideNonce, cold); const unsigned count = cold ? 5 : (deployment ? 12 : 4);
        for (unsigned i = 0; i < count; ++i) {
            const auto path = root + L"\\" + leaves[i]; Handle created;
            created.value = CreateFileW(path.c_str(), GENERIC_READ | GENERIC_WRITE | READ_CONTROL | WRITE_DAC,
                0, &fileSA, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr);
            require(created.value != INVALID_HANDLE_VALUE, "CreatePrivateClosedLeaf");
            privateAcl(created.value, user, system, false);
        }
        std::printf("{\"nonce\":\"%s\",\"pid\":%lu,\"created\":true,\"protectedDACL\":true,\"files\":%u}\n", nonce.c_str(), GetCurrentProcessId(), count);
        return 0;
    } catch (const Failure& f) {
        std::printf("{\"nonce\":\"%s\",\"pid\":%lu,\"created\":false,\"query\":\"%s\",\"error\":%lu}\n",
            nonce.c_str(), GetCurrentProcessId(), f.query, f.error); return 1;
    } catch (...) { return 1; }
}
static int sealRoot(int argc, wchar_t** argv) {
    if ((argc != 4 && argc != 5) || !validNonce(argv[2]) || (argc == 5 && wcscmp(argv[4], L"coldclick"))) return 64;
    const bool cold = argc == 5;
    const std::wstring root(argv[3]), nonceWide(argv[2]); const std::string nonce(nonceWide.begin(), nonceWide.end());
    try {
        freshRootPath(root, nonceWide);
        Token token; require(OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &token.handle) != FALSE, "SealOwnToken");
        auto userData = queryToken(token.handle, TokenUser, "SealTokenUser", sizeof(TOKEN_USER));
        const PSID user = reinterpret_cast<TOKEN_USER*>(userData.data())->User.Sid; boundedSidSize(userData, user, "SealUserSID");
        BYTE system[SECURITY_MAX_SID_SIZE]{}; DWORD bytes = sizeof(system);
        require(CreateWellKnownSid(WinLocalSystemSid, nullptr, system, &bytes) != FALSE, "SealSystemSID");
        Handle heldRoot; boundOwned(heldRoot, root, user, true); privateAcl(heldRoot.value, user, system, true);
        const auto allowed = closedLeaves(nonceWide, cold);
        WIN32_FIND_DATAW entry{}; HANDLE enumeration = FindFirstFileW((root + L"\\*").c_str(), &entry);
        require(enumeration != INVALID_HANDLE_VALUE, "SealClosedFiles"); unsigned count = 0;
        try {
            do {
                const std::wstring leaf(entry.cFileName); if (leaf == L"." || leaf == L"..") continue;
                if (std::find(allowed.begin(), allowed.end(), leaf) == allowed.end()) throw Failure{"SealForeignLeaf", ERROR_INVALID_DATA};
                Handle file; boundOwned(file, root + L"\\" + leaf, user, false); privateAcl(file.value, user, system, false); ++count;
            } while (FindNextFileW(enumeration, &entry));
            if (GetLastError() != ERROR_NO_MORE_FILES || (cold ? count != 5 : (count != 4 && count != 12))) throw Failure{"SealClosedSetCount", ERROR_INVALID_DATA};
        } catch (...) { FindClose(enumeration); throw; }
        FindClose(enumeration);
        std::printf("{\"nonce\":\"%s\",\"pid\":%lu,\"sealed\":true,\"protectedDACL\":true,\"files\":%u}\n", nonce.c_str(), GetCurrentProcessId(), count);
        return 0;
    } catch (const Failure& f) {
        std::printf("{\"nonce\":\"%s\",\"pid\":%lu,\"sealed\":false,\"query\":\"%s\",\"error\":%lu}\n",
            nonce.c_str(), GetCurrentProcessId(), f.query, f.error); return 1;
    } catch (...) { return 1; }
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
int wmain(int argc, wchar_t** argv) {
    if (argc >= 2 && !wcscmp(argv[1], L"--TEST-create-private-sdk-root")) return createPrivateRoot(argc, argv);
    if (argc >= 2 && !wcscmp(argv[1], L"--TEST-seal-sdk-root")) return sealRoot(argc, argv);
    if (argc != 5 || !validNonce(argv[2])) return 64;
    const bool deploy = wcscmp(argv[1], L"--TEST-sdk-deployment") == 0;
    const bool history = wcscmp(argv[1], L"--TEST-sdk-cold-history") == 0;
    const bool senderHistory = wcscmp(argv[1], L"--TEST-sdk-cold-sender-history") == 0;
    const bool cold = history || senderHistory || wcscmp(argv[1], L"--TEST-sdk-cold-sender") == 0;
    const bool sdk = deploy || cold || wcscmp(argv[1], L"--TEST-sdk-bootstrap") == 0;
    const bool medium = !deploy && (sdk || wcscmp(argv[1], L"--TEST-lua-medium-token-preflight") == 0);
    if (!sdk && !medium && wcscmp(argv[1], L"--TEST-lua-token-preflight")) return 64;
    const std::wstring wideNonce(argv[2]), root(argv[3]), probe(argv[4]);
    const std::string nonce(wideNonce.begin(), wideNonce.end());
    std::string baseline = "null", held = "null", output, stderrText, birth = "null";
    std::string restrictedBefore = "null", restrictedAfter = "null", parentAfter = "null";
    bool loweringAttempted = false, integrityLowered = false;
    DWORD childPid = 0, exitCode = 0; ULONGLONG collectionBootMs = 0;
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
            probe != root + (sdk ? L"\\TEST-sdk-" : L"\\TEST-token-") + wideNonce + L".exe")
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
        if (!deploy) require(CreateRestrictedToken(parent.handle, LUA_TOKEN, 0, nullptr, 0, nullptr, 0, nullptr,
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
        file(out, root + (history ? L"\\TEST-history-child-stdout.txt" : L"\\TEST-child-stdout.txt"));
        file(err, root + (history ? L"\\TEST-history-child-stderr.txt" : L"\\TEST-child-stderr.txt"));
        file(input, root + (history ? L"\\TEST-history-child-stdin.txt" : L"\\TEST-child-stdin.txt"));
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
        std::wstring command = L"\"" + probe + L"\" " + (sdk ? (deploy ? L"--TEST-sdk-deploy " : (history ? L"--TEST-sdk-cold-history " : senderHistory ? L"--TEST-sdk-cold-sender-history " : cold ? L"--TEST-sdk-cold-sender " : L"--TEST-sdk-bootstrap ")) : L"--read-only-TEST-token-preflight ") + wideNonce;
        if (sdk) command += L" \"" + root + L"\" " + std::wstring(before.sid.begin(), before.sid.end()) + L" " +
            std::wstring(before.auth.begin(), before.auth.end()) + L" " + std::to_wstring(before.session);
        const ULONGLONG sdkDeadline = GetTickCount64() + (deploy ? 90000 : 30000);
        require(CreateProcessAsUserW(deploy ? parent.handle : restricted.handle, probe.c_str(), command.data(), nullptr, nullptr, TRUE,
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
        if (deploy && factsJson(after) != baseline) throw Failure{"DeploymentPrimaryChanged", ERROR_INVALID_DATA};
        if (sdk && !deploy && (after.rid != SECURITY_MANDATORY_MEDIUM_RID || after.elevated || admin))
            throw Failure{"SDKMediumHeldAdmission", ERROR_INVALID_DATA};
        if (!identity) throw Failure{"SameIdentitySession", ERROR_INVALID_DATA};
        require(ResumeThread(child.pi.hThread) == 1, "ResumeOwnChildOnce");
        const ULONGLONG deadline = sdk ? sdkDeadline : GetTickCount64() + 12000;
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
    if (cold && collected) collectionBootMs = GetTickCount64();
    const std::string record = "{\"nonce\":" + quoted(nonce) + ",\"pid\":" + std::to_string(GetCurrentProcessId()) +
        ",\"variant\":" + quoted(sdk ? (deploy ? "sdk-deploy" : (history ? "sdk-cold-history" : senderHistory ? "sdk-cold-sender-history" : cold ? "sdk-cold-sender" : "sdk-bootstrap")) : (medium ? "lua-medium" : "lua-only")) + ",\"restrictedBefore\":" + restrictedBefore +
        ",\"restrictedAfter\":" + restrictedAfter + ",\"parentAfter\":" + parentAfter +
        ",\"loweringAttempted\":" + (loweringAttempted ? "true" : "false") + ",\"integrityLowered\":" + (integrityLowered ? "true" : "false") +
        ",\"baseline\":" + baseline + ",\"held\":" + held + ",\"childPid\":" + std::to_string(childPid) +
        ",\"childBirth\":" + birth + ",\"enabledAdmins\":" + (!adminQueried ? "null" : (admin ? "true" : "false")) +
        ",\"sameIdentitySession\":" + (identity ? "true" : "false") + ",\"collected\":" + (collected ? "true" : "false") +
        ",\"timedOut\":" + (timedOut ? "true" : "false") + ",\"childExit\":" + (exitKnown ? std::to_string(exitCode) : "null") +
        ",\"childTerminated\":" + (terminated ? "true" : "false") + ",\"cleanupError\":" + (cleanupError ? std::to_string(cleanupError) : "null") +
        ",\"childRecord\":" + quoted(output) + ",\"childStderr\":" + quoted(stderrText) +
        ",\"queriesComplete\":" + (complete ? "true" : "false") + ",\"query\":" + (errorQuery ? quoted(errorQuery) : "null") +
        ",\"error\":" + (errorQuery ? std::to_string(errorCode) : "null") + ",\"sdkNativeCallback\":false,\"notificationEffects\":" + (cold ? "null" : "0") +
        (cold ? ",\"collectionBootMs\":" + (collected ? std::to_string(collectionBootMs) : "null") : "") + "}";
    if (record.size() > 16384) return 65;
    std::puts(record.c_str());
    return complete ? 0 : 1;
}
