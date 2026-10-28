// Narrow real-Pi ConPTY fixture. Fixed arguments, inherited TEST environment,
// owned job lifetime; no shell, registration or desktop qualification.
#define _WIN32_WINNT 0x0A00
#define NOMINMAX
#include <windows.h>
#include <iostream>
#include <thread>
#include <string>
#include <vector>

int wmain(int argc, wchar_t** argv) {
    if (argc != 3) return 2;
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
    // Windows paths cannot contain quotes; the only variable command token is
    // the quoted physical Pi executable. Every CLI argument is fixed here.
    const std::wstring executable = argv[1];
    if (executable.find(L'"') != std::wstring::npos) return 2;
    std::wstring command = L"\"" + executable + L"\" --offline --no-approve --no-session --no-mcp --no-tools --provider TEST-local --model TEST-local --tui-mode regular \"TEST say done\"";
    PROCESS_INFORMATION child{};
    HANDLE job = CreateJobObjectW(nullptr, nullptr);
    JOBOBJECT_EXTENDED_LIMIT_INFORMATION limits{};
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if (!job || !SetInformationJobObject(job, JobObjectExtendedLimitInformation, &limits, sizeof limits)) return 2;
    if (!CreateProcessW(executable.c_str(), command.data(), nullptr, nullptr, FALSE,
                        EXTENDED_STARTUPINFO_PRESENT | CREATE_UNICODE_ENVIRONMENT | CREATE_SUSPENDED,
                        nullptr, argv[2], &startup.StartupInfo, &child)) return 2;
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
    const DWORD wait = WaitForSingleObject(child.hProcess, 30000);
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
