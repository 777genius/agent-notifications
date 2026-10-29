// Explicit, test-only audio route. Ordinary hosts retain eRender loopback.
#pragma once
#include <audioclient.h>
#include <mmdeviceapi.h>
#include <wrl/client.h>
#include <string>
#include <devicetopology.h>
#include <functiondiscoverykeys_devpkey.h>
#include <cfgmgr32.h>
#include <cfg.h>
#include <iostream>
#include <stdexcept>

namespace PiWindowsAudioFixture {
struct Endpoint {
    std::wstring id, topology, instance, name;
    Microsoft::WRL::ComPtr<IMMDevice> device;
};
struct Route {
    Microsoft::WRL::ComPtr<IMMDevice> capture;
    DWORD flags = AUDCLNT_STREAMFLAGS_LOOPBACK;
    std::string json = "{\"kind\":\"render_loopback\"}";
};
static void check(bool ok, const char* message) { if (!ok) throw std::runtime_error(message); }
static std::string quote(const std::wstring& value) {
    check(value.size() <= 4096, "audio_fixture_identity_bound");
    const int size = WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), nullptr, 0, nullptr, nullptr);
    std::string utf8(size, '\0'), result = "\"";
    WideCharToMultiByte(CP_UTF8, 0, value.data(), static_cast<int>(value.size()), utf8.data(), size, nullptr, nullptr);
    for (unsigned char ch : utf8) { check(ch >= 32, "audio_fixture_control_character"); if (ch == '\\' || ch == '"') result += '\\'; result += ch; }
    return result + '"';
}
static void binding(const std::wstring& actual, const std::wstring& expected, const char* field) {
    if (actual != expected)
        throw std::runtime_error(std::string("audio_fixture_") + field + "_changed:expected=" + quote(expected) + ":actual=" + quote(actual));
}
static std::wstring property(IMMDevice* device, const PROPERTYKEY& key) {
    Microsoft::WRL::ComPtr<IPropertyStore> store;
    PROPVARIANT value{}; PropVariantInit(&value);
    check(SUCCEEDED(device->OpenPropertyStore(STGM_READ, &store)), "audio_fixture_property_store_unavailable");
    const HRESULT hr = store->GetValue(key, &value);
    if (FAILED(hr) || value.vt != VT_LPWSTR || !value.pwszVal)
        throw std::runtime_error("audio_fixture_device_property_unavailable:pid=" + std::to_string(key.pid) + ":type=" + std::to_string(value.vt));
    const std::wstring result(value.pwszVal); PropVariantClear(&value);
    check(!result.empty() && result.size() <= 4096, "audio_fixture_device_property_bound"); return result;
}
static Endpoint inspect(IMMDeviceEnumerator* enumerator, IMMDevice* device, EDataFlow expected) {
    Endpoint result; result.device = device;
    Microsoft::WRL::ComPtr<IMMEndpoint> endpoint;
    EDataFlow flow{}; DWORD state{}; LPWSTR id{};
    check(SUCCEEDED(device->QueryInterface(IID_PPV_ARGS(&endpoint))) && SUCCEEDED(endpoint->GetDataFlow(&flow)) && flow == expected &&
          SUCCEEDED(device->GetState(&state)) && state == DEVICE_STATE_ACTIVE && SUCCEEDED(device->GetId(&id)) && id,
          "audio_fixture_endpoint_flow_or_state_changed");
    result.id = id; CoTaskMemFree(id);
    result.name = property(device, PKEY_Device_FriendlyName);
    check(result.name.find(L"(VB-Audio Virtual Cable)") != std::wstring::npos, "audio_fixture_virtual_endpoint_required");
    Microsoft::WRL::ComPtr<IDeviceTopology> topology, adapterTopology;
    Microsoft::WRL::ComPtr<IConnector> connector, connected;
    Microsoft::WRL::ComPtr<IPart> part;
    UINT count{};
    check(SUCCEEDED(device->Activate(__uuidof(IDeviceTopology), CLSCTX_ALL, nullptr, &topology)) &&
          SUCCEEDED(topology->GetConnectorCount(&count)) && count == 1 && SUCCEEDED(topology->GetConnector(0, &connector)) &&
          SUCCEEDED(connector->GetConnectedTo(&connected)) && SUCCEEDED(connected.As(&part)) &&
          SUCCEEDED(part->GetTopologyObject(&adapterTopology)) && SUCCEEDED(adapterTopology->GetDeviceId(&id)) && id,
          "audio_fixture_adapter_topology_unavailable");
    result.topology = id; CoTaskMemFree(id);
    Microsoft::WRL::ComPtr<IMMDevice> adapter;
    check(SUCCEEDED(enumerator->GetDevice(result.topology.c_str(), &adapter)), "audio_fixture_adapter_device_unavailable");
    result.instance = property(adapter.Get(), PKEY_Device_InstanceId);
    return result;
}
static void driver(const std::wstring& instance) {
    check(!instance.empty() && instance.size() <= 4096, "audio_fixture_driver_instance_bound");
    DEVINST node{}; ULONG status{}, problem{};
    check(CM_Locate_DevNodeW(&node, const_cast<wchar_t*>(instance.c_str()), CM_LOCATE_DEVNODE_NORMAL) == CR_SUCCESS &&
          CM_Get_DevNode_Status(&status, &problem, node, 0) == CR_SUCCESS && !problem && (status & DN_STARTED),
          "audio_fixture_driver_instance_not_started");
}
static std::string metadata(const std::wstring& instance, const Endpoint& render, const Endpoint& capture) {
    return "{\"kind\":\"signed_virtual_cable_capture\",\"driver_instance\":" + quote(instance) +
        ",\"render_endpoint_id\":" + quote(render.id) + ",\"capture_endpoint_id\":" + quote(capture.id) +
        ",\"render_topology_id\":" + quote(render.topology) + ",\"capture_topology_id\":" + quote(capture.topology) + "}";
}
static std::string discoverPair(const std::wstring& instance, IMMDeviceEnumerator* enumerator, IMMDevice* defaultRender) {
    const auto render = inspect(enumerator, defaultRender, eRender);
    if (_wcsicmp(render.instance.c_str(), instance.c_str()) != 0)
        throw std::runtime_error("audio_fixture_render_driver_mismatch:actual=" + quote(render.instance));
    Microsoft::WRL::ComPtr<IMMDeviceCollection> devices; UINT count{}, matches{}; Endpoint capture;
    check(SUCCEEDED(enumerator->EnumAudioEndpoints(eCapture, DEVICE_STATE_ACTIVE, &devices)) && SUCCEEDED(devices->GetCount(&count)),
          "audio_fixture_capture_enumeration_unavailable");
    for (UINT index = 0; index < count; ++index) {
        Microsoft::WRL::ComPtr<IMMDevice> candidate;
        check(SUCCEEDED(devices->Item(index, &candidate)), "audio_fixture_capture_identity_unavailable");
        if (property(candidate.Get(), PKEY_Device_FriendlyName).find(L"(VB-Audio Virtual Cable)") == std::wstring::npos) continue;
        auto inspected = inspect(enumerator, candidate.Get(), eCapture);
        if (_wcsicmp(inspected.instance.c_str(), instance.c_str()) == 0) { ++matches; capture = inspected; }
    }
    check(matches == 1, "audio_fixture_sole_driver_capture_required");
    return metadata(instance, render, capture);
}
static std::string discover(const std::wstring& instance) {
    Microsoft::WRL::ComPtr<IMMDeviceEnumerator> enumerator;
    check(SUCCEEDED(CoCreateInstance(__uuidof(MMDeviceEnumerator), nullptr, CLSCTX_ALL, IID_PPV_ARGS(&enumerator))),
          "audio_fixture_enumerator_unavailable");
    // A fresh driver can publish one default before publishing its final default.
    // This is initial discovery only: freeze no receipt until the complete exact
    // driver-bound pair is unchanged for two seconds within one 15-second budget.
    const auto started = GetTickCount64(), deadline = started + 15000;
    ULONGLONG stableSince{}; std::string previous; unsigned candidates{}, observations{}; size_t traceBytes{};
    while (true) {
        driver(instance);
        Microsoft::WRL::ComPtr<IMMDevice> defaultRender;
        const HRESULT result = enumerator->GetDefaultAudioEndpoint(eRender, eConsole, &defaultRender);
        if (result == HRESULT_FROM_WIN32(ERROR_NOT_FOUND)) {
            previous.clear(); stableSince = 0;
        } else {
            if (FAILED(result)) throw std::runtime_error("audio_fixture_default_render_query_failed:hr=" + std::to_string(result));
            const auto pair = discoverPair(instance, enumerator.Get(), defaultRender.Get());
            const auto now = GetTickCount64(); ++observations;
            if (pair != previous) {
                const auto line = "{\"initial_publication_candidate\":" + pair + ",\"elapsed_ms\":" + std::to_string(now - started) + "}\n";
                check(++candidates <= 8 && traceBytes + line.size() <= 32768, "audio_fixture_initial_publication_trace_bound");
                traceBytes += line.size(); std::cerr << line;
                previous = pair; stableSince = now;
            } else if (now - stableSince >= 2000 && now <= deadline) {
                std::cerr << "{\"initial_publication_stable_ms\":" << now - stableSince << ",\"observations\":" << observations << "}\n";
                return pair;
            }
        }
        check(GetTickCount64() < deadline, "audio_fixture_initial_pair_publication_timeout");
        Sleep(50);
    }
}
// Five opaque atoms come only from the verified, frozen fixture receipt. An
// explicit invalid selection refuses; it never falls back to another device.
static Route select(IMMDeviceEnumerator* enumerator, int count = 0, wchar_t** args = nullptr) {
    Route route;
    Microsoft::WRL::ComPtr<IMMDevice> defaultRender;
    check(SUCCEEDED(enumerator->GetDefaultAudioEndpoint(eRender, eConsole, &defaultRender)), "audio_default_render_unavailable");
    if (!count) {
        route.capture = defaultRender; LPWSTR id{};
        check(SUCCEEDED(defaultRender->GetId(&id)) && id, "audio_render_identity_unavailable");
        route.json = "{\"kind\":\"render_loopback\",\"render_endpoint_id\":" + quote(id) + "}";
        CoTaskMemFree(id); return route;
    }
    check(count == 6 && std::wstring(args[0]) == L"--virtual-cable", "audio_fixture_arguments_invalid");
    const std::wstring instance(args[1]); driver(instance);
    const auto render = inspect(enumerator, defaultRender.Get(), eRender);
    binding(render.id, args[2], "render_endpoint");
    binding(render.topology, args[4], "render_topology");
    if (_wcsicmp(render.instance.c_str(), instance.c_str()) != 0)
        throw std::runtime_error("audio_fixture_render_driver_changed:expected=" + quote(instance) + ":actual=" + quote(render.instance));
    Microsoft::WRL::ComPtr<IMMDevice> selected;
    check(SUCCEEDED(enumerator->GetDevice(args[3], &selected)), "audio_fixture_capture_endpoint_missing");
    const auto capture = inspect(enumerator, selected.Get(), eCapture);
    binding(capture.id, args[3], "capture_endpoint");
    binding(capture.topology, args[5], "capture_topology");
    if (_wcsicmp(capture.instance.c_str(), instance.c_str()) != 0)
        throw std::runtime_error("audio_fixture_capture_driver_changed:expected=" + quote(instance) + ":actual=" + quote(capture.instance));
    route.capture = selected; route.flags = 0; route.json = metadata(instance, render, capture); return route;
}
}
