// Narrow real-Pi ConPTY fixture. Fixed arguments, inherited TEST environment,
// owned job lifetime; no shell, registration or desktop qualification.
#define _WIN32_WINNT 0x0A00
#define NOMINMAX
#include <windows.h>
#include <cstdio>
#include <io.h>
#include <iostream>
#include <sstream>
#include <thread>
#include <string>
#include <vector>

// An independent child observes only its actual OS/CRT standard handles. It
// neither changes console state nor stands in for Pi's own native TUI evidence.
static int observeStandardHandles(const wchar_t* report) {
    std::ostringstream json;
    json << "{\"scope\":\"conpty_child_standard_handles\",\"pi_executed\":false,\"desktop_qualified\":false,\"handles\":{";
    const DWORD identifiers[]{STD_INPUT_HANDLE, STD_OUTPUT_HANDLE, STD_ERROR_HANDLE};
    const char* names[]{"stdin", "stdout", "stderr"};
    FILE* streams[]{stdin, stdout, stderr};
    for (int i = 0; i < 3; i++) {
        const HANDLE handle = GetStdHandle(identifiers[i]);
        SetLastError(ERROR_SUCCESS);
        const DWORD kind = GetFileType(handle);
        const DWORD kindError = kind == FILE_TYPE_UNKNOWN ? GetLastError() : ERROR_SUCCESS;
        DWORD mode = 0;
        const BOOL console = GetConsoleMode(handle, &mode);
        const DWORD consoleError = console ? ERROR_SUCCESS : GetLastError();
        const int descriptor = _fileno(streams[i]);
        const bool tty = descriptor >= 0 && _isatty(descriptor) != 0;
        if (i) json << ',';
        json << '"' << names[i] << "\":{\"file_type\":" << kind << ",\"file_type_error\":" << kindError
             << ",\"console_mode_available\":" << (console ? "true" : "false") << ",\"console_mode\":" << mode
             << ",\"console_error\":" << consoleError << ",\"crt_descriptor\":" << descriptor
             << ",\"crt_isatty\":" << (tty ? "true" : "false") << '}';
    }
    json << "}}\n";
    const HANDLE file = CreateFileW(report, GENERIC_WRITE, 0, nullptr, CREATE_NEW, FILE_ATTRIBUTE_NORMAL, nullptr);
    if (file == INVALID_HANDLE_VALUE) return 2;
    const std::string body = json.str();
    DWORD written = 0;
    const bool saved = WriteFile(file, body.data(), static_cast<DWORD>(body.size()), &written, nullptr) && written == body.size();
    CloseHandle(file);
    return saved ? 0 : 2;
}

