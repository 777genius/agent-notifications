// TEST-only real Win32 registry contract. All registration is redirected into
// one exclusive private HKCU subtree; no SDK/package/COM/Shell target operation.
#define NOMINMAX
#include "installed.h"
#include <thread>
#include <cstring>
using namespace wcb;
struct Descriptor {
  PSECURITY_DESCRIPTOR p=nullptr;
  explicit Descriptor(const std::wstring& acl){
    require(ConvertStringSecurityDescriptorToSecurityDescriptorW(acl.c_str(),SDDL_REVISION_1,&p,nullptr));
  }
  ~Descriptor(){LocalFree(p);
  }
  SECURITY_ATTRIBUTES attributes(){return {sizeof(SECURITY_ATTRIBUTES),p,FALSE};
  }
};
static std::wstring ownACL(const wchar_t* rights){
  const auto sid=wide(ownerSID());
  return L"O:"+sid+L"D:P(A;;"+rights+L";;;"+sid+L")(A;;"+rights+L";;;SY)";
}
static void testCreate(HKEY parent,const wchar_t* leaf,Key& key,SECURITY_ATTRIBUTES* sa=nullptr,DWORD options=0,REGSAM access=KEY_ALL_ACCESS){
  DWORD disposition=0;
  require(RegCreateKeyExW(parent,leaf,0,nullptr,options,access,sa,&key.h,&disposition)==ERROR_SUCCESS);
  require(disposition==REG_CREATED_NEW_KEY);
}
static void missing(HKEY parent,const std::wstring& leaf){
  Key key;
  require(RegOpenKeyExW(parent,leaf.c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&key.h)==ERROR_FILE_NOT_FOUND);
}
static std::vector<uint8_t> security(HKEY key){
  DWORD n=0;
  const auto fields=OWNER_SECURITY_INFORMATION|DACL_SECURITY_INFORMATION;
  require(RegGetKeySecurity(key,fields,nullptr,&n)==ERROR_INSUFFICIENT_BUFFER&&n&&n<=65536);
  std::vector<uint8_t> result(n);
  DWORD size=n;
  require(RegGetKeySecurity(key,fields,reinterpret_cast<PSECURITY_DESCRIPTOR>(result.data()),&size)==ERROR_SUCCESS&&size==n);
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
    require(RegOpenKeyExW(HKEY_CURRENT_USER,L"Software",REG_OPTION_OPEN_LINK,KEY_READ|KEY_WRITE|DELETE,&realSoftware.h)==ERROR_SUCCESS);
    regularRegistryKey(realSoftware.h);
    Descriptor descriptor(ownACL(L"KA"));
    auto sa=descriptor.attributes();
    testCreate(realSoftware.h,leaf.c_str(),root,&sa);
    ownedRegistry(root.h,g);
    put(root.h,L"TESTOwner",leaf,GetTickCount64()+30000);
    require(RegOverridePredefKey(HKEY_CURRENT_USER,root.h)==ERROR_SUCCESS);
    mapped=true;
  }
  ~Profile(){if(mapped)RegOverridePredefKey(HKEY_CURRENT_USER,nullptr);
  }
  void close(const Generation& g){
    ownedRegistry(root.h,g);
    require(value(root.h,L"TESTOwner")==leaf);
    require(RegOverridePredefKey(HKEY_CURRENT_USER,nullptr)==ERROR_SUCCESS);
    mapped=false;
    root.close();
    if(containsLinks){
      // Public recursive deletion is not an admitted no-follow link unlink.
      // The exclusive owned TEST namespace survives only this disposable job.
      realSoftware.close();
      std::cout<<"TEST retained_registry_subtree=Software\\"<<narrow(leaf)
        <<" retained_until_job_teardown=true reason=registry_link_contract_fixture\n";
    }else{
      require(RegDeleteTreeW(realSoftware.h,leaf.c_str())==ERROR_SUCCESS);
      require(RegFlushKey(realSoftware.h)==ERROR_SUCCESS);
      realSoftware.close();
    }
  }
};
static void linkRefusesChild(HKEY source,HKEY target,const Generation& g,uint64_t end){
  installedBudget(end);
  Key child;
  DWORD disposition=0;
  const auto status=RegCreateKeyExW(source,wide(g.snapshot.aumid).c_str(),0,nullptr,
    REG_OPTION_OPEN_LINK,KEY_READ|KEY_WRITE,nullptr,&child.h,&disposition);
  installedBudget(end);
  std::cout<<"TEST direct relative link-root create status="<<status<<'\n';
  require(status!=ERROR_SUCCESS);
  missing(target,wide(g.snapshot.aumid));
  require(value(target,L"sentinel")==L"unchanged");
}
static bool registryContracts(){
  Generation g;
  Profile profile(g);
  const auto end=GetTickCount64()+30000;
  Key software,classes;
  const auto sid=wide(g.sid);
  Descriptor shared(L"O:"+sid+L"D:P(A;CI;KA;;;"+sid+L")(A;CI;KA;;;SY)");
  auto sharedSA=shared.attributes();
  testCreate(HKEY_CURRENT_USER,L"Software",software,&sharedSA);
  testCreate(software.h,L"Classes",classes,&sharedSA);
  // Breakage: old setup requires the shared parent and fails on a fresh profile.
  // The literal old admission returns missing; actual apply creates valid own keys.
  missing(classes.h,L"AppUserModelId");
  missing(classes.h,L"CLSID");
  const auto classesSecurity=security(classes.h);
  applyApp(g,end);
  applyClass(g,end);
  registryReadback(g);
  Key parent,foreign;
  require(RegOpenKeyExW(classes.h,L"AppUserModelId",REG_OPTION_OPEN_LINK,KEY_ALL_ACCESS,&parent.h)==ERROR_SUCCESS);
  const auto parentSecurity=security(parent.h);
  Key clsid;
  require(RegOpenKeyExW(classes.h,L"CLSID",REG_OPTION_OPEN_LINK,KEY_READ,&clsid.h)==ERROR_SUCCESS);
  const auto clsidSecurity=security(clsid.h);
  SECURITY_DESCRIPTOR_CONTROL control{};
  DWORD revision=0;
  require(GetSecurityDescriptorControl(reinterpret_cast<PSECURITY_DESCRIPTOR>(const_cast<uint8_t*>(parentSecurity.data())),&control,&revision)
    &&!(control&SE_DACL_PROTECTED));
  DWORD type=0,size=0;
  require(RegQueryValueExW(parent.h,L"OwnerGeneration",nullptr,&type,nullptr,&size)==ERROR_FILE_NOT_FOUND);
  testCreate(parent.h,L"ForeignSibling",foreign);
  put(foreign.h,L"sentinel",L"unchanged",end);
  // Breakage: rollback deletes shared container/foreign sibling or changes ACLs.
  restoreApp(g,end);
  restoreClass(g,end);
  missing(parent.h,wide(g.snapshot.aumid));
  require(value(foreign.h,L"sentinel")==L"unchanged");
  require(security(parent.h)==parentSecurity&&security(classes.h)==classesSecurity&&security(clsid.h)==clsidSecurity);
  clsid.close();
  // Breakage: collision overwrites another generation's owned stamp/values.
  Key collision;
  createKey(g,parent.h,wide(g.snapshot.aumid),collision,end);
  put(collision.h,L"OwnerGeneration",L"foreign-generation",end);
  collision.close();
  refused([&]{applyApp(g,end);},end);
  refused([&]{restoreApp(g,end);},end);
  require(RegOpenKeyExW(parent.h,wide(g.snapshot.aumid).c_str(),REG_OPTION_OPEN_LINK,KEY_READ,&collision.h)==ERROR_SUCCESS);
  require(value(collision.h,L"OwnerGeneration")==L"foreign-generation");
  require(RegQueryValueExW(collision.h,L"DisplayName",nullptr,&type,nullptr,&size)==ERROR_FILE_NOT_FOUND);
  collision.close();
  foreign.close();
  parent.close();
  require(RegDeleteTreeW(classes.h,L"AppUserModelId")==ERROR_SUCCESS);
  // Breakage: access denied is treated as missing and setup repairs shared ACLs.
  Key denied;
  Descriptor deny(L"O:"+wide(g.sid)+L"D:P(D;;CC;;;"+wide(g.sid)+L")(A;;KA;;;"+wide(g.sid)+L")(A;;KA;;;SY)");
  auto denySA=deny.attributes();
  testCreate(classes.h,L"AppUserModelId",denied,&denySA,0,KEY_READ|WRITE_DAC|DELETE);
  const auto deniedSecurity=security(denied.h);
  refused([&]{applyApp(g,end);},end);
  missing(denied.h,wide(g.snapshot.aumid));
  require(security(denied.h)==deniedSecurity);
  // TEST-owned ACL cleanup only, after the production refusal assertion.
  Descriptor allow(ownACL(L"KA"));
  require(RegSetKeySecurity(denied.h,DACL_SECURITY_INFORMATION,allow.p)==ERROR_SUCCESS);
  denied.close();
  require(RegDeleteTreeW(classes.h,L"AppUserModelId")==ERROR_SUCCESS);
  // Breakage: a configured registry link is followed and receives an owned leaf.
  Key target,link;
  testCreate(classes.h,L"SafeTarget",target);
  put(target.h,L"sentinel",L"unchanged",end);
  DWORD disposition=0;
  const auto status=RegCreateKeyExW(classes.h,L"AppUserModelId",0,nullptr,REG_OPTION_CREATE_LINK,KEY_ALL_ACCESS,nullptr,&link.h,&disposition);
  bool linksQualified=true;
  if(status==ERROR_ACCESS_DENIED||status==ERROR_PRIVILEGE_NOT_HELD||status==ERROR_NOT_SUPPORTED){
    linksQualified=false;
    std::cout<<"TEST registry link fixture unsupported status="<<status<<"; link qualification pending\n";
  }else{
    require(status==ERROR_SUCCESS&&disposition==REG_CREATED_NEW_KEY);
    profile.containsLinks=true;
    linkRefusesChild(link.h,target.h,g,end);
    link.close();
    refused([&]{applyApp(g,end);},end);
    missing(target.h,wide(g.snapshot.aumid));
    require(RegOpenKeyExW(classes.h,L"AppUserModelId",REG_OPTION_OPEN_LINK,KEY_ALL_ACCESS,&link.h)==ERROR_SUCCESS);
    require(RegQueryValueExW(link.h,L"SymbolicLinkValue",nullptr,&type,nullptr,&size)==ERROR_FILE_NOT_FOUND);
    const auto route=L"\\Registry\\User\\"+wide(g.sid)+L"\\Software\\"+profile.leaf+L"\\Software\\Classes\\SafeTarget";
    require(RegSetValueExW(link.h,L"SymbolicLinkValue",0,REG_LINK,reinterpret_cast<const BYTE*>(route.c_str()),DWORD(route.size()*2))==ERROR_SUCCESS);
    // Prove the public local creation flag returns the source, not its target.
    Key nofollow;
    DWORD opened=0;
    require(RegCreateKeyExW(classes.h,L"AppUserModelId",0,nullptr,REG_OPTION_OPEN_LINK,
      KEY_READ|KEY_WRITE,nullptr,&nofollow.h,&opened)==ERROR_SUCCESS&&opened==REG_OPENED_EXISTING_KEY);
    DWORD linkType=0,linkBytes=0;
    require(RegQueryValueExW(nofollow.h,L"SymbolicLinkValue",nullptr,&linkType,nullptr,&linkBytes)==ERROR_SUCCESS
      &&linkType==REG_LINK&&linkBytes==route.size()*2);
    linkRefusesChild(nofollow.h,target.h,g,end);
    nofollow.close();
    link.close();
    refused([&]{applyApp(g,end);},end);
    missing(target.h,wide(g.snapshot.aumid));
    require(value(target.h,L"sentinel")==L"unchanged");
    require(RegOpenKeyExW(classes.h,L"AppUserModelId",REG_OPTION_OPEN_LINK,KEY_READ,&link.h)==ERROR_SUCCESS);
    std::vector<uint8_t> sourceRoute(route.size()*2);
    DWORD routeBytes=DWORD(sourceRoute.size()),routeType=0;
    require(RegQueryValueExW(link.h,L"SymbolicLinkValue",nullptr,&routeType,sourceRoute.data(),&routeBytes)==ERROR_SUCCESS
      &&routeType==REG_LINK&&routeBytes==sourceRoute.size()
      &&!memcmp(sourceRoute.data(),route.data(),sourceRoute.size()));
    link.close();
  }
  target.close();
  classes.close();
  software.close();
  profile.close(g);
  if(linksQualified)std::cout<<"TEST real registry contracts passed; private process-local HKCU mapping only\n";
  return linksQualified;
}
static void writePrivate(const fs::path& path,const std::vector<uint8_t>& data,SECURITY_ATTRIBUTES& sa){
  Handle file(CreateFileW(path.c_str(),GENERIC_WRITE,0,&sa,CREATE_NEW,FILE_ATTRIBUTE_NORMAL,nullptr));
  require(file.h!=INVALID_HANDLE_VALUE);
  DWORD written=0;
  require(WriteFile(file.h,data.data(),DWORD(data.size()),&written,nullptr)&&written==data.size());
  require(FlushFileBuffers(file.h));
  file.closeChecked();
}
static void prepareAndCollect(){
  wchar_t runner[32768]{},module[32768]{};
  const auto runnerSize=GetEnvironmentVariableW(L"RUNNER_TEMP",runner,32768);
  const auto moduleSize=GetModuleFileNameW(nullptr,module,32768);
  require(runnerSize&&runnerSize<32768&&moduleSize&&moduleSize<32768);
  auto base=open(runner,true);
  const auto root=fs::path(physical(base.h))/(L"TEST-registration-"+wide(randomID()));
  Descriptor descriptor(ownACL(L"FA"));
  auto sa=descriptor.attributes();
  require(CreateDirectoryW(root.c_str(),&sa));
  auto directory=open(root,true);
  for(const auto leaf:{L"records",L"attempts"})require(CreateDirectoryW((root/leaf).c_str(),&sa));
  auto original=open(module,false);
  const auto image=bytes(original.h,16*1024*1024);
  writePrivate(root/L"helper.exe",image,sa);
  Snapshot snapshot{randomID(),narrow(root.wstring()),ownerSID(),sha(image),"AgentNotifications.TEST.Registry",
    "{01234567-89ab-cdef-0123-456789abcdef}","OpenAI.Codex","CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B",
    "OpenAI.Codex_2p2nqsd0c76g0","OpenAI.Codex_1.2.3.4_x64__2p2nqsd0c76g0"};
  snapshot.validate();
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
  if(startup.hStdInput&&startup.hStdInput!=INVALID_HANDLE_VALUE)
    require(SetHandleInformation(startup.hStdInput,HANDLE_FLAG_INHERIT,HANDLE_FLAG_INHERIT));
  else startup.hStdInput=nullptr;
  for(const auto handle:{startup.hStdOutput,startup.hStdError})
    require(handle&&handle!=INVALID_HANDLE_VALUE&&SetHandleInformation(handle,HANDLE_FLAG_INHERIT,HANDLE_FLAG_INHERIT));
  require(CreateProcessW((root/L"helper.exe").c_str(),commandLine.data(),nullptr,nullptr,TRUE,0,nullptr,nullptr,&startup,&child));
  Handle process(child.hProcess),thread(child.hThread);
  thread.closeChecked();
  auto wait=WaitForSingleObject(process.h,40000);
  if(wait==WAIT_TIMEOUT){require(TerminateProcess(process.h,124));
  wait=WaitForSingleObject(process.h,5000);
  }
  require(wait==WAIT_OBJECT_0);
  DWORD exit=1;
  require(GetExitCodeProcess(process.h,&exit));
  process.closeChecked();
  directory.closeChecked();
  require(DeleteFileW((root/L"generation.wne").c_str())&&DeleteFileW((root/L"helper.exe").c_str()));
  require(RemoveDirectoryW((root/L"records").c_str())&&RemoveDirectoryW((root/L"attempts").c_str())&&RemoveDirectoryW(root.c_str()));
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
  }catch(const std::exception& error){std::cerr<<"TEST registration contract failed: "<<error.what()<<'\n';
  return 1;
  }
}
