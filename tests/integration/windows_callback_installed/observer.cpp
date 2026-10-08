// TEST-only fixed observer. Never registers a COM class or calls Launcher/Show.
#define NOMINMAX
#include <windows.h>
#include <appxpackaging.h>
#include <wintrust.h>
#include <psapi.h>
#include <softpub.h>
#include <shlwapi.h>
#include <tlhelp32.h>
#include <uiautomation.h>
#include <wtsapi32.h>
#include <wrl.h>
#include <winrt/Windows.Data.Json.h>
#include <winrt/Windows.Data.Xml.Dom.h>
#include <winrt/Windows.Security.Cryptography.h>
#include <winrt/Windows.Storage.Streams.h>
#include <winrt/Windows.Management.Deployment.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.ApplicationModel.h>
#include <winrt/Windows.System.h>
#include <thread>
#include <atomic>
#include <algorithm>
#include <utility>
#include <optional>
#include <iostream>
#include "../../../native/windows-callback/custody.h"
using namespace wcb;
using Microsoft::WRL::ComPtr;
using namespace winrt::Windows::Data::Json;
using namespace winrt::Windows::Management::Deployment;
static fs::path root;
static std::string nonce, source;
static uint64_t end;
static std::string phase = "entry";
// No WinRT activation at initialization. Collected only for this fixed archive
// intake, and exported only if its original strict contract fails.
static std::optional<JsonObject> archiveFacts;
static std::string archiveStep;
// Enabled only by exact --inert-uri, whose inputs are private fixed literals.
static bool inertUriDiagnostics = false;
static std::string inertUriFacts;
static void inertUriNote(const char* key, const std::wstring& value) {
 if (inertUriDiagnostics) inertUriFacts += std::string(key)+"="+narrow(value.substr(0,256))+"\n";
}
static void inertUriAttributes(const char* label, const winrt::Windows::Foundation::Uri& uri) {
 inertUriNote(label,uri.AbsoluteUri().c_str());
 inertUriNote("scheme",uri.SchemeName().c_str()); inertUriNote("host",uri.Host().c_str());
 inertUriNote("query",uri.Query().c_str()); inertUriNote("fragment",uri.Fragment().c_str());
 wchar_t roundtrip[MAX_PATH]{}; DWORD n = MAX_PATH;
 const auto status = PathCreateFromUrlW(uri.AbsoluteUri().c_str(),roundtrip,&n,0);
 inertUriNote("attribute_roundtrip_hresult",std::to_wstring(DWORD(status)));
 if (SUCCEEDED(status)) inertUriNote("attribute_roundtrip",roundtrip);
}
static void archiveAt(const char* step) { archiveStep = step; }
static std::vector<Handle> roots;
static void budget() { require(GetTickCount64() < end); }
static void put(JsonObject& j, const wchar_t* k, const std::string& v) {
 j.Insert(k, JsonValue::CreateStringValue(wide(v)));
}
static void num(JsonObject& j, const wchar_t* k, uint64_t v) {
 j.Insert(k, JsonValue::CreateNumberValue(double(v)));
}
static JsonObject fact() {
 JsonObject j;
 put(j,L"source",source); put(j,L"nonce",nonce);
 num(j,L"boot_ms",GetTickCount64());
 return j;
}
static void publish(const wchar_t* leaf, JsonObject j) {
 const auto value = winrt::to_string(j.Stringify());
 require(value.size() <= 65536);
 Handle file(CreateFileW((root/leaf).c_str(),GENERIC_WRITE,0,nullptr,CREATE_NEW,
  FILE_FLAG_OPEN_REPARSE_POINT|FILE_FLAG_WRITE_THROUGH,nullptr));
 require(file.h != INVALID_HANDLE_VALUE);
 DWORD written = 0;
 require(WriteFile(file.h,value.data(),DWORD(value.size()),&written,nullptr)
  && written == value.size() && FlushFileBuffers(file.h));
 file.closeChecked();
}
static JsonObject read(const wchar_t* leaf) {
 auto file = open(root/leaf,false);
 auto data = bytes(file.h);
 return JsonObject::Parse(wide(std::string(data.begin(),data.end())));
}
static std::wstring string(JsonObject j, const wchar_t* key) {
 return std::wstring(j.GetNamedString(key));
}
static void join(JsonObject j) {
 require(string(j,L"source") == wide(source) && string(j,L"nonce") == wide(nonce));
}
static std::string processSID(HANDLE process, DWORD& integrity) {
 Handle token;
 require(OpenProcessToken(process,TOKEN_QUERY,&token.h));
 auto information = [&](TOKEN_INFORMATION_CLASS type) {
  DWORD n = 0;
  GetTokenInformation(token.h,type,nullptr,0,&n);
  require(n && n <= 65536);
  std::vector<uint8_t> data(n);
  require(GetTokenInformation(token.h,type,data.data(),n,&n));
  return data;
 };
 auto type = information(TokenType);
 require(*reinterpret_cast<TOKEN_TYPE*>(type.data()) == TokenPrimary);
 auto user = information(TokenUser);
 LPWSTR text = nullptr;
 require(ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(user.data())->User.Sid,&text));
 std::string sid = narrow(text);
 LocalFree(text);
 auto level = information(TokenIntegrityLevel);
 auto label = reinterpret_cast<TOKEN_MANDATORY_LABEL*>(level.data())->Label.Sid;
 integrity = *GetSidSubAuthority(label,*GetSidSubAuthorityCount(label)-1);
 return sid;
}
static uint64_t birth(HANDLE h) {
 FILETIME created{}, exited{}, kernel{}, user{};
 require(GetProcessTimes(h,&created,&exited,&kernel,&user));
 return (uint64_t(created.dwHighDateTime)<<32)|created.dwLowDateTime;
}
static std::wstring image(HANDLE h) {
 std::wstring out(32768,L'\0');
 DWORD n = DWORD(out.size());
 require(QueryFullProcessImageNameW(h,0,out.data(),&n) && n && n < out.size());
 out.resize(n);
 return out;
}
static DWORD ownSession() {
 DWORD session = 0;
 require(ProcessIdToSessionId(GetCurrentProcessId(),&session) && session);
 return session;
}
static void interactive() {
 const auto session = ownSession();
 LPWSTR info = nullptr; DWORD size = 0;
 require(WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE,session,WTSConnectState,&info,&size));
 const bool active = size == sizeof(WTS_CONNECTSTATE_CLASS)
  && *reinterpret_cast<WTS_CONNECTSTATE_CLASS*>(info) == WTSActive;
 WTSFreeMemory(info); require(active);
 const auto desktop = OpenInputDesktop(0,FALSE,DESKTOP_READOBJECTS);
 require(desktop != nullptr);
 wchar_t name[128]{}, current[128]{}; DWORD needed = 0;
 const bool same = GetUserObjectInformationW(desktop,UOI_NAME,name,sizeof(name),&needed)
  && GetUserObjectInformationW(GetThreadDesktop(GetCurrentThreadId()),UOI_NAME,current,sizeof(current),&needed)
  && std::wstring(name) == L"Default" && std::wstring(name) == current;
 require(CloseDesktop(desktop) && same);
}
enum class PrerequisiteStage { baseline, postDeployment, postSetup };
static void prerequisites(PrerequisiteStage stage) {
 require(stage == PrerequisiteStage::baseline || stage == PrerequisiteStage::postDeployment || stage == PrerequisiteStage::postSetup);
 const auto leaf = stage == PrerequisiteStage::baseline
  ? L"prerequisites.json" : stage == PrerequisiteStage::postDeployment
   ? L"post-deployment-prerequisites.json" : L"post-setup-prerequisites.json";
 budget(); interactive();
 OSVERSIONINFOEXW version{}; version.dwOSVersionInfoSize = sizeof(version);
 using Version = LONG (WINAPI*)(OSVERSIONINFOW*);
 const auto getter = reinterpret_cast<Version>(GetProcAddress(GetModuleHandleW(L"ntdll.dll"),"RtlGetVersion"));
 require(getter && getter(reinterpret_cast<OSVERSIONINFOW*>(&version)) == 0);
 auto report = fact();
 num(report,L"windows_build",version.dwBuildNumber); num(report,L"windows_product_type",version.wProductType);
 const bool desktopClient = version.wProductType == VER_NT_WORKSTATION && version.dwMajorVersion >= 10 && version.dwBuildNumber >= 22000;
 report.Insert(L"windows11_client",JsonValue::CreateBooleanValue(desktopClient));
 for (const auto key : {L"Software\\Classes\\CLSID",L"Software\\Classes\\AppUserModelId"}) {
  HKEY h = nullptr;
  const auto status = RegOpenKeyExW(HKEY_CURRENT_USER,key,0,KEY_READ,&h);
  if (status == ERROR_SUCCESS) require(RegCloseKey(h) == ERROR_SUCCESS);
  put(report,key,status == ERROR_SUCCESS ? "present" : "refused");
  num(report,(std::wstring(key)+L"_open_status").c_str(),DWORD(status));
 }
 DWORD integrity = 0;
 put(report,L"sid",processSID(GetCurrentProcess(),integrity));
 num(report,L"integrity_rid",integrity); num(report,L"session",ownSession());
 USHORT machine = 0, native = 0;
 require(IsWow64Process2(GetCurrentProcess(),&machine,&native));
 num(report,L"native_machine",native); num(report,L"process_machine",machine);
 publish(leaf,report);
 require(desktopClient); budget();
}
static std::string fileSHA(HANDLE file) {
 LARGE_INTEGER zero{}; require(SetFilePointerEx(file,zero,nullptr,FILE_BEGIN));
 BCRYPT_ALG_HANDLE algorithm = nullptr; BCRYPT_HASH_HANDLE hash = nullptr;
 require(BCryptOpenAlgorithmProvider(&algorithm,BCRYPT_SHA256_ALGORITHM,nullptr,0) == 0);
 struct Close { BCRYPT_ALG_HANDLE a; BCRYPT_HASH_HANDLE& h;
  ~Close() { if (h) BCryptDestroyHash(h); BCryptCloseAlgorithmProvider(a,0); } } close{algorithm,hash};
 require(BCryptCreateHash(algorithm,&hash,nullptr,0,nullptr,0,0) == 0);
 std::vector<uint8_t> buffer(1024*1024); uint64_t total = 0;
 for (;;) {
  DWORD read = 0; budget(); require(ReadFile(file,buffer.data(),DWORD(buffer.size()),&read,nullptr));
  if (!read) break;
  require((total += read) <= 1073741824 && BCryptHashData(hash,buffer.data(),read,0) == 0);
 }
 std::array<uint8_t,32> digest{};
 require(total && BCryptFinishHash(hash,digest.data(),DWORD(digest.size()),0) == 0);
 const char* digits = "0123456789abcdef"; std::string out;
 for (auto byte : digest) { out += digits[byte>>4]; out += digits[byte&15]; }
 return out;
}
static uint32_t codexProtocols(const std::vector<uint8_t>& raw) {
 require(!raw.empty() && raw.size() <= 65536);
 using namespace winrt::Windows::Data::Xml::Dom;
 archiveAt("manifest_xml_settings");
 XmlLoadSettings settings; settings.ProhibitDtd(true); settings.ResolveExternals(false);
 archiveAt("manifest_xml_document");
 XmlDocument document;
 archiveAt("manifest_buffer_create");
 const auto buffer = winrt::Windows::Security::Cryptography::CryptographicBuffer::CreateFromByteArray(
  winrt::array_view<const uint8_t>(raw.data(),raw.data()+raw.size()));
 // GetStream provides XML bytes, not a UTF8 string. Let the documented byte
 // overload interpret the encoding, preserving the exact bounded byte sequence.
 archiveAt("manifest_xml_load");
 document.LoadXmlFromBuffer(buffer,settings);
 archiveAt("manifest_protocol_query");
 return document.SelectNodes(L"//*[local-name()='Protocol' and @Name='codex']").Size();
}
// Breakage: raw UTF16 is wrongly converted as UTF8, or byte loading accepts a
// DTD/external declaration, malformed bytes/document or duplicate codex registration.
// Literal independent documents test the real SDK parser in memory only.
static void xmlContracts() {
 const std::string utf8 = "<?xml version='1.0' encoding='UTF-8'?><Package><Protocol Name='codex'/></Package>";
 const std::string utf16 = "<?xml version='1.0' encoding='UTF-16'?><Package><Protocol Name='codex'/></Package>";
 auto bytes8 = [](const std::string& text) { return std::vector<uint8_t>(text.begin(),text.end()); };
 auto bytes16 = [](const std::string& text, bool bom) {
  std::vector<uint8_t> out;
  if (bom) out = {0xff,0xfe};
  for (unsigned char c : text) { out.push_back(c); out.push_back(0); }
  return out;
 };
 const auto plain8 = bytes8(utf8);
 std::vector<uint8_t> bom8{0xef,0xbb,0xbf}; bom8.insert(bom8.end(),plain8.begin(),plain8.end());
 const auto plain16 = bytes16(utf16,false), bom16 = bytes16(utf16,true);
 for (const auto& valid : {plain8,bom8,plain16,bom16}) require(codexProtocols(valid) == 1);
 // Breakage: permissive UTF8 replacement preserves the codex count and hides
 // a corrupt byte in unrelated text. Only an actual parser error passes.
 auto invalid8 = bytes8("<?xml version='1.0' encoding='UTF-8'?><Package><Ignored>");
 invalid8.push_back(0xff);
 const auto suffix = bytes8("</Ignored><Protocol Name='codex'/></Package>");
 invalid8.insert(invalid8.end(),suffix.begin(),suffix.end());
 bool encodingRejected = false;
 try { codexProtocols(invalid8); }
 catch (const winrt::hresult_error&) { encodingRejected = true; }
 require(encodingRejected);
 for (const auto& invalid : {
   "<Package><Protocol Name='codex'></Package>",
   "<!DOCTYPE Package [<!ENTITY id 'codex'>]><Package><Protocol Name='&id;'/></Package>",
   "<!DOCTYPE Package SYSTEM 'https://TEST.invalid/blocked.dtd'><Package><Protocol Name='codex'/></Package>",
   "<Package><Protocol Name='codex'/><Protocol Name='codex'/></Package>"}) {
  bool rejected = false;
  try { require(codexProtocols(bytes8(invalid)) == 1); }
  catch (const winrt::hresult_error&) { rejected = true; }
  catch (const std::runtime_error&) { rejected = true; }
  require(rejected);
 }
 // Demonstrate the old boundary's failure on a plausible raw XML encoding.
 // This is not a claim that the downloaded vendor manifest uses this encoding.
 bool oldRejected = false;
 try {
  using namespace winrt::Windows::Data::Xml::Dom;
  XmlLoadSettings settings; settings.ProhibitDtd(true); settings.ResolveExternals(false);
  XmlDocument old;
  old.LoadXml(wide(std::string(bom16.begin(),bom16.end())),settings);
 } catch (const winrt::hresult_error&) { oldRejected = true; }
 require(oldRejected);
 std::cout << "TEST byte XML contracts passed; legacy UTF16 string-load rejected; no native target effects\n";
}
struct Archive {
 Handle file;
 ComPtr<IAppxManifestPackageId> id;
 std::wstring full;
 std::string digest;
 Archive():file(open(root/L"client.msix",false)) {
  archiveAt("archive_size");
  LARGE_INTEGER size{};
  require(GetFileSizeEx(file.h,&size) && size.QuadPart > 0 && size.QuadPart <= 1073741824);
  archiveAt("archive_sha256");
  digest = fileSHA(file.h); put(*archiveFacts,L"archive_sha",digest);
  // System APPX SIP/catalog trust, not a user-provided hash or TEST certificate.
  WINTRUST_FILE_INFO info{sizeof(info)};
  const auto path = (root/L"client.msix").wstring();
  info.pcwszFilePath = path.c_str(); info.hFile = file.h;
  WINTRUST_DATA trust{sizeof(trust)};
  trust.dwUIChoice = WTD_UI_NONE; trust.fdwRevocationChecks = WTD_REVOKE_WHOLECHAIN;
  trust.dwUnionChoice = WTD_CHOICE_FILE; trust.pFile = &info;
  trust.dwStateAction = WTD_STATEACTION_VERIFY;
  GUID action = WINTRUST_ACTION_GENERIC_VERIFY_V2;
  archiveAt("winverifytrust");
  budget(); const auto result = WinVerifyTrust(nullptr,&action,&trust);
  auto signature = fact(); put(signature,L"archive_sha",digest);
  num(signature,L"winverifytrust_result",DWORD(result));
  if (result == ERROR_SUCCESS) {
   archiveAt("signature_provider");
   auto provider = WTHelperProvDataFromStateData(trust.hWVTStateData);
   require(provider != nullptr);
   archiveAt("signature_signer");
   auto signer = WTHelperGetProvSignerFromChain(provider,0,FALSE,0);
   require(signer && signer->csCertChain && signer->csCertChain <= 32 && signer->pasCertChain[0].pCert);
   const auto cert = signer->pasCertChain[0].pCert;
   archiveAt("signature_certificate_bound");
   require(cert->cbCertEncoded > 0 && cert->cbCertEncoded <= 65536);
   put(signature,L"signer_cert_sha",sha(std::vector<uint8_t>(cert->pbCertEncoded,cert->pbCertEncoded+cert->cbCertEncoded)));
   wchar_t subject[1024]{};
   archiveAt("signature_subject");
   const auto size = CertGetNameStringW(cert,CERT_NAME_SIMPLE_DISPLAY_TYPE,0,nullptr,subject,1024);
   require(size > 1 && size < 1024); put(signature,L"signer_display_name",narrow(subject));
   num(signature,L"chain_cert_count",signer->csCertChain); num(signature,L"counter_signers",signer->csCounterSigners);
  }
  trust.dwStateAction = WTD_STATEACTION_CLOSE;
  archiveAt("signature_state_close");
  const auto closed = WinVerifyTrust(nullptr,&action,&trust);
  num(signature,L"trust_state_close_result",DWORD(closed));
  archiveAt("signature_publication");
  publish(phase == "deploy" ? L"deployment-signature.json" : L"signature.json",signature);
  archiveAt("signature_result_and_original_budget");
  require(result == ERROR_SUCCESS && closed == ERROR_SUCCESS); budget();
  ComPtr<IStream> stream;
  archiveAt("archive_stream");
  winrt::check_hresult(SHCreateStreamOnFileEx(path.c_str(),STGM_READ|STGM_SHARE_DENY_WRITE,
   FILE_ATTRIBUTE_NORMAL,FALSE,nullptr,&stream));
  ComPtr<IAppxFactory> factory;
  archiveAt("appx_factory");
  winrt::check_hresult(CoCreateInstance(CLSID_AppxFactory,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&factory)));
  ComPtr<IAppxPackageReader> package;
  archiveAt("appx_package_reader");
  winrt::check_hresult(factory->CreatePackageReader(stream.Get(),&package));
  ComPtr<IAppxManifestReader> manifest;
  archiveAt("appx_manifest");
  winrt::check_hresult(package->GetManifest(&manifest));
  archiveAt("manifest_package_id");
  winrt::check_hresult(manifest->GetPackageId(&id));
  auto value = [&](const wchar_t* key, auto getter) {
   archiveAt(narrow(key).c_str());
   LPWSTR p = nullptr; winrt::check_hresult((id.Get()->*getter)(&p));
   require(p != nullptr); std::wstring out(p); CoTaskMemFree(p);
   require(!out.empty() && out.size() <= 256); put(*archiveFacts,key,narrow(out)); return out;
  };
  const auto name = value(L"name",&IAppxManifestPackageId::GetName);
  const auto publisher = value(L"publisher",&IAppxManifestPackageId::GetPublisher);
  const auto family = value(L"family",&IAppxManifestPackageId::GetPackageFamilyName);
  full = value(L"full_name",&IAppxManifestPackageId::GetPackageFullName);
  APPX_PACKAGE_ARCHITECTURE arch{};
  archiveAt("manifest_architecture");
  winrt::check_hresult(id->GetArchitecture(&arch)); num(*archiveFacts,L"architecture",arch);
  LPWSTR resource = nullptr;
  archiveAt("manifest_resource_id");
  winrt::check_hresult(id->GetResourceId(&resource));
  const bool empty = !resource || !*resource; CoTaskMemFree(resource);
  archiveFacts->Insert(L"resource_absent",JsonValue::CreateBooleanValue(empty));
  // This slice does not install frameworks. A dependency is an explicit prerequisite refusal.
  ComPtr<IAppxManifestPackageDependenciesEnumerator> dependencies;
  archiveAt("manifest_dependencies");
  winrt::check_hresult(manifest->GetPackageDependencies(&dependencies));
  archiveAt("manifest_dependencies_current");
  BOOL any = FALSE; winrt::check_hresult(dependencies->GetHasCurrent(&any));
  archiveFacts->Insert(L"dependencies_present",JsonValue::CreateBooleanValue(any != FALSE));
  archiveAt("manifest_xml_stream");
  ComPtr<IStream> xmlStream; winrt::check_hresult(manifest->GetStream(&xmlStream));
  std::vector<uint8_t> xmlBytes(65537); ULONG xmlSize = 0;
  archiveAt("manifest_xml_read");
  winrt::check_hresult(xmlStream->Read(xmlBytes.data(),ULONG(xmlBytes.size()),&xmlSize));
  num(*archiveFacts,L"manifest_bytes_read",xmlSize);
  archiveAt("manifest_xml_byte_bound");
  require(xmlSize && xmlSize <= 65536);
  xmlBytes.resize(xmlSize);
  put(*archiveFacts,L"manifest_sha",sha(xmlBytes));
  const char* digits = "0123456789abcdef"; std::string prefix;
  for (size_t i = 0; i < std::min<size_t>(4,xmlBytes.size()); ++i) {
   prefix += digits[xmlBytes[i]>>4]; prefix += digits[xmlBytes[i]&15];
  }
  put(*archiveFacts,L"manifest_first4_hex",prefix);
  const auto protocolCount = codexProtocols(xmlBytes);
  num(*archiveFacts,L"codex_protocol_count",protocolCount);
  auto intake = fact(); put(intake,L"archive_sha",digest); put(intake,L"name",narrow(name));
  put(intake,L"manifest_sha",sha(xmlBytes)); put(intake,L"manifest_first4_hex",prefix);
  put(intake,L"publisher",narrow(publisher)); put(intake,L"family",narrow(family));
  put(intake,L"full_name",narrow(full)); num(intake,L"architecture",arch);
  intake.Insert(L"resource_absent",JsonValue::CreateBooleanValue(empty));
  intake.Insert(L"dependencies_present",JsonValue::CreateBooleanValue(any != FALSE));
  num(intake,L"codex_protocol_count",protocolCount); put(intake,L"signature","system_trust");
  const auto leaf = phase == "deploy" ? L"deployment-intake.json" : L"intake.json";
  archiveAt("intake_publication");
  publish(leaf,intake);
  archiveAt("strict_identity_architecture_resource_dependency_protocol_contract");
  require(name == L"OpenAI.Codex" && publisher == L"CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B"
   && family == L"OpenAI.Codex_2p2nqsd0c76g0" && arch == APPX_PACKAGE_ARCHITECTURE_X64
   && empty && !any && protocolCount == 1);
  archiveAt("original_archive_budget"); budget();
 }
};
static void packages(const std::wstring& expected, bool absent) {
 PackageManager manager;
 budget(); unsigned count = 0;
 for (const auto& package : manager.FindPackagesForUser(L"",L"OpenAI.Codex_2p2nqsd0c76g0")) {
  budget(); require(++count <= 8 && !absent);
  const auto id = package.Id();
  require(id.Name() == L"OpenAI.Codex" && id.Publisher() == L"CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B"
   && id.FamilyName() == L"OpenAI.Codex_2p2nqsd0c76g0" && id.FullName() == expected
   && id.Architecture() == winrt::Windows::System::ProcessorArchitecture::X64 && !package.IsResourcePackage());
 }
 require(count == (absent ? 0U : 1U)); budget();
}
// Only decoded ordinary DOS output accepts '/' as a Windows separator.
// Never use this value to open a file or normalize the canonical held path.
static std::wstring dosSeparators(std::wstring decoded) {
 std::replace(decoded.begin(),decoded.end(),L'/',L'\\');
 return decoded;
}
// The physical file/directory handles stay canonical and held. Only the URI
// adapter strips the documented extended DOS prefix, never UNC/device aliases.
static winrt::Windows::Foundation::Uri deploymentUri(const std::wstring& canonical) {
 archiveAt("deployment_uri_drive_path_contract");
 require(rootGrammar(narrow(canonical)) && canonical.size()-4 < MAX_PATH);
 const auto dos = canonical.substr(4);
 wchar_t url[32768]{}; DWORD n = 32768;
 archiveAt("deployment_uri_conversion");
 winrt::check_hresult(UrlCreateFromPathW(dos.c_str(),url,&n,0));
 archiveAt("deployment_winrt_uri_constructor");
 const winrt::Windows::Foundation::Uri target(url);
 archiveAt("deployment_file_uri_attributes");
 if (inertUriDiagnostics) inertUriAttributes("new_absolute_uri",target);
 archiveAt("deployment_file_uri_roundtrip");
 require(target.SchemeName() == L"file" && target.Host().empty()
  && target.Query().empty() && target.Fragment().empty());
 wchar_t roundtrip[MAX_PATH]{}; n = MAX_PATH;
 winrt::check_hresult(PathCreateFromUrlW(target.AbsoluteUri().c_str(),roundtrip,&n,0));
 inertUriNote("new_roundtrip",roundtrip); inertUriNote("expected_dos",dos);
 archiveAt("deployment_file_uri_exact_roundtrip_contract");
 require(dosSeparators(roundtrip) == dos);
 return target;
}
// Breakage: the actual URL/WinRT boundary refuses extended DOS paths, or loses
// literal spaces/#/%/Unicode when mapping the held local path to a file URI.
static void uriContracts() {
 bool oldRejected = false;
 const char* oldStage = "UrlCreateFromPathW";
 try {
  wchar_t oldUrl[32768]{}; DWORD n = 32768;
  archiveAt("inert_old_url_conversion");
  inertUriNote("old_raw_path",L"\\\\?\\C:\\TEST-installed\\client.msix");
  const auto status = UrlCreateFromPathW(L"\\\\?\\C:\\TEST-installed\\client.msix",oldUrl,&n,0);
  inertUriNote("old_url_hresult",std::to_wstring(DWORD(status)));
  winrt::check_hresult(status); inertUriNote("old_url",oldUrl);
  oldStage = "WinRTUri"; archiveAt("inert_old_winrt_uri_constructor");
  const winrt::Windows::Foundation::Uri old(oldUrl);
  inertUriNote("old_chain",L"accepted");
  archiveAt("inert_old_uri_attributes"); inertUriAttributes("old_absolute_uri",old);
 } catch (const winrt::hresult_error& error) {
  const bool chainStage = archiveStep == "inert_old_url_conversion"
   || archiveStep == "inert_old_winrt_uri_constructor";
  inertUriNote("old_probe_hresult",std::to_wstring(DWORD(error.code().value)));
  inertUriNote("old_probe_failure_stage",wide(archiveStep));
  oldRejected = chainStage && error.code().value == E_INVALIDARG;
 }
 const std::wstring canonical = L"\\\\?\\C:\\TEST installed # %20 \u03a9\u4e2d\\client.msix";
 inertUriNote("new_raw_path",canonical);
 const auto target = deploymentUri(canonical);
 wchar_t roundtrip[MAX_PATH]{}; DWORD n = MAX_PATH;
 winrt::check_hresult(PathCreateFromUrlW(target.AbsoluteUri().c_str(),roundtrip,&n,0));
 require(dosSeparators(roundtrip) == L"C:\\TEST installed # %20 \u03a9\u4e2d\\client.msix");
 // Breakage: separator conversion also decodes percent text, folds case,
 // resolves dot segments, changes drives, or merges independently named paths.
 auto decoded = [](const winrt::Windows::Foundation::Uri& uri) {
  wchar_t path[MAX_PATH]{}; DWORD n = MAX_PATH;
  winrt::check_hresult(PathCreateFromUrlW(uri.AbsoluteUri().c_str(),path,&n,0));
  return dosSeparators(path);
 };
 for (const auto& pair : std::array<std::pair<const wchar_t*,const wchar_t*>,3>{{
   {L"\\\\?\\C:\\TEST%20value\\client.msix",L"\\\\?\\C:\\TEST value\\client.msix"},
   {L"\\\\?\\C:\\TEST%23value\\client.msix",L"\\\\?\\C:\\TEST#value\\client.msix"},
   {L"\\\\?\\C:\\TEST%2Fvalue\\client.msix",L"\\\\?\\C:\\TEST\\value\\client.msix"}}}) {
  const auto left = deploymentUri(pair.first), right = deploymentUri(pair.second);
  require(left.AbsoluteUri() != right.AbsoluteUri() && decoded(left) != decoded(right));
 }
 const std::wstring expected = L"C:\\TEST\\client.msix";
 for (const auto changed : {L"D:/TEST/client.msix",L"C:/test/client.msix",L"C:/TEST/./client.msix",L"C:/OTHER/client.msix"})
  require(dosSeparators(changed) != expected);
 for (const auto invalid : {L"\\\\?\\UNC\\server\\share\\client.msix",L"\\\\.\\C:\\client.msix",L"C:\\client.msix"}) {
  bool rejected = false;
  try { deploymentUri(invalid); }
  catch (const std::runtime_error&) { rejected = true; }
  require(rejected);
 }
 // Preserve the old assertion, after collecting the new literal observations.
 archiveAt("inert_old_E_INVALIDARG_contract"); require(oldRejected);
 std::cout << "TEST file URI contracts passed; legacy extended DOS rejected E_INVALIDARG at "
  << oldStage << "; no target effects\n";
}
static void archive(bool deploy) {
 archiveFacts = fact(); num(*archiveFacts,L"original_deadline_boot_ms",end);
 archiveAt("open_archive");
 Archive held;
 archiveAt("current_user_family_absence"); packages(held.full,true);
 auto report = fact(); put(report,L"full_name",narrow(held.full));
 put(report,L"family","OpenAI.Codex_2p2nqsd0c76g0"); put(report,L"signature","system_trust"); put(report,L"archive_sha",held.digest);
 if (!deploy) { archiveAt("accepted_archive_publication"); publish(L"archive.json",report); return; }
 archiveAt("deployment_intake_join");
 auto frozen = read(L"archive.json"); join(frozen);
 require(string(frozen,L"full_name") == held.full && string(frozen,L"archive_sha") == wide(held.digest));
 archiveAt("archive_intake_join_complete"); budget();
 archiveAt("deployment_archive_physical_path");
 const auto target = deploymentUri(physical(held.file.h));
 archiveAt("deployment_package_manager_constructor"); budget();
 PackageManager manager;
 archiveAt("deployment_intent_publication"); budget();
 publish(L"deployment.intent.json",report); budget();
 auto operation = manager.AddPackageAsync(target,nullptr,DeploymentOptions::None);
 using winrt::Windows::Foundation::AsyncStatus;
 while (operation.Status() == AsyncStatus::Started) { budget(); Sleep(10); }
 require(operation.Status() == AsyncStatus::Completed);
 winrt::check_hresult(operation.ErrorCode());
 auto result = operation.GetResults(); winrt::check_hresult(result.ExtendedErrorCode()); budget();
 packages(held.full,false);
 put(report,L"outcome","installed_exact"); publish(L"deployment.json",report);
}
struct Route {
 fs::path path;
 Snapshot snapshot;
 Record record;
 std::string snapshotSHA;
 std::vector<Handle> files;
 Route() {
  auto request = read(L"route.json"); join(request);
  const auto p = fs::path(string(request,L"snapshot_path"));
  path = p.parent_path();
  require(p.filename() == L"generation.wne" && rootGrammar(narrow(path.wstring())));
  auto ancestor = path.root_path();
  files.push_back(open(ancestor,true));
  for (const auto& component : path.relative_path()) {
   ancestor /= component; files.push_back(open(ancestor,true));
  }
  files.push_back(open(p,false));
  auto data = bytes(files.back().h);
  snapshotSHA = sha(data); snapshot = Snapshot::read(data);
  require(snapshotSHA == narrow(string(request,L"snapshot_sha")) && snapshot.root == narrow(physical(files[files.size()-2].h))
   && snapshot.sid == ownerSID());
  files.push_back(open(path/L"helper.exe",false));
  require(sha(bytes(files.back().h,16*1024*1024)) == snapshot.digest);
  const auto reference = narrow(string(request,L"reference")); require(hex(reference,32));
  files.push_back(open(path/L"records",true));
  files.push_back(open(path/L"attempts",true));
  files.push_back(open(path/L"records"/(wide(reference)+L".wne"),false));
  record = Record::read(bytes(files.back().h)); record.bind(snapshot,snapshotSHA);
  require(record.reference == reference && record.thread == narrow(string(request,L"thread")));
  for (size_t i = path.relative_path().empty() ? 0 : 1; i < files.size(); ++i) {
   // Only generation objects, not ordinary ancestors, have this strict DACL.
   if (physical(files[i].h).rfind(path.wstring(),0) == 0) owned(files[i].h,snapshot.sid);
  }
  packages(wide(snapshot.full),false); budget();
 }
};
// Only this positive, held kernel-image classification can exclude a provider.
// Opening/querying a provider is not optional: unknown must reject the census.
struct KnownForeignShell final {};
static bool shellImage(const std::wstring& path) {
 wchar_t windows[32768]{};
 const auto n = GetWindowsDirectoryW(windows,32768); require(n && n < 32768);
 const std::wstring base(windows,n);
 // Same concrete locations as the independently reviewed Shell census. This
 // narrows the previous directory-prefix/basename test; no aliases are added.
 const std::array<std::wstring,4> allowed{
  base+L"\\explorer.exe", base+L"\\System32\\ShellHost.exe",
  base+L"\\SystemApps\\ShellExperienceHost_cw5n1h2txyewy\\ShellExperienceHost.exe",
  base+L"\\SystemApps\\MicrosoftWindows.Client.CBS_cw5n1h2txyewy\\ShellHost.exe"
 };
 for (const auto& candidate : allowed) if (_wcsicmp(path.c_str(),candidate.c_str()) == 0) return true;
 return false;
}
static void shellSignature(HANDLE file, const std::wstring& path) {
 WINTRUST_FILE_INFO info{sizeof(info)}; info.pcwszFilePath = path.c_str(); info.hFile = file;
 WINTRUST_DATA trust{sizeof(trust)};
 trust.dwUIChoice = WTD_UI_NONE; trust.fdwRevocationChecks = WTD_REVOKE_WHOLECHAIN;
 trust.dwUnionChoice = WTD_CHOICE_FILE; trust.pFile = &info;
 trust.dwStateAction = WTD_STATEACTION_VERIFY;
 GUID action = WINTRUST_ACTION_GENERIC_VERIFY_V2;
 budget(); const auto verified = WinVerifyTrust(nullptr,&action,&trust);
 trust.dwStateAction = WTD_STATEACTION_CLOSE;
 const auto closed = WinVerifyTrust(nullptr,&action,&trust);
 require(verified == ERROR_SUCCESS && closed == ERROR_SUCCESS); budget();
}
struct Process {
 Handle handle, shellFile;
 std::vector<Handle> shellParents;
 std::wstring path;
 uint64_t born;
 DWORD pid, integrity = 0;
 Process(DWORD id, bool shell, const Route* route):
  handle(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE,FALSE,id)),pid(id) {
  require(handle.h != nullptr && handle.h != INVALID_HANDLE_VALUE);
  path = image(handle.h);
  require(GetProcessId(handle.h) == id && WaitForSingleObject(handle.h,0) == WAIT_TIMEOUT);
  // Read only the held process/kernel image before excluding known foreign
  // roots. Never inspect foreign paths, signatures, tokens or other resources.
  if (shell && !shellImage(path)) throw KnownForeignShell{};
  born = birth(handle.h);
  DWORD session = 0;
  require(ProcessIdToSessionId(pid,&session) && session == ownSession()
   && processSID(handle.h,integrity) == ownerSID());
  if (shell) {
   const auto admitted = fs::path(L"\\\\?\\"+path);
   require(rootGrammar(narrow(admitted.wstring())));
   auto parent = admitted.root_path(); shellParents.push_back(open(parent,true));
   for (const auto& part : admitted.parent_path().relative_path()) {
    budget(); parent /= part; shellParents.push_back(open(parent,true));
   }
   shellFile = Handle(CreateFileW(admitted.c_str(),GENERIC_READ|READ_CONTROL,FILE_SHARE_READ,nullptr,
    OPEN_EXISTING,FILE_FLAG_OPEN_REPARSE_POINT,nullptr));
   require(shellFile.h != INVALID_HANDLE_VALUE);
   BY_HANDLE_FILE_INFORMATION information{};
   // Signed Windows system files may have legitimate servicing hard links.
   require(GetFileInformationByHandle(shellFile.h,&information)
    && !(information.dwFileAttributes&(FILE_ATTRIBUTE_REPARSE_POINT|FILE_ATTRIBUTE_DIRECTORY))
    && information.nNumberOfLinks > 0);
   require(_wcsicmp(physical(shellFile.h).c_str(),admitted.c_str()) == 0);
   shellSignature(shellFile.h,admitted.wstring());
  } else {
   require(route != nullptr && L"\\\\?\\"+path == (route->path/L"helper.exe").wstring());
   auto file = open(path,false);
   require(physical(file.h) == (route->path/L"helper.exe").wstring()
    && sha(bytes(file.h,16*1024*1024)) == route->snapshot.digest);
  }
  live();
 }
 void live() {
  require(WaitForSingleObject(handle.h,0) == WAIT_TIMEOUT && birth(handle.h) == born);
  if (shellFile.h != INVALID_HANDLE_VALUE)
   require(_wcsicmp(physical(shellFile.h).c_str(),(L"\\\\?\\"+path).c_str()) == 0);
 }
};
static std::vector<DWORD> helpers(const Route& route) {
 std::vector<DWORD> matches;
 Handle census(CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS,0));
 require(census.h != INVALID_HANDLE_VALUE);
 PROCESSENTRY32W item{sizeof(item)};
 require(Process32FirstW(census.h,&item)); unsigned count = 0;
 do {
  require(++count <= 8192);
  if (_wcsicmp(item.szExeFile,L"helper.exe") != 0) continue;
  Handle h(OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION|SYNCHRONIZE,FALSE,item.th32ProcessID));
  require(h.h != INVALID_HANDLE_VALUE);
  const auto candidate = image(h.h);
  // This comparison admits only our helper image; no foreign leaf is opened.
  if (L"\\\\?\\"+candidate == (route.path/L"helper.exe").wstring()) matches.push_back(item.th32ProcessID);
 } while (Process32NextW(census.h,&item));
 require(GetLastError() == ERROR_NO_MORE_FILES && matches.size() <= 1);
 return matches;
}
static std::wstring name(IUIAutomationElement* element) {
 BSTR value = nullptr; winrt::check_hresult(element->get_CurrentName(&value));
 require(!value || SysStringLen(value) <= 4096);
 std::wstring result(value ? value : L""); SysFreeString(value); return result;
}
struct Selection { ComPtr<IUIAutomationElement> row, title, body; DWORD pid = 0; };
static Selection census(IUIAutomation* automation, IUIAutomationTreeWalker* walker,
 const std::wstring& title, const std::wstring& body) {
 Selection found;
 ComPtr<IUIAutomationElement> desktop, first;
 budget(); winrt::check_hresult(automation->GetRootElement(&desktop));
 budget(); winrt::check_hresult(walker->GetFirstChildElement(desktop.Get(),&first));
 unsigned rootsSeen = 0, nodes = 0, titles = 0;
 while (first) {
  budget(); require(++rootsSeen <= 128);
  int pid = 0; winrt::check_hresult(first->get_CurrentProcessId(&pid));
  std::unique_ptr<Process> owner;
  require(pid > 0);
  try { owner = std::make_unique<Process>(DWORD(pid),true,nullptr); }
  catch (const KnownForeignShell&) {
   // A positively classified non-Shell kernel image is the only exclusion.
   // Every allowlisted/unknown failure escapes and invalidates this census.
  }
  if (owner) {
   struct Node { ComPtr<IUIAutomationElement> element; unsigned depth; };
   std::vector<Node> pending{{first,0}};
   while (!pending.empty()) {
    auto node = std::move(pending.back()); pending.pop_back();
    budget(); owner->live(); require(++nodes <= 2048 && node.depth <= 24);
    int provider = 0; winrt::check_hresult(node.element->get_CurrentProcessId(&provider)); require(provider == pid);
    const auto literal = name(node.element.Get());
    if (literal == body) { require(!found.body); found.body = node.element; }
    if (literal == title) {
     require(++titles == 1); found.title = node.element; found.pid = DWORD(pid);
     auto parent = node.element;
     for (unsigned depth = 0; depth < 8; ++depth) {
      ComPtr<IUIAutomationElement> next;
      budget(); winrt::check_hresult(walker->GetParentElement(parent.Get(),&next)); require(next != nullptr);
      winrt::check_hresult(next->get_CurrentProcessId(&provider)); require(provider == pid);
      CONTROLTYPEID type = 0; winrt::check_hresult(next->get_CurrentControlType(&type)); parent = next;
      if (type == UIA_ListItemControlTypeId) { found.row = next; break; }
     }
     require(found.row != nullptr);
    }
    ComPtr<IUIAutomationElement> child;
    budget(); winrt::check_hresult(walker->GetFirstChildElement(node.element.Get(),&child));
    while (child) {
     budget(); require(nodes+pending.size() < 2048); pending.push_back({child,node.depth+1});
     ComPtr<IUIAutomationElement> sibling;
     winrt::check_hresult(walker->GetNextSiblingElement(child.Get(),&sibling)); child = sibling;
    }
   }
  }
  ComPtr<IUIAutomationElement> next;
  budget(); winrt::check_hresult(walker->GetNextSiblingElement(first.Get(),&next)); first = next;
 }
 if (found.row) {
  require(found.body != nullptr);
  auto parent = found.body; bool sameRow = false;
  for (unsigned depth = 0; depth < 8; ++depth) {
   ComPtr<IUIAutomationElement> next;
   budget(); winrt::check_hresult(walker->GetParentElement(parent.Get(),&next)); require(next.Get() != nullptr);
   int provider = 0; winrt::check_hresult(next->get_CurrentProcessId(&provider)); require(DWORD(provider) == found.pid);
   BOOL same = FALSE;
   winrt::check_hresult(automation->CompareElements(next.Get(),found.row.Get(),&same));
   if (same) { sameRow = true; break; }
   parent = next;
  }
  require(sameRow);
  for (const auto element : {found.row.Get(),found.title.Get(),found.body.Get()}) {
   int provider = 0; BOOL enabled = FALSE, offscreen = TRUE;
   budget(); winrt::check_hresult(element->get_CurrentProcessId(&provider)); require(DWORD(provider) == found.pid);
   budget(); winrt::check_hresult(element->get_CurrentIsEnabled(&enabled));
   budget(); winrt::check_hresult(element->get_CurrentIsOffscreen(&offscreen)); require(enabled && !offscreen);
  }
 }
 budget(); return found;
}
// Independent observation is armed before the synchronous UIA call. It never
// controls the callback process or its SDK operation/lease.
struct ReceiverCollector {
 const Route& route;
 const uint64_t captureEnd;
 std::atomic<bool> armed{false}, entered{false}, stop{false}, failed{false};
 std::atomic<uint64_t> clickBoot{0}, clickBirth{0};
 std::exception_ptr failure;
 std::thread worker;
 explicit ReceiverCollector(const Route& r):route(r),captureEnd(end),worker([this] { observe(); }) {}
 ~ReceiverCollector() { stop.store(true); if (worker.joinable()) worker.join(); }
 void observe() noexcept {
  try {
   winrt::init_apartment(winrt::apartment_type::multi_threaded);
   require(helpers(route).empty()); armed.store(true);
   while (!entered.load()) {
    if (stop.load()) return;
    require(GetTickCount64() < captureEnd); Sleep(1);
   }
   std::unique_ptr<Process> receiver;
   while (!receiver) {
    require(GetTickCount64() < captureEnd && !stop.load());
    auto ids = helpers(route);
    if (!ids.empty()) receiver = std::make_unique<Process>(ids[0],false,&route); else Sleep(1);
   }
   require(receiver->born >= clickBirth.load());
   auto proof = fact(); put(proof,L"snapshot_sha",route.snapshotSHA);
   put(proof,L"reference",route.record.reference); put(proof,L"generation",route.snapshot.generation);
   put(proof,L"selected_full_name",route.snapshot.full); num(proof,L"click_boot_ms",clickBoot.load());
   USHORT machine = 0, native = 0;
   require(IsWow64Process2(receiver->handle.h,&machine,&native));
   require(machine == IMAGE_FILE_MACHINE_AMD64 || (machine == IMAGE_FILE_MACHINE_UNKNOWN && native == IMAGE_FILE_MACHINE_AMD64));
   DWORD handles = 0; require(GetProcessHandleCount(receiver->handle.h,&handles));
   PROCESS_MEMORY_COUNTERS memory{sizeof(memory)};
   require(GetProcessMemoryInfo(receiver->handle.h,&memory,sizeof(memory)));
   num(proof,L"receiver_handle_count",handles); num(proof,L"receiver_peak_working_set",memory.PeakWorkingSetSize);
   put(proof,L"receiver_token_type","primary");
   num(proof,L"receiver_pid",receiver->pid); put(proof,L"receiver_birth",std::to_string(receiver->born));
   num(proof,L"receiver_integrity_rid",receiver->integrity); num(proof,L"receiver_session",ownSession());
   num(proof,L"receiver_process_machine",machine); num(proof,L"receiver_native_machine",native);
   put(proof,L"receiver_sid",ownerSID()); put(proof,L"receiver_image",narrow(receiver->path));
   publish(L"receiver-held.json",proof);
   const auto collectionEnd = clickBoot.load()+70000;
   const auto now = GetTickCount64();
   const auto wait = WaitForSingleObject(receiver->handle.h,DWORD(collectionEnd > now ? collectionEnd-now : 0));
   require(wait == WAIT_OBJECT_0 && GetTickCount64() < collectionEnd);
   DWORD code = 0; require(GetExitCodeProcess(receiver->handle.h,&code) && code == 0);
   require(birth(receiver->handle.h) == receiver->born);
   num(proof,L"receiver_exit_code",code); num(proof,L"receiver_collected_boot_ms",GetTickCount64());
   proof.Insert(L"collected",JsonValue::CreateBooleanValue(true)); publish(L"receiver.json",proof);
  } catch (...) {
   failure = std::current_exception(); failed.store(true); armed.store(true);
  }
 }
 void ready() {
  while (!armed.load()) { budget(); Sleep(1); }
  require(!failed.load());
 }
 void admit() {
  FILETIME time{}; GetSystemTimeAsFileTime(&time);
  clickBirth.store((uint64_t(time.dwHighDateTime)<<32)|time.dwLowDateTime);
  clickBoot.store(GetTickCount64()); require(!failed.load()); budget(); entered.store(true);
 }
 void collect() {
  require(worker.joinable()); worker.join();
  if (failure) std::rethrow_exception(failure);
 }
};
static void sameSelection(IUIAutomation* automation, const Selection& old, const Selection& fresh) {
 require(fresh.row && fresh.title && fresh.body && fresh.pid == old.pid);
 for (const auto pair : {std::pair{old.row.Get(),fresh.row.Get()},std::pair{old.title.Get(),fresh.title.Get()},
      std::pair{old.body.Get(),fresh.body.Get()}}) {
  BOOL same = FALSE; budget(); winrt::check_hresult(automation->CompareElements(pair.first,pair.second,&same)); require(same);
 }
}
static void click() {
 Route route;
 auto sender = read(L"sender.json"); join(sender);
 require(sender.GetNamedBoolean(L"collected") && sender.GetNamedNumber(L"code") == 0);
 const auto request = read(L"route.json");
 const auto title = string(request,L"title"), body = string(request,L"body");
 require(title == L"Navigation TEST "+wide(nonce) && body == L"Cold callback TEST "+wide(nonce));
 require(helpers(route).empty()); interactive();
 ComPtr<IUIAutomation> automation;
 budget(); winrt::check_hresult(CoCreateInstance(CLSID_CUIAutomation,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&automation)));
 ComPtr<IUIAutomationTreeWalker> walker;
 winrt::check_hresult(automation->get_RawViewWalker(&walker));
 Selection selected;
 while (!selected.row) { budget(); selected = census(automation.Get(),walker.Get(),title,body); if (!selected.row) Sleep(50); }
 Process shell(selected.pid,true,nullptr);
 auto proof = fact(); put(proof,L"snapshot_sha",route.snapshotSHA);
 put(proof,L"reference",route.record.reference); put(proof,L"generation",route.snapshot.generation);
 num(proof,L"shell_pid",shell.pid); put(proof,L"shell_birth",std::to_string(shell.born));
 num(proof,L"shell_integrity_rid",shell.integrity);
 publish(L"click.intent.json",proof);
 ReceiverCollector collector(route); collector.ready();
 // Intent I/O and collector startup cannot make the earlier UI observation current.
 auto final = census(automation.Get(),walker.Get(),title,body);
 sameSelection(automation.Get(),selected,final);
 ComPtr<IUIAutomationInvokePattern> pattern;
 budget(); winrt::check_hresult(final.row->GetCurrentPatternAs(UIA_InvokePatternId,IID_PPV_ARGS(&pattern)));
 interactive(); shell.live(); require(helpers(route).empty()); budget();
 collector.admit(); budget(); winrt::check_hresult(pattern->Invoke()); budget();
 put(proof,L"invoke","returned_once"); num(proof,L"click_boot_ms",collector.clickBoot.load());
 publish(L"click.json",proof);
 collector.collect();
}