// Both the host-only observer and the unmodified Pi invocation use this exact
// console, handle-inheritance, environment and owned-job creation envelope.
static int runAttached(std::wstring executable, std::wstring command, const wchar_t* cwd, DWORD budget) {
    HANDLE inputRead = nullptr, inputWrite = nullptr, outputRead = nullptr, outputWrite = nullptr;
    if (!CreatePipe(&inputRead, &inputWrite, nullptr, 0) || !CreatePipe(&outputRead, &outputWrite, nullptr, 0)) return 2;
    HPCON console = nullptr;
    std::cerr << "TEST-CONPTY create_pseudoconsole\n" << std::flush;
    if (FAILED(CreatePseudoConsole(COORD{120, 40}, inputRead, outputWrite, 0, &console))) return 2;
    std::cerr << "TEST-CONPTY pseudoconsole_ready\n" << std::flush;
    CloseHandle(inputRead);
    CloseHandle(outputWrite);
    SIZE_T size = 0;
    InitializeProcThreadAttributeList(nullptr, 1, 0, &size);
    std::vector<unsigned char> storage(size);
    STARTUPINFOEXW startup{};
    startup.StartupInfo.cb = sizeof startup;
    startup.lpAttributeList = reinterpret_cast<LPPROC_THREAD_ATTRIBUTE_LIST>(storage.data());
    if (!InitializeProcThreadAttributeList(startup.lpAttributeList, 1, 0, &size)
        || !UpdateProcThreadAttribute(startup.lpAttributeList, 0, PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, console, sizeof console, nullptr, nullptr)) return 2;
    PROCESS_INFORMATION child{};
    HANDLE job = CreateJobObjectW(nullptr, nullptr);
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits{};
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if (!job || !SetInformationJobObject(job, JobObjectExtendedLimitInformation, &limits, sizeof limits)) return 2;
    if (!CreateProcessW(executable.c_str(), command.data(), nullptr, nullptr, FALSE,
                        EXTENDED_STARTUPINFO_PRESENT | CREATE_UNICODE_ENVIRONMENT | CREATE_SUSPENDED,
                        nullptr, cwd, &startup.StartupInfo, &child)) return 2;
    if (!AssignProcessToJobObject(job, child.hProcess)) {
        TerminateProcess(child.hProcess, 2);
        WaitForSingleObject(child.hProcess, 2000);
        return 2;
    }
    const DWORD resumed = ResumeThread(child.hThread);
    std::cerr << "TEST-CONPTY resume_thread_result=" << resumed << " pid=" << child.dwProcessId << "\n" << std::flush;
    CloseHandle(child.hThread);
    std::thread output([&]() {
        char bytes[4096];
        DWORD count = 0, written = 0;
        while (ReadFile(outputRead, bytes, sizeof bytes, &count, nullptr) && count) {
            if (!WriteFile(GetStdHandle(STD_OUTPUT_HANDLE), bytes, count, &written, nullptr) || written != count) break;
        }
    });
    std::thread input([&]() {
        char bytes[256];
        DWORD count = 0, written = 0;
        while (ReadFile(GetStdHandle(STD_INPUT_HANDLE), bytes, sizeof bytes, &count, nullptr) && count) {
            if (!WriteFile(inputWrite, bytes, count, &written, nullptr) || written != count) break;
        }
    });
    const DWORD wait = WaitForSingleObject(child.hProcess, budget);
    if (wait != WAIT_OBJECT_0) {
        TerminateJobObject(job, 3);
        WaitForSingleObject(child.hProcess, 2000);
    }
    DWORD code = 3;
    GetExitCodeProcess(child.hProcess, &code);
    ClosePseudoConsole(console);
    CancelSynchronousIo(input.native_handle());
    input.join();
    output.join();
    CloseHandle(inputWrite);
    CloseHandle(outputRead);
    CloseHandle(child.hProcess);
    CloseHandle(job);
    DeleteProcThreadAttributeList(startup.lpAttributeList);
    return static_cast<int>(code);
}

int wmain(int argc, wchar_t** argv) {
    if (argc == 3 && std::wstring(argv[1]) == L"--stdio-probe-child") return observeStandardHandles(argv[2]);
    if (argc == 4 && std::wstring(argv[1]) == L"--probe-stdio") {
        wchar_t ownPath[32768]{};
        const DWORD length = GetModuleFileNameW(nullptr, ownPath, 32768);
        if (!length || length >= 32768) return 2;
        const std::wstring executable = ownPath, report = argv[3];
        if (executable.find(L'"') != std::wstring::npos || report.find(L'"') != std::wstring::npos) return 2;
        return runAttached(executable, L"\"" + executable + L"\" --stdio-probe-child \"" + report + L"\"", argv[2], 5000);
    }
    if (argc != 3) return 2;
    // Windows paths cannot contain quotes; the only variable command token is
    // the quoted physical Pi executable. Every Pi CLI argument stays fixed.
    const std::wstring executable = argv[1];
    if (executable.find(L'"') != std::wstring::npos) return 2;
    return runAttached(executable,
        L"\"" + executable + L"\" --offline --no-approve --no-session --no-mcp --no-tools --provider TEST-local --model TEST-local --tui-mode regular \"TEST say done\"",
        argv[2], 30000);
}
