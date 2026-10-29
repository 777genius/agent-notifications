// Manual, disposable hosted-guest diagnostics. Never a product receipt.
#define NOMINMAX
#include <windows.h>
#include <mmdeviceapi.h>
#include <endpointvolume.h>
#include <audiopolicy.h>
#include <audioclient.h>
#include <mmreg.h>
#include <mmsystem.h>
#include <ks.h>
#include <ksmedia.h>
#include <functiondiscoverykeys_devpkey.h>
#include <wrl/client.h>
#include <cstdint>
#include <cmath>
#include <cstring>
#include <algorithm>
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

// Compare the fixture's actual WinMM WAV playback with an independent shared
// WASAPI renderer against the exact same default render endpoint/loopback.
// This does not change a driver option, endpoint default, mute or volume.
static void pcm(const wchar_t* cue) {
    ComPtr<IMMDeviceEnumerator> enumerator;
    ComPtr<IMMDevice> device;
    if (FAILED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator))) ||
        FAILED(enumerator->GetDefaultAudioEndpoint(eRender, eConsole, &device))) throw "default render unavailable";
    std::cout << "{\"product_executed\":false,\"host_policy_changed\":false,\"phases\":[";
    for (int phase = 0; phase < 2; ++phase) {
        if (phase) std::cout << ',';
        ComPtr<IAudioClient> captureClient, renderClient;
        ComPtr<IAudioCaptureClient> capture;
        ComPtr<IAudioRenderClient> render;
        WAVEFORMATEX* format{};
        HRESULT captureResult = device->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &captureClient);
        if (SUCCEEDED(captureResult)) captureResult = captureClient->GetMixFormat(&format);
        const auto* extended = format && format->wFormatTag == WAVE_FORMAT_EXTENSIBLE && format->cbSize >= 22 ? reinterpret_cast<WAVEFORMATEXTENSIBLE*>(format) : nullptr;
        const bool floating = format && (format->wFormatTag == WAVE_FORMAT_IEEE_FLOAT || (extended && IsEqualGUID(extended->SubFormat, KSDATAFORMAT_SUBTYPE_IEEE_FLOAT)));
        const bool integer = format && (format->wFormatTag == WAVE_FORMAT_PCM || (extended && IsEqualGUID(extended->SubFormat, KSDATAFORMAT_SUBTYPE_PCM)));
        const bool supported = format && (floating && format->wBitsPerSample == 32 || integer && (format->wBitsPerSample == 16 || format->wBitsPerSample == 24 || format->wBitsPerSample == 32));
        if (SUCCEEDED(captureResult)) captureResult = captureClient->Initialize(AUDCLNT_SHAREMODE_SHARED, AUDCLNT_STREAMFLAGS_LOOPBACK, 10000000, 0, format, nullptr);
        if (SUCCEEDED(captureResult)) captureResult = captureClient->GetService(IID_PPV_ARGS(&capture));
        if (SUCCEEDED(captureResult)) captureResult = captureClient->Start();
        const bool captureStarted = SUCCEEDED(captureResult);
        HRESULT renderResult = S_OK; UINT32 bufferFrames{};
        if (phase == 1 && supported) {
            renderResult = device->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &renderClient);
            if (SUCCEEDED(renderResult)) renderResult = renderClient->Initialize(AUDCLNT_SHAREMODE_SHARED, 0, 1000000, 0, format, nullptr);
            if (SUCCEEDED(renderResult)) renderResult = renderClient->GetBufferSize(&bufferFrames);
            if (SUCCEEDED(renderResult)) renderResult = renderClient->GetService(IID_PPV_ARGS(&render));
        }
        bool played = false, renderStarted = false;
        uint64_t framesWritten{}, nonzeroRenderFrames{}, packetsObserved{}, silentPackets{}, sampleCount{}, nonzeroBytes{};
        DWORD packetFlags{}; double peak{};
        const auto start = GetTickCount64();
        while (captureStarted && GetTickCount64() - start < 2300) {
            const auto elapsed = GetTickCount64() - start;
            if (elapsed >= 250 && phase == 0 && !played) {
                // Exactly one attempted playback, matching the unchanged fixture.
                played = true;
                if (!PlaySoundW(cue, nullptr, SND_FILENAME | SND_ASYNC | SND_NODEFAULT)) renderResult = E_FAIL;
            }
            if (elapsed >= 250 && phase == 1 && SUCCEEDED(renderResult) && render) {
                UINT32 padding{};
                renderResult = renderClient->GetCurrentPadding(&padding);
                const UINT32 available = padding <= bufferFrames ? bufferFrames - padding : 0;
                if (SUCCEEDED(renderResult) && available) {
                    BYTE* output{};
                    renderResult = render->GetBuffer(available, &output);
                    if (SUCCEEDED(renderResult)) {
                        for (UINT32 frame = 0; frame < available; ++frame) {
                            const bool tone = framesWritten < format->nSamplesPerSec / 2;
                            const double value = tone ? 0.25 * std::sin(2 * 3.14159265358979323846 * 880 * framesWritten / format->nSamplesPerSec) : 0;
                            if (tone) ++nonzeroRenderFrames;
                            for (WORD channel = 0; channel < format->nChannels; ++channel) {
                                BYTE* sample = output + frame * format->nBlockAlign + channel * (format->wBitsPerSample / 8);
                                if (floating) { const float converted = static_cast<float>(value); std::memcpy(sample, &converted, 4); }
                                else if (format->wBitsPerSample == 16) { const int16_t converted = static_cast<int16_t>(value * 32767); std::memcpy(sample, &converted, 2); }
                                else if (format->wBitsPerSample == 24) { const int32_t converted = static_cast<int32_t>(value * 8388607); for (int byte = 0; byte < 3; ++byte) sample[byte] = static_cast<BYTE>(converted >> (byte * 8)); }
                                else { const int32_t converted = static_cast<int32_t>(value * 2147483647); std::memcpy(sample, &converted, 4); }
                            }
                            ++framesWritten;
                        }
                        renderResult = render->ReleaseBuffer(available, 0);
                        if (!renderStarted && SUCCEEDED(renderResult)) { renderResult = renderClient->Start(); renderStarted = SUCCEEDED(renderResult); }
                    }
                }
            }
            UINT32 packets{};
            captureResult = capture->GetNextPacketSize(&packets);
            while (SUCCEEDED(captureResult) && packets) {
                BYTE* bytes{}; UINT32 frames{}; DWORD flags{};
                captureResult = capture->GetBuffer(&bytes, &frames, &flags, nullptr, nullptr);
                if (FAILED(captureResult)) break;
                ++packetsObserved; packetFlags |= flags;
                const uint64_t count = static_cast<uint64_t>(frames) * format->nChannels;
                sampleCount += count;
                if (flags & AUDCLNT_BUFFERFLAGS_SILENT) ++silentPackets;
                else if (bytes) {
                    for (uint64_t index = 0; index < static_cast<uint64_t>(frames) * format->nBlockAlign; ++index) if (bytes[index]) ++nonzeroBytes;
                    if (supported) for (uint64_t index = 0; index < count; ++index) {
                        const BYTE* sample = bytes + index * (format->wBitsPerSample / 8);
                        double value{};
                        if (floating) { float converted{}; std::memcpy(&converted, sample, 4); value = converted; }
                        else if (format->wBitsPerSample == 16) { int16_t converted{}; std::memcpy(&converted, sample, 2); value = converted / 32768.0; }
                        else if (format->wBitsPerSample == 24) { int32_t converted = sample[0] | sample[1] << 8 | sample[2] << 16; if (converted & 0x800000) converted |= ~0xffffff; value = converted / 8388608.0; }
                        else { int32_t converted{}; std::memcpy(&converted, sample, 4); value = converted / 2147483648.0; }
                        if (std::isfinite(value)) peak = std::max(peak, std::abs(value));
                    }
                }
                capture->ReleaseBuffer(frames);
                captureResult = capture->GetNextPacketSize(&packets);
            }
            Sleep(5);
        }
        if (captureStarted) captureClient->Stop();
        if (renderStarted) renderClient->Stop();
        if (phase == 0) PlaySoundW(nullptr, nullptr, 0);
        std::cout << "{\"playback\":\"" << (phase ? "same_endpoint_wasapi_880hz" : "unchanged_fixture_winmm_wav")
                  << "\",\"capture_started\":" << (captureStarted ? "true" : "false") << ",\"capture_hresult\":" << captureResult
                  << ",\"format_supported\":" << (supported ? "true" : "false") << ",\"format_tag\":" << (format ? format->wFormatTag : 0)
                  << ",\"format_bits\":" << (format ? format->wBitsPerSample : 0) << ",\"format_channels\":" << (format ? format->nChannels : 0)
                  << ",\"format_rate\":" << (format ? format->nSamplesPerSec : 0) << ",\"floating\":" << (floating ? "true" : "false")
                  << ",\"valid_bits\":" << (extended ? extended->Samples.wValidBitsPerSample : 0) << ",\"subtype_data1\":" << (extended ? extended->SubFormat.Data1 : 0)
                  << ",\"render_hresult\":" << renderResult << ",\"played\":" << (played ? "true" : "false")
                  << ",\"render_started\":" << (renderStarted ? "true" : "false") << ",\"render_frames\":" << framesWritten
                  << ",\"render_tone_frames\":" << nonzeroRenderFrames << ",\"packets\":" << packetsObserved << ",\"silent_packets\":" << silentPackets
                  << ",\"packet_flags_or\":" << packetFlags << ",\"samples\":" << sampleCount << ",\"nonzero_bytes\":" << nonzeroBytes << ",\"peak\":" << peak << '}';
        if (format) CoTaskMemFree(format);
    }
    std::cout << "]}\n";
}

int wmain(int argc, wchar_t** argv) {
    if ((argc != 2 && argc != 3) || FAILED(CoInitializeEx(nullptr, COINIT_MULTITHREADED))) return 2;
    try {
        auto env = [](const wchar_t* key) { wchar_t value[256]{}; const DWORD size = GetEnvironmentVariableW(key, value, 256); return size && size < 256 ? std::wstring(value, size) : L""; };
        if (env(L"GITHUB_REPOSITORY") != L"777genius/agent-notifications" || env(L"GITHUB_EVENT_NAME") != L"workflow_dispatch"
            || env(L"GITHUB_RUN_ATTEMPT") != L"1" || env(L"GITHUB_REF") != L"refs/heads/test/pi-host-audio-pcm")
            throw "fresh manual diagnostic branch required";
        if (std::wstring(argv[1]) == L"shell") shell();
        else if (std::wstring(argv[1]) == L"audio") audio();
        else if (std::wstring(argv[1]) == L"pcm" && argc == 3) pcm(argv[2]);
        else return 2;
    } catch (const char* error) { std::cerr << error << '\n'; return 2; }
    return 0;
}