int wmain(int argc, wchar_t** argv) {
 if (argc == 2 && (!wcscmp(argv[1],L"--inert-xml") || !wcscmp(argv[1],L"--inert-uri"))) {
  try {
   std::thread([limit=GetTickCount64()+10000] {
    while (GetTickCount64() < limit) Sleep(10);
    ExitProcess(124); // Only this inert TEST observer; no target operation exists.
   }).detach();
   inertUriDiagnostics = !wcscmp(argv[1],L"--inert-uri");
   archiveAt("inert_winrt_initialize");
   winrt::init_apartment(winrt::apartment_type::multi_threaded);
   if (!wcscmp(argv[1],L"--inert-xml")) xmlContracts();
   else uriContracts();
   return 0;
  } catch (...) {
   std::cerr << "TEST inert contracts failed stage=" << archiveStep << "\n";
   if (inertUriDiagnostics) std::cerr << inertUriFacts;
   try { throw; }
   catch (const winrt::hresult_error& error) {
    std::cerr << "hresult=" << DWORD(error.code().value) << "\n";
   }
   catch (const std::exception&) { std::cerr << "failure_kind=contract_or_standard_exception\n"; }
   catch (...) { std::cerr << "failure_kind=unknown_exception\n"; }
   return 1;
  }
 }
 try {
  require(argc == 5);
  const std::wstring mode = argv[1]; phase = narrow(mode); root = argv[2]; nonce = narrow(argv[3]); source = narrow(argv[4]);
  require(hex(nonce,32) && hex(source,40) && root.filename() == L"TEST-installed-"+wide(nonce));
  wchar_t runner[32768]{};
  const auto n = GetEnvironmentVariableW(L"RUNNER_TEMP",runner,32768);
  require(n && n < 32768 && fs::path(runner) == root.parent_path());
  auto p = fs::absolute(root).root_path(); roots.push_back(open(p,true));
  for (const auto& part : fs::absolute(root).relative_path()) { p /= part; roots.push_back(open(p,true)); }
  root = physical(roots.back().h);
  require(rootGrammar(narrow(root.wstring())));
  end = GetTickCount64()+(mode == L"deploy" ? 120000 : 30000);
  // A finite TEST actor watchdog is collection infrastructure, not proof of SDK drain.
  std::thread([limit=GetTickCount64()+135000] {
   while (GetTickCount64() < limit) Sleep(100);
   ExitProcess(124);
  }).detach();
  winrt::init_apartment(winrt::apartment_type::multi_threaded);
  if (mode == L"prerequisites") prerequisites(PrerequisiteStage::baseline);
  else if (mode == L"prerequisites-post-deploy") prerequisites(PrerequisiteStage::postDeployment);
  else if (mode == L"prerequisites-post-setup") prerequisites(PrerequisiteStage::postSetup);
  else if (mode == L"archive") archive(false);
  else if (mode == L"deploy") archive(true);
  else if (mode == L"click") click();
  else require(false);
  return 0;
 } catch (...) {
  try { auto report = fact(); put(report,L"phase",phase); put(report,L"outcome","refused_or_unknown");
   if (archiveFacts) {
    put(report,L"last_archive_step",archiveStep); report.Insert(L"archive_observations",*archiveFacts);
    try { throw; }
    catch (const winrt::hresult_error& error) {
     put(report,L"failure_kind","hresult"); num(report,L"hresult",DWORD(error.code().value));
    }
    catch (const std::exception&) { put(report,L"failure_kind","contract_or_io_refusal"); }
    catch (...) { put(report,L"failure_kind","unknown_exception"); }
   }
   report.Insert(L"retained_until_job_teardown",JsonValue::CreateBooleanValue(true));
   publish((wide(phase)+L".failure.json").c_str(),report); } catch (...) {}
  return 1;
 }
}
