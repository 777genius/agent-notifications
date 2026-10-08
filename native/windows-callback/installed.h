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
  inline fs::path shortcutPath(const Generation& g){
    PWSTR path=nullptr;
    winrt::check_hresult(SHGetKnownFolderPath(FOLDERID_Programs,0,nullptr,&path));
    fs::path result=fs::path(path)/(L"AgentNotifications-"+wide(g.snapshot.generation)+L".lnk");
    CoTaskMemFree(path);
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
  inline std::vector<uint8_t> linkBytes(IShellLinkW* link){
    Microsoft::WRL::ComPtr<IPersistStream> persist;
    winrt::check_hresult(link->QueryInterface(IID_PPV_ARGS(&persist)));
    ULARGE_INTEGER max{
    };
    winrt::check_hresult(persist->GetSizeMax(&max));
    require(max.QuadPart>0&&max.QuadPart<=65536);
    Microsoft::WRL::ComPtr<IStream> stream;
    winrt::check_hresult(CreateStreamOnHGlobal(nullptr,TRUE,&stream));
    winrt::check_hresult(persist->Save(stream.Get(),TRUE));
    STATSTG stat{
    };
    winrt::check_hresult(stream->Stat(&stat,STATFLAG_NONAME));
    require(stat.cbSize.QuadPart>0&&stat.cbSize.QuadPart<=65536);
    HGLOBAL memory=nullptr;
    winrt::check_hresult(GetHGlobalFromStream(stream.Get(),&memory));
    const auto p=static_cast<const uint8_t*>(GlobalLock(memory));
    require(p);
    std::vector<uint8_t> out(p,p+size_t(stat.cbSize.QuadPart));
    GlobalUnlock(memory);
    return out;
  }
  inline void shortcutReadbackBytes(const Generation& g,const std::vector<uint8_t>& b){
    Microsoft::WRL::ComPtr<IShellLinkW> link;
    winrt::check_hresult(CoCreateInstance(CLSID_ShellLink,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&link)));
    Microsoft::WRL::ComPtr<IPersistStream> persist;
    winrt::check_hresult(link.As(&persist));
    Microsoft::WRL::ComPtr<IStream> stream;
    winrt::check_hresult(CreateStreamOnHGlobal(nullptr,TRUE,&stream));
    ULONG n=0;
    winrt::check_hresult(stream->Write(b.data(),ULONG(b.size()),&n));
    require(n==b.size());
    LARGE_INTEGER zero{
    };
    winrt::check_hresult(stream->Seek(zero,STREAM_SEEK_SET,nullptr));
    winrt::check_hresult(persist->Load(stream.Get()));
    wchar_t target[32768]{
    },args[128]{
    };
    winrt::check_hresult(link->GetPath(target,32768,nullptr,SLGP_RAWPATH));
    winrt::check_hresult(link->GetArguments(args,128));
    require(std::wstring(target)==(g.root/L"helper.exe").wstring()&&std::wstring(args)==L"--callback");
    Microsoft::WRL::ComPtr<IPropertyStore> properties;
    winrt::check_hresult(link.As(&properties));
    PROPVARIANT v{
    };
    winrt::check_hresult(properties->GetValue(PKEY_AppUserModel_ID,&v));
    require(v.vt==VT_LPWSTR&&v.pwszVal&&v.pwszVal==wide(g.snapshot.aumid));
    PropVariantClear(&v);
    GUID clsid{
    };
    winrt::check_hresult(CLSIDFromString(wide(g.snapshot.clsid).c_str(),&clsid));
    winrt::check_hresult(properties->GetValue(PKEY_AppUserModel_ToastActivatorCLSID,&v));
    require(v.vt==VT_CLSID&&v.puuid&&IsEqualGUID(*v.puuid,clsid));
    PropVariantClear(&v);
  }
  inline void shortcutReadbackAt(const Generation& g,const fs::path& p){
    auto file=open(p,false);
    owned(file.h,g.sid);
    require(physical(file.h)==p.wstring()||physical(file.h)==L"\\\\?\\"+p.wstring());
    shortcutReadbackBytes(g,bytes(file.h,65536));
  }
  inline void shortcutReadback(const Generation& g){
    shortcutReadbackAt(g,shortcutPath(g));
  }
  inline std::vector<Handle> shortcutParents(const fs::path& p){
    std::vector<Handle> held;
    fs::path path=p.root_path();
    held.push_back(open(path,true));
    for(const auto& part:p.parent_path().relative_path()){
      path/=part;
      held.push_back(open(path,true));
    }
    require(physical(held.back().h)==L"\\\\?\\"+p.parent_path().wstring()||physical(held.back().h)==p.parent_path().wstring());
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
    const size_t n=name.size()*sizeof(wchar_t);
    std::vector<uint8_t> storage(offsetof(FILE_RENAME_INFO,FileName)+n);
    auto info=reinterpret_cast<FILE_RENAME_INFO*>(storage.data());
    info->ReplaceIfExists=FALSE;
    info->RootDirectory=parent;
    info->FileNameLength=DWORD(n);
    memcpy(info->FileName,name.data(),n);
    installedBudget(end);
    require(SetFileInformationByHandle(file,FileRenameInfo,info,DWORD(storage.size())));
    installedBudget(end);
  }
  inline void applyShortcut(const Generation& g,uint64_t end){
    auto p=shortcutPath(g);
    auto parents=shortcutParents(p);
    if(GetFileAttributesW(p.c_str())!=INVALID_FILE_ATTRIBUTES){
      shortcutReadback(g);
      installedBudget(end);
      return;
    }
    require(GetLastError()==ERROR_FILE_NOT_FOUND);
    Microsoft::WRL::ComPtr<IShellLinkW> link;
    winrt::check_hresult(CoCreateInstance(CLSID_ShellLink,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&link)));
    winrt::check_hresult(link->SetPath((g.root/L"helper.exe").c_str()));
    winrt::check_hresult(link->SetArguments(L"--callback"));
    Microsoft::WRL::ComPtr<IPropertyStore> properties;
    winrt::check_hresult(link.As(&properties));
    PROPVARIANT v{
    };
    winrt::check_hresult(InitPropVariantFromString(wide(g.snapshot.aumid).c_str(),&v));
    winrt::check_hresult(properties->SetValue(PKEY_AppUserModel_ID,v));
    PropVariantClear(&v);
    GUID clsid{
    };
    winrt::check_hresult(CLSIDFromString(wide(g.snapshot.clsid).c_str(),&clsid));
    winrt::check_hresult(InitPropVariantFromCLSID(clsid,&v));
    winrt::check_hresult(properties->SetValue(PKEY_AppUserModel_ToastActivatorCLSID,v));
    PropVariantClear(&v);
    winrt::check_hresult(properties->Commit());
    auto data=linkBytes(link.Get());
    shortcutReadbackBytes(g,data);
    installedBudget(end);
    auto stage=p;
    stage+=L".stage";
    if(GetFileAttributesW(stage.c_str())==INVALID_FILE_ATTRIBUTES){
      require(GetLastError()==ERROR_FILE_NOT_FOUND);
      installedBudget(end);
      exclusiveFile(stage,g.sid,data);
      installedBudget(end);
    }
    auto file=writableLeaf(stage,g);
    auto held=bytes(file.h,65536);
    require(held==data);
    shortcutReadbackBytes(g,held);
    installedBudget(end);
    renameLeaf(file.h,parents.back().h,p.filename().wstring(),end);
    file.closeChecked();
    shortcutReadback(g);
    installedBudget(end);
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
    auto p=shortcutPath(g);
    auto parents=shortcutParents(p);
    if(GetFileAttributesW(p.c_str())==INVALID_FILE_ATTRIBUTES){
      require(GetLastError()==ERROR_FILE_NOT_FOUND);
      return;
    }
    auto file=writableLeaf(p,g);
    shortcutReadbackBytes(g,bytes(file.h,65536));
    installedBudget(end);
    FILE_DISPOSITION_INFO disposition{
      TRUE
    };
    require(SetFileInformationByHandle(file.h,FileDispositionInfo,&disposition,sizeof(disposition)));
    installedBudget(end);
    file.closeChecked();
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
}
