// Narrow TEST observer. Real Terminal/Shell input + external WASAPI capture.
// Reuse the existing WTS/input/audio inventory and token-query primitives.
#define _WIN32_WINNT 0x0A00
#define wmain piPrerequisiteEntry
#include "pi_native_preflight_windows.cpp"
#undef wmain
#include "navigation_windows_token_queries.h"
#include <UIAutomation.h>
#include <mfapi.h>
#include <mfidl.h>
#include <mfreadwrite.h>
#include <filesystem>
#include <fstream>
#include <algorithm>
#include <cwctype>
#include <stdexcept>

namespace fs = std::filesystem;
static void need(bool value, const char* code) { if (!value) throw std::runtime_error(code); }
static void hrcheck(HRESULT value) { if (FAILED(value)) throw std::runtime_error("native_hresult:" + std::to_string(value)); }
static std::string utf8(const std::wstring& value) {
    const int size = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    std::string result(size, 0);
    WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), result.data(), size, nullptr, nullptr);
    return result;
}
static std::wstring title(HWND window) {
    wchar_t value[1024]{}; GetWindowTextW(window, value, 1024); return value;
}
static DWORD owner(HWND window) { DWORD pid = 0; GetWindowThreadProcessId(window, &pid); return pid; }
static bool testTitle(const std::wstring& value) { return value.rfind(L"TEST-Pi-", 0) == 0 && value.size() <= 256; }
static bool terminalClass(HWND window) {
    wchar_t value[128]{}; GetClassNameW(window, value, 128);
    return std::wstring(value) == L"CASCADIA_HOSTING_WINDOW_CLASS" || std::wstring(value) == L"TEST.Pi.Product.Neutral";
}
static HWND boundWindow(const wchar_t* raw, const wchar_t* pid, const std::wstring& expected) {
    const auto window = reinterpret_cast<HWND>(static_cast<uintptr_t>(std::stoull(raw)));
    need(testTitle(expected) && IsWindow(window) && IsWindowVisible(window) && owner(window) == std::stoul(pid)
         && terminalClass(window) && title(window).rfind(expected, 0) == 0, "owned_terminal_identity_changed");
    return window;
}
static HWND keyboardRoot() {
    GUITHREADINFO info{}; info.cbSize = sizeof info;
    const DWORD thread = GetWindowThreadProcessId(GetForegroundWindow(), nullptr);
    return thread && GetGUIThreadInfo(thread, &info) && info.hwndFocus ? GetAncestor(info.hwndFocus, GA_ROOT) : nullptr;
}
static bool mouse(POINT point) {
    INPUT values[3]{};
    for (auto& value : values) value.type = INPUT_MOUSE;
    values[0].mi.dx = (point.x - GetSystemMetrics(SM_XVIRTUALSCREEN)) * 65535 / (GetSystemMetrics(SM_CXVIRTUALSCREEN) - 1);
    values[0].mi.dy = (point.y - GetSystemMetrics(SM_YVIRTUALSCREEN)) * 65535 / (GetSystemMetrics(SM_CYVIRTUALSCREEN) - 1);
    values[0].mi.dwFlags = MOUSEEVENTF_MOVE | MOUSEEVENTF_ABSOLUTE | MOUSEEVENTF_VIRTUALDESK;
    values[1].mi.dwFlags = MOUSEEVENTF_LEFTDOWN; values[2].mi.dwFlags = MOUSEEVENTF_LEFTUP;
    return SendInput(3, values, sizeof(INPUT)) == 3;
}
static void foreground(HWND window) {
    ShowWindow(window, SW_RESTORE); SetForegroundWindow(window); pump(100);
    if (GetForegroundWindow() != window) {
        RECT rect{}; need(GetWindowRect(window, &rect), "terminal_rectangle_missing");
        POINT point{rect.left + 100, rect.top + 12};
        need(GetAncestor(WindowFromPoint(point), GA_ROOT) == window && mouse(point), "owned_terminal_occluded_or_input_denied");
    }
    const auto until = GetTickCount64() + 2000;
    while ((GetForegroundWindow() != window || keyboardRoot() != window) && GetTickCount64() < until) pump(20);
    need(GetForegroundWindow() == window && keyboardRoot() == window, "native_terminal_focus_not_observed");
}
static void key(WORD vk, bool up) {
    INPUT value{}; value.type = INPUT_KEYBOARD; value.ki.wVk = vk; value.ki.dwFlags = up ? KEYEVENTF_KEYUP : 0;
    need(SendInput(1, &value, sizeof value) == 1, "native_keyboard_input_denied");
}
static void typeLine(HWND window, const std::wstring& value) {
    need(value.size() <= 1024 && GetForegroundWindow() == window && keyboardRoot() == window, "typing_requires_owned_foreground");
    key(VK_CONTROL, false); key('U', false); key('U', true); key(VK_CONTROL, true);
    for (wchar_t ch : value) {
        INPUT pair[2]{};
        for (auto& item : pair) { item.type = INPUT_KEYBOARD; item.ki.wScan = ch; item.ki.dwFlags = KEYEVENTF_UNICODE; }
        pair[1].ki.dwFlags |= KEYEVENTF_KEYUP;
        need(SendInput(2, pair, sizeof(INPUT)) == 2, "native_unicode_input_denied"); pump(25);
    }
    key(VK_RETURN, false); key(VK_RETURN, true);
}
static void screenshot(const fs::path& path) {
    const int width = GetSystemMetrics(SM_CXSCREEN), height = GetSystemMetrics(SM_CYSCREEN);
    need(width > 0 && height > 0 && static_cast<uint64_t>(width) * height <= 16000000, "screen_capture_bound");
    HDC screen = GetDC(nullptr), memory = CreateCompatibleDC(screen);
    BITMAPINFO info{}; info.bmiHeader.biSize = sizeof(BITMAPINFOHEADER); info.bmiHeader.biWidth = width;
    info.bmiHeader.biHeight = -height; info.bmiHeader.biPlanes = 1; info.bmiHeader.biBitCount = 32; info.bmiHeader.biCompression = BI_RGB;
    void* bits = nullptr; HBITMAP bitmap = CreateDIBSection(screen, &info, DIB_RGB_COLORS, &bits, nullptr, 0);
    need(screen && memory && bitmap && bits, "screen_capture_unavailable");
    const auto previous = SelectObject(memory, bitmap);
    need(BitBlt(memory, 0, 0, width, height, screen, 0, 0, SRCCOPY | CAPTUREBLT), "screen_capture_failed");
    BITMAPFILEHEADER header{}; header.bfType = 0x4d42; header.bfOffBits = sizeof header + sizeof(BITMAPINFOHEADER);
    header.bfSize = header.bfOffBits + width * height * 4;
    std::ofstream file(path, std::ios::binary); file.write(reinterpret_cast<char*>(&header), sizeof header);
    file.write(reinterpret_cast<char*>(&info.bmiHeader), sizeof info.bmiHeader);
    file.write(static_cast<char*>(bits), static_cast<std::streamsize>(width) * height * 4);
    SelectObject(memory, previous); DeleteObject(bitmap); DeleteDC(memory); ReleaseDC(nullptr, screen);
    need(file.good(), "screen_capture_file_failed");
}
static bool shellOwner(DWORD pid) {
    HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
    if (!process) return false;
    wchar_t image[32768]{}, windows[32768]{}; DWORD size = 32768;
    const bool known = QueryFullProcessImageNameW(process, 0, image, &size) && GetWindowsDirectoryW(windows, 32768);
    NavigationTokenTEST::Token token, self;
    bool matched = false;
    if (known && OpenProcessToken(process, TOKEN_QUERY, &token.handle) && OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &self.handle)) {
        const auto a = NavigationTokenTEST::facts(token.handle), b = NavigationTokenTEST::facts(self.handle);
        std::wstring actual = image, prefix = std::wstring(windows) + L"\\";
        std::transform(actual.begin(), actual.end(), actual.begin(), towlower);
        std::transform(prefix.begin(), prefix.end(), prefix.begin(), towlower);
        const auto leaf = fs::path(actual).filename().wstring();
        DWORD code = 0;
        matched = GetExitCodeProcess(process, &code) && code == STILL_ACTIVE && a.sid == b.sid && a.session == b.session
            && actual.rfind(prefix, 0) == 0 && (leaf == L"shellexperiencehost.exe" || leaf == L"shellhost.exe" || leaf == L"explorer.exe");
    }
    CloseHandle(process); return matched;
}
static std::wstring elementName(IUIAutomationElement* element) {
    BSTR raw = nullptr; hrcheck(element->get_CurrentName(&raw));
    std::wstring value = raw ? raw : L""; if (raw) SysFreeString(raw);
    need(value.size() <= 4096, "native_ui_text_bound"); return value;
}
static ComPtr<IUIAutomationElement> card(IUIAutomation* automation, const std::wstring& expected, const std::wstring& body) {
    ComPtr<IUIAutomationElement> root, text; hrcheck(automation->GetRootElement(&root));
    VARIANT name{}; name.vt = VT_BSTR; name.bstrVal = SysAllocString(expected.c_str());
    ComPtr<IUIAutomationCondition> match; hrcheck(automation->CreatePropertyCondition(UIA_NamePropertyId, name, &match)); VariantClear(&name);
    ComPtr<IUIAutomationElementArray> all; hrcheck(root->FindAll(TreeScope_Descendants, match.Get(), &all));
    int total = 0; hrcheck(all->get_Length(&total)); need(total <= 1, "product_toast_title_ambiguous");
    if (!total) return nullptr;
    hrcheck(all->GetElement(0, &text));
    int pid = 0; hrcheck(text->get_CurrentProcessId(&pid)); if (!shellOwner(pid)) return nullptr;
    ComPtr<IUIAutomationTreeWalker> walker; hrcheck(automation->get_ControlViewWalker(&walker));
    auto current = text;
    for (int depth = 0; depth < 6; depth++) {
        CONTROLTYPEID type = 0; int currentPID = 0;
        hrcheck(current->get_CurrentControlType(&type)); hrcheck(current->get_CurrentProcessId(&currentPID));
        if (currentPID != pid) return nullptr;
        if (type == UIA_ButtonControlTypeId || type == UIA_ListItemControlTypeId) {
            ComPtr<IUIAutomationCondition> yes; hrcheck(automation->CreateTrueCondition(&yes));
            ComPtr<IUIAutomationElementArray> children; hrcheck(current->FindAll(TreeScope_Descendants, yes.Get(), &children));
            int count = 0; hrcheck(children->get_Length(&count)); need(count <= 64, "product_toast_group_bound");
            bool titleSeen = false, bodySeen = false;
            for (int index = 0; index < count; index++) {
                ComPtr<IUIAutomationElement> child; hrcheck(children->GetElement(index, &child));
                const auto value = elementName(child.Get());
                titleSeen = titleSeen || value == expected; bodySeen = bodySeen || value.find(body) != std::wstring::npos;
            }
            if (titleSeen && bodySeen) return current;
        }
        ComPtr<IUIAutomationElement> parent; hrcheck(walker->GetParentElement(current.Get(), &parent));
        if (!parent) break; current = parent;
    }
    return nullptr;
}
static int toast(const std::wstring& expected, const std::wstring& body, const fs::path& image, bool click) {
    need(testTitle(expected) && !body.empty() && body.size() <= 256, "owned_product_toast_selector_required");
    ComPtr<IUIAutomation> automation; hrcheck(CoCreateInstance(CLSID_CUIAutomation, nullptr, CLSCTX_INPROC_SERVER, IID_PPV_ARGS(&automation)));
    const auto until = GetTickCount64() + 15000;
    while (GetTickCount64() < until) {
        auto selected = card(automation.Get(), expected, body);
        if (selected) {
            BOOL offscreen = TRUE, clickable = FALSE; POINT point{};
            hrcheck(selected->get_CurrentIsOffscreen(&offscreen)); hrcheck(selected->GetClickablePoint(&point, &clickable));
            int pid = 0; hrcheck(selected->get_CurrentProcessId(&pid));
            need(!offscreen && clickable && shellOwner(pid), "product_toast_not_visible_clickable_shell");
            screenshot(image);
            if (click) {
                need(owner(GetAncestor(WindowFromPoint(point), GA_ROOT)) == static_cast<DWORD>(pid), "product_toast_click_occluded");
                need(mouse(point), "product_toast_native_mouse_denied");
            }
            std::cout << "{\"found\":true,\"native_shell_pid\":" << pid << ",\"real_mouse_click\":" << jsonBool(click) << "}\n";
            return 0;
        }
        pump(100);
    }
    std::cout << "{\"found\":false}\n"; return 3;
}
static int capture(const fs::path& path, const fs::path& stop, int routeCount, wchar_t** routeArgs) {
    ComPtr<IMMDeviceEnumerator> enumerator; ComPtr<IMMDevice> endpoint; ComPtr<IAudioClient> client; ComPtr<IAudioCaptureClient> reader;
    hrcheck(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator)));
    const auto route = PiWindowsAudioFixture::select(enumerator.Get(), routeCount, routeArgs);
    endpoint = route.capture;
    hrcheck(endpoint->Activate(__uuidof(IAudioClient), CLSCTX_ALL, nullptr, &client));
    WAVEFORMATEX* format = nullptr; hrcheck(client->GetMixFormat(&format));
    bool floating = format->wFormatTag == WAVE_FORMAT_IEEE_FLOAT;
    if (format->wFormatTag == WAVE_FORMAT_EXTENSIBLE && format->cbSize >= 22)
        floating = IsEqualGUID(reinterpret_cast<WAVEFORMATEXTENSIBLE*>(format)->SubFormat, KSDATAFORMAT_SUBTYPE_IEEE_FLOAT);
    need((floating && format->wBitsPerSample == 32) || (!floating && format->wBitsPerSample == 16), "unsupported_native_loopback_format");
    hrcheck(client->Initialize(AUDCLNT_SHAREMODE_SHARED, route.flags, 10000000, 0, format, nullptr));
    hrcheck(client->GetService(IID_PPV_ARGS(&reader))); hrcheck(client->Start());
    std::ofstream file(path, std::ios::binary); need(file.good() && !fs::exists(stop), "native_capture_path_invalid");
    std::cout << "{\"ready\":true,\"sample_rate\":" << format->nSamplesPerSec << ",\"audio_route\":" << route.json << "}\n" << std::flush;
    const auto started = GetTickCount64(); uint64_t samples = 0, nonzero = 0; double peak = 0;
    while (!fs::exists(stop) && GetTickCount64() - started < 30000) {
        UINT32 packets = 0; hrcheck(reader->GetNextPacketSize(&packets));
        while (packets) {
            BYTE* bytes = nullptr; UINT32 frames = 0; DWORD flags = 0;
            hrcheck(reader->GetBuffer(&bytes, &frames, &flags, nullptr, nullptr));
            for (UINT32 index = 0; index < frames; index++) {
                double value = 0;
                if (!(flags & AUDCLNT_BUFFERFLAGS_SILENT) && bytes) {
                    for (UINT32 channel = 0; channel < format->nChannels; channel++)
                        value += floating ? reinterpret_cast<float*>(bytes)[index * format->nChannels + channel]
                                          : reinterpret_cast<int16_t*>(bytes)[index * format->nChannels + channel] / 32768.0;
                    value /= format->nChannels;
                }
                need(std::isfinite(value), "native_audio_not_finite");
                value = std::clamp(value, -1.0, 1.0); peak = std::max(peak, std::abs(value));
                int16_t pcm = static_cast<int16_t>(value * 32767); file.write(reinterpret_cast<char*>(&pcm), sizeof pcm);
                samples++; if (std::abs(static_cast<int>(pcm)) > 128) nonzero++;
            }
            hrcheck(reader->ReleaseBuffer(frames)); hrcheck(reader->GetNextPacketSize(&packets));
            need(samples <= static_cast<uint64_t>(format->nSamplesPerSec) * 31, "native_audio_capture_bound");
        }
        pump(5);
    }
    hrcheck(client->Stop()); DWORD state = 0; hrcheck(endpoint->GetState(&state));
    const auto rate = format->nSamplesPerSec; CoTaskMemFree(format);
    need(state == DEVICE_STATE_ACTIVE && file.good() && fs::exists(stop), "native_audio_capture_incomplete");
    std::cout << "{\"complete\":true,\"sample_rate\":" << rate << ",\"samples\":" << samples
              << ",\"peak\":" << peak << ",\"nonzero_samples\":" << nonzero
              << ",\"elapsed_ms\":" << GetTickCount64() - started << ",\"audio_route\":" << route.json << "}\n"; return 0;
}
static int decode(const fs::path& source, const fs::path& output) {
    // Decode the actual configured asset for comparison only; never render it.
    hrcheck(MFStartup(MF_VERSION, MFSTARTUP_NOSOCKET));
    ComPtr<IMFSourceReader> reader; hrcheck(MFCreateSourceReaderFromURL(source.c_str(), nullptr, &reader));
    ComPtr<IMFMediaType> type; hrcheck(MFCreateMediaType(&type));
    hrcheck(type->SetGUID(MF_MT_MAJOR_TYPE, MFMediaType_Audio)); hrcheck(type->SetGUID(MF_MT_SUBTYPE, MFAudioFormat_PCM));
    hrcheck(type->SetUINT32(MF_MT_AUDIO_BITS_PER_SAMPLE, 16));
    hrcheck(reader->SetCurrentMediaType(MF_SOURCE_READER_FIRST_AUDIO_STREAM, nullptr, type.Get()));
    ComPtr<IMFMediaType> actual; hrcheck(reader->GetCurrentMediaType(MF_SOURCE_READER_FIRST_AUDIO_STREAM, &actual));
    UINT32 sampleRate = 0, channelCount = 0, bitCount = 0;
    hrcheck(actual->GetUINT32(MF_MT_AUDIO_SAMPLES_PER_SECOND, &sampleRate));
    hrcheck(actual->GetUINT32(MF_MT_AUDIO_NUM_CHANNELS, &channelCount));
    hrcheck(actual->GetUINT32(MF_MT_AUDIO_BITS_PER_SAMPLE, &bitCount));
    need(bitCount == 16 && (channelCount == 1 || channelCount == 2) && sampleRate >= 8000 && sampleRate <= 192000,
         "configured_cue_pcm_format");
    std::ofstream file(output, std::ios::binary); char empty[44]{}; file.write(empty, sizeof empty);
    uint32_t bytes = 0;
    for (;;) {
        DWORD flags = 0; ComPtr<IMFSample> sample;
        hrcheck(reader->ReadSample(MF_SOURCE_READER_FIRST_AUDIO_STREAM, 0, nullptr, &flags, nullptr, &sample));
        if (sample) {
            ComPtr<IMFMediaBuffer> buffer; hrcheck(sample->ConvertToContiguousBuffer(&buffer));
            BYTE* data = nullptr; DWORD size = 0; hrcheck(buffer->Lock(&data, nullptr, &size));
            need(bytes + size <= 30 * sampleRate * channelCount * 2, "configured_cue_decode_bound");
            file.write(reinterpret_cast<char*>(data), size); bytes += size; hrcheck(buffer->Unlock());
        }
        if (flags & MF_SOURCE_READERF_ENDOFSTREAM) break;
    }
    const uint32_t riff = bytes + 36, sixteen = 16, rate = sampleRate, average = sampleRate * channelCount * 2;
    const uint16_t pcm = 1, channels = static_cast<uint16_t>(channelCount), align = static_cast<uint16_t>(channelCount * 2), bits = 16;
    file.seekp(0); file.write("RIFF", 4); file.write(reinterpret_cast<const char*>(&riff), 4); file.write("WAVEfmt ", 8);
    file.write(reinterpret_cast<const char*>(&sixteen), 4); file.write(reinterpret_cast<const char*>(&pcm), 2);
    file.write(reinterpret_cast<const char*>(&channels), 2); file.write(reinterpret_cast<const char*>(&rate), 4);
    file.write(reinterpret_cast<const char*>(&average), 4); file.write(reinterpret_cast<const char*>(&align), 2);
    file.write(reinterpret_cast<const char*>(&bits), 2); file.write("data", 4); file.write(reinterpret_cast<const char*>(&bytes), 4);
    need(bytes && file.good(), "configured_cue_decode_failed");
    std::cout << "{\"decoded\":true,\"bytes\":" << bytes << "}\n"; return 0;
}
int wmain(int argc, wchar_t** argv) {
    try {
        need(argc >= 2, "native_observer_command_required"); const std::wstring mode = argv[1];
        if (mode == L"preflight" && (argc == 3 || argc == 9)) return piPrerequisiteEntry(argc - 1, argv + 1);
        hrcheck(CoInitializeEx(nullptr, COINIT_MULTITHREADED));
        if (mode == L"capture" && (argc == 4 || argc == 10)) return capture(argv[2], argv[3], argc - 4, argv + 4);
        if (mode == L"decode" && argc == 4) return decode(argv[2], argv[3]);
        if (mode == L"neutral" && argc == 4) {
            const std::wstring name = argv[2]; need(testTitle(name) && !fs::exists(argv[3]), "owned_neutral_window_required");
            WNDCLASSW klass{}; klass.lpfnWndProc = windowProc; klass.hInstance = GetModuleHandleW(nullptr);
            klass.lpszClassName = L"TEST.Pi.Product.Neutral"; RegisterClassW(&klass);
            HWND window = CreateWindowExW(0, klass.lpszClassName, name.c_str(), WS_OVERLAPPEDWINDOW | WS_VISIBLE,
                                          40, 160, 500, 250, nullptr, nullptr, klass.hInstance, nullptr);
            need(window != nullptr, "neutral_window_unavailable");
            std::cout << "{\"hwnd\":" << reinterpret_cast<uintptr_t>(window) << ",\"pid\":" << GetCurrentProcessId() << "}\n" << std::flush;
            const auto until = GetTickCount64() + 45000;
            while (IsWindow(window) && !fs::exists(argv[3]) && GetTickCount64() < until) pump(10);
            if (IsWindow(window)) DestroyWindow(window); return 0;
        }
        if (mode == L"foreground") {
            HWND window = GetForegroundWindow();
            std::cout << "{\"hwnd\":" << reinterpret_cast<uintptr_t>(window) << ",\"pid\":" << owner(window)
                      << ",\"keyboard_hwnd\":" << reinterpret_cast<uintptr_t>(keyboardRoot()) << "}\n"; return 0;
        }
        if (mode == L"window" && argc == 3) {
            struct Search { std::wstring expected; std::vector<HWND> found; } search{argv[2], {}};
            need(testTitle(search.expected), "owned_window_title_required");
            EnumWindows([](HWND window, LPARAM value) -> BOOL {
                auto& search = *reinterpret_cast<Search*>(value);
                if (IsWindowVisible(window) && terminalClass(window) && title(window).rfind(search.expected, 0) == 0) search.found.push_back(window);
                return TRUE;
            }, reinterpret_cast<LPARAM>(&search));
            need(search.found.size() <= 1, "owned_terminal_window_ambiguous");
            const HWND window = search.found.empty() ? nullptr : search.found[0];
            std::cout << "{\"hwnd\":" << reinterpret_cast<uintptr_t>(window) << ",\"pid\":" << owner(window) << "}\n"; return 0;
        }
        if ((mode == L"activate" || mode == L"close" || mode == L"type") && argc >= 5) {
            HWND window = boundWindow(argv[2], argv[3], argv[4]);
            if (mode == L"close") PostMessageW(window, WM_CLOSE, 0, 0);
            else { foreground(window); if (mode == L"type") { need(argc == 6, "native_text_required"); typeLine(window, argv[5]); } }
            std::cout << "{\"ok\":true}\n"; return 0;
        }
        if ((mode == L"toast" || mode == L"toast-click") && argc == 5) return toast(argv[2], argv[3], argv[4], mode == L"toast-click");
        throw std::runtime_error("unsupported_native_observer_command");
    } catch (const NavigationTokenTEST::Failure& error) {
        std::cerr << error.query << ':' << error.error; return 2;
    } catch (const std::exception& error) { std::cerr << error.what(); return 2; }
}
