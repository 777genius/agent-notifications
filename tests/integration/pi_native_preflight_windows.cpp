// Synthetic native observer prerequisites only: no Pi, toast or product registration.
#define NOMINMAX
#include <windows.h>
#include <wtsapi32.h>
#include <audioclient.h>
#include <mmdeviceapi.h>
#include <mmreg.h>
#include <ks.h>
#include <ksmedia.h>
#include <uiautomationclient.h>
#include <wrl/client.h>
#include <cmath>
#include <cstdint>
#include <iostream>
#include <sstream>
#include <string>
#include <vector>
#include "pi_windows_audio_fixture.h"

using Microsoft::WRL::ComPtr;
static HWND clickedWindow = nullptr;
static LRESULT CALLBACK windowProc(HWND window, UINT message, WPARAM wParam, LPARAM lParam) {
    if (message == WM_LBUTTONUP) clickedWindow = window;
    return DefWindowProcW(window, message, wParam, lParam);
}
static void pump(DWORD duration) {
    const ULONGLONG end = GetTickCount64() + duration;
    while (GetTickCount64() < end) {
        MSG message;
        while (PeekMessageW(&message, nullptr, 0, 0, PM_REMOVE)) {
            TranslateMessage(&message);
            DispatchMessageW(&message);
        }
        Sleep(5);
    }
}
static const char* jsonBool(bool value) { return value ? "true" : "false"; }

static HWND preflightKeyboardRoot() {
    GUITHREADINFO info{};
    info.cbSize = sizeof info;
    const DWORD thread = GetWindowThreadProcessId(GetForegroundWindow(), nullptr);
    return thread && GetGUIThreadInfo(thread, &info) && info.hwndFocus ? GetAncestor(info.hwndFocus, GA_ROOT) : nullptr;
}
static const char* desktopKind(HANDLE object, const wchar_t* expected) {
    wchar_t name[128]{};
    DWORD needed = 0;
    if (!object || !GetUserObjectInformationW(object, UOI_NAME, name, sizeof name, &needed)) return "unavailable";
    return std::wstring(name) == expected ? "expected" : "other";
}
struct OwnedInputAttempt {
    HWND window = nullptr, clicked = nullptr, foreground = nullptr, keyboard = nullptr;
    bool raised = false, requested = false, unoccluded = false;
    UINT sent = 0;
    DWORD inputError = 0;
    bool focused() const { return foreground == window && keyboard == window; }
    bool received() const { return unoccluded && sent == 3 && clicked == window && focused(); }
};
static OwnedInputAttempt observeOwnedInput(HWND window) {
    OwnedInputAttempt result;
    result.window = window;
    // Only our disposable TEST window is raised. A programmatic foreground
    // request may be denied for a background CI process; the real click below
    // must independently deliver input and activate this exact HWND.
    ShowWindow(window, SW_SHOWNORMAL);
    result.raised = SetWindowPos(window, HWND_TOPMOST, 0, 0, 0, 0,
                                 SWP_NOMOVE | SWP_NOSIZE | SWP_NOACTIVATE | SWP_SHOWWINDOW) != FALSE;
    UpdateWindow(window);
    result.requested = SetForegroundWindow(window) != FALSE;
    pump(100);
    RECT client{};
    POINT point{};
    const int left = GetSystemMetrics(SM_XVIRTUALSCREEN), top = GetSystemMetrics(SM_YVIRTUALSCREEN);
    const int width = GetSystemMetrics(SM_CXVIRTUALSCREEN), height = GetSystemMetrics(SM_CYVIRTUALSCREEN);
    if (GetClientRect(window, &client)) {
        point = {(client.left + client.right) / 2, (client.top + client.bottom) / 2};
        result.unoccluded = ClientToScreen(window, &point) && width > 1 && height > 1
            && point.x >= left && point.x < left + width && point.y >= top && point.y < top + height
            && GetAncestor(WindowFromPoint(point), GA_ROOT) == window;
    }
    clickedWindow = nullptr;
    if (result.raised && result.unoccluded) {
        INPUT inputs[3]{};
        for (auto& item : inputs) item.type = INPUT_MOUSE;
        inputs[0].mi.dx = static_cast<LONG>((static_cast<int64_t>(point.x) - left) * 65535 / (width - 1));
        inputs[0].mi.dy = static_cast<LONG>((static_cast<int64_t>(point.y) - top) * 65535 / (height - 1));
        inputs[0].mi.dwFlags = MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK;
        inputs[1].mi.dwFlags = MOUSEEVENTF_LEFTDOWN;
        inputs[2].mi.dwFlags = MOUSEEVENTF_LEFTUP;
        SetLastError(ERROR_SUCCESS);
        result.sent = SendInput(3, inputs, sizeof(INPUT));
        result.inputError = result.sent == 3 ? ERROR_SUCCESS : GetLastError();
        const auto deadline = GetTickCount64() + 2000;
        while (GetTickCount64() < deadline) {
            pump(20);
            if (clickedWindow == window && GetForegroundWindow() == window && preflightKeyboardRoot() == window) break;
        }
    }
    result.clicked = clickedWindow;
    result.foreground = GetForegroundWindow();
    result.keyboard = preflightKeyboardRoot();
    return result;
}

