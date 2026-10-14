// TEST-only real Win32 registry and Shell link contracts. Registration is
// redirected into one exclusive private HKCU subtree. Shortcut target fixtures
// are bounded, exclusive TEST-owned files; no SDK/package/notification effect
// and no shipping/user shortcut or target is created.
#define NOMINMAX
#define WCB_TEST_SHORTCUT_DEADLINE_PROBES 1
#include "installed.h"
#include <thread>
#include <cstring>
#include <new>
#include <type_traits>
#include <utility>
using namespace wcb;
// Fixed TEST stages and last TEST API observation are independent facts.
// A caught exception has no inferred native status: a production helper or
// assertion can fail after the last successful/expected TEST API result.
// No GetLastError is inferred from a production exception or registry LSTATUS.
static const char* testStage="entry";
static const char* lastTestAPI="none";
static DWORD lastTestResult=0;
static bool lastTestResultKnown=false;
static void stage(const char* name){
  testStage=name;
  lastTestAPI="none";
  lastTestResult=0;
  lastTestResultKnown=false;
}
static LSTATUS registryStatus(const char* name,LSTATUS result){
  lastTestAPI=name;
  lastTestResult=DWORD(result);
  lastTestResultKnown=true;
  return result;
}
static BOOL win32Result(const char* name,BOOL result){
  const auto status=result?ERROR_SUCCESS:GetLastError();
  lastTestAPI=name;
  lastTestResult=status;
  lastTestResultKnown=true;
  return result;
}
struct Descriptor {
  PSECURITY_DESCRIPTOR p=nullptr;
  explicit Descriptor(const std::wstring& acl){
    require(win32Result("ConvertStringSecurityDescriptorToSecurityDescriptorW",
      ConvertStringSecurityDescriptorToSecurityDescriptorW(acl.c_str(),SDDL_REVISION_1,&p,nullptr)));
  }
  ~Descriptor(){LocalFree(p);
  }
  SECURITY_ATTRIBUTES attributes(){return {sizeof(SECURITY_ATTRIBUTES),p,FALSE};
  }
};
struct Apartment {
  bool active=false;
  Apartment(){
    const auto status=CoInitializeEx(nullptr,COINIT_MULTITHREADED);
    require(status==S_OK||status==S_FALSE);
    active=true;
  }
  ~Apartment(){
    if(active)CoUninitialize();
  }
};
static std::wstring ownACL(const wchar_t* rights){
  const auto sid=wide(ownerSID());
  return L"O:"+sid+L"D:P(A;;"+rights+L";;;"+sid+L")(A;;"+rights+L";;;SY)";
}
static void testCreate(HKEY parent,const wchar_t* leaf,Key& key,SECURITY_ATTRIBUTES* sa=nullptr,DWORD options=0,REGSAM access=KEY_ALL_ACCESS){
  DWORD disposition=0;
  require(registryStatus("RegCreateKeyExW",RegCreateKeyExW(parent,leaf,0,nullptr,options,access,sa,&key.h,&disposition))==ERROR_SUCCESS);
  require(disposition==REG_CREATED_NEW_KEY);
}
static void missing(HKEY parent,const std::wstring& leaf){
  Key key;
  require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(parent,leaf.c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&key.h))==ERROR_FILE_NOT_FOUND);
}
static std::vector<uint8_t> security(HKEY key){
  DWORD n=0;
  const auto fields=OWNER_SECURITY_INFORMATION|DACL_SECURITY_INFORMATION;
  require(registryStatus("RegGetKeySecurity",RegGetKeySecurity(key,fields,nullptr,&n))==ERROR_INSUFFICIENT_BUFFER&&n&&n<=65536);
  std::vector<uint8_t> result(n);
  DWORD size=n;
  require(registryStatus("RegGetKeySecurity",RegGetKeySecurity(key,fields,reinterpret_cast<PSECURITY_DESCRIPTOR>(result.data()),&size))==ERROR_SUCCESS&&size==n);
  return result;
}
template<class Action> static void refused(Action action,uint64_t end){
  installedBudget(end);
  bool rejected=false;
  try{action();
  }catch(const std::runtime_error&){rejected=true;
  }
  installedBudget(end);
  require(rejected);
}
struct Profile {
  Key realSoftware,root;
  std::wstring leaf=L"AgentNotifications-TEST-"+wide(randomID());
  bool mapped=false,containsLinks=false;
  explicit Profile(const Generation& g){
    stage("profile_open_software");
    require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(HKEY_CURRENT_USER,L"Software",REG_OPTION_OPEN_LINK,KEY_READ|KEY_WRITE|DELETE,&realSoftware.h))==ERROR_SUCCESS);
    stage("profile_software_regular");
    regularRegistryKey(realSoftware.h,nullptr,RegistryRole::Software);
    stage("profile_private_descriptor");
    Descriptor descriptor(ownACL(L"KA"));
    auto sa=descriptor.attributes();
    stage("profile_private_create");
    testCreate(realSoftware.h,leaf.c_str(),root,&sa);
    stage("profile_private_owner");
    ownedRegistry(root.h,g);
    stage("profile_private_stamp");
    put(root.h,L"TESTOwner",leaf,GetTickCount64()+30000);
    stage("profile_hkcu_override");
    require(registryStatus("RegOverridePredefKey",RegOverridePredefKey(HKEY_CURRENT_USER,root.h))==ERROR_SUCCESS);
    mapped=true;
  }
  ~Profile(){if(mapped)RegOverridePredefKey(HKEY_CURRENT_USER,nullptr);
  }
  void close(const Generation& g){
    stage("profile_close_owner");
    ownedRegistry(root.h,g);
    require(value(root.h,L"TESTOwner")==leaf);
    require(registryStatus("RegOverridePredefKey",RegOverridePredefKey(HKEY_CURRENT_USER,nullptr))==ERROR_SUCCESS);
    mapped=false;
    root.close();
    if(containsLinks){
      // Public recursive deletion is not an admitted no-follow link unlink.
      // The exclusive owned TEST namespace survives only this disposable job.
      realSoftware.close();
      std::cout<<"TEST retained_registry_subtree=Software\\"<<narrow(leaf)
        <<" retained_until_job_teardown=true reason=registry_link_contract_fixture\n";
    }else{
      require(registryStatus("RegDeleteTreeW",RegDeleteTreeW(realSoftware.h,leaf.c_str()))==ERROR_SUCCESS);
      require(registryStatus("RegFlushKey",RegFlushKey(realSoftware.h))==ERROR_SUCCESS);
      realSoftware.close();
    }
  }
};
struct UserView {
  bool mapped=false;
  explicit UserView(HKEY root){
    require(registryStatus("RegOverridePredefKey",RegOverridePredefKey(HKEY_USERS,root))==ERROR_SUCCESS);
    mapped=true;
  }
  ~UserView(){if(mapped)RegOverridePredefKey(HKEY_USERS,nullptr);}
  void close(){
    require(registryStatus("RegOverridePredefKey",RegOverridePredefKey(HKEY_USERS,nullptr))==ERROR_SUCCESS);
    mapped=false;
  }
};
static void aliasContracts(const Generation& g,uint64_t end,bool chain){
  installedBudget(end);
  Profile profile(g);
  Key software,source,users,hive,foreign;
  Descriptor descriptor(ownACL(L"KA"));
  auto sa=descriptor.attributes();
  stage("alias_private_roots");
  testCreate(profile.root.h,L"Software",software,&sa);
  testCreate(profile.root.h,L"Users",users,&sa);
  testCreate(users.h,L"ForeignTarget",foreign,&sa);
  put(foreign.h,L"sentinel",L"unchanged",end);
  const auto leaf=wide(g.sid)+L"_Classes";
  const auto route=L"\\Registry\\User\\"+leaf;
  testCreate(users.h,leaf.c_str(),hive,&sa,chain?REG_OPTION_CREATE_LINK:0);
  if(chain){
    profile.containsLinks=true;
    const auto target=L"\\Registry\\User\\"+wide(g.sid)+L"\\Software\\"+profile.leaf+L"\\Users\\ForeignTarget";
    require(registryStatus("RegSetValueExW",RegSetValueExW(hive.h,L"SymbolicLinkValue",0,REG_LINK,
      reinterpret_cast<const BYTE*>(target.data()),DWORD(target.size()*2)))==ERROR_SUCCESS);
  }else put(hive.h,L"TESTAnchor",L"private-fixed-classes",end);
  testCreate(software.h,L"Classes",source,&sa,REG_OPTION_CREATE_LINK);
  profile.containsLinks=true;
  const auto sourceSecurity=security(source.h),hiveSecurity=security(hive.h),foreignSecurity=security(foreign.h);
  UserView view(users.h);
  std::vector<uint8_t> admittedRoute;
  const std::string foreignSID=g.sid=="S-1-5-18"?"S-1-5-19":"S-1-5-18";
  auto trySetRoute=[&](const std::vector<uint8_t>& bytes){
    installedBudget(end);
    const auto status=registryStatus("RegSetValueExW",
      RegSetValueExW(source.h,L"SymbolicLinkValue",0,REG_LINK,bytes.data(),DWORD(bytes.size())));
    if(status==ERROR_SUCCESS)admittedRoute=bytes;
    installedBudget(end);
    return status;
  };
  auto setRoute=[&](const std::vector<uint8_t>& bytes){
    require(trySetRoute(bytes)==ERROR_SUCCESS);
  };
  auto encoded=[](const std::wstring& text){
    const auto p=reinterpret_cast<const uint8_t*>(text.data());
    return std::vector<uint8_t>(p,p+text.size()*sizeof(wchar_t));
  };
  auto unchanged=[&]{
    DWORD type=0,size=512;
    std::array<uint8_t,512> bytes{};
    require(registryStatus("RegQueryValueExW",RegQueryValueExW(source.h,L"SymbolicLinkValue",nullptr,&type,bytes.data(),&size))==ERROR_SUCCESS);
    require(type==REG_LINK&&size==admittedRoute.size()&&std::equal(admittedRoute.begin(),admittedRoute.end(),bytes.begin()));
    missing(hive.h,L"CLSID");
    missing(hive.h,L"AppUserModelId");
    require(value(foreign.h,L"sentinel")==L"unchanged");
    require(security(source.h)==sourceSecurity&&security(hive.h)==hiveSecurity&&security(foreign.h)==foreignSecurity);
  };
  auto refusedRouteParser=[&]{
    installedBudget(end);
    OperatorDiagnostic diagnostic;
    bool rejected=false;
    try{
      ClassesAnchor anchor;
      anchor.open(g.sid,end,KEY_READ,&diagnostic);
    }catch(const std::runtime_error&){
      require(!strcmp(diagnostic.phase,"registry_classes_link_value"));
      rejected=true;
    }
    installedBudget(end);
    require(rejected);
  };
  if(chain){
    // Breakage: the fixed target itself is a link and a chained target is followed.
    stage("alias_chained_target_refused");
    setRoute(encoded(route));
    refused([&]{ClassesAnchor anchor;anchor.open(g.sid,end,KEY_READ);},end);
    unchanged();
  }else{
    // Breakage: documented owner-bound Classes aliases are all refused, or the
    // alias is followed instead of admitting the separate fixed private anchor.
    // This positive is READ ONLY: absolute native links bypass HKU overrides.
    stage("alias_exact_target_admitted_read_only");
    for(const auto& text : {route,route+std::wstring(1,L'\0')}){
      setRoute(encoded(text));
      ClassesAnchor anchor;
      anchor.open(g.sid,end,KEY_READ);
      require(value(anchor.root(),L"TESTAnchor")==L"private-fixed-classes");
      require(security(anchor.root())==hiveSecurity);
      anchor.close();
      unchanged();
    }
    // Breakage: foreign targets, embedded/multiple NUL or malformed UTF-16
    // become authority for opening an alias-selected hive.
    stage("alias_malformed_target_refused");
    auto embedded=route;embedded[1]=L'\0';
    auto odd=encoded(route);odd.pop_back();
    auto surrogate=encoded(route);surrogate[0]=0;surrogate[1]=0xd8;
    const std::vector<std::pair<const char*,std::vector<uint8_t>>> malformed{
      {"alias_foreign_target",encoded(L"\\Registry\\User\\"+wide(foreignSID)+L"_Classes")},
      {"alias_suffix_target",encoded(route+L"\\Foreign")},
      {"alias_embedded_nul",encoded(embedded)},
      {"alias_multiple_nul",encoded(route+std::wstring(2,L'\0'))},
      {"alias_surrogate",surrogate}
    };
    for(const auto& item : malformed){
      stage(item.first);
      setRoute(item.second);
      refusedRouteParser();
      unchanged();
      std::cout<<"TEST alias_malformed_fixture_constructed stage="<<item.first
        <<" parser_contract=run parser_rejection=observed\n";
    }
    // REG_LINK is UTF-16 data. Windows refuses this odd-byte fixture at the
    // registry boundary, so it never reaches ClassesAnchor and must not be
    // reported as parser rejection. If a Windows version constructs it, the
    // production parser still has to reject it exactly like other malformed
    // values.
    stage("alias_odd_bytes");
    const auto oddStatus=trySetRoute(odd);
    if(oddStatus==ERROR_ACCESS_DENIED){
      unchanged();
      std::cout<<"TEST alias_malformed_fixture_unconstructable stage=alias_odd_bytes"
        <<" api=RegSetValueExW result="<<oddStatus
        <<" parser_contract=not_run parser_rejection=not_evaluated\n";
    }else{
      require(oddStatus==ERROR_SUCCESS);
      refusedRouteParser();
      unchanged();
      std::cout<<"TEST alias_malformed_fixture_constructed stage=alias_odd_bytes"
        <<" parser_contract=run parser_rejection=observed\n";
    }
    stage("alias_foreign_owner_refused");
    setRoute(encoded(route));
    refused([&]{ClassesAnchor anchor;anchor.open(foreignSID,end,KEY_READ);},end);
    unchanged();
  }
  view.close();
  foreign.close();hive.close();users.close();source.close();software.close();
  stage("alias_profile_close");
  profile.close(g);
}
static void shortcutContracts(const Generation& g,uint64_t end);
static bool registryContracts(){
  stage("child_generation_custody");
  Generation g;
  // The actual constructor must hold the volume ancestor, not start at its leaf.
  stage("child_held_drive_ancestor");
  require(!g.guards.empty()&&physical(g.guards.front().h)==g.root.wstring().substr(0,7));
  stage("child_profile");
  Profile profile(g);
  const auto end=GetTickCount64()+30000;
  Key software,classes;
  const auto sid=wide(g.sid);
  Descriptor shared(L"O:"+sid+L"D:P(A;CI;KA;;;"+sid+L")(A;CI;KA;;;SY)");
  auto sharedSA=shared.attributes();
  stage("child_synthetic_software");
  testCreate(HKEY_CURRENT_USER,L"Software",software,&sharedSA);
  stage("child_synthetic_classes");
  testCreate(software.h,L"Classes",classes,&sharedSA);
  // Breakage: a successful non-link value query is mistaken for an API error,
  // or loses its actual caller role while rejecting normal applyClass.
  stage("child_guard_nonlink_presence");
  const auto softwareSecurity=security(software.h);
  const auto initialClassesSecurity=security(classes.h);
  put(software.h,L"SymbolicLinkValue",L"synthetic-nonlink",end);
  {
    OperatorDiagnostic diagnostic;
    bool classified=false;
    try{applyClass(g,end,&diagnostic);}
    catch(const RegistryFailure&){require(false);}
    catch(const std::runtime_error&){
      require(!strcmp(diagnostic.phase,"registry_software_other_value"));
      classified=true;diagnostic.emit();
    }
    require(classified);
  }
  missing(classes.h,L"CLSID");
  require(value(software.h,L"SymbolicLinkValue")==L"synthetic-nonlink");
  require(security(software.h)==softwareSecurity&&security(classes.h)==initialClassesSecurity);
  // Breakage: a genuinely unqueryable held handle is called value-presence
  // refusal or assigned a historical code instead of its actual returned5.
  stage("child_guard_query_access_denied");
  {
    Key writeOnly;
    require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(HKEY_CURRENT_USER,L"Software",REG_OPTION_OPEN_LINK,KEY_WRITE,&writeOnly.h))==ERROR_SUCCESS);
    OperatorDiagnostic diagnostic;
    bool classified=false;
    try{regularRegistryKey(writeOnly.h,&diagnostic,RegistryRole::Software);}
    catch(const RegistryFailure& error){
      require(error.code==ERROR_ACCESS_DENIED&&!strcmp(error.phase,"registry_software_query"));
      classified=true;diagnostic.emit();
    }
    require(classified);
    writeOnly.close();
  }
  require(security(software.h)==softwareSecurity&&security(classes.h)==initialClassesSecurity);
  stage("child_guard_fixture_cleanup");
  installedBudget(end);
  require(registryStatus("RegDeleteValueW",RegDeleteValueW(software.h,L"SymbolicLinkValue"))==ERROR_SUCCESS);
  installedBudget(end);
  // Breakage: old setup requires the shared parent and fails on a fresh profile.
  // The literal old admission returns missing; actual apply creates valid own keys.
  stage("child_shared_parents_missing");
  missing(classes.h,L"AppUserModelId");
  missing(classes.h,L"CLSID");
  stage("child_classes_security");
  const auto classesSecurity=security(classes.h);
  stage("child_apply_app");
  applyApp(g,end);
  stage("child_apply_class");
  applyClass(g,end);
  stage("child_registry_readback");
  registryReadback(g,end);
  stage("child_shared_parent_security");
  Key parent,foreign;
  require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(classes.h,L"AppUserModelId",REG_OPTION_OPEN_LINK,KEY_ALL_ACCESS,&parent.h))==ERROR_SUCCESS);
  const auto parentSecurity=security(parent.h);
  Key clsid;
  require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(classes.h,L"CLSID",REG_OPTION_OPEN_LINK,KEY_READ,&clsid.h))==ERROR_SUCCESS);
  const auto clsidSecurity=security(clsid.h);
  SECURITY_DESCRIPTOR_CONTROL control{};
  DWORD revision=0;
  require(win32Result("GetSecurityDescriptorControl",
    GetSecurityDescriptorControl(reinterpret_cast<PSECURITY_DESCRIPTOR>(const_cast<uint8_t*>(parentSecurity.data())),&control,&revision))
    &&!(control&SE_DACL_PROTECTED));
  DWORD type=0,size=0;
  require(registryStatus("RegQueryValueExW",RegQueryValueExW(parent.h,L"OwnerGeneration",nullptr,&type,nullptr,&size))==ERROR_FILE_NOT_FOUND);
  stage("child_foreign_sibling");
  testCreate(parent.h,L"ForeignSibling",foreign);
  put(foreign.h,L"sentinel",L"unchanged",end);
  // Breakage: rollback deletes shared container/foreign sibling or changes ACLs.
  stage("child_restore_app");
  restoreApp(g,end);
  stage("child_restore_class");
  restoreClass(g,end);
  stage("child_rollback_preserved");
  missing(parent.h,wide(g.snapshot.aumid));
  require(value(foreign.h,L"sentinel")==L"unchanged");
  require(security(parent.h)==parentSecurity&&security(classes.h)==classesSecurity&&security(clsid.h)==clsidSecurity);
  clsid.close();
  // Breakage: collision overwrites another generation's owned stamp/values.
  stage("child_foreign_stamp");
  Key collision;
  createKey(g,parent.h,wide(g.snapshot.aumid),collision,end);
  put(collision.h,L"OwnerGeneration",L"foreign-generation",end);
  collision.close();
  refused([&]{applyApp(g,end);},end);
  refused([&]{restoreApp(g,end);},end);
  require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(parent.h,wide(g.snapshot.aumid).c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&collision.h))==ERROR_SUCCESS);
  require(value(collision.h,L"OwnerGeneration")==L"foreign-generation");
  require(registryStatus("RegQueryValueExW",RegQueryValueExW(collision.h,L"DisplayName",nullptr,&type,nullptr,&size))==ERROR_FILE_NOT_FOUND);
  collision.close();
  foreign.close();
  parent.close();
  require(registryStatus("RegDeleteTreeW",RegDeleteTreeW(classes.h,L"AppUserModelId"))==ERROR_SUCCESS);
  // Breakage: access denied is treated as missing and setup repairs shared ACLs.
  stage("child_denied_parent");
  Key denied;
  Descriptor deny(L"O:"+wide(g.sid)+L"D:P(D;;CC;;;"+wide(g.sid)+L")(A;;KA;;;"+wide(g.sid)+L")(A;;KA;;;SY)");
  auto denySA=deny.attributes();
  testCreate(classes.h,L"AppUserModelId",denied,&denySA,0,KEY_READ|WRITE_DAC|DELETE);
  const auto deniedSecurity=security(denied.h);
  refused([&]{applyApp(g,end);},end);
  // Breakage: the genuine denied shared-parent API loses numeric provenance.
  OperatorDiagnostic diagnostic;
  bool classified=false;
  try{SharedRegistryParent observed;observed.open(g,L"AppUserModelId",end,&diagnostic);}
  catch(const RegistryFailure& error){
    require(error.code==ERROR_ACCESS_DENIED&&!strcmp(error.phase,"shared_container_open"));
    classified=true;diagnostic.emit();
  }
  require(classified);
  missing(denied.h,wide(g.snapshot.aumid));
  require(security(denied.h)==deniedSecurity);
  // TEST-owned ACL cleanup only, after the production refusal assertion.
  stage("child_denied_cleanup");
  Descriptor allow(ownACL(L"KA"));
  require(registryStatus("RegSetKeySecurity",RegSetKeySecurity(denied.h,DACL_SECURITY_INFORMATION,allow.p))==ERROR_SUCCESS);
  denied.close();
  require(registryStatus("RegDeleteTreeW",RegDeleteTreeW(classes.h,L"AppUserModelId"))==ERROR_SUCCESS);
  // Breakage: a configured registry link is followed and receives an owned leaf.
  stage("child_link_target");
  Key target,link;
  testCreate(classes.h,L"SafeTarget",target);
  put(target.h,L"sentinel",L"unchanged",end);
  DWORD disposition=0;
  stage("child_link_create");
  const auto status=registryStatus("RegCreateKeyExW",
    RegCreateKeyExW(classes.h,L"AppUserModelId",0,nullptr,REG_OPTION_CREATE_LINK,KEY_ALL_ACCESS,nullptr,&link.h,&disposition));
  bool linksQualified=true;
  if(status==ERROR_ACCESS_DENIED||status==ERROR_PRIVILEGE_NOT_HELD||status==ERROR_NOT_SUPPORTED){
    linksQualified=false;
    std::cout<<"TEST registry link fixture unsupported status="<<status<<"; link qualification pending\n";
  }else{
    require(status==ERROR_SUCCESS&&disposition==REG_CREATED_NEW_KEY);
    profile.containsLinks=true;
    link.close();
    stage("child_unfinished_link_apply_refused");
    refused([&]{applyApp(g,end);},end);
    missing(target.h,wide(g.snapshot.aumid));
    require(value(target.h,L"sentinel")==L"unchanged");
    require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(classes.h,L"AppUserModelId",REG_OPTION_OPEN_LINK,KEY_ALL_ACCESS,&link.h))==ERROR_SUCCESS);
    require(registryStatus("RegQueryValueExW",RegQueryValueExW(link.h,L"SymbolicLinkValue",nullptr,&type,nullptr,&size))==ERROR_FILE_NOT_FOUND);
    stage("child_link_configure");
    const auto route=L"\\Registry\\User\\"+wide(g.sid)+L"\\Software\\"+profile.leaf+L"\\Software\\Classes\\SafeTarget";
    require(registryStatus("RegSetValueExW",
      RegSetValueExW(link.h,L"SymbolicLinkValue",0,REG_LINK,reinterpret_cast<const BYTE*>(route.c_str()),DWORD(route.size()*2)))==ERROR_SUCCESS);
    // Prove the public local creation flag returns the source, not its target.
    stage("child_configured_link_nofollow");
    Key nofollow;
    DWORD opened=0;
    require(registryStatus("RegCreateKeyExW",RegCreateKeyExW(classes.h,L"AppUserModelId",0,nullptr,REG_OPTION_OPEN_LINK,
      KEY_READ|KEY_WRITE,nullptr,&nofollow.h,&opened))==ERROR_SUCCESS&&opened==REG_OPENED_EXISTING_KEY);
    DWORD linkType=0,linkBytes=0;
    require(registryStatus("RegQueryValueExW",RegQueryValueExW(nofollow.h,L"SymbolicLinkValue",nullptr,&linkType,nullptr,&linkBytes))==ERROR_SUCCESS
      &&linkType==REG_LINK&&linkBytes==route.size()*2);
    // Breakage: the real configured source REG_LINK loses its distinct type
    // class; no target payload or raw type is emitted by the production guard.
    stage("child_guard_link_presence");
    {
      OperatorDiagnostic diagnostic;
      bool classified=false;
      try{regularRegistryKey(nofollow.h,&diagnostic,RegistryRole::SharedContainer);}
      catch(const RegistryFailure&){require(false);}
      catch(const std::runtime_error&){
        require(!strcmp(diagnostic.phase,"registry_shared_link_value"));
        classified=true;diagnostic.emit();
      }
      require(classified);
    }
    nofollow.close();
    link.close();
    stage("child_configured_link_apply_refused");
    refused([&]{applyApp(g,end);},end);
    missing(target.h,wide(g.snapshot.aumid));
    require(value(target.h,L"sentinel")==L"unchanged");
    require(registryStatus("RegOpenKeyExW",RegOpenKeyExW(classes.h,L"AppUserModelId",REG_OPTION_OPEN_LINK,KEY_READ,&link.h))==ERROR_SUCCESS);
    stage("child_link_source_preserved");
    std::vector<uint8_t> sourceRoute(route.size()*2);
    DWORD routeBytes=DWORD(sourceRoute.size()),routeType=0;
    require(registryStatus("RegQueryValueExW",RegQueryValueExW(link.h,L"SymbolicLinkValue",nullptr,&routeType,sourceRoute.data(),&routeBytes))==ERROR_SUCCESS
      &&routeType==REG_LINK&&routeBytes==sourceRoute.size()
      &&!memcmp(sourceRoute.data(),route.data(),sourceRoute.size()));
    link.close();
  }
  target.close();
  classes.close();
  software.close();
  stage("child_profile_close");
  profile.close(g);
  if(linksQualified){
    aliasContracts(g,end,false);
    aliasContracts(g,end,true);
    std::cout<<"TEST real registry contracts passed; private process-local HKCU mapping only\n";
  }
  shortcutContracts(g,end);
  return linksQualified;
}
static void shortcutApiFailureContract(){
  static_assert(std::is_base_of_v<winrt::hresult_error,ShortcutApiFailure>);
  static_assert(std::is_base_of_v<std::exception,ShortcutApiFailure>);
  stage("child_shortcut_failure_wrapper");
  bool classified=false;
  try{
    shortcutHresult("IShellLinkW::SetPath",E_INVALIDARG);
  }catch(const ShortcutApiFailure& error){
    require(std::string(error.api)=="IShellLinkW::SetPath");
    require(error.apiFailure&&error.statusKnown&&error.status==E_INVALIDARG);
    require(error.code().value==E_INVALIDARG);
    require(error.status==error.code().value);
    require(std::string(error.what()).find("IShellLinkW::SetPath")!=std::string::npos);
    classified=true;
  }
  require(classified);
  bool stdClassified=false;
  try{
    shortcutHresult("IShellLinkW::SetArguments",E_OUTOFMEMORY);
  }catch(const std::exception& error){
    require(std::string(error.what()).find("IShellLinkW::SetArguments")!=std::string::npos);
    stdClassified=true;
  }
  require(stdClassified);
  bool winrtClassified=false;
  try{
    shortcutHresult("IPropertyStore::Commit",E_FAIL);
  }catch(const winrt::hresult_error& error){
    require(error.code().value==E_FAIL);
    winrtClassified=true;
  }
  require(winrtClassified);
  std::cout<<"TEST synthetic shortcut API wrapper passed synthetic=true"
    <<" api=IShellLinkW::SetPath result=2147942487"
    <<" production_diagnostic_contract=not_qualified exactAPI=unknown\n";
}
static void shortcutResultClassification(){
  stage("child_shortcut_result_classification");
  ITEMIDLIST emptyPidl{};
  require(!shortcutPidlPresent(nullptr)&&!shortcutPidlPresent(&emptyPidl));
  require(!shortcutNamePresent(nullptr)&&!shortcutNamePresent(L""));
  bool nonSok=false;
  try{
    shortcutHresult("IShellLinkW::GetIDList",S_FALSE);
  }catch(const ShortcutContractViolation& error){
    require(!error.apiFailure&&error.statusKnown&&error.status==S_FALSE);
    require(!strcmp(error.api,"IShellLinkW::GetIDList"));
    nonSok=true;
  }
  require(nonSok);
  bool nullOutput=false;
  try{
    shortcutContractResult("IShellLinkW::GetIDList",S_OK,shortcutPidlPresent(&emptyPidl));
  }catch(const ShortcutContractViolation& error){
    require(!error.apiFailure&&error.statusKnown&&error.status==S_OK);
    nullOutput=true;
  }
  require(nullOutput);
  bool emptyName=false;
  try{
    shortcutContractResult("SHGetNameFromIDList",S_OK,shortcutNamePresent(L""));
  }catch(const ShortcutContractViolation& error){
    require(!error.apiFailure&&error.statusKnown&&error.status==S_OK);
    emptyName=true;
  }
  require(emptyName);
  bool apiFailure=false;
  try{
    shortcutContractResult("SHGetNameFromIDList",E_FAIL,false);
  }catch(const ShortcutApiFailure& error){
    require(error.apiFailure&&error.statusKnown&&error.status==E_FAIL);
    apiFailure=true;
  }
  require(apiFailure);
  std::cout<<"TEST shortcut result classification passed policy_fixture=true"
    <<" api_coverage=false non_s_ok_classified=true empty_pidl_classified=true"
    <<" empty_name_classified=true"
    <<" successful_hresult_not_api_failure=true exactAPI=unknown\n";
}
static void shortcutOwnershipContracts(){
  stage("child_shortcut_cleanup_owner");
  Microsoft::WRL::ComPtr<IMalloc> allocator;
  shortcutHresult("CoGetMalloc",CoGetMalloc(1,allocator.GetAddressOf()));
  auto allocation=static_cast<PWSTR>(CoTaskMemAlloc(64));
  require(allocation&&allocator->DidAlloc(allocation)==1);
  bool released=false;
  try{
    ScopedCoTaskMem owner(allocation);
    require(owner.value==allocation&&allocator->DidAlloc(allocation)==1);
    throw ShortcutContractViolation("TEST-owned-co-task-memory",E_FAIL);
  }catch(const ShortcutContractViolation&){
    released=true;
  }
  require(released&&allocator->DidAlloc(allocation)==0);

  stage("child_shortcut_cleanup_pidl");
  auto pidl=ILCreateFromPathW(L"C:\\");
  require(shortcutPidlPresent(pidl)&&allocator->DidAlloc(pidl)==1);
  released=false;
  try{
    ScopedPidl owner(pidl);
    require(owner.value==pidl&&allocator->DidAlloc(pidl)==1);
    throw ShortcutContractViolation("TEST-owned-pidl",E_FAIL);
  }catch(const ShortcutContractViolation&){
    released=true;
  }
  require(released&&allocator->DidAlloc(pidl)==0);

  stage("child_shortcut_cleanup_global_lock");
  auto memory=GlobalAlloc(GMEM_MOVEABLE,64);
  require(memory);
  released=false;
  try{
    ScopedGlobalLock owner(memory);
    require(owner.value&&(GlobalFlags(memory)&GMEM_LOCKCOUNT)==1);
    throw ShortcutContractViolation("TEST-owned-global-lock",E_FAIL);
  }catch(const ShortcutContractViolation&){
    released=true;
  }
  require(released&&(GlobalFlags(memory)&GMEM_LOCKCOUNT)==0);
  require(GlobalFree(memory)==nullptr);
  std::cout<<"TEST shortcut cleanup owners passed"
    <<" co_task_mem=true pidl=true global_lock=true exactAPI=unknown\n";
}
template<class Work> static void requireShortcutDeadlineAfterApi(
 uint64_t end,const char* api,const char* boundary,Work work){
  ShortcutDeadlineProbe probe(api,boundary);
  bool deadlineFailure=false;
  try{
    work(probe);
  }catch(const ShortcutDeadlineExceeded& error){
    deadlineFailure=probe.observed&&error.api==api&&error.boundary==boundary
      &&error.end==end&&error.observed==end;
  }
  // Returning without the exact deadline exception fails here. Unrelated
  // runtime errors intentionally escape instead of becoming a false PASS.
  require(deadlineFailure);
}
static ShortcutFixture substituted(
 const ShortcutFixture& fixture,std::wstring&& target,HANDLE custody){
  return {std::move(target),fixture.args,fixture.aumid,fixture.clsid,custody};
}
static std::wstring syntheticLongShortcutTarget(const ShortcutFixture& fixture){
  const auto prototype=shortcutParsingTarget(fixture.target);
  const auto separator=fixture.target.find_last_of(L'\\');
  require(separator!=std::wstring::npos&&separator>2);
  std::wstring target=prototype.substr(0,separator);
  for(size_t part=0;target.size()<=MAX_PATH;part++){
    target+=L'\\';
    target.append(96,wchar_t(L'a'+part%26));
  }
  target+=L"\\helper.exe";
  require(target.size()>MAX_PATH&&target.size()<20000&&target.starts_with(prototype.substr(0,3)));
  return target;
}
enum class ShortcutFixtureFailure {
  none,
  directory_after_create,
  directory_after_open,
  directory_after_owned,
  directory_after_identity,
  second_directory_after_create,
  second_directory_after_open,
  second_directory_after_owned,
  second_directory_after_identity,
  expected_file_after_create,
  expected_file_after_owned,
  expected_file_after_identity,
  substitute_file_after_create,
  substitute_file_after_owned,
  substitute_file_after_identity,
  target_tree_after_validation,
  expected_target_allocation,
  substitute_target_allocation,
  shortcut_file_after_create,
  shortcut_file_after_owned,
  shortcut_file_after_identity,
  shortcut_file_target_allocation,
  shortcut_file_after_validation
};
static const char* shortcutFixtureFailureName(ShortcutFixtureFailure point){
  switch(point){
  case ShortcutFixtureFailure::none:return "none";
  case ShortcutFixtureFailure::directory_after_create:return "directory_after_create";
  case ShortcutFixtureFailure::directory_after_open:return "directory_after_open";
  case ShortcutFixtureFailure::directory_after_owned:return "directory_after_owned";
  case ShortcutFixtureFailure::directory_after_identity:return "directory_after_identity";
  case ShortcutFixtureFailure::second_directory_after_create:return "second_directory_after_create";
  case ShortcutFixtureFailure::second_directory_after_open:return "second_directory_after_open";
  case ShortcutFixtureFailure::second_directory_after_owned:return "second_directory_after_owned";
  case ShortcutFixtureFailure::second_directory_after_identity:return "second_directory_after_identity";
  case ShortcutFixtureFailure::expected_file_after_create:return "expected_file_after_create";
  case ShortcutFixtureFailure::expected_file_after_owned:return "expected_file_after_owned";
  case ShortcutFixtureFailure::expected_file_after_identity:return "expected_file_after_identity";
  case ShortcutFixtureFailure::substitute_file_after_create:return "substitute_file_after_create";
  case ShortcutFixtureFailure::substitute_file_after_owned:return "substitute_file_after_owned";
  case ShortcutFixtureFailure::substitute_file_after_identity:return "substitute_file_after_identity";
  case ShortcutFixtureFailure::target_tree_after_validation:return "target_tree_after_validation";
  case ShortcutFixtureFailure::expected_target_allocation:return "expected_target_allocation";
  case ShortcutFixtureFailure::substitute_target_allocation:return "substitute_target_allocation";
  case ShortcutFixtureFailure::shortcut_file_after_create:return "shortcut_file_after_create";
  case ShortcutFixtureFailure::shortcut_file_after_owned:return "shortcut_file_after_owned";
  case ShortcutFixtureFailure::shortcut_file_after_identity:return "shortcut_file_after_identity";
  case ShortcutFixtureFailure::shortcut_file_target_allocation:return "shortcut_file_target_allocation";
  case ShortcutFixtureFailure::shortcut_file_after_validation:return "shortcut_file_after_validation";
  }
  require(false);
  return "unknown";
}
struct ShortcutFixtureInjectedFailure : std::runtime_error {
  ShortcutFixtureFailure point;
  explicit ShortcutFixtureInjectedFailure(ShortcutFixtureFailure point)
    :std::runtime_error(std::string("TEST shortcut fixture failure ")+shortcutFixtureFailureName(point)),
     point(point){}
};
static void injectShortcutFixtureFailure(ShortcutFixtureFailure actual,ShortcutFixtureFailure expected){
  if(actual==expected)throw ShortcutFixtureInjectedFailure(actual);
}
struct ShortcutFixtureInjectedAllocationFailure : std::bad_alloc {
  ShortcutFixtureFailure point;
  explicit ShortcutFixtureInjectedAllocationFailure(ShortcutFixtureFailure point):point(point){}
  const char* what()const noexcept override{
    return "TEST shortcut fixture allocation failure";
  }
};
static void injectShortcutFixtureAllocationFailure(
  ShortcutFixtureFailure actual,ShortcutFixtureFailure expected){
  if(actual==expected)throw ShortcutFixtureInjectedAllocationFailure(actual);
}
struct ShortcutFixtureCollisionFailure : std::runtime_error {
  DWORD status;
  explicit ShortcutFixtureCollisionFailure(DWORD status)
    :std::runtime_error("TEST shortcut fixture collision"),status(status){}
};
// TEST-only rollback observability. Native and injected outcomes are separate:
// api* always retains the real Win32 result, while reported* is the cleanup
// result consumed by the contract. A test override turns only a successful API
// result into a refusal and never invents a Windows error code.
enum class ShortcutCleanupAction { none,close_handle,delete_file,remove_directory };
enum class ShortcutCleanupSubject { file,directory };
enum class ShortcutCleanupSource { win32,test_override };
struct ShortcutCleanupCall {
  ShortcutCleanupAction action=ShortcutCleanupAction::none;
  ShortcutCleanupSubject subject=ShortcutCleanupSubject::file;
  size_t index=0;
  bool apiCalled=false,apiSucceeded=false,apiResultKnown=false;
  DWORD apiStatus=ERROR_SUCCESS;
  bool reportedSucceeded=false,reportedResultKnown=false;
  DWORD reportedStatus=ERROR_SUCCESS;
  ShortcutCleanupSource source=ShortcutCleanupSource::win32;
};
struct ShortcutCleanupResult {
  static constexpr size_t maxCalls=16;
  std::array<ShortcutCleanupCall,maxCalls> calls{};
  size_t count=0;
  bool overflow=false;
  void reset()noexcept{
    calls={};
    count=0;
    overflow=false;
  }
  void record(const ShortcutCleanupCall& call)noexcept{
    if(count==calls.size()){
      overflow=true;
      return;
    }
    calls[count++]=call;
  }
};
struct ShortcutCleanupOverride {
  ShortcutCleanupAction action=ShortcutCleanupAction::none;
  ShortcutCleanupSubject subject=ShortcutCleanupSubject::file;
  size_t index=0;
  bool consumed=false;
};
struct ShortcutCleanupProbe {
  static constexpr size_t maxOverrides=4;
  ShortcutCleanupResult result;
  std::array<ShortcutCleanupOverride,maxOverrides> overrides{};
  size_t overrideCount=0;
  bool invalidOverride=false;
  void reset()noexcept{
    result.reset();
    overrides={};
    overrideCount=0;
    invalidOverride=false;
  }
  void refuse(ShortcutCleanupAction action,ShortcutCleanupSubject subject,size_t index){
    require(action!=ShortcutCleanupAction::none&&overrideCount<overrides.size());
    overrides[overrideCount++]={action,subject,index,false};
  }
  bool reportedFailures(size_t first)const noexcept{
    for(size_t index=first;index<result.count;++index)
      if(!result.calls[index].reportedSucceeded)return true;
    return false;
  }
};
static bool shortcutCleanupApi(ShortcutCleanupProbe* probe,ShortcutCleanupAction action,
  ShortcutCleanupSubject subject,size_t index,BOOL apiResult)noexcept{
  ShortcutCleanupCall call;
  call.action=action;
  call.subject=subject;
  call.index=index;
  call.apiCalled=true;
  call.apiSucceeded=apiResult!=FALSE;
  call.apiResultKnown=true;
  call.apiStatus=apiResult?ERROR_SUCCESS:GetLastError();
  call.reportedSucceeded=call.apiSucceeded;
  call.reportedResultKnown=true;
  call.reportedStatus=call.apiStatus;
  if(probe)
    for(auto& overrideEntry:probe->overrides)
      if(!overrideEntry.consumed&&overrideEntry.action==action
        &&overrideEntry.subject==subject&&overrideEntry.index==index){
        overrideEntry.consumed=true;
        if(!call.apiSucceeded)probe->invalidOverride=true;
        call.reportedSucceeded=false;
        call.reportedResultKnown=false;
        call.reportedStatus=ERROR_SUCCESS;
        call.source=ShortcutCleanupSource::test_override;
        break;
      }
  if(probe)probe->result.record(call);
  return call.apiSucceeded;
}
static void shortcutRollbackClose(ShortcutCleanupProbe* probe,
  ShortcutCleanupAction action,ShortcutCleanupSubject subject,size_t index,HANDLE handle)noexcept{
  shortcutCleanupApi(probe,action,subject,index,CloseHandle(handle));
}
static bool shortcutRollbackDelete(ShortcutCleanupProbe* probe,
  ShortcutCleanupSubject subject,size_t index,const fs::path& storagePath)noexcept{
  return shortcutCleanupApi(probe,ShortcutCleanupAction::delete_file,
    subject,index,DeleteFileW(storagePath.c_str()));
}
static bool shortcutRollbackRemove(ShortcutCleanupProbe* probe,
  ShortcutCleanupSubject subject,size_t index,const fs::path& storagePath)noexcept{
  return shortcutCleanupApi(probe,ShortcutCleanupAction::remove_directory,
    subject,index,RemoveDirectoryW(storagePath.c_str()));
}
struct ShortcutCreatedDirectory {
  fs::path parsingPath,storagePath;
  Handle held{INVALID_HANDLE_VALUE};
  bool created=false;
};
struct ShortcutCreatedFile {
  fs::path parsingPath,storagePath;
  Handle held{INVALID_HANDLE_VALUE};
  bool created=false;
};
class ScopedShortcutTargetTree {
  static constexpr size_t maxDirectories=4,maxFiles=2;
  fs::path base;
  std::array<ShortcutCreatedDirectory,maxDirectories> directories{};
  std::array<ShortcutCreatedFile,maxFiles> files{};
  size_t directoryCount=0,fileCount=0;
  SECURITY_ATTRIBUTES* savedAttributes=nullptr;
  ShortcutCleanupProbe localCleanupProbe;
  ShortcutCleanupProbe* cleanupProbe;
  void rollbackNoexcept()noexcept{
    for(size_t index=0;index<files.size();++index){
      auto& file=files[index];
      if(file.held.h!=INVALID_HANDLE_VALUE){
        shortcutRollbackClose(cleanupProbe,ShortcutCleanupAction::close_handle,
          ShortcutCleanupSubject::file,index,file.held.h);
        file.held.h=INVALID_HANDLE_VALUE;
      }
    }
    for(size_t index=0;index<directories.size();++index){
      auto& directory=directories[index];
      if(directory.held.h!=INVALID_HANDLE_VALUE){
        shortcutRollbackClose(cleanupProbe,ShortcutCleanupAction::close_handle,
          ShortcutCleanupSubject::directory,index,directory.held.h);
        directory.held.h=INVALID_HANDLE_VALUE;
      }
    }
    for(size_t index=fileCount;index>0;--index){
      auto& file=files[index-1];
      if(file.created&&shortcutRollbackDelete(cleanupProbe,
        ShortcutCleanupSubject::file,index-1,file.storagePath))file.created=false;
    }
    for(size_t index=directoryCount;index>0;--index){
      auto& directory=directories[index-1];
      if(directory.created&&shortcutRollbackRemove(cleanupProbe,
        ShortcutCleanupSubject::directory,index-1,directory.storagePath))
        directory.created=false;
    }
  }
  void createDirectory(size_t index,const fs::path& path,
    ShortcutFixtureFailure afterCreate,ShortcutFixtureFailure afterOpen,
    ShortcutFixtureFailure afterOwned,ShortcutFixtureFailure afterIdentity){
    require(index<maxDirectories&&index==directoryCount);
    auto& directory=directories[index];
    // Reserve both transport representations before the native effect. The
    // noexcept rollback consumes only the preplanned extended storage path.
    directory.parsingPath=path;
    directory.storagePath=shortcutStoragePath(path);
    ++directoryCount;
    const BOOL created=CreateDirectoryW(directory.storagePath.c_str(),savedAttributes);
    if(created)directory.created=true;
    injectShortcutFixtureFailure(afterCreate,afterCreate);
    require(win32Result("CreateDirectoryW",created));
    directory.held=open(directory.storagePath,true);
    injectShortcutFixtureFailure(afterOpen,afterOpen);
    owned(directory.held.h,ownerSID());
    injectShortcutFixtureFailure(afterOwned,afterOwned);
    require(physical(directory.held.h)==directory.storagePath.wstring());
    injectShortcutFixtureFailure(afterIdentity,afterIdentity);
  }
  void createTarget(size_t index,const wchar_t* name,
    ShortcutFixtureFailure afterCreate,ShortcutFixtureFailure afterOwned,
    ShortcutFixtureFailure afterIdentity){
    require(index<maxFiles&&index==fileCount);
    auto& file=files[index];
    const auto parsingPath=directories[3].parsingPath/name;
    file.parsingPath=parsingPath;
    file.storagePath=shortcutStoragePath(parsingPath);
    ++fileCount;
    // Match open()'s read-only custody handle. DELETE is requested only after
    // the close result is observed, so cleanup remains explicit and the live
    // identity check can open the same target without a DELETE sharing conflict.
    const HANDLE created=CreateFileW(file.storagePath.c_str(),
      GENERIC_READ|READ_CONTROL,FILE_SHARE_READ,savedAttributes,
      CREATE_NEW,FILE_ATTRIBUTE_NORMAL|FILE_FLAG_OPEN_REPARSE_POINT,nullptr);
    if(created!=INVALID_HANDLE_VALUE){
      file.held.h=created;
      file.created=true;
    }
    injectShortcutFixtureFailure(afterCreate,afterCreate);
    require(win32Result("CreateFileW",created!=INVALID_HANDLE_VALUE));
    owned(file.held.h,ownerSID());
    injectShortcutFixtureFailure(afterOwned,afterOwned);
    const auto held=physical(file.held.h);
    require(shortcutStoragePath(held)==file.storagePath);
    injectShortcutFixtureFailure(afterIdentity,afterIdentity);
  }
public:
  std::wstring expectedTarget,substituteTarget;
  explicit ScopedShortcutTargetTree(const Generation& g,SECURITY_ATTRIBUTES& attributes,
    ShortcutFixtureFailure failure=ShortcutFixtureFailure::none,
    ShortcutCleanupProbe* externalCleanupProbe=nullptr)
    :savedAttributes(&attributes),
     base(shortcutLocalDriveName((g.root/L"shortcut-targets").wstring())),
     cleanupProbe(externalCleanupProbe?externalCleanupProbe:&localCleanupProbe){
    try{
      stage("child_shortcut_long_target_fixture");
      createDirectory(0,base,
        failure==ShortcutFixtureFailure::directory_after_create?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::directory_after_open?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::directory_after_owned?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::directory_after_identity?failure:ShortcutFixtureFailure::none);
      auto path=base;
      for(size_t part=0;part<2;part++){
        path/=(std::wstring(96,wchar_t(L'a'+part%26)));
        createDirectory(part+1,path,ShortcutFixtureFailure::none,
          ShortcutFixtureFailure::none,ShortcutFixtureFailure::none,
          ShortcutFixtureFailure::none);
      }
      path/=(std::wstring(96,wchar_t(L'a'+2%26)));
      createDirectory(3,path,
        failure==ShortcutFixtureFailure::second_directory_after_create?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::second_directory_after_open?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::second_directory_after_owned?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::second_directory_after_identity?failure:ShortcutFixtureFailure::none);
      createTarget(0,L"expected-long.exe",
        failure==ShortcutFixtureFailure::expected_file_after_create?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::expected_file_after_owned?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::expected_file_after_identity?failure:ShortcutFixtureFailure::none);
      injectShortcutFixtureAllocationFailure(failure,
        ShortcutFixtureFailure::expected_target_allocation);
      expectedTarget=shortcutParsingTarget(files[0].parsingPath.wstring());
      createTarget(1,L"substituted-long.exe",
        failure==ShortcutFixtureFailure::substitute_file_after_create?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::substitute_file_after_owned?failure:ShortcutFixtureFailure::none,
        failure==ShortcutFixtureFailure::substitute_file_after_identity?failure:ShortcutFixtureFailure::none);
      injectShortcutFixtureAllocationFailure(failure,
        ShortcutFixtureFailure::substitute_target_allocation);
      substituteTarget=shortcutParsingTarget(files[1].parsingPath.wstring());
      require(expectedTarget.size()>MAX_PATH&&substituteTarget.size()>MAX_PATH);
      injectShortcutFixtureFailure(failure,ShortcutFixtureFailure::target_tree_after_validation);
    }catch(...){
      rollbackNoexcept();
      throw;
    }
  }
  HANDLE expectedCustody()const{return files[0].held.h;}
  HANDLE substituteCustody()const{return files[1].held.h;}
  const fs::path& longFileParent()const{return directories[3].parsingPath;}
  void cleanup(){
    stage("child_shortcut_long_target_cleanup");
    const auto first=cleanupProbe->result.count;
    rollbackNoexcept();
    require(!cleanupProbe->result.overflow&&!cleanupProbe->invalidOverride
      &&!cleanupProbe->reportedFailures(first));
    expectedTarget.clear();
    substituteTarget.clear();
  }
  ~ScopedShortcutTargetTree()noexcept{rollbackNoexcept();}
};
class ScopedShortcutFile {
  fs::path parsingPath,storagePath;
  Handle file;
  bool created=false;
  ShortcutCleanupProbe localCleanupProbe;
  ShortcutCleanupProbe* cleanupProbe;
  void rollbackNoexcept()noexcept{
    if(file.h!=INVALID_HANDLE_VALUE){
      shortcutRollbackClose(cleanupProbe,ShortcutCleanupAction::close_handle,
        ShortcutCleanupSubject::file,0,file.h);
      file.h=INVALID_HANDLE_VALUE;
    }
    if(created&&shortcutRollbackDelete(cleanupProbe,
      ShortcutCleanupSubject::file,0,storagePath))created=false;
  }
public:
  std::wstring target;
  explicit ScopedShortcutFile(const fs::path& parent,const wchar_t* name,SECURITY_ATTRIBUTES& attributes,
    ShortcutFixtureFailure failure=ShortcutFixtureFailure::none,
    ShortcutCleanupProbe* externalCleanupProbe=nullptr)
    :parsingPath(shortcutLocalDriveName((parent/name).wstring())),
     storagePath(shortcutStoragePath(parsingPath)),
     cleanupProbe(externalCleanupProbe?externalCleanupProbe:&localCleanupProbe){
    try{
      // Keep the held handle compatible with installed.h's read-only open()
      // custody check. Cleanup closes before requesting DELETE by name.
      const HANDLE createdHandle=CreateFileW(storagePath.c_str(),
        GENERIC_READ|READ_CONTROL,FILE_SHARE_READ,&attributes,CREATE_NEW,
        FILE_ATTRIBUTE_NORMAL|FILE_FLAG_OPEN_REPARSE_POINT,nullptr);
      if(createdHandle!=INVALID_HANDLE_VALUE){
        file.h=createdHandle;
        created=true;
      }
      injectShortcutFixtureFailure(failure,ShortcutFixtureFailure::shortcut_file_after_create);
      const bool createSucceeded=win32Result("CreateFileW",createdHandle!=INVALID_HANDLE_VALUE)!=FALSE;
      if(!createSucceeded&&lastTestResultKnown&&lastTestResult==ERROR_FILE_EXISTS)
        throw ShortcutFixtureCollisionFailure(lastTestResult);
      require(createSucceeded);
      owned(file.h,ownerSID());
      injectShortcutFixtureFailure(failure,ShortcutFixtureFailure::shortcut_file_after_owned);
      const auto held=physical(file.h);
      require(shortcutStoragePath(held)==storagePath);
      injectShortcutFixtureFailure(failure,ShortcutFixtureFailure::shortcut_file_after_identity);
      injectShortcutFixtureAllocationFailure(failure,
        ShortcutFixtureFailure::shortcut_file_target_allocation);
      target=shortcutParsingTarget(parsingPath.wstring());
      injectShortcutFixtureFailure(failure,ShortcutFixtureFailure::shortcut_file_after_validation);
    }catch(...){
      rollbackNoexcept();
      throw;
    }
  }
  HANDLE custody()const{return file.h;}
  void cleanup(){
    const auto first=cleanupProbe->result.count;
    rollbackNoexcept();
    require(!cleanupProbe->result.overflow&&!cleanupProbe->invalidOverride
      &&!cleanupProbe->reportedFailures(first));
    parsingPath.clear();
    storagePath.clear();
  }
  ~ScopedShortcutFile()noexcept{rollbackNoexcept();}
};
struct ShortcutTargetTreePaths {
  std::array<fs::path,4> directories;
  std::array<fs::path,2> files;
};
static ShortcutTargetTreePaths shortcutTargetTreePaths(const Generation& g){
  ShortcutTargetTreePaths result{};
  auto path=fs::path(shortcutLocalDriveName((g.root/L"shortcut-targets").wstring()));
  result.directories[0]=path;
  for(size_t part=0;part<3;part++){
    path/=(std::wstring(96,wchar_t(L'a'+part%26)));
    result.directories[part+1]=path;
  }
  result.files[0]=path/L"expected-long.exe";
  result.files[1]=path/L"substituted-long.exe";
  return result;
}
static void requireShortcutFixturePathAbsent(const fs::path& path){
  SetLastError(ERROR_SUCCESS);
  const auto attributes=GetFileAttributesW(shortcutStoragePath(path).c_str());
  const auto error=GetLastError();
  require(attributes==INVALID_FILE_ATTRIBUTES
    &&(error==ERROR_FILE_NOT_FOUND||error==ERROR_PATH_NOT_FOUND));
}
static const ShortcutCleanupCall& shortcutCleanupCall(const ShortcutCleanupResult& result,
  ShortcutCleanupAction action,ShortcutCleanupSubject subject,size_t index){
  for(size_t position=0;position<result.count;++position){
    const auto& call=result.calls[position];
    if(call.action==action&&call.subject==subject&&call.index==index)return call;
  }
  require(false);
  return result.calls[0];
}
static void requireShortcutCleanupRefusal(const ShortcutCleanupProbe& probe,
  ShortcutCleanupAction action,ShortcutCleanupSubject subject,size_t index){
  const auto& call=shortcutCleanupCall(probe.result,action,subject,index);
  require(!probe.invalidOverride&&call.apiCalled&&call.apiResultKnown
    &&call.apiSucceeded&&call.apiStatus==ERROR_SUCCESS
    &&call.source==ShortcutCleanupSource::test_override
    &&!call.reportedSucceeded&&!call.reportedResultKnown
    &&call.reportedStatus==ERROR_SUCCESS);
}
static void requireShortcutCleanupWin32Success(const ShortcutCleanupProbe& probe,
  ShortcutCleanupAction action,ShortcutCleanupSubject subject,size_t index){
  const auto& call=shortcutCleanupCall(probe.result,action,subject,index);
  require(call.apiCalled&&call.apiResultKnown&&call.apiSucceeded
    &&call.apiStatus==ERROR_SUCCESS&&call.source==ShortcutCleanupSource::win32
    &&call.reportedResultKnown&&call.reportedSucceeded
    &&call.reportedStatus==ERROR_SUCCESS);
}
static void requireTargetTreeConstructorRollback(const Generation& g,
  SECURITY_ATTRIBUTES& attributes,ShortcutFixtureFailure failure,uint64_t end,
  ShortcutCleanupProbe* cleanupProbe=nullptr){
  installedBudget(end);
  bool exactFailure=false;
  try{
    ScopedShortcutTargetTree fixture(g,attributes,failure,cleanupProbe);
  }catch(const ShortcutFixtureInjectedFailure& error){
    exactFailure=error.point==failure;
  }catch(const ShortcutFixtureInjectedAllocationFailure& error){
    exactFailure=error.point==failure;
  }
  installedBudget(end);
  require(exactFailure);
  const auto paths=shortcutTargetTreePaths(g);
  for(const auto& file:paths.files)requireShortcutFixturePathAbsent(file);
  for(size_t index=paths.directories.size();index>0;--index)
    requireShortcutFixturePathAbsent(paths.directories[index-1]);
}
static void requireShortcutFileConstructorRollback(
  const fs::path& parent,SECURITY_ATTRIBUTES& attributes,
  ShortcutFixtureFailure failure,uint64_t end,
  ShortcutCleanupProbe* cleanupProbe=nullptr){
  installedBudget(end);
  const auto path=shortcutLocalDriveName((parent/L"helper.constructor.exe").wstring());
  require(path.size()>MAX_PATH&&shortcutStoragePath(path).wstring().size()>MAX_PATH);
  bool exactFailure=false;
  try{
    ScopedShortcutFile fixture(parent,L"helper.constructor.exe",attributes,failure,cleanupProbe);
  }catch(const ShortcutFixtureInjectedFailure& error){
    exactFailure=error.point==failure;
  }catch(const ShortcutFixtureInjectedAllocationFailure& error){
    exactFailure=error.point==failure;
  }
  installedBudget(end);
  require(exactFailure);
  requireShortcutFixturePathAbsent(path);
}
static void shortcutCleanupResultContracts(const Generation& g,
  SECURITY_ATTRIBUTES& attributes,uint64_t end){
  stage("child_shortcut_cleanup_close_delete_results");
  {
    ScopedShortcutTargetTree parentTree(g,attributes);
    const auto parent=parentTree.longFileParent();
    for(const auto refusal:{
      std::pair<ShortcutCleanupAction,const wchar_t*>(
        ShortcutCleanupAction::close_handle,L"helper.cleanup-close.exe"),
      std::pair<ShortcutCleanupAction,const wchar_t*>(
        ShortcutCleanupAction::delete_file,L"helper.cleanup-delete.exe")}){
      installedBudget(end);
      ShortcutCleanupProbe probe;
      probe.refuse(refusal.first,ShortcutCleanupSubject::file,0);
      const auto path=shortcutLocalDriveName((parent/refusal.second).wstring());
      require(path.size()>MAX_PATH&&shortcutStoragePath(path).wstring().size()>MAX_PATH);
      {
        ScopedShortcutFile fixture(parent,refusal.second,attributes,
          ShortcutFixtureFailure::none,&probe);
      }
      requireShortcutCleanupRefusal(probe,refusal.first,ShortcutCleanupSubject::file,0);
      const auto other=refusal.first==ShortcutCleanupAction::close_handle
        ?ShortcutCleanupAction::delete_file:ShortcutCleanupAction::close_handle;
      requireShortcutCleanupWin32Success(probe,other,ShortcutCleanupSubject::file,0);
      requireShortcutFixturePathAbsent(path);
      installedBudget(end);
    }
    parentTree.cleanup();
  }
  stage("child_shortcut_cleanup_remove_and_order");
  ShortcutCleanupProbe treeProbe;
  treeProbe.refuse(ShortcutCleanupAction::remove_directory,ShortcutCleanupSubject::directory,3);
  {
    ScopedShortcutTargetTree fixture(g,attributes,ShortcutFixtureFailure::none,&treeProbe);
  }
  require(treeProbe.result.count==12&&!treeProbe.result.overflow);
  for(size_t index=0;index<2;++index){
    const auto& close=treeProbe.result.calls[index];
    require(close.action==ShortcutCleanupAction::close_handle
      &&close.subject==ShortcutCleanupSubject::file&&close.index==index);
  }
  for(size_t index=0;index<4;++index){
    const auto& close=treeProbe.result.calls[2+index];
    require(close.action==ShortcutCleanupAction::close_handle
      &&close.subject==ShortcutCleanupSubject::directory&&close.index==index);
  }
  for(size_t index=0;index<2;++index){
    const auto& remove=treeProbe.result.calls[6+index];
    require(remove.action==ShortcutCleanupAction::delete_file
      &&remove.subject==ShortcutCleanupSubject::file&&remove.index==1-index);
  }
  for(size_t index=0;index<4;++index){
    const auto& remove=treeProbe.result.calls[8+index];
    require(remove.action==ShortcutCleanupAction::remove_directory
      &&remove.subject==ShortcutCleanupSubject::directory&&remove.index==3-index);
  }
  for(size_t index=0;index<2;++index)
    requireShortcutCleanupWin32Success(treeProbe,ShortcutCleanupAction::close_handle,
      ShortcutCleanupSubject::file,index);
  for(size_t index=0;index<2;++index)
    requireShortcutCleanupWin32Success(treeProbe,ShortcutCleanupAction::delete_file,
      ShortcutCleanupSubject::file,1-index);
  for(size_t index=0;index<4;++index)
    requireShortcutCleanupWin32Success(treeProbe,ShortcutCleanupAction::close_handle,
      ShortcutCleanupSubject::directory,index);
  requireShortcutCleanupRefusal(treeProbe,ShortcutCleanupAction::remove_directory,
    ShortcutCleanupSubject::directory,3);
  for(size_t index=0;index<3;++index)
    requireShortcutCleanupWin32Success(treeProbe,ShortcutCleanupAction::remove_directory,
      ShortcutCleanupSubject::directory,2-index);
  require(treeProbe.reportedFailures(0));
  const auto paths=shortcutTargetTreePaths(g);
  for(const auto& file:paths.files)requireShortcutFixturePathAbsent(file);
  for(size_t index=paths.directories.size();index>0;--index)
    requireShortcutFixturePathAbsent(paths.directories[index-1]);
  installedBudget(end);
  stage("child_shortcut_cleanup_primary_error_preserved");
  ShortcutCleanupProbe primaryProbe;
  primaryProbe.refuse(ShortcutCleanupAction::close_handle,ShortcutCleanupSubject::file,1);
  primaryProbe.refuse(ShortcutCleanupAction::delete_file,ShortcutCleanupSubject::file,1);
  primaryProbe.refuse(ShortcutCleanupAction::remove_directory,ShortcutCleanupSubject::directory,3);
  bool exactAllocationFailure=false;
  try{
    ScopedShortcutTargetTree fixture(g,attributes,
      ShortcutFixtureFailure::substitute_target_allocation,&primaryProbe);
  }catch(const ShortcutFixtureInjectedAllocationFailure& error){
    exactAllocationFailure=error.point==ShortcutFixtureFailure::substitute_target_allocation;
  }
  installedBudget(end);
  require(exactAllocationFailure&&!primaryProbe.invalidOverride
    &&primaryProbe.reportedFailures(0));
  requireShortcutCleanupRefusal(primaryProbe,ShortcutCleanupAction::close_handle,
    ShortcutCleanupSubject::file,1);
  requireShortcutCleanupRefusal(primaryProbe,ShortcutCleanupAction::delete_file,
    ShortcutCleanupSubject::file,1);
  requireShortcutCleanupRefusal(primaryProbe,ShortcutCleanupAction::remove_directory,
    ShortcutCleanupSubject::directory,3);
  for(const auto& file:paths.files)requireShortcutFixturePathAbsent(file);
  for(size_t index=paths.directories.size();index>0;--index)
    requireShortcutFixturePathAbsent(paths.directories[index-1]);
}
static void shortcutConstructorCleanupContracts(const Generation& g,uint64_t end){
  Descriptor descriptor(ownACL(L"FA"));
  auto attributes=descriptor.attributes();
  shortcutCleanupResultContracts(g,attributes,end);
  stage("child_shortcut_target_tree_constructor_rollback");
  for(const auto failure:{
    ShortcutFixtureFailure::directory_after_create,
    ShortcutFixtureFailure::directory_after_open,
    ShortcutFixtureFailure::directory_after_owned,
    ShortcutFixtureFailure::directory_after_identity,
    ShortcutFixtureFailure::second_directory_after_create,
    ShortcutFixtureFailure::second_directory_after_open,
    ShortcutFixtureFailure::second_directory_after_owned,
    ShortcutFixtureFailure::second_directory_after_identity,
    ShortcutFixtureFailure::expected_file_after_create,
    ShortcutFixtureFailure::expected_file_after_owned,
    ShortcutFixtureFailure::expected_file_after_identity,
    ShortcutFixtureFailure::substitute_file_after_create,
    ShortcutFixtureFailure::substitute_file_after_owned,
    ShortcutFixtureFailure::substitute_file_after_identity,
    ShortcutFixtureFailure::target_tree_after_validation,
    ShortcutFixtureFailure::expected_target_allocation,
    ShortcutFixtureFailure::substitute_target_allocation})
    requireTargetTreeConstructorRollback(g,attributes,failure,end);
  stage("child_shortcut_file_constructor_rollback");
  {
    ScopedShortcutTargetTree parentTree(g,attributes);
    for(const auto failure:{
      ShortcutFixtureFailure::shortcut_file_after_create,
      ShortcutFixtureFailure::shortcut_file_after_owned,
      ShortcutFixtureFailure::shortcut_file_after_identity,
      ShortcutFixtureFailure::shortcut_file_target_allocation,
      ShortcutFixtureFailure::shortcut_file_after_validation})
      requireShortcutFileConstructorRollback(parentTree.longFileParent(),attributes,failure,end);
    parentTree.cleanup();
  }
  stage("child_shortcut_constructor_foreign_collision");
  {
    ScopedShortcutFile foreign(g.root,L"helper.foreign.exe",attributes);
    bool collisionRefused=false;
    try{
      ScopedShortcutFile collision(g.root,L"helper.foreign.exe",attributes);
    }catch(const ShortcutFixtureCollisionFailure& error){
      collisionRefused=error.status==ERROR_FILE_EXISTS&&!strcmp(lastTestAPI,"CreateFileW")
        &&lastTestResultKnown&&lastTestResult==ERROR_FILE_EXISTS;
    }
    installedBudget(end);
    require(collisionRefused&&lastTestResultKnown
      &&lastTestResult==ERROR_FILE_EXISTS&&GetFileAttributesW(
      shortcutStoragePath(foreign.target).c_str())!=INVALID_FILE_ATTRIBUTES);
    foreign.cleanup();
    requireShortcutFixturePathAbsent(shortcutLocalDriveName(
      (g.root/L"helper.foreign.exe").wstring()));
  }
  std::cout<<"TEST shortcut constructor cleanup ownership passed"
    <<" transactional=true original_failure_preserved=true"
    <<" allocation_failure_preserved=true created_only=true"
    <<" foreign_collision_preserved=true"
    <<" rollback_storage=extended cleanup_results=close+delete+remove"
    <<" cleanup_primary_error_preserved=true order=handles_before_delete_deepest_first"
    <<" failures=multi_directory+second_file+post_create+open_owned_identity"
    <<" +member_allocation+later_validation exactAPI=unknown\n";
}
static void requireTargetSubstitutionRefused(const ShortcutFixture& expected,ShortcutFixture substitute,uint64_t end){
  installedBudget(end);
  const auto substitutedBytes=configuredShortcutBytes(substitute,end);
  shortcutReadbackBytes(substitute,substitutedBytes,end);
  installedBudget(end);
  bool refused=false;
  try{
    shortcutReadbackBytes(expected,substitutedBytes,end);
  }catch(const ShortcutTargetMismatch&){
    refused=true;
  }
  installedBudget(end);
  require(refused);
}
static void requireSyntheticTargetUnavailable(const ShortcutFixture& prototype,uint64_t end){
  const auto target=syntheticLongShortcutTarget(prototype);
  ScopedPidl parsed;
  bool unavailable=false;
  HRESULT syntheticStatus=E_FAIL;
  try{
    parseShortcutTarget({target,prototype.args,prototype.aumid,prototype.clsid,
      INVALID_HANDLE_VALUE},parsed,end);
  }catch(const ShortcutApiFailure& error){
    syntheticStatus=error.status;
    unavailable=std::string(error.api)=="SHParseDisplayName"
      &&error.statusKnown&&FAILED(error.status)&&!parsed.value;
  }
  require(unavailable&&GetFileAttributesW(shortcutStoragePath(target).c_str())==INVALID_FILE_ATTRIBUTES
    &&GetLastError()==ERROR_FILE_NOT_FOUND);
  std::cout<<"TEST synthetic shortcut target classified unavailable"
    <<" exists=false api=SHParseDisplayName typed_api_failure=true"
    <<" status_known=true result="<<DWORD(syntheticStatus)
    <<" pidl_fabricated=false exactAPI=SHParseDisplayName\n";
}
static std::vector<uint8_t> legacySetPathShortcutBytes(
 const ShortcutFixture& fixture,uint64_t end){
  shortcutBudget(end);
  const auto target=shortcutParsingTarget(fixture.target);
  Microsoft::WRL::ComPtr<IShellLinkW> link;
  shortcutHresult("CoCreateInstance(CLSID_ShellLink)",
    CoCreateInstance(CLSID_ShellLink,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&link)));
  shortcutHresult("IShellLinkW::SetPath",link->SetPath(target.c_str()));
  shortcutHresult("IShellLinkW::SetArguments",link->SetArguments(fixture.args.c_str()));
  Microsoft::WRL::ComPtr<IPropertyStore> properties;
  shortcutHresult("IShellLinkW::QueryInterface(IPropertyStore)",link.As(&properties));
  ScopedPropVariant value;
  shortcutHresult("InitPropVariantFromString",
    InitPropVariantFromString(fixture.aumid.c_str(),&value.value));
  shortcutHresult("IPropertyStore::SetValue(PKEY_AppUserModel_ID)",
    properties->SetValue(PKEY_AppUserModel_ID,value.value));
  value.clear(end);
  GUID clsid{};
  shortcutHresult("CLSIDFromString",CLSIDFromString(fixture.clsid.c_str(),&clsid));
  shortcutHresult("InitPropVariantFromCLSID",InitPropVariantFromCLSID(clsid,&value.value));
  shortcutHresult("IPropertyStore::SetValue(PKEY_AppUserModel_ToastActivatorCLSID)",
    properties->SetValue(PKEY_AppUserModel_ToastActivatorCLSID,value.value));
  value.clear(end);
  shortcutHresult("IPropertyStore::Commit",properties->Commit());
  shortcutBudget(end);
  return linkBytes(link.Get(),end);
}
static void shortcutContracts(const Generation& g,uint64_t end){
  stage("child_shortcut_com");
  Apartment apartment;
  installedBudget(end);
  shortcutApiFailureContract();
  installedBudget(end);
  shortcutResultClassification();
  installedBudget(end);
  shortcutOwnershipContracts();
  installedBudget(end);
  shortcutConstructorCleanupContracts(g,end);
  installedBudget(end);
  stage("child_shortcut_path_contract");
  const auto expected=g.root.wstring().substr(4)+L"\\helper.exe";
  installedBudget(end);
  require(shellTargetPath(g)==expected&&!expected.starts_with(L"\\\\?\\"));
  require(g.helperFile.h!=INVALID_HANDLE_VALUE
    &&physical(g.helperFile.h)==(g.root/L"helper.exe").wstring());
  installedBudget(end);
  stage("child_shortcut_property_serialization");
  const auto shortFixture=shortcutFixture(g);
  installedBudget(end);
  const auto shortBytes=configuredShortcutBytes(shortFixture,end);
  shortcutReadbackBytes(shortFixture,shortBytes,end);
  installedBudget(end);
  stage("child_shortcut_legacy_reader_compatibility");
  const auto legacyBytes=legacySetPathShortcutBytes(shortFixture,end);
  shortcutReadbackBytes(shortFixture,legacyBytes,end);
  installedBudget(end);
  stage("child_shortcut_deadline_inside_serialization");
  requireShortcutDeadlineAfterApi(end,"IPersistStream::Save",
    "serialization_after_IPersistStream_Save",[&](ShortcutDeadlineProbe& probe){
    configuredShortcutBytes(shortFixture,end,&probe);
  });
  installedBudget(end);
  stage("child_shortcut_deadline_inside_readback");
  requireShortcutDeadlineAfterApi(end,"IPersistStream::Load",
    "readback_after_IPersistStream_Load",[&](ShortcutDeadlineProbe& probe){
    shortcutReadbackBytes(shortFixture,shortBytes,end,&probe);
  });
  installedBudget(end);
  stage("child_shortcut_short_substitution_refused");
  {
    Descriptor descriptor(ownACL(L"FA"));
    auto attributes=descriptor.attributes();
    ScopedShortcutFile shortSubstitute(g.root,L"helper.substituted.exe",attributes);
    requireTargetSubstitutionRefused(shortFixture,
      substituted(shortFixture,std::wstring(shortSubstitute.target),shortSubstitute.custody()),end);
    shortSubstitute.cleanup();
    require(GetFileAttributesW(shortcutStoragePath(g.root/L"helper.substituted.exe").c_str())==INVALID_FILE_ATTRIBUTES
      &&GetLastError()==ERROR_FILE_NOT_FOUND);
  }
  stage("child_shortcut_synthetic_target_classification");
  requireSyntheticTargetUnavailable(shortFixture,end);
  stage("child_shortcut_long_target_round_trip");
  installedBudget(end);
  {
    Descriptor descriptor(ownACL(L"FA"));
    auto attributes=descriptor.attributes();
    ScopedShortcutTargetTree longTargets(g,attributes);
    const auto longFixture=substituted(shortFixture,
      std::wstring(longTargets.expectedTarget),longTargets.expectedCustody());
    const auto longSubstitute=substituted(shortFixture,
      std::wstring(longTargets.substituteTarget),longTargets.substituteCustody());
    require(longFixture.target.size()>MAX_PATH);
    const auto longBytes=configuredShortcutBytes(longFixture,end);
    shortcutReadbackBytes(longFixture,longBytes,end);
    installedBudget(end);
    stage("child_shortcut_long_substitution_refused");
    requireTargetSubstitutionRefused(longFixture,longSubstitute,end);
    longTargets.cleanup();
    require(GetFileAttributesW(shortcutStoragePath(longFixture.target).c_str())==INVALID_FILE_ATTRIBUTES
      &&GetLastError()==ERROR_FILE_NOT_FOUND);
    require(GetFileAttributesW(shortcutStoragePath(longSubstitute.target).c_str())==INVALID_FILE_ATTRIBUTES
      &&GetLastError()==ERROR_FILE_NOT_FOUND);
  }
  // The former expired-on-entry assertion accepted any std::runtime_error and
  // could therefore turn an unrelated contract failure into a false PASS.
  // The exact post-API tests above retain deterministic deadline coverage and
  // deliberately let every unrelated failure escape.
  installedBudget(end);
  std::cout<<"TEST shortcut contract passed"
    <<" path=dos+exact-parsing-name+held-custody short_round_trip=true"
    <<" actual_long_round_trip=true synthetic_target_unavailable=true"
    <<" target_substitution_refused=true properties=vt_lpwstring+vt_clsid"
      <<" persistence=ipersiststream legacy_setpath_reader_compatible=true"
      <<" cleanup=co_task_mem+pidl+global_lock"
      <<" constructor_cleanup=transactional+created_only+foreign_preserved"
      <<" rollback_storage=extended+cleanup_results_observable"
      <<" test_target_cleanup=true"
    <<" deadline_inside_serialization_after_api=true"
    <<" deadline_inside_readback_after_api=true"
    <<" deadline_boundaries=post_API_Save+post_API_Load exactAPI=unknown\n";
}
static void writePrivate(const fs::path& path,const std::vector<uint8_t>& data,SECURITY_ATTRIBUTES& sa){
  Handle file(CreateFileW(path.c_str(),GENERIC_WRITE,0,&sa,CREATE_NEW,FILE_ATTRIBUTE_NORMAL,nullptr));
  require(file.h!=INVALID_HANDLE_VALUE);
  DWORD written=0;
  require(win32Result("WriteFile",WriteFile(file.h,data.data(),DWORD(data.size()),&written,nullptr))&&written==data.size());
  require(win32Result("FlushFileBuffers",FlushFileBuffers(file.h)));
  file.closeChecked();
}
static void prepareAndCollect(){
  stage("parent_paths");
  wchar_t runner[32768]{},module[32768]{};
  const auto runnerSize=GetEnvironmentVariableW(L"RUNNER_TEMP",runner,32768);
  const auto moduleSize=GetModuleFileNameW(nullptr,module,32768);
  require(runnerSize&&runnerSize<32768&&moduleSize&&moduleSize<32768);
  stage("parent_temp_custody");
  auto base=open(runner,true);
  const auto root=fs::path(physical(base.h))/(L"TEST-registration-"+wide(randomID()));
  Descriptor descriptor(ownACL(L"FA"));
  auto sa=descriptor.attributes();
  stage("parent_private_root");
  require(win32Result("CreateDirectoryW",CreateDirectoryW(root.c_str(),&sa)));
  auto directory=open(root,true);
  for(const auto leaf:{L"records",L"attempts"})require(win32Result("CreateDirectoryW",CreateDirectoryW((root/leaf).c_str(),&sa)));
  stage("parent_copy_image");
  auto original=open(module,false);
  const auto image=bytes(original.h,16*1024*1024);
  writePrivate(root/L"helper.exe",image,sa);
  Snapshot snapshot{randomID(),narrow(root.wstring()),ownerSID(),sha(image),"AgentNotifications.TEST.Registry",
    "{01234567-89ab-cdef-0123-456789abcdef}","OpenAI.Codex","CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B",
    "OpenAI.Codex_2p2nqsd0c76g0","OpenAI.Codex_1.2.3.4_x64__2p2nqsd0c76g0"};
  stage("parent_snapshot_validate");
  snapshot.validate();
  stage("parent_snapshot_publish");
  writePrivate(root/L"generation.wne",encode(1,snapshot.fields()),sa);
  auto commandLine=L"\""+(root/L"helper.exe").wstring()+L"\" --registry-child";
  STARTUPINFOW startup{sizeof(startup)};
  PROCESS_INFORMATION child{};
  startup.dwFlags=STARTF_USESTDHANDLES;
  startup.hStdInput=GetStdHandle(STD_INPUT_HANDLE);
  startup.hStdOutput=GetStdHandle(STD_OUTPUT_HANDLE);
  startup.hStdError=GetStdHandle(STD_ERROR_HANDLE);
  // Only the TEST actor's standard streams are inheritable; fixture handles
  // and protected file/registry handles were all created non-inheritable.
  stage("parent_stream_handles");
  if(startup.hStdInput&&startup.hStdInput!=INVALID_HANDLE_VALUE)
    require(win32Result("SetHandleInformation",SetHandleInformation(startup.hStdInput,HANDLE_FLAG_INHERIT,HANDLE_FLAG_INHERIT)));
  else startup.hStdInput=nullptr;
  for(const auto handle:{startup.hStdOutput,startup.hStdError})
    require(handle&&handle!=INVALID_HANDLE_VALUE&&win32Result("SetHandleInformation",SetHandleInformation(handle,HANDLE_FLAG_INHERIT,HANDLE_FLAG_INHERIT)));
  stage("parent_child_start");
  require(win32Result("CreateProcessW",CreateProcessW((root/L"helper.exe").c_str(),commandLine.data(),nullptr,nullptr,TRUE,0,nullptr,nullptr,&startup,&child)));
  Handle process(child.hProcess),thread(child.hThread);
  thread.closeChecked();
  stage("parent_child_wait");
  auto wait=WaitForSingleObject(process.h,40000);
  if(wait==WAIT_TIMEOUT){require(win32Result("TerminateProcess",TerminateProcess(process.h,124)));
  wait=WaitForSingleObject(process.h,5000);
  }
  if(wait==WAIT_FAILED){
    const auto error=GetLastError();
    registryStatus("WaitForSingleObject.error",LSTATUS(error));
    require(false);
  }
  registryStatus("WaitForSingleObject.result",LSTATUS(wait));
  require(wait==WAIT_OBJECT_0);
  stage("parent_child_exit");
  DWORD exit=1;
  require(win32Result("GetExitCodeProcess",GetExitCodeProcess(process.h,&exit)));
  process.closeChecked();
  stage("parent_filesystem_cleanup");
  directory.closeChecked();
  require(win32Result("DeleteFileW",DeleteFileW((root/L"generation.wne").c_str()))&&win32Result("DeleteFileW",DeleteFileW((root/L"helper.exe").c_str())));
  require(win32Result("RemoveDirectoryW",
    RemoveDirectoryW((root/L"records").c_str()))&&win32Result("RemoveDirectoryW",RemoveDirectoryW((root/L"attempts").c_str()))&&win32Result("RemoveDirectoryW",RemoveDirectoryW(root.c_str())));
  stage("parent_child_exit_contract");
  registryStatus("child_exit",LSTATUS(exit));
  require(exit==0);
}
int wmain(int argc,wchar_t** argv){
  try{
    wchar_t actions[8]{};
    require(GetEnvironmentVariableW(L"GITHUB_ACTIONS",actions,8)==4&&!wcscmp(actions,L"true"));
    const bool child=argc==2&&!wcscmp(argv[1],L"--registry-child");
    require(argc==1||child);
    std::thread([limit=GetTickCount64()+(child?35000:50000)]{
      while(GetTickCount64()<limit)Sleep(10);ExitProcess(124);
    }).detach();
    if(child)return registryContracts()?0:77;
    prepareAndCollect();
    return 0;
  }catch(const ShortcutContractViolation& error){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known="<<error.statusKnown
      <<" last_observed_test_api="<<error.api
      <<" last_observed_test_result_known="<<error.statusKnown
      <<" last_observed_test_result="<<DWORD(error.status)<<" exactAPI=unknown"
      <<" reason=shortcut_contract_violation\n";
    return 1;
  }catch(const ShortcutApiFailure& error){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known=true last_observed_test_api="<<error.api
      <<" last_observed_test_result_known=true"
      <<" last_observed_test_result="<<DWORD(error.code().value)
      <<" exactAPI=unknown"
      <<" reason=shortcut_api_failure\n";
    return 1;
  }catch(const ShortcutDeadlineExceeded& error){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known=true last_observed_test_api="<<error.api
      <<" last_observed_test_result_known=false"
      <<" last_observed_test_result=0 exactAPI=unknown"
      <<" reason=shortcut_deadline_exceeded boundary="<<error.boundary
      <<" deadline_end="<<error.end<<" deadline_observed="<<error.observed<<"\n";
    return 1;
  }catch(const std::exception&){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known=false last_observed_test_api="<<lastTestAPI
      <<" last_observed_test_result_known="<<lastTestResultKnown
      <<" last_observed_test_result="<<lastTestResult<<" exactAPI=unknown"
      <<" reason=contract_rejected\n";
  return 1;
  }
}
