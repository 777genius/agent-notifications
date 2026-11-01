#pragma once
#include "custody.h"
#include "capacity.h"
#include "operator_diagnostic.h"
#include <shlobj.h>
#include <propkey.h>
#include <propvarutil.h>
#include <wrl/client.h>
#include <winrt/Windows.Data.Xml.Dom.h>
#include <winrt/Windows.UI.Notifications.h>
#include <iostream>
#include <array>
#include <cstring>
#include <exception>
#pragma comment(lib,"Shell32.lib")
#pragma comment(lib,"Propsys.lib")
namespace wcb {
  inline void installedBudget(uint64_t end,OperatorDiagnostic* diagnostic=nullptr){
    if(diagnostic)diagnostic->enter("operator_deadline");
    require(GetTickCount64()<end);
  }
  struct Key {
    HKEY h=nullptr;
    ~Key(){
      if(h)RegCloseKey(h);
    }
    void close(){
      auto k=h;
      h=nullptr;
      require(RegCloseKey(k)==ERROR_SUCCESS);
    }
  };
  inline std::wstring stamp(const Generation& g){
    return L"WinEnvelope1:"+wide(g.snapshot.generation)+L":"+wide(g.snapshotDigest);
  }
  inline std::wstring command(const Generation& g){
    return L"\""+(g.root/L"helper.exe").wstring()+L"\" --callback";
  }
  // C++/WinRT's hresult_error is deliberately independent of std::exception.
  // Keep both public exception contracts: production's WinRT catch preserves
  // the HRESULT, while TEST and older std::exception paths retain what().
  struct ShortcutApiFailure : winrt::hresult_error, std::exception {
    const char* api;
    HRESULT status;
    bool statusKnown=true;
    static constexpr bool apiFailure=true;
    std::string detail;
    ShortcutApiFailure(const char* name,HRESULT status)
      :winrt::hresult_error(status,winrt::to_hstring(std::string("shortcut API ")+name+" failed")),
       api(name),status(status),detail(std::string("shortcut API ")+name+" failed"){}
    const char* what()const noexcept override{return detail.c_str();}
  };
  struct ShortcutContractViolation : std::runtime_error {
    const char* api;
    HRESULT status;
    bool statusKnown=true;
    static constexpr bool apiFailure=false;
    std::string detail;
    ShortcutContractViolation(const char* name,HRESULT value,bool known=true)
      :std::runtime_error("shortcut result contract violation"),
       api(name),status(value),statusKnown(known),
       detail(std::string("shortcut result contract violation api=")+name+
         " status="+std::to_string(static_cast<long long>(value))+
         " statusKnown="+(known?"true":"false")){}
    const char* what()const noexcept override{return detail.c_str();}
  };
#ifdef WCB_TEST_SHORTCUT_DEADLINE_PROBES
  struct ShortcutDeadlineExceeded : std::runtime_error {
    std::string api,boundary,detail;
    uint64_t end,observed;
    ShortcutDeadlineExceeded(const char* api,const char* boundary,uint64_t end,uint64_t observed)
      :std::runtime_error("shortcut deadline exceeded"),api(api),boundary(boundary),
       detail(std::string("shortcut deadline exceeded api=")+api+
         " boundary="+boundary+" end="+std::to_string(end)+
         " observed="+std::to_string(observed)),end(end),observed(observed){}
    const char* what()const noexcept override{return detail.c_str();}
  };
  inline void shortcutBudgetSample(uint64_t end,uint64_t observed,const char* api,const char* boundary){
    if(end&&observed>=end)throw ShortcutDeadlineExceeded(api,boundary,end,observed);
  }
#endif
  // Zero retains the existing read-only helper entry points. Production apply
  // and TEST paths carry the operator deadline through every new overload.
  inline void shortcutBudget(uint64_t end){
    if(end)installedBudget(end);
  }
#ifdef WCB_TEST_SHORTCUT_DEADLINE_PROBES
  // TEST uses this explicit monotonic-sample seam to make one post-API budget
  // boundary deterministic without sleeping or racing GetTickCount64. The seam
  // applies the same strict observed>=end expiry predicate at that exact check;
  // production shortcutBudget remains the unchanged installedBudget path.
  struct ShortcutDeadlineProbe {
    const char* api,*boundary;
    bool observed=false;
    ShortcutDeadlineProbe(const char* api,const char* boundary)noexcept:api(api),boundary(boundary){}
  };
  inline void shortcutDeadlineAfterApi(uint64_t end,const char* api,const char* boundary,ShortcutDeadlineProbe* probe){
    if(!probe){
      shortcutBudget(end);
      return;
    }
    require(end&&!probe->observed&&std::strcmp(probe->api,api)==0&&std::strcmp(probe->boundary,boundary)==0);
    probe->observed=true;
    shortcutBudgetSample(end,end,api,boundary);
  }
#define WCB_SHORTCUT_DEADLINE_TEST_PARAM ,ShortcutDeadlineProbe* deadlineProbe=nullptr
#define WCB_SHORTCUT_DEADLINE_TEST_ARG ,deadlineProbe
#define WCB_SHORTCUT_AFTER_API(end,api,boundary,probe) shortcutDeadlineAfterApi(end,api,boundary,probe)
#else
#define WCB_SHORTCUT_DEADLINE_TEST_PARAM
#define WCB_SHORTCUT_DEADLINE_TEST_ARG
#define WCB_SHORTCUT_AFTER_API(end,api,boundary,probe) shortcutBudget(end)
#endif
  inline void shortcutHresult(const char* api,HRESULT status){
    if(FAILED(status))throw ShortcutApiFailure(api,status);
    if(status!=S_OK)throw ShortcutContractViolation(api,status);
  }
  // A successful HRESULT is not an API failure. S_FALSE and successful calls
  // with absent output are result-contract violations with their exact status.
  inline void shortcutContractResult(const char* api,HRESULT status,bool outputPresent){
    shortcutHresult(api,status);
    if(!outputPresent)throw ShortcutContractViolation(api,status);
  }
  inline bool shortcutPidlPresent(PIDLIST_ABSOLUTE value)noexcept{
    return value&&!ILIsEmpty(value);
  }
  inline bool shortcutNamePresent(const wchar_t* value)noexcept{
    return value&&*value;
  }
  // API-owned outputs are adopted before any status classification, budget
  // check or allocation can throw. Cleanup destructors are finite and do not
  // participate in the result/error contract.
  struct ScopedCoTaskMem {
    PWSTR value=nullptr;
    ScopedCoTaskMem()=default;
    explicit ScopedCoTaskMem(PWSTR owned)noexcept:value(owned){}
    ScopedCoTaskMem(const ScopedCoTaskMem&)=delete;
    ScopedCoTaskMem& operator=(const ScopedCoTaskMem&)=delete;
    ~ScopedCoTaskMem()noexcept{if(value)CoTaskMemFree(value);}
  };
  struct ScopedPidl {
    PIDLIST_ABSOLUTE value=nullptr;
    ScopedPidl()=default;
    explicit ScopedPidl(PIDLIST_ABSOLUTE owned)noexcept:value(owned){}
    ScopedPidl(const ScopedPidl&)=delete;
    ScopedPidl& operator=(const ScopedPidl&)=delete;
    ~ScopedPidl()noexcept{if(value)ILFree(value);}
  };
  struct ScopedGlobalLock {
    HGLOBAL memory=nullptr;
    void* value=nullptr;
    explicit ScopedGlobalLock(HGLOBAL owned)noexcept:memory(owned),value(GlobalLock(owned)){}
    ScopedGlobalLock(const ScopedGlobalLock&)=delete;
    ScopedGlobalLock& operator=(const ScopedGlobalLock&)=delete;
    ~ScopedGlobalLock()noexcept{if(value)GlobalUnlock(memory);}
  };
  struct ShortcutTargetMismatch : std::runtime_error {
    ShortcutTargetMismatch():std::runtime_error("shortcut target mismatch"){}
  };
  inline void verifyShortcutTarget(const std::wstring& expected,const std::wstring& actual){
    if(actual!=expected)throw ShortcutTargetMismatch();
  }
  inline std::wstring shellTargetPath(const Generation& g){
    const auto custody=(g.root/L"helper.exe").wstring();
    require(custody.size()>4&&custody.starts_with(L"\\\\?\\"));
    auto target=custody.substr(4);
    require(target.size()>3&&target[1]==L':'&&target[2]==L'\\'&&target.back()!=L'\\');
    return target;
  }
  inline fs::path extendedShortcutTarget(const std::wstring& target){
    require(target.size()>3&&target[1]==L':'&&target[2]==L'\\'&&target.back()!=L'\\'
      &&!target.starts_with(L"\\\\?\\")&&target.find(L':',2)==std::wstring::npos);
    return fs::path(L"\\\\?\\"+target);
  }
  struct ShortcutFixture {
    std::wstring target,args,aumid,clsid;
    HANDLE custody=INVALID_HANDLE_VALUE;
  };
  inline ShortcutFixture shortcutFixture(const Generation& g){
    return {shellTargetPath(g),L"--callback",wide(g.snapshot.aumid),wide(g.snapshot.clsid),g.helperFile.h};
  }
  // The expected parsing name is checked against the same no-reparse file that
  // Generation or TEST holds. File identity prevents a same-name replacement
  // from being adopted as the authorized target. The fixture target is always
  // a real filesystem item; nonexistent display names remain an explicit
  // unsupported-target case rather than being fabricated into a PIDL.
  inline void verifyShortcutTargetCustody(const ShortcutFixture& fixture,uint64_t end=0){
    shortcutBudget(end);
    require(fixture.custody!=INVALID_HANDLE_VALUE);
    const auto heldPath=physical(fixture.custody);
    require(heldPath==fixture.target||heldPath==L"\\\\?\\"+fixture.target);
    shortcutBudget(end);
    BY_HANDLE_FILE_INFORMATION heldInfo{
    };
    require(GetFileInformationByHandle(fixture.custody,&heldInfo));
    shortcutBudget(end);
    auto candidate=open(extendedShortcutTarget(fixture.target),false);
    shortcutBudget(end);
    BY_HANDLE_FILE_INFORMATION candidateInfo{
    };
    require(GetFileInformationByHandle(candidate.h,&candidateInfo));
    require(!(heldInfo.dwFileAttributes&FILE_ATTRIBUTE_DIRECTORY)
      &&!(candidateInfo.dwFileAttributes&FILE_ATTRIBUTE_DIRECTORY)
      &&heldInfo.dwVolumeSerialNumber==candidateInfo.dwVolumeSerialNumber
      &&heldInfo.nFileIndexHigh==candidateInfo.nFileIndexHigh
      &&heldInfo.nFileIndexLow==candidateInfo.nFileIndexLow);
    shortcutBudget(end);
  }
  inline fs::path shortcutPath(const Generation& g,uint64_t end=0){
    shortcutBudget(end);
    ScopedCoTaskMem path;
    const auto status=SHGetKnownFolderPath(FOLDERID_Programs,0,nullptr,&path.value);
    // The guard owns even an unexpected failure output. Classify the returned
    // status before checking the deadline so API/result semantics are retained.
    shortcutContractResult("SHGetKnownFolderPath",status,shortcutNamePresent(path.value));
    shortcutBudget(end);
    fs::path result=fs::path(path.value)/(L"AgentNotifications-"+wide(g.snapshot.generation)+L".lnk");
    shortcutBudget(end);
    return result;
  }
  inline std::wstring value(HKEY key,const wchar_t* name){
    DWORD type=0,n=0;
    require(RegQueryValueExW(key,name,nullptr,&type,nullptr,&n)==ERROR_SUCCESS&&type==REG_SZ&&n>=2&&n<=65536&&n%2==0);
    std::wstring s(n/2,L'\0');
    DWORD size=n;
    require(RegQueryValueExW(key,name,nullptr,&type,reinterpret_cast<BYTE*>(s.data()),&size)==ERROR_SUCCESS&&type==REG_SZ&&size==n&&s.back()==0&&s.find(L'\0')==s.size()-1);
    s.pop_back();
    return s;
  }
  inline void put(HKEY key,const wchar_t* name,const std::wstring& s,uint64_t end){
    installedBudget(end);
    require(RegSetValueExW(key,name,0,REG_SZ,reinterpret_cast<const BYTE*>(s.c_str()),DWORD((s.size()+1)*2))==ERROR_SUCCESS);
    installedBudget(end);
  }
  inline void regularRegistryKey(HKEY key,OperatorDiagnostic* diagnostic=nullptr,RegistryRole role=RegistryRole::OwnedLeaf){
    DWORD type=0,size=0;
    // Open with OPEN_LINK first: never inspect the link target as the container.
    const auto phases=registryPhases(role);
    if(diagnostic)diagnostic->enter(phases.query);
    const auto status=RegQueryValueExW(key,L"SymbolicLinkValue",nullptr,&type,nullptr,&size);
    const char* phase=phases.query;
    if(status==ERROR_SUCCESS)phase=type==REG_LINK?phases.linkValue:phases.otherValue;
    registryResult(diagnostic,phase,status,ERROR_FILE_NOT_FOUND);
  }
  inline void resolvedRegistryChild(HKEY from,const wchar_t* leaf,uint64_t end,OperatorDiagnostic* diagnostic){
    // An ordinary read additionally rejects an unfinished link. It does not
    // establish identity with the admitted no-follow handle.
    Key resolved;
    installedBudget(end,diagnostic);
    registryResult(diagnostic,"shared_resolved_open",RegOpenKeyExW(from,leaf,0,KEY_READ,&resolved.h));
    regularRegistryKey(resolved.h,diagnostic,RegistryRole::Resolved);
    if(diagnostic)diagnostic->enter("registry_close");
    resolved.close();
    installedBudget(end,diagnostic);
  }
  struct ClassesAnchor {
    Key software,source,hive;
    HKEY root()const{return hive.h?hive.h:source.h;}
    void open(const std::string& sid,uint64_t end,REGSAM access,OperatorDiagnostic* diagnostic=nullptr){
      installedBudget(end,diagnostic);
      require(sid==ownerSID()&&sid.size()<=256);
      registryResult(diagnostic,"shared_software_open",RegOpenKeyExW(HKEY_CURRENT_USER,L"Software",REG_OPTION_OPEN_LINK,KEY_READ,&software.h));
      regularRegistryKey(software.h,diagnostic,RegistryRole::Software);
      resolvedRegistryChild(HKEY_CURRENT_USER,L"Software",end,diagnostic);
      installedBudget(end,diagnostic);
      registryResult(diagnostic,"shared_classes_open",RegOpenKeyExW(software.h,L"Classes",REG_OPTION_OPEN_LINK,access,&source.h));
      DWORD type=0,size=0;
      if(diagnostic)diagnostic->enter("registry_classes_query");
      const auto status=RegQueryValueExW(source.h,L"SymbolicLinkValue",nullptr,&type,nullptr,&size);
      installedBudget(end,diagnostic);
      if(status==ERROR_FILE_NOT_FOUND){
        resolvedRegistryChild(software.h,L"Classes",end,diagnostic);
        return;
      }
      registryResult(diagnostic,"registry_classes_query",status);
      if(diagnostic)diagnostic->enter(type==REG_LINK?"registry_classes_link_value":"registry_classes_other_value");
      // The documented current-user Classes alias alone is allowed. Compare
      // exact UTF-16 bytes, with at most one final NUL. No normalization,
      // embedded NUL or arbitrary target is admitted.
      const auto leaf=wide(sid)+L"_Classes";
      const auto expected=L"\\Registry\\User\\"+leaf;
      std::array<wchar_t,256> target{};
      const auto plainBytes=expected.size()*sizeof(wchar_t);
      require(type==REG_LINK&&(size==plainBytes||size==plainBytes+sizeof(wchar_t))&&size<=sizeof(target));
      DWORD actualType=0,actualSize=size;
      installedBudget(end,diagnostic);
      registryResult(diagnostic,"registry_classes_query",RegQueryValueExW(source.h,L"SymbolicLinkValue",nullptr,&actualType,reinterpret_cast<BYTE*>(target.data()),&actualSize));
      if(diagnostic)diagnostic->enter(actualType==REG_LINK?"registry_classes_link_value":"registry_classes_other_value");
      require(actualType==REG_LINK&&actualSize==size&&wmemcmp(target.data(),expected.data(),expected.size())==0);
      if(size!=plainBytes)require(target[expected.size()]==0);
      installedBudget(end,diagnostic);
      // Never follow the inspected alias. Open only the independently derived
      // per-user hive, not HKCR or the merged RegOpenUserClassesRoot view.
      registryResult(diagnostic,"shared_classes_open",RegOpenKeyExW(HKEY_USERS,leaf.c_str(),REG_OPTION_OPEN_LINK,access,&hive.h));
      regularRegistryKey(hive.h,diagnostic,RegistryRole::Resolved);
      resolvedRegistryChild(HKEY_USERS,leaf.c_str(),end,diagnostic);
    }
    void close(){
      if(hive.h)hive.close();
      if(source.h)source.close();
      if(software.h)software.close();
    }
  };
  struct SharedRegistryParent {
    ClassesAnchor anchor;
    Key key;
    bool find(const Generation& g,const wchar_t* name,uint64_t end,REGSAM access,OperatorDiagnostic* diagnostic=nullptr){
      require(!wcscmp(name,L"CLSID")||!wcscmp(name,L"AppUserModelId"));
      // The class root only needs child creation when requested; deletion
      // rights belong to the fixed shared container, never the shared hive.
      anchor.open(g.sid,end,KEY_READ|(access&KEY_CREATE_SUB_KEY),diagnostic);
      installedBudget(end,diagnostic);
      const auto status=RegOpenKeyExW(anchor.root(),name,REG_OPTION_OPEN_LINK,access,&key.h);
      installedBudget(end,diagnostic);
      if(status==ERROR_FILE_NOT_FOUND)return false;
      registryResult(diagnostic,"shared_container_open",status);
      regularRegistryKey(key.h,diagnostic,RegistryRole::SharedContainer);
      resolvedRegistryChild(anchor.root(),name,end,diagnostic);
      return true;
    }
    void open(const Generation& g,const wchar_t* name,uint64_t end,OperatorDiagnostic* diagnostic=nullptr){
      if(find(g,name,end,KEY_READ|KEY_CREATE_SUB_KEY,diagnostic))return;
      DWORD disposition=0;
      // Missing-only shared preparation retains default inherited ACLs.
      installedBudget(end,diagnostic);
      const auto status=RegCreateKeyExW(anchor.root(),name,0,nullptr,REG_OPTION_OPEN_LINK,
        KEY_READ|KEY_CREATE_SUB_KEY,nullptr,&key.h,&disposition);
      installedBudget(end,diagnostic);
      registryResult(diagnostic,"shared_container_create",status);
      require(disposition==REG_CREATED_NEW_KEY);
      regularRegistryKey(key.h,diagnostic,RegistryRole::SharedContainer);
      installedBudget(end,diagnostic);
      registryResult(diagnostic,"shared_container_flush",RegFlushKey(key.h));
      installedBudget(end,diagnostic);
    }
    void close(){
      if(key.h)key.close();
      anchor.close();
    }
  };
  inline bool absentRegistration(const Generation& g,const wchar_t* container,const std::wstring& leaf,uint64_t end){
    SharedRegistryParent parent;
    if(!parent.find(g,container,end,KEY_READ)){
      parent.close();
      return true;
    }
    Key key;
    installedBudget(end);
    const auto status=RegOpenKeyExW(parent.key.h,leaf.c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&key.h);
    installedBudget(end);
    if(status==ERROR_SUCCESS){
      regularRegistryKey(key.h);
      key.close();
    }else require(status==ERROR_FILE_NOT_FOUND);
    parent.close();
    return status==ERROR_FILE_NOT_FOUND;
  }
  inline Key* createKey(const Generation& g,HKEY parent,const std::wstring& leaf,Key& key,uint64_t end,OperatorDiagnostic* diagnostic=nullptr,RegistryRole parentRole=RegistryRole::SharedContainer){
    require(!leaf.empty()&&leaf.find_first_of(L"\\/")==std::wstring::npos);
    regularRegistryKey(parent,diagnostic,parentRole);
    if(diagnostic)diagnostic->enter("owned_leaf_descriptor");
    PSECURITY_DESCRIPTOR sd=nullptr;
    auto acl=L"O:"+wide(g.sid)+L"D:P(A;;KA;;;"+wide(g.sid)+L")(A;;KA;;;SY)";
    require(ConvertStringSecurityDescriptorToSecurityDescriptorW(acl.c_str(),SDDL_REVISION_1,&sd,nullptr));
    struct Free {
      PSECURITY_DESCRIPTOR p;
      ~Free(){LocalFree(p);}
    } free{sd};
    SECURITY_ATTRIBUTES sa{sizeof(sa),sd,FALSE};
    DWORD disposition=0;
    installedBudget(end,diagnostic);
    auto status=RegCreateKeyExW(parent,leaf.c_str(),0,nullptr,REG_OPTION_OPEN_LINK,
      KEY_READ|KEY_WRITE,&sa,&key.h,&disposition);
    installedBudget(end,diagnostic);
    registryResult(diagnostic,"owned_leaf_create",status);
    regularRegistryKey(key.h,diagnostic);
    if(diagnostic)diagnostic->enter("owned_leaf_stamp");
    if(disposition==REG_CREATED_NEW_KEY)put(key.h,L"OwnerGeneration",stamp(g),end);
    else require(disposition==REG_OPENED_EXISTING_KEY&&value(key.h,L"OwnerGeneration")==stamp(g));
    return &key;
  }
  inline void ownedRegistry(HKEY key,const Generation& g){
    PSECURITY_DESCRIPTOR sd=nullptr;
    PSID owner=nullptr;
    PACL acl=nullptr;
    require(GetSecurityInfo(key,SE_REGISTRY_KEY,OWNER_SECURITY_INFORMATION|DACL_SECURITY_INFORMATION,&owner,nullptr,&acl,nullptr,&sd)==ERROR_SUCCESS);
    struct Free{
      PSECURITY_DESCRIPTOR p;
      ~Free(){
        LocalFree(p);
      }
    }
    free{
      sd
    };
    LPWSTR name=nullptr;
    require(owner&&ConvertSidToStringSidW(owner,&name));
    auto sid=narrow(name);
    LocalFree(name);
    SECURITY_DESCRIPTOR_CONTROL control{
    };
    DWORD revision=0;
    require(sid==g.sid&&GetSecurityDescriptorControl(sd,&control,&revision)&&(control&SE_DACL_PROTECTED)&&acl&&acl->AceCount==2);
    bool own=false,system=false;
    for(DWORD i=0;i<2;i++){
      void* raw=nullptr;
      require(GetAce(acl,i,&raw));
      auto a=static_cast<ACCESS_ALLOWED_ACE*>(raw);
      require(a->Header.AceType==ACCESS_ALLOWED_ACE_TYPE&&a->Header.AceFlags==0&&a->Mask==KEY_ALL_ACCESS);
      LPWSTR s=nullptr;
      require(ConvertSidToStringSidW(&a->SidStart,&s));
      auto v=narrow(s);
      LocalFree(s);
      if(v==g.sid&&!own)own=true;
      else if(v=="S-1-5-18"&&!system)system=true;
      else require(false);
    }
    require(own&&system);
  }
  inline void expectedValues(HKEY key,const Generation& g,const std::vector<std::pair<std::wstring,std::wstring>>& expected,bool complete){
    ownedRegistry(key,g);
    DWORD count=0;
    require(RegQueryInfoKeyW(key,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr,&count,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&count<=expected.size());
    for(DWORD i=0;i<count;i++){
      wchar_t name[128]{
      };
      DWORD length=128;
      require(RegEnumValueW(key,i,name,&length,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS);
      bool found=false;
      for(const auto& item:expected)if(item.first==name){
        require(value(key,name)==item.second);
        found=true;
      }
      require(found);
    }
    if(complete)require(count==expected.size());
  }
  inline void putExpected(HKEY key,const wchar_t* name,const std::wstring& wanted,uint64_t end){
    DWORD type=0,n=0;
    auto status=RegQueryValueExW(key,name,nullptr,&type,nullptr,&n);
    if(status==ERROR_FILE_NOT_FOUND)put(key,name,wanted,end);
    else{
      require(status==ERROR_SUCCESS&&value(key,name)==wanted);
    }
  }
  inline void onlyValues(HKEY key,unsigned values,unsigned children){
    DWORD actualValues=0,actualChildren=0;
    require(RegQueryInfoKeyW(key,nullptr,nullptr,nullptr,&actualChildren,nullptr,nullptr,&actualValues,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&actualValues==values&&actualChildren==children);
  }
  inline void classReadback(const Generation& g,uint64_t end){
    SharedRegistryParent parent;
    require(parent.find(g,L"CLSID",end,KEY_READ));
    Key cls,local;
    installedBudget(end);
    require(RegOpenKeyExW(parent.key.h,wide(g.snapshot.clsid).c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&cls.h)==ERROR_SUCCESS);
    regularRegistryKey(cls.h);
    ownedRegistry(cls.h,g);
    require(value(cls.h,L"OwnerGeneration")==stamp(g));
    onlyValues(cls.h,1,1);
    installedBudget(end);
    require(RegOpenKeyExW(cls.h,L"LocalServer32",REG_OPTION_OPEN_LINK,KEY_READ,&local.h)==ERROR_SUCCESS);
    regularRegistryKey(local.h);
    ownedRegistry(local.h,g);
    require(value(local.h,L"")==command(g)&&value(local.h,L"OwnerGeneration")==stamp(g));
    onlyValues(local.h,2,0);
    local.close();
    cls.close();
    parent.close();
    installedBudget(end);
  }
  inline void appReadback(const Generation& g,uint64_t end){
    SharedRegistryParent parent;
    require(parent.find(g,L"AppUserModelId",end,KEY_READ));
    Key app;
    installedBudget(end);
    require(RegOpenKeyExW(parent.key.h,wide(g.snapshot.aumid).c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&app.h)==ERROR_SUCCESS);
    regularRegistryKey(app.h);
    ownedRegistry(app.h,g);
    require(value(app.h,L"OwnerGeneration")==stamp(g)&&value(app.h,L"DisplayName")==L"Agent Notifications"&&value(app.h,L"CustomActivator")==wide(g.snapshot.clsid));
    onlyValues(app.h,3,0);
    app.close();
    parent.close();
    installedBudget(end);
  }
  inline void registryReadback(const Generation& g,uint64_t end){
    classReadback(g,end);
    appReadback(g,end);
  }
  struct ScopedPropVariant {
    PROPVARIANT value{
    };
    ScopedPropVariant()=default;
    ScopedPropVariant(const ScopedPropVariant&)=delete;
    ScopedPropVariant& operator=(const ScopedPropVariant&)=delete;
    void clear(uint64_t end=0){
      shortcutBudget(end);
      shortcutHresult("PropVariantClear",PropVariantClear(&value));
      shortcutBudget(end);
    }
    ~ScopedPropVariant()noexcept{
      PropVariantClear(&value);
    }
  };
  inline std::vector<uint8_t> linkBytes(IShellLinkW* link,uint64_t end=0 WCB_SHORTCUT_DEADLINE_TEST_PARAM){
    Microsoft::WRL::ComPtr<IPersistStream> persist;
    shortcutBudget(end);
    shortcutHresult("IShellLinkW::QueryInterface",link->QueryInterface(IID_PPV_ARGS(&persist)));
    shortcutBudget(end);
    ULARGE_INTEGER max{
    };
    shortcutBudget(end);
    shortcutHresult("IPersistStream::GetSizeMax",persist->GetSizeMax(&max));
    shortcutBudget(end);
    require(max.QuadPart>0&&max.QuadPart<=65536);
    Microsoft::WRL::ComPtr<IStream> stream;
    shortcutBudget(end);
    shortcutHresult("CreateStreamOnHGlobal",CreateStreamOnHGlobal(nullptr,TRUE,&stream));
    shortcutBudget(end);
    shortcutHresult("IPersistStream::Save",persist->Save(stream.Get(),TRUE));
    WCB_SHORTCUT_AFTER_API(end,"IPersistStream::Save","serialization_after_IPersistStream_Save",deadlineProbe);
    STATSTG stat{
    };
    shortcutBudget(end);
    shortcutHresult("IStream::Stat",stream->Stat(&stat,STATFLAG_NONAME));
    shortcutBudget(end);
    require(stat.cbSize.QuadPart>0&&stat.cbSize.QuadPart<=65536);
    HGLOBAL memory=nullptr;
    shortcutBudget(end);
    const auto memoryStatus=GetHGlobalFromStream(stream.Get(),&memory);
    shortcutContractResult("GetHGlobalFromStream",memoryStatus,memory!=nullptr);
    // The HGLOBAL remains owned by the delete-on-release stream. Allocate
    // before locking that borrowed memory; the lock owner is active before the
    // first post-lock budget check and unwinds on failure.
    shortcutBudget(end);
    std::vector<uint8_t> out(size_t(stat.cbSize.QuadPart));
    shortcutBudget(end);
    ScopedGlobalLock locked(memory);
    shortcutBudget(end);
    require(locked.value);
    memcpy(out.data(),locked.value,out.size());
    shortcutBudget(end);
    return out;
  }
  // Shell returns extended drive syntax for a real long filesystem item.
  // Remove only that namespace spelling: do not fold case, resolve aliases,
  // admit UNC/device paths, or change the held file identity requirement.
  inline std::wstring shortcutDOSParsingName(std::wstring target){
    if(!target.starts_with(L"\\\\?\\"))return target;
    require(target.size()>7&&(target[4]>=L'A'&&target[4]<=L'Z'
      ||target[4]>=L'a'&&target[4]<=L'z')&&target[5]==L':'
      &&target[6]==L'\\'&&target.back()!=L'\\'
      &&target.find(L':',6)==std::wstring::npos);
    return target.substr(4);
  }
  inline std::wstring exactShortcutTarget(IShellLinkW* link,uint64_t end=0){
    ScopedPidl idlist;
    shortcutBudget(end);
    const auto idStatus=link->GetIDList(&idlist.value);
    shortcutContractResult("IShellLinkW::GetIDList",idStatus,shortcutPidlPresent(idlist.value));
    shortcutBudget(end);
    ScopedCoTaskMem name;
    const auto nameStatus=SHGetNameFromIDList(idlist.value,SIGDN_FILESYSPATH,&name.value);
    shortcutContractResult("SHGetNameFromIDList",nameStatus,shortcutNamePresent(name.value));
    shortcutBudget(end);
    std::wstring target(name.value);
    shortcutBudget(end);
    return shortcutDOSParsingName(std::move(target));
  }
  inline void parseShortcutTarget(const ShortcutFixture& fixture,ScopedPidl& target,uint64_t end=0){
    shortcutBudget(end);
    // Microsoft documents SHParseDisplayName as the preferred string-to-PIDL
    // conversion and IShellLinkW::SetIDList as the fully qualified target
    // contract. This is used only after the real target's no-reparse file
    // identity is held; a nonexistent synthetic display name is not a PIDL.
    SFGAOF attributes=0;
    const auto status=SHParseDisplayName(fixture.target.c_str(),nullptr,&target.value,
      SFGAO_FILESYSTEM|SFGAO_FOLDER,&attributes);
    shortcutContractResult("SHParseDisplayName",status,shortcutPidlPresent(target.value));
    shortcutBudget(end);
    require((attributes&SFGAO_FILESYSTEM)!=0&&(attributes&SFGAO_FOLDER)==0);
    shortcutBudget(end);
  }
  inline void shortcutReadbackBytes(const ShortcutFixture& fixture,const std::vector<uint8_t>& b,uint64_t end=0 WCB_SHORTCUT_DEADLINE_TEST_PARAM){
    shortcutBudget(end);
    verifyShortcutTargetCustody(fixture,end);
    shortcutBudget(end);
    Microsoft::WRL::ComPtr<IShellLinkW> link;
    shortcutHresult("CoCreateInstance(CLSID_ShellLink)",CoCreateInstance(CLSID_ShellLink,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&link)));
    shortcutBudget(end);
    Microsoft::WRL::ComPtr<IPersistStream> persist;
    shortcutHresult("IShellLinkW::QueryInterface(IPersistStream)",link.As(&persist));
    shortcutBudget(end);
    Microsoft::WRL::ComPtr<IStream> stream;
    shortcutHresult("CreateStreamOnHGlobal",CreateStreamOnHGlobal(nullptr,TRUE,&stream));
    shortcutBudget(end);
    ULONG n=0;
    shortcutHresult("IStream::Write",stream->Write(b.data(),ULONG(b.size()),&n));
    shortcutBudget(end);
    require(n==b.size());
    LARGE_INTEGER zero{
    };
    shortcutHresult("IStream::Seek",stream->Seek(zero,STREAM_SEEK_SET,nullptr));
    shortcutBudget(end);
    shortcutHresult("IPersistStream::Load",persist->Load(stream.Get()));
    WCB_SHORTCUT_AFTER_API(end,"IPersistStream::Load","readback_after_IPersistStream_Load",deadlineProbe);
    // IShellLinkW::GetPath is documented to return at most MAX_PATH. It is a
      // short-path cross-check only. SHGetNameFromIDList returns the Shell
      // item's exact filesystem parsing name without that output-buffer contract,
      // so it also covers the supported long-path range. PIDL binary bytes are
      // intentionally not compared: they are namespace encodings, not a stable
      // identity contract. Exact parsing-name equality plus the held no-reparse
      // file identity excludes an alternate target without extra resolution.
    if(fixture.target.size()<MAX_PATH){
      std::array<wchar_t,MAX_PATH> shortTarget{
      };
      shortcutHresult("IShellLinkW::GetPath",link->GetPath(shortTarget.data(),int(shortTarget.size()),nullptr,SLGP_RAWPATH));
      shortcutBudget(end);
      verifyShortcutTarget(fixture.target,std::wstring(shortTarget.data()));
    }
    verifyShortcutTarget(fixture.target,exactShortcutTarget(link.Get(),end));
    verifyShortcutTargetCustody(fixture,end);
    shortcutBudget(end);
    std::array<wchar_t,128> args{
    };
    shortcutHresult("IShellLinkW::GetArguments",link->GetArguments(args.data(),int(args.size())));
    shortcutBudget(end);
    require(std::wstring(args.data())==fixture.args);
    Microsoft::WRL::ComPtr<IPropertyStore> properties;
    shortcutHresult("IShellLinkW::QueryInterface(IPropertyStore)",link.As(&properties));
    shortcutBudget(end);
    ScopedPropVariant v;
    shortcutHresult("IPropertyStore::GetValue(PKEY_AppUserModel_ID)",properties->GetValue(PKEY_AppUserModel_ID,&v.value));
    shortcutBudget(end);
    require(v.value.vt==VT_LPWSTR&&v.value.pwszVal&&std::wstring(v.value.pwszVal)==fixture.aumid);
    v.clear(end);
    GUID clsid{
    };
    shortcutHresult("CLSIDFromString",CLSIDFromString(fixture.clsid.c_str(),&clsid));
    shortcutBudget(end);
    shortcutHresult("IPropertyStore::GetValue(PKEY_AppUserModel_ToastActivatorCLSID)",properties->GetValue(PKEY_AppUserModel_ToastActivatorCLSID,&v.value));
    shortcutBudget(end);
    require(v.value.vt==VT_CLSID&&v.value.puuid&&IsEqualGUID(*v.value.puuid,clsid));
  }
  inline void shortcutReadbackBytes(const Generation& g,const std::vector<uint8_t>& b,uint64_t end=0 WCB_SHORTCUT_DEADLINE_TEST_PARAM){
    shortcutReadbackBytes(shortcutFixture(g),b,end WCB_SHORTCUT_DEADLINE_TEST_ARG);
  }
  inline void shortcutReadbackAt(const Generation& g,const fs::path& p,uint64_t end=0){
    shortcutBudget(end);
    auto file=open(p,false);
    shortcutBudget(end);
    owned(file.h,g.sid);
    require(physical(file.h)==p.wstring()||physical(file.h)==L"\\\\?\\"+p.wstring());
    shortcutReadbackBytes(g,bytes(file.h,65536),end);
    shortcutBudget(end);
  }
  inline void shortcutReadback(const Generation& g,uint64_t end=0){
    shortcutReadbackAt(g,shortcutPath(g,end),end);
  }
  inline std::vector<Handle> shortcutParents(const fs::path& p,uint64_t end=0){
    std::vector<Handle> held;
    fs::path path=p.root_path();
    shortcutBudget(end);
    held.push_back(open(path,true));
    shortcutBudget(end);
    for(const auto& part:p.parent_path().relative_path()){
      path/=part;
      held.push_back(open(path,true));
      shortcutBudget(end);
    }
    require(physical(held.back().h)==L"\\\\?\\"+p.parent_path().wstring()||physical(held.back().h)==p.parent_path().wstring());
    shortcutBudget(end);
    return held;
  }
  inline void leafKey(HKEY key){
    DWORD children=0;
    require(RegQueryInfoKeyW(key,nullptr,nullptr,nullptr,&children,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&children==0);
  }
  inline void classChildren(HKEY key){
    DWORD children=0;
    require(RegQueryInfoKeyW(key,nullptr,nullptr,nullptr,&children,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&children<=1);
    if(children){
      wchar_t name[128]{
      };
      DWORD n=128;
      require(RegEnumKeyExW(key,0,name,&n,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&std::wstring(name)==L"LocalServer32");
    }
  }
  inline void applyClass(const Generation& g,uint64_t end,OperatorDiagnostic* diagnostic=nullptr){
    SharedRegistryParent parent;
    parent.open(g,L"CLSID",end,diagnostic);
    Key cls,local;
    if(diagnostic)diagnostic->enter("class_leaf_create");
    createKey(g,parent.key.h,wide(g.snapshot.clsid),cls,end,diagnostic);
    if(diagnostic)diagnostic->enter("class_postimage");
    expectedValues(cls.h,g,{
      {
        L"OwnerGeneration",stamp(g)
      }
    },true);
    if(diagnostic)diagnostic->enter("class_children");
    classChildren(cls.h);
    if(diagnostic)diagnostic->enter("local_leaf_create");
    createKey(g,cls.h,L"LocalServer32",local,end,diagnostic,RegistryRole::OwnedLeaf);
    if(diagnostic)diagnostic->enter("local_postimage");
    expectedValues(local.h,g,{
      {
        L"OwnerGeneration",stamp(g)
      },{
        L"",command(g)
      }
    },false);
    if(diagnostic)diagnostic->enter("local_children");
    leafKey(local.h);
    if(diagnostic)diagnostic->enter("local_default_value");
    putExpected(local.h,L"",command(g),end);
    installedBudget(end,diagnostic);
    registryResult(diagnostic,"local_flush",RegFlushKey(local.h));
    installedBudget(end,diagnostic);
    registryResult(diagnostic,"class_flush",RegFlushKey(cls.h));
    installedBudget(end,diagnostic);
    if(diagnostic)diagnostic->enter("registry_close");
    local.close();
    cls.close();
    parent.close();
  }
  inline void applyApp(const Generation& g,uint64_t end){
    SharedRegistryParent parent;
    parent.open(g,L"AppUserModelId",end);
    Key app;
    createKey(g,parent.key.h,wide(g.snapshot.aumid),app,end);
    expectedValues(app.h,g,{
      {
        L"OwnerGeneration",stamp(g)
      },{
        L"DisplayName",L"Agent Notifications"
      },{
        L"CustomActivator",wide(g.snapshot.clsid)
      }
    },false);
    leafKey(app.h);
    putExpected(app.h,L"DisplayName",L"Agent Notifications",end);
    putExpected(app.h,L"CustomActivator",wide(g.snapshot.clsid),end);
    installedBudget(end);
    require(RegFlushKey(app.h)==ERROR_SUCCESS);
    installedBudget(end);
    app.close();
    parent.close();
  }
  inline Handle writableLeaf(const fs::path& p,const Generation& g){
    Handle file(CreateFileW(p.c_str(),GENERIC_READ|READ_CONTROL|DELETE,FILE_SHARE_READ,nullptr,OPEN_EXISTING,FILE_FLAG_OPEN_REPARSE_POINT,nullptr));
    require(file.h!=INVALID_HANDLE_VALUE);
    BY_HANDLE_FILE_INFORMATION info{
    };
    require(GetFileInformationByHandle(file.h,&info)&&!(info.dwFileAttributes&(FILE_ATTRIBUTE_REPARSE_POINT|FILE_ATTRIBUTE_DIRECTORY))&&info.nNumberOfLinks==1);
    owned(file.h,g.sid);
    require(physical(file.h)==L"\\\\?\\"+p.wstring()||physical(file.h)==p.wstring());
    return file;
  }
  inline void renameLeaf(HANDLE file,HANDLE parent,const std::wstring& name,uint64_t end){
    require(!name.empty()&&name!=L"."&&name!=L".."
      &&name.find_first_of(L"\\/:")==std::wstring::npos
      &&name.find(L'\0')==std::wstring::npos&&name.back()!=L'.'&&name.back()!=L' ');
    // The real Win32 private-path contract rejects a relative FileName with
    // RootDirectory (ERROR_INVALID_PARAMETER). Use the supported absolute-name
    // form once. All ancestors remain held without delete sharing; derive the
    // directory spelling from that same physical handle, never caller input.
    installedBudget(end);
    const auto destination=(fs::path(physical(parent))/name).wstring();
    require(destination.size()<32768);
    const size_t n=destination.size()*sizeof(wchar_t);
    std::vector<uint8_t> storage(offsetof(FILE_RENAME_INFO,FileName)+n+sizeof(wchar_t));
    auto info=reinterpret_cast<FILE_RENAME_INFO*>(storage.data());
    info->ReplaceIfExists=FALSE;
    info->RootDirectory=nullptr;
    info->FileNameLength=DWORD(n);
    memcpy(info->FileName,destination.data(),n);
    installedBudget(end);
    if(!SetFileInformationByHandle(file,FileRenameInfo,info,DWORD(storage.size()))){
      const auto error=GetLastError();
      throw RegistryFailure("shortcut_stage_rename",error);
    }
    installedBudget(end);
  }
  inline std::vector<uint8_t> configuredShortcutBytes(const ShortcutFixture& fixture,uint64_t end=0 WCB_SHORTCUT_DEADLINE_TEST_PARAM){
    shortcutBudget(end);
    verifyShortcutTargetCustody(fixture,end);
    shortcutBudget(end);
    ScopedPidl target;
    parseShortcutTarget(fixture,target,end);
    Microsoft::WRL::ComPtr<IShellLinkW> link;
    shortcutHresult("CoCreateInstance(CLSID_ShellLink)",CoCreateInstance(CLSID_ShellLink,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&link)));
    shortcutBudget(end);
    shortcutHresult("IShellLinkW::SetIDList",link->SetIDList(target.value));
    shortcutBudget(end);
    shortcutHresult("IShellLinkW::SetArguments",link->SetArguments(fixture.args.c_str()));
    shortcutBudget(end);
    Microsoft::WRL::ComPtr<IPropertyStore> properties;
    shortcutHresult("IShellLinkW::QueryInterface(IPropertyStore)",link.As(&properties));
    shortcutBudget(end);
    ScopedPropVariant v;
    shortcutHresult("InitPropVariantFromString",InitPropVariantFromString(fixture.aumid.c_str(),&v.value));
    shortcutBudget(end);
    shortcutHresult("IPropertyStore::SetValue(PKEY_AppUserModel_ID)",properties->SetValue(PKEY_AppUserModel_ID,v.value));
    shortcutBudget(end);
    v.clear(end);
    GUID clsid{
    };
    shortcutHresult("CLSIDFromString",CLSIDFromString(fixture.clsid.c_str(),&clsid));
    shortcutBudget(end);
    shortcutHresult("InitPropVariantFromCLSID",InitPropVariantFromCLSID(clsid,&v.value));
    shortcutBudget(end);
    shortcutHresult("IPropertyStore::SetValue(PKEY_AppUserModel_ToastActivatorCLSID)",properties->SetValue(PKEY_AppUserModel_ToastActivatorCLSID,v.value));
    shortcutBudget(end);
    v.clear(end);
    shortcutHresult("IPropertyStore::Commit",properties->Commit());
    shortcutBudget(end);
    return linkBytes(link.Get(),end WCB_SHORTCUT_DEADLINE_TEST_ARG);
  }
  inline std::vector<uint8_t> configuredShortcutBytes(const Generation& g,uint64_t end=0 WCB_SHORTCUT_DEADLINE_TEST_PARAM){
    return configuredShortcutBytes(shortcutFixture(g),end WCB_SHORTCUT_DEADLINE_TEST_ARG);
  }
  inline void applyShortcutAt(const Generation& g,const fs::path& p,uint64_t end,OperatorDiagnostic* diagnostic=nullptr){
    if(diagnostic)diagnostic->enter("shortcut_parent_custody");
    auto parents=shortcutParents(p,end);
    installedBudget(end);
    if(GetFileAttributesW(p.c_str())!=INVALID_FILE_ATTRIBUTES){
      if(diagnostic)diagnostic->enter("shortcut_existing_readback");
      shortcutReadbackAt(g,p,end);
      installedBudget(end);
      return;
    }
    if(diagnostic)diagnostic->enter("shortcut_absence_contract");
    require(GetLastError()==ERROR_FILE_NOT_FOUND);
    installedBudget(end);
    if(diagnostic)diagnostic->enter("shortcut_serialization");
    auto data=configuredShortcutBytes(g,end);
    if(diagnostic)diagnostic->enter("shortcut_serialized_readback");
    shortcutReadbackBytes(g,data,end);
    installedBudget(end);
    auto stage=p;
    stage+=L".stage";
    installedBudget(end);
    if(diagnostic)diagnostic->enter("shortcut_stage_lookup");
    if(GetFileAttributesW(stage.c_str())==INVALID_FILE_ATTRIBUTES){
      require(GetLastError()==ERROR_FILE_NOT_FOUND);
      installedBudget(end);
      if(diagnostic)diagnostic->enter("shortcut_stage_create");
      exclusiveFile(stage,g.sid,data);
      installedBudget(end);
    }
    if(diagnostic)diagnostic->enter("shortcut_stage_open");
    auto file=writableLeaf(stage,g);
    if(diagnostic)diagnostic->enter("shortcut_stage_bytes");
    auto held=bytes(file.h,65536);
    require(held==data);
    installedBudget(end);
    if(diagnostic)diagnostic->enter("shortcut_stage_readback");
    shortcutReadbackBytes(g,held,end);
    installedBudget(end);
    if(diagnostic)diagnostic->enter("shortcut_stage_rename");
    renameLeaf(file.h,parents.back().h,p.filename().wstring(),end);
    if(diagnostic)diagnostic->enter("shortcut_stage_close");
    file.closeChecked();
    installedBudget(end);
    if(diagnostic)diagnostic->enter("shortcut_final_readback");
    shortcutReadbackAt(g,p,end);
    installedBudget(end);
  }
  inline void applyShortcut(const Generation& g,uint64_t end,OperatorDiagnostic* diagnostic=nullptr){
    if(diagnostic)diagnostic->enter("shortcut_path");
    const auto p=shortcutPath(g,end);
    applyShortcutAt(g,p,end,diagnostic);
  }
  inline void restoreClass(const Generation& g,uint64_t end){
    SharedRegistryParent parent;
    if(!parent.find(g,L"CLSID",end,KEY_READ|KEY_SET_VALUE|DELETE)){
      parent.close();
      return;
    }
    Key cls;
    installedBudget(end);
    const auto status=RegOpenKeyExW(parent.key.h,wide(g.snapshot.clsid).c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&cls.h);
    installedBudget(end);
    if(status==ERROR_FILE_NOT_FOUND){parent.close();return;}
    require(status==ERROR_SUCCESS);
    regularRegistryKey(cls.h);
    require(value(cls.h,L"OwnerGeneration")==stamp(g));
    expectedValues(cls.h,g,{
      {
        L"OwnerGeneration",stamp(g)
      }
    },true);
    DWORD children=0;
    require(RegQueryInfoKeyW(cls.h,nullptr,nullptr,nullptr,&children,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&children<=1);
    if(children){
      Key local;
      installedBudget(end);
      require(RegOpenKeyExW(cls.h,L"LocalServer32",REG_OPTION_OPEN_LINK,KEY_READ,&local.h)==ERROR_SUCCESS);
      regularRegistryKey(local.h);
      expectedValues(local.h,g,{
        {
          L"OwnerGeneration",stamp(g)
        },{
          L"",command(g)
        }
      },false);
      DWORD nested=0;
      require(RegQueryInfoKeyW(local.h,nullptr,nullptr,nullptr,&nested,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&nested==0);
    }
    cls.close();
    installedBudget(end);
    require(RegDeleteTreeW(parent.key.h,wide(g.snapshot.clsid).c_str())==ERROR_SUCCESS);
    installedBudget(end);
    parent.close();
  }
  inline void restoreApp(const Generation& g,uint64_t end){
    SharedRegistryParent parent;
    if(!parent.find(g,L"AppUserModelId",end,KEY_READ|KEY_SET_VALUE|DELETE)){
      parent.close();
      return;
    }
    Key app;
    installedBudget(end);
    const auto status=RegOpenKeyExW(parent.key.h,wide(g.snapshot.aumid).c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&app.h);
    installedBudget(end);
    if(status==ERROR_FILE_NOT_FOUND){parent.close();return;}
    require(status==ERROR_SUCCESS);
    regularRegistryKey(app.h);
    require(value(app.h,L"OwnerGeneration")==stamp(g));
    expectedValues(app.h,g,{
      {
        L"OwnerGeneration",stamp(g)
      },{
        L"DisplayName",L"Agent Notifications"
      },{
        L"CustomActivator",wide(g.snapshot.clsid)
      }
    },false);
    DWORD children=0;
    require(RegQueryInfoKeyW(app.h,nullptr,nullptr,nullptr,&children,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr,nullptr)==ERROR_SUCCESS&&children==0);
    app.close();
    installedBudget(end);
    require(RegDeleteTreeW(parent.key.h,wide(g.snapshot.aumid).c_str())==ERROR_SUCCESS);
    installedBudget(end);
    parent.close();
  }
  inline void restoreShortcut(const Generation& g,uint64_t end){
    auto p=shortcutPath(g,end);
    auto parents=shortcutParents(p,end);
    auto stage=p;
    stage+=L".stage";
    // A failed publish may leave only the owned stage, not a final shortcut.
    // Refuse foreign stages; only final links require complete COM readback.
    for(const auto& leaf:{stage,p}){
      installedBudget(end);
      if(GetFileAttributesW(leaf.c_str())==INVALID_FILE_ATTRIBUTES){
        require(GetLastError()==ERROR_FILE_NOT_FOUND);
        continue;
      }
      auto file=writableLeaf(leaf,g);
      installedBudget(end);
      if(leaf==p)shortcutReadbackBytes(g,bytes(file.h,65536),end);
      installedBudget(end);
      FILE_DISPOSITION_INFO disposition{
        TRUE
      };
      require(SetFileInformationByHandle(file.h,FileDispositionInfo,&disposition,sizeof(disposition)));
      installedBudget(end);
      file.closeChecked();
    }
  }
  inline std::vector<std::string> operatorFields(){
    std::vector<uint8_t>b;
    char c;
    while(std::cin.get(c)){
      require(b.size()<65536);
      b.push_back(uint8_t(c));
    }
    return decode(b,3,4);
  }
  inline std::wstring xml(const std::string& value){
    require(value.empty()||text(value,4096));
    std::wstring out;
    for(auto c:wide(value)){
      switch(c){
        case L'&':out+=L"&amp;";
        break;
        case L'<':out+=L"&lt;";
        break;
        case L'>':out+=L"&gt;";
        break;
        case L'"':out+=L"&quot;";
        break;
        case L'\'':out+=L"&apos;";
        break;
        default:out+=c;
      }
    }
    return out;
  }
#undef WCB_SHORTCUT_AFTER_API
#undef WCB_SHORTCUT_DEADLINE_TEST_ARG
#undef WCB_SHORTCUT_DEADLINE_TEST_PARAM
}