// Read-only inventory, collected by the same process/session as capture. No
// service, endpoint, default role, driver or system setting is changed.
static std::string serviceInventory(const wchar_t* name) {
    DWORD state = 0, error = ERROR_SUCCESS, needed = 0;
    const SC_HANDLE manager = OpenSCManagerW(nullptr, nullptr, SC_MANAGER_CONNECT);
    SC_HANDLE service = nullptr;
    SERVICE_STATUS_PROCESS status{};
    if (!manager) error = GetLastError();
    else {
        service = OpenServiceW(manager, name, SERVICE_QUERY_STATUS);
        if (!service) error = GetLastError();
        else if (!QueryServiceStatusEx(service, SC_STATUS_PROCESS_INFO, reinterpret_cast<BYTE*>(&status), sizeof status, &needed)) error = GetLastError();
        else state = status.dwCurrentState;
    }
    if (service) CloseServiceHandle(service);
    if (manager) CloseServiceHandle(manager);
    return "{\"state\":" + std::to_string(state) + ",\"query_error\":" + std::to_string(error) + "}";
}

static std::string audioInventory(IMMDeviceEnumerator* enumerator) {
    std::ostringstream result;
    result << "{\"AudioSrv\":" << serviceInventory(L"AudioSrv")
           << ",\"AudioEndpointBuilder\":" << serviceInventory(L"AudioEndpointBuilder");
    ComPtr<IMMDeviceCollection> devices;
    HRESULT hr = enumerator ? enumerator->EnumAudioEndpoints(eRender, DEVICE_STATEMASK_ALL, &devices) : E_POINTER;
    UINT total = 0, active = 0, disabled = 0, unplugged = 0, notPresent = 0, unknown = 0;
    if (SUCCEEDED(hr)) hr = devices->GetCount(&total);
    if (SUCCEEDED(hr)) {
        for (UINT index = 0; index < total; index++) {
            ComPtr<IMMDevice> device;
            DWORD state = 0;
            if (FAILED(devices->Item(index, &device)) || FAILED(device->GetState(&state))) unknown++;
            else {
                if (state & DEVICE_STATE_ACTIVE) active++;
                if (state & DEVICE_STATE_DISABLED) disabled++;
                if (state & DEVICE_STATE_UNPLUGGED) unplugged++;
                if (state & DEVICE_STATE_NOTPRESENT) notPresent++;
            }
        }
    }
    result << ",\"render_endpoints\":{\"hresult\":" << hr << ",\"total\":" << total
           << ",\"active\":" << active << ",\"disabled\":" << disabled
           << ",\"unplugged\":" << unplugged << ",\"not_present\":" << notPresent << ",\"unknown\":" << unknown << "},\"default_roles\":{";
    const ERole roles[] = {eConsole, eMultimedia, eCommunications};
    const char* labels[] = {"console", "multimedia", "communications"};
    for (int index = 0; index < 3; index++) {
        ComPtr<IMMDevice> device;
        DWORD state = 0;
        hr = enumerator ? enumerator->GetDefaultAudioEndpoint(eRender, roles[index], &device) : E_POINTER;
        const HRESULT stateResult = SUCCEEDED(hr) ? device->GetState(&state) : hr;
        if (index) result << ',';
        result << '\"' << labels[index] << "\":{\"hresult\":" << hr
               << ",\"state_hresult\":" << stateResult << ",\"state\":" << state << '}';
    }
    result << "}}";
    return result.str();
}

