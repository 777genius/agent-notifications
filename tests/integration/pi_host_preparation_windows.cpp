// Manual, disposable hosted-guest diagnostics. Never a product receipt.
#define NOMINMAX
#include <windows.h>
#include <mmdeviceapi.h>
#include <endpointvolume.h>
#include <audiopolicy.h>
#include <functiondiscoverykeys_devpkey.h>
#include <wrl/client.h>
#include <cstdint>
#include <iostream>
#include <string>
#include <vector>

using Microsoft::WRL::ComPtr;

static std::string quoted(const std::wstring& value) {
    int size = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    std::string encoded(size, '\0'), result = "\"";
    WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), encoded.data(), size, nullptr, nullptr);
    for (unsigned char c : encoded) {
        if (c == '\\' || c == '"') result += '\\';
        if (c < 32) result += '?'; else result += c;
    }
    return result + '"';
}

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

static void audio() {
    ComPtr<IMMDeviceEnumerator> enumerator;
    HRESULT hr = CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator));
    if (FAILED(hr)) throw "endpoint enumerator unavailable";
    std::cout << "{\"read_only\":true,\"product_executed\":false,\"roles\":[";
    for (int role = 0; role < 3; ++role) {
        if (role) std::cout << ',';
        ComPtr<IMMDevice> device;
        hr = enumerator->GetDefaultAudioEndpoint(eRender, static_cast<ERole>(role), &device);
        std::cout << "{\"role\":" << role << ",\"hresult\":" << hr;
        if (SUCCEEDED(hr)) {
            LPWSTR id{}; device->GetId(&id);
            std::cout << ",\"id\":" << quoted(id ? id : L"");
            if (id) CoTaskMemFree(id);
            ComPtr<IPropertyStore> store; PROPVARIANT name{}; PropVariantInit(&name);
            if (SUCCEEDED(device->OpenPropertyStore(STGM_READ, &store)) && SUCCEEDED(store->GetValue(PKEY_Device_FriendlyName, &name)) && name.vt == VT_LPWSTR)
                std::cout << ",\"name\":" << quoted(name.pwszVal);
            PropVariantClear(&name);
            ComPtr<IAudioEndpointVolume> volume; BOOL muted{}; float level{};
            hr = device->Activate(__uuidof(IAudioEndpointVolume), CLSCTX_ALL, nullptr, &volume);
            if (SUCCEEDED(hr)) hr = volume->GetMute(&muted);
            if (SUCCEEDED(hr)) hr = volume->GetMasterVolumeLevelScalar(&level);
            std::cout << ",\"volume_hresult\":" << hr << ",\"muted\":" << (muted ? "true" : "false") << ",\"volume\":" << level;
            ComPtr<IAudioSessionManager2> manager; ComPtr<IAudioSessionEnumerator> sessions; int count{};
            hr = device->Activate(__uuidof(IAudioSessionManager2), CLSCTX_ALL, nullptr, &manager);
            if (SUCCEEDED(hr)) hr = manager->GetSessionEnumerator(&sessions);
            if (SUCCEEDED(hr)) hr = sessions->GetCount(&count);
            std::cout << ",\"session_hresult\":" << hr << ",\"sessions\":[";
            for (int index = 0; SUCCEEDED(hr) && index < count && index < 32; ++index) {
                if (index) std::cout << ',';
                ComPtr<IAudioSessionControl> control; ComPtr<IAudioSessionControl2> identity; ComPtr<ISimpleAudioVolume> sessionVolume;
                DWORD pid{}; AudioSessionState state{}; BOOL sessionMuted{}; float sessionLevel{};
                HRESULT result = sessions->GetSession(index, &control);
                if (SUCCEEDED(result)) result = control.As(&identity);
                if (SUCCEEDED(result)) result = control.As(&sessionVolume);
                if (SUCCEEDED(result)) result = identity->GetProcessId(&pid);
                if (SUCCEEDED(result)) result = control->GetState(&state);
                if (SUCCEEDED(result)) result = sessionVolume->GetMute(&sessionMuted);
                if (SUCCEEDED(result)) result = sessionVolume->GetMasterVolume(&sessionLevel);
                std::cout << "{\"hresult\":" << result << ",\"pid\":" << pid << ",\"state\":" << state
                          << ",\"muted\":" << (sessionMuted ? "true" : "false") << ",\"volume\":" << sessionLevel
                          << ",\"system_sounds\":" << (identity && identity->IsSystemSoundsSession() == S_OK ? "true" : "false") << '}';
            }
            std::cout << ']';
        }
        std::cout << '}';
    }
    std::cout << "]}\n";
}

int wmain(int argc, wchar_t** argv) {
    if (argc != 2 || FAILED(CoInitializeEx(nullptr, COINIT_MULTITHREADED))) return 2;
    try {
        auto env = [](const wchar_t* key) { wchar_t value[256]{}; const DWORD size = GetEnvironmentVariableW(key, value, 256); return size && size < 256 ? std::wstring(value, size) : L""; };
        if (env(L"GITHUB_REPOSITORY") != L"777genius/agent-notifications" || env(L"GITHUB_EVENT_NAME") != L"workflow_dispatch"
            || env(L"GITHUB_RUN_ATTEMPT") != L"1" || env(L"GITHUB_REF") != L"refs/heads/test/pi-host-preparation-2")
            throw "fresh manual diagnostic branch required";
        if (std::wstring(argv[1]) == L"shell") shell();
        else if (std::wstring(argv[1]) == L"audio") audio();
        else return 2;
    } catch (const char* error) { std::cerr << error << '\n'; return 2; }
    return 0;
}
