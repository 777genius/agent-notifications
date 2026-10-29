// Finite guest preparation and explicit endpoint discovery for CI TEST fixtures.
#define NOMINMAX
#include <windows.h>
#include <mmdeviceapi.h>
#include <endpointvolume.h>
#include <audioclient.h>
#include "pi_windows_audio_fixture.h"
#include <functiondiscoverykeys_devpkey.h>
#include <wrl/client.h>
#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

using Microsoft::WRL::ComPtr;

static std::vector<BYTE> user(HANDLE process) {
    HANDLE token{}; DWORD size{};
    if (!OpenProcessToken(process, TOKEN_QUERY, &token)) throw "token unavailable";
    GetTokenInformation(token, TokenUser, nullptr, 0, &size);
    std::vector<BYTE> buffer(size);
    const BOOL read = size && GetTokenInformation(token, TokenUser, buffer.data(), size, &size);
    CloseHandle(token);
    if (!read) throw "token user unavailable";
    return buffer;
}

static void shell() {
    const HWND window = GetForegroundWindow(); DWORD pid{}, ownSession{}, session{};
    GetWindowThreadProcessId(window, &pid);
    HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION | SYNCHRONIZE, FALSE, pid);
    if (!process) throw "foreground process unavailable";
    wchar_t actual[32768]{}, windows[32768]{}; DWORD count = 32768;
    const BOOL imageRead = QueryFullProcessImageNameW(process, 0, actual, &count);
    const UINT windowsSize = GetWindowsDirectoryW(windows, 32768);
    const std::wstring expected = std::wstring(windows) + L"\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\SearchHost.exe";
    const bool search = imageRead && windowsSize > 0 && windowsSize < 32768 && _wcsicmp(actual, expected.c_str()) == 0;
    if (!search) {
        CloseHandle(process);
        std::cout << "{\"status\":\"not_applicable\",\"input_attempted\":false,\"product_executed\":false}\n";
        return;
    }
    const auto own = user(GetCurrentProcess()), other = user(process);
    const bool bound = ProcessIdToSessionId(GetCurrentProcessId(), &ownSession) && ProcessIdToSessionId(pid, &session)
        && session == ownSession && session != 0
        && EqualSid(reinterpret_cast<const TOKEN_USER*>(own.data())->User.Sid, reinterpret_cast<const TOKEN_USER*>(other.data())->User.Sid)
        && WaitForSingleObject(process, 0) == WAIT_TIMEOUT && GetForegroundWindow() == window;
    if (!bound) { CloseHandle(process); throw "foreground Shell identity changed"; }
    INPUT inputs[2]{};
    for (auto& input : inputs) { input.type = INPUT_KEYBOARD; input.ki.wVk = VK_ESCAPE; }
    inputs[1].ki.dwFlags = KEYEVENTF_KEYUP;
    const UINT sent = SendInput(2, inputs, sizeof(INPUT));
    const auto deadline = GetTickCount64() + 2000;
    while (GetTickCount64() < deadline && GetForegroundWindow() == window && IsWindowVisible(window)) Sleep(20);
    const bool dismissed = sent == 2 && (GetForegroundWindow() != window || !IsWindowVisible(window));
    CloseHandle(process);
    std::cout << "{\"status\":\"" << (dismissed ? "dismissed" : "unqualified")
              << "\",\"verified_search_overlay\":true,\"input_attempted\":true,\"sent\":" << sent
              << ",\"session\":" << session << ",\"pid\":" << pid
              << ",\"hwnd\":" << reinterpret_cast<uintptr_t>(window)
              << ",\"foreground_after\":" << reinterpret_cast<uintptr_t>(GetForegroundWindow())
              << ",\"product_executed\":false}\n";
    if (!dismissed) throw "single bound Search overlay Escape was not observed";
}

int wmain(int argc, wchar_t** argv) {
    if (argc < 2 || FAILED(CoInitializeEx(nullptr, COINIT_MULTITHREADED))) return 2;
    try {
        const std::wstring mode(argv[1]);
        if (mode == L"shell" && argc == 2) {
            auto env = [](const wchar_t* key) { wchar_t value[256]{}; const DWORD size = GetEnvironmentVariableW(key, value, 256); return size && size < 256 ? std::wstring(value, size) : L""; };
            if (env(L"GITHUB_REPOSITORY") != L"777genius/agent-notifications" || env(L"GITHUB_EVENT_NAME") != L"workflow_dispatch" ||
                env(L"GITHUB_RUN_ATTEMPT") != L"1" || env(L"PI_WINDOWS_HOST_SETUP") != L"1") throw "fresh manual hosted setup required";
            shell();
        } else if (mode == L"discover" && argc == 3) {
            std::cout << PiWindowsAudioFixture::discover(argv[2]) << '\n';
        } else if (mode == L"verify" && argc == 8) {
            ComPtr<IMMDeviceEnumerator> enumerator;
            if (FAILED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator)))) throw "enumerator unavailable";
            std::cout << PiWindowsAudioFixture::select(enumerator.Get(), argc - 2, argv + 2).json << '\n';
        } else return 2;
    } catch (const char* error) { std::cerr << error << '\n'; return 2; }
      catch (const std::exception& error) { std::cerr << error.what() << '\n'; return 2; }
    return 0;
}