int wmain(int argc, wchar_t** argv) {
    if (argc != 2 && argc != 8) return 2;
    const HRESULT init = CoInitializeEx(nullptr, COINIT_MULTITHREADED);
    if (FAILED(init)) return 2;
    DWORD session = 0;
    ProcessIdToSessionId(GetCurrentProcessId(), &session);
    LPWSTR connectionBytes = nullptr;
    DWORD connectionLength = 0;
    const bool stateKnown = WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE, WTS_CURRENT_SESSION,
                                                       WTSConnectState, &connectionBytes, &connectionLength) != FALSE;
    const bool connected = stateKnown && connectionLength == sizeof(WTS_CONNECTSTATE_CLASS)
        && *reinterpret_cast<WTS_CONNECTSTATE_CLASS*>(connectionBytes) == WTSActive;
    if (connectionBytes) WTSFreeMemory(connectionBytes);
    HDESK input = OpenInputDesktop(0, FALSE, DESKTOP_READOBJECTS);
    wchar_t desktopName[128]{};
    DWORD nameLength = 0;
    const bool inputDesktop = input && GetUserObjectInformationW(input, UOI_NAME, desktopName, sizeof desktopName, &nameLength)
        && std::wstring(desktopName) == L"Default";
    const std::string inputKind = desktopKind(input, L"Default");
    const std::string threadKind = desktopKind(GetThreadDesktop(GetCurrentThreadId()), L"Default");
    const std::string stationKind = desktopKind(GetProcessWindowStation(), L"WinSta0");
    if (input) CloseDesktop(input);
    OSVERSIONINFOEXW os{};
    os.dwOSVersionInfoSize = sizeof os;
    using RtlGetVersionFunction = LONG(WINAPI*)(OSVERSIONINFOEXW*);
    const auto rtlGetVersion = reinterpret_cast<RtlGetVersionFunction>(GetProcAddress(GetModuleHandleW(L"ntdll.dll"), "RtlGetVersion"));
    const bool windowsClient = rtlGetVersion && rtlGetVersion(&os) == 0 && os.wProductType == VER_NT_WORKSTATION;
    const bool shell = GetShellWindow() != nullptr;
    ComPtr<IUIAutomation> automation;
    ComPtr<IUIAutomationElement> rootElement;
    const bool uia = SUCCEEDED(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)))
        && SUCCEEDED(automation->GetRootElement(&rootElement)) && rootElement;
    const bool interactive = session != 0 && connected && inputDesktop;
    bool twoWindows = false, foreground = false, inputObserved = false;
    std::vector<OwnedInputAttempt> inputAttempts;
    // A locked/server session does not receive synthetic input or audio.
    if (interactive && windowsClient && shell && uia) {
        WNDCLASSW klass{};
        klass.lpfnWndProc = windowProc;
        klass.hInstance = GetModuleHandleW(nullptr);
        klass.lpszClassName = L"TEST.Pi.Native.Observer";
        RegisterClassW(&klass);
        std::vector<HWND> windows;
        for (int index = 0; index < 2; index++) {
            HWND window = CreateWindowExW(0, klass.lpszClassName, index == 0 ? L"TEST Pi origin" : L"TEST Pi distractor",
                                          WS_OVERLAPPEDWINDOW | WS_VISIBLE, 100 + index * 440, 160, 400, 220,
                                          nullptr, nullptr, klass.hInstance, nullptr);
            if (window) windows.push_back(window);
        }
        pump(200);
        twoWindows = windows.size() == 2 && windows[0] != windows[1] && IsWindowVisible(windows[0]) && IsWindowVisible(windows[1]);
        if (twoWindows) {
            foreground = true;
            inputObserved = true;
            for (HWND window : {windows[0], windows[1], windows[0]}) {
                const auto attempt = observeOwnedInput(window);
                inputAttempts.push_back(attempt);
                foreground = foreground && attempt.focused();
                inputObserved = inputObserved && attempt.received();
            }
        }
        for (HWND window : windows) DestroyWindow(window);
        UnregisterClassW(klass.lpszClassName, klass.hInstance);
    }
    ComPtr<IMMDeviceEnumerator> enumerator;
    ComPtr<IMMDevice> endpoint;
    ComPtr<IAudioClient> client;
    ComPtr<IAudioCaptureClient> capture;
    WAVEFORMATEX* format = nullptr;
    HRESULT audioResult = CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator));
    const std::string inventory = audioInventory(enumerator.Get());
    PiWindowsAudioFixture::Route audioRoute;
    if (SUCCEEDED(audioResult)) {
        try { audioRoute = PiWindowsAudioFixture::select(enumerator.Get(), argc - 2, argv + 2); endpoint = audioRoute.capture; }
        catch (const std::exception& error) {
            std::cerr << error.what() << '\n';
            std::cout << "{\"checks\":{},\"measurements\":{\"audio_route_invalid\":true}}\n";
            return 2;
        }
    }
    const bool audioEndpoint = SUCCEEDED(audioResult) && endpoint;
    std::uint64_t samples = 0;
    double peak = 0;
    bool captureStarted = false, played = false;
    if (audioEndpoint && interactive && windowsClient) {
        audioResult = endpoint->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &client);
        if (SUCCEEDED(audioResult)) audioResult = client->GetMixFormat(&format);
        if (SUCCEEDED(audioResult)) audioResult = client->Initialize(AUDCLNT_SHAREMODE_SHARED, audioRoute.flags, 10000000, 0, format, nullptr);
        if (SUCCEEDED(audioResult)) audioResult = client->GetService(IID_PPV_ARGS(&capture));
        if (SUCCEEDED(audioResult)) audioResult = client->Start();
        captureStarted = SUCCEEDED(audioResult);
        if (captureStarted) {
            bool floating = format->wFormatTag == WAVE_FORMAT_IEEE_FLOAT;
            if (format->wFormatTag == WAVE_FORMAT_EXTENSIBLE && format->cbSize >= 22) {
                const auto* extended = reinterpret_cast<WAVEFORMATEXTENSIBLE*>(format);
                floating = IsEqualGUID(extended->SubFormat, KSDATAFORMAT_SUBTYPE_IEEE_FLOAT);
            }
            const ULONGLONG start = GetTickCount64();
            while (GetTickCount64() - start < 2000) {
                if (!played && GetTickCount64() - start >= 250) {
                    played = PlaySoundW(argv[1], nullptr, SND_FILENAME | SND_ASYNC | SND_NODEFAULT) != FALSE;
                }
                UINT32 packets = 0;
                audioResult = capture->GetNextPacketSize(&packets);
                while (SUCCEEDED(audioResult) && packets) {
                    BYTE* bytes = nullptr;
                    UINT32 frames = 0;
                    DWORD flags = 0;
                    audioResult = capture->GetBuffer(&bytes, &frames, &flags, nullptr, nullptr);
                    if (FAILED(audioResult)) break;
                    const auto count = static_cast<std::uint64_t>(frames) * format->nChannels;
                    samples += count;
                    if (!(flags & AUDCLNT_BUFFERFLAGS_SILENT) && bytes) {
                        for (std::uint64_t index = 0; index < count; index++) {
                            double value = 0;
                            if (floating && format->wBitsPerSample == 32) value = reinterpret_cast<const float*>(bytes)[index];
                            else if (!floating && format->wBitsPerSample == 16) value = reinterpret_cast<const std::int16_t*>(bytes)[index] / 32768.0;
                            peak = (std::isfinite(value) && std::abs(value) > peak) ? std::abs(value) : peak;
                        }
                    }
                    capture->ReleaseBuffer(frames);
                    audioResult = capture->GetNextPacketSize(&packets);
                }
                pump(5);
            }
            client->Stop();
            PlaySoundW(nullptr, nullptr, 0);
        }
    }
    if (format) CoTaskMemFree(format);
    std::ostringstream inputFacts;
    inputFacts << '[';
    for (size_t index = 0; index < inputAttempts.size(); index++) {
        const auto& value = inputAttempts[index];
        if (index) inputFacts << ',';
        inputFacts << "{\"owned_hwnd\":" << reinterpret_cast<uintptr_t>(value.window)
                   << ",\"raised\":" << jsonBool(value.raised) << ",\"foreground_request\":" << jsonBool(value.requested)
                   << ",\"unoccluded\":" << jsonBool(value.unoccluded) << ",\"sent\":" << value.sent
                   << ",\"input_error\":" << value.inputError << ",\"clicked_hwnd\":" << reinterpret_cast<uintptr_t>(value.clicked)
                   << ",\"foreground_hwnd\":" << reinterpret_cast<uintptr_t>(value.foreground)
                   << ",\"keyboard_hwnd\":" << reinterpret_cast<uintptr_t>(value.keyboard) << '}';
    }
    inputFacts << ']';
    std::cout << "{\"checks\":{\"windows_client\":" << jsonBool(windowsClient)
              << ",\"interactive_gui\":" << jsonBool(interactive) << ",\"shell\":" << jsonBool(shell)
              << ",\"uia\":" << jsonBool(uia) << ",\"two_windows\":" << jsonBool(twoWindows)
              << ",\"foreground_observer\":" << jsonBool(foreground) << ",\"input_observer\":" << jsonBool(inputObserved)
              << ",\"audio_endpoint\":" << jsonBool(audioEndpoint) << ",\"audio_capture\":" << jsonBool(captureStarted && samples >= 4800)
              << ",\"known_cue_observed\":" << jsonBool(played && peak > 0.01)
              << "},\"measurements\":{\"session\":" << session << ",\"connection_known\":" << jsonBool(stateKnown)
              << ",\"input_desktop\":" << jsonBool(inputDesktop) << ",\"audio_hresult\":" << audioResult
              << ",\"input_desktop_kind\":\"" << inputKind << "\",\"thread_desktop_kind\":\"" << threadKind
              << "\",\"window_station_kind\":\"" << stationKind << "\",\"owned_input_attempts\":" << inputFacts.str()
              << ",\"audio_samples\":" << samples << ",\"audio_peak\":" << peak
              << ",\"known_cue_method\":\"PlaySoundW\""
              << ",\"audio_route\":" << audioRoute.json
              << ",\"audio_inventory\":" << inventory << "}}\n";
    return 0;
}
