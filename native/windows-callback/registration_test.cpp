// TEST-only real Win32 registry and Shell link contracts. Registration is
// redirected into one exclusive private HKCU subtree. Shortcut target fixtures
// are bounded, exclusive TEST-owned files; no SDK/package/notification effect
// and no shipping/user shortcut or target is created.
#define NOMINMAX
#define WCB_TEST_SHORTCUT_DEADLINE_PROBES 1
#include "installed.h"
#include <thread>
#include <cstring>
#include <cstdio>
#include <exception>
#include <type_traits>
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
static std::string statusValueText(bool known,DWORD value){
  return known?std::to_string(static_cast<unsigned long>(value)):std::string("unknown");
}
// TEST-only fixture failures retain the exact boundary without including any
// private path in process output. Injected failures are contract observations,
// not Win32 API results.
struct TestFixtureFailure : std::exception {
  const char* api;
  DWORD status;
  bool apiFailure;
  bool synthetic;
  char detail[256]{};
  TestFixtureFailure(const char* name,DWORD value,bool apiResult=false,bool syntheticResult=true)
    :api(name),status(value),apiFailure(apiResult),synthetic(syntheticResult){
    std::snprintf(detail,sizeof(detail),
      "TEST fixture failure api=%s status=%lu apiFailure=%s synthetic=%s",
      name,static_cast<unsigned long>(value),apiResult?"true":"false",
      syntheticResult?"true":"false");
  }
  const char* what()const noexcept override{return detail;}
};
static void fixtureWin32(const char* name,BOOL result){
  if(win32Result(name,result))return;
  throw TestFixtureFailure(name,lastTestResult,true,false);
}
enum class FixtureEffectFault {
  none,after_shortcut_file_effect,before_shortcut_file_custody,
  after_target_tree_directory_effect,after_target_tree_file_effect,
  before_target_tree_file_custody
};
enum class FixtureCleanupKind {close_handle,delete_file,remove_directory};
struct FixtureCleanupDecision {
  DWORD result=ERROR_SUCCESS;
  bool statusKnown=false;
};
struct TestFixtureFaults {
  static inline FixtureEffectFault effect=FixtureEffectFault::none;
  static inline FixtureCleanupKind cleanup=FixtureCleanupKind::close_handle;
  static inline bool cleanupAllKinds=false;
  static inline size_t cleanupFailures=0;
  static inline bool fidQueryFailure=false;
  static inline bool fidQueryFailureOnCleanup=false;
  static inline DWORD fidQueryStatus=ERROR_SUCCESS;
  static inline HANDLE fidHandle=INVALID_HANDLE_VALUE;
  static inline bool identityMismatchOnCleanup=false;
  static inline bool replaceFile=false,replaceDirectory=false;
  static inline size_t replaceOnAttempt=0,replaceIndex=0;
  static inline bool replacementAttempted=false,replacementRenamed=false;
  static inline bool replacementCreated=false;
  static inline std::array<BY_HANDLE_FILE_INFORMATION,6> adoptedFids{};
  static inline unsigned adoptedFidMask=0;
  static void reset()noexcept{
    effect=FixtureEffectFault::none;cleanupAllKinds=false;cleanupFailures=0;
    fidQueryFailure=fidQueryFailureOnCleanup=false;fidQueryStatus=ERROR_SUCCESS;
    fidHandle=INVALID_HANDLE_VALUE;
    identityMismatchOnCleanup=false;
    replaceFile=replaceDirectory=false;replaceOnAttempt=0;replaceIndex=0;
    replacementAttempted=replacementRenamed=replacementCreated=false;
  }
  static void resetAdoptedFids()noexcept{
    adoptedFids={};
    adoptedFidMask=0;
  }
  static void captureAdoptedFid(size_t entryIndex,
   const BY_HANDLE_FILE_INFORMATION& identity)noexcept{
    if(entryIndex<adoptedFids.size()){
      adoptedFids[entryIndex]=identity;
      adoptedFidMask|=1u<<entryIndex;
    }
  }
  static void afterEffect(FixtureEffectFault point,const char* api){
    if(effect!=point)return;
    effect=FixtureEffectFault::none;
    throw TestFixtureFailure(api,ERROR_OPERATION_ABORTED,false,true);
  }
  // This is a synthetic unwind at the first post-effect, pre-custody boundary.
  // It does not inject an allocator failure or qualify a native API status.
  static void syntheticUnwind(FixtureEffectFault point,const char* api){
    if(effect!=point)return;
    effect=FixtureEffectFault::none;
    throw TestFixtureFailure(api,ERROR_NOT_ENOUGH_MEMORY,false,true);
  }
  static void failCleanup(FixtureCleanupKind kind)noexcept{
    cleanup=kind;cleanupAllKinds=false;cleanupFailures=1;
  }
  static void failCleanupAll(size_t count)noexcept{
    cleanupAllKinds=true;cleanupFailures=count;
  }
  static FixtureCleanupDecision cleanupResult(FixtureCleanupKind kind)noexcept{
    if(!cleanupFailures||(!cleanupAllKinds&&cleanup!=kind))return {};
    --cleanupFailures;
    return {ERROR_ACCESS_DENIED,false};
  }
  static bool replacementPoint(bool file,size_t index,size_t attempt)noexcept{
    return replaceOnAttempt&&replaceOnAttempt==attempt&&replaceIndex==index
      &&(file?replaceFile:replaceDirectory);
  }
};
// The seam executes a real GetFileInformationByHandle failure against an
// invalid probe handle while retaining the owned handle. The native error is
// read at that boundary and preserved by fixtureWin32 below.
static BOOL fixtureIdentityQuery(HANDLE value,BY_HANDLE_FILE_INFORMATION* identity,
 bool cleanupCall=false){
  const bool inject=cleanupCall?TestFixtureFaults::fidQueryFailureOnCleanup
    :TestFixtureFaults::fidQueryFailure;
  const HANDLE query=inject?INVALID_HANDLE_VALUE:value;
  if(inject){
    if(cleanupCall)TestFixtureFaults::fidQueryFailureOnCleanup=false;
    else TestFixtureFaults::fidQueryFailure=false;
  }
  const BOOL result=GetFileInformationByHandle(query,identity);
  if(inject)TestFixtureFaults::fidQueryStatus=result?ERROR_SUCCESS:GetLastError();
  return win32Result("GetFileInformationByHandle",result);
}
struct FixtureCleanupRecord {
  const char* api=nullptr;
  DWORD result=ERROR_SUCCESS;
  bool statusKnown=false;
  size_t entryIndex=~size_t{0};
  BY_HANDLE_FILE_INFORMATION identity{};
  bool identityKnown=false;
};
struct FixtureRollbackReport {
  // Six owned effects can each yield one close and one removal failure.
  static constexpr size_t Capacity=12;
  std::array<FixtureCleanupRecord,Capacity> records{};
  size_t count=0;
  bool overflow=false;
  unsigned remainingMask=0;
  bool clean()const noexcept{return count==0;}
  bool complete()const noexcept{return !overflow;}
  bool nativeStatusKnown()const noexcept{
    for(size_t i=0;i<count&&i<records.size();++i)
      if(records[i].statusKnown)return true;
    return false;
  }
  void add(const char* api,DWORD result,bool statusKnown)noexcept{
    if(count==records.size()){overflow=true;return;}
    records[count]={api,result,statusKnown};
    ++count;
  }
  void add(const char* api,DWORD result,bool statusKnown,size_t entryIndex,
   const BY_HANDLE_FILE_INFORMATION& identity,bool identityKnown)noexcept{
    if(count==records.size()){overflow=true;return;}
    records[count]={api,result,statusKnown,entryIndex,identity,identityKnown};
    ++count;
  }
  static bool sameRecordIdentity(const BY_HANDLE_FILE_INFORMATION& left,
   const BY_HANDLE_FILE_INFORMATION& right)noexcept{
    return left.dwVolumeSerialNumber==right.dwVolumeSerialNumber
      &&left.nFileIndexHigh==right.nFileIndexHigh
      &&left.nFileIndexLow==right.nFileIndexLow;
  }
  bool matches(size_t index,const char* expected,DWORD expectedStatus,
   bool expectedStatusKnown)const noexcept{
    if(index>=count||index>=records.size())return false;
    const auto& record=records[index];
    if(!record.api||!expected)return false;
    return !std::strcmp(record.api,expected)
      &&record.result==expectedStatus
      &&record.statusKnown==expectedStatusKnown;
  }
  bool matches(size_t index,const char* expected,DWORD expectedStatus,
   bool expectedStatusKnown,size_t expectedEntry,
   const BY_HANDLE_FILE_INFORMATION& expectedIdentity,
   bool expectedIdentityKnown)const noexcept{
    if(!matches(index,expected,expectedStatus,expectedStatusKnown))return false;
    const auto& record=records[index];
    return record.entryIndex==expectedEntry
      &&record.identityKnown==expectedIdentityKnown
      &&(!expectedIdentityKnown||sameRecordIdentity(record.identity,expectedIdentity));
  }
  bool entryMatches(size_t index,size_t expectedEntry,bool expectedIdentityKnown)const noexcept{
    return index<count&&index<records.size()
      &&records[index].entryIndex==expectedEntry
      &&records[index].identityKnown==expectedIdentityKnown;
  }
};
struct FixtureCleanupObservation {
  bool known=false;
  const char* api=nullptr;
  DWORD result=ERROR_SUCCESS;
  size_t count=0;
  size_t attempts=0;
  bool statusKnown=false;
  FixtureRollbackReport report{};
};
static FixtureCleanupObservation fixtureCleanupObservation;
static void clearFixtureCleanupObservation()noexcept{fixtureCleanupObservation={};}
static void observeFixtureCleanup(const FixtureRollbackReport& report)noexcept{
  ++fixtureCleanupObservation.attempts;
  if(report.clean())return;
  if(!fixtureCleanupObservation.known){
    fixtureCleanupObservation.known=true;
    fixtureCleanupObservation.api=report.records[0].api;
    fixtureCleanupObservation.result=report.records[0].result;
    fixtureCleanupObservation.statusKnown=report.records[0].statusKnown;
    fixtureCleanupObservation.report.remainingMask=report.remainingMask;
  }
  fixtureCleanupObservation.count+=report.count;
  for(size_t index=0;index<report.count&&index<report.records.size();++index)
    fixtureCleanupObservation.report.add(report.records[index].api,
      report.records[index].result,report.records[index].statusKnown,
      report.records[index].entryIndex,report.records[index].identity,
      report.records[index].identityKnown);
  if(report.overflow)fixtureCleanupObservation.report.overflow=true;
}
struct FixtureCleanupFailure : std::exception {
  FixtureRollbackReport report;
  const char* api;
  DWORD status;
  size_t count;
  bool statusKnown;
  char detail[512]{};
  explicit FixtureCleanupFailure(const FixtureRollbackReport& value)
    :report(value),
     api(value.records[0].api),status(value.records[0].result),count(value.count),
     statusKnown(value.records[0].statusKnown){
    std::snprintf(detail,sizeof(detail),
      "TEST fixture cleanup failed first_api=%s first_result=%lu "
      "first_status_known=%s failure_count=%zu complete=%s",
      value.records[0].api,static_cast<unsigned long>(value.records[0].result),
      value.records[0].statusKnown?"true":"false",value.count,
      value.complete()?"true":"false");
  }
  const char* what()const noexcept override{return detail;}
  bool matches(size_t index,const char* expected,DWORD expectedStatus,
   bool expectedStatusKnown)const noexcept{
    return report.matches(index,expected,expectedStatus,expectedStatusKnown);
  }
};
struct FixtureHandleState {
  size_t entryIndex=~size_t{0};
  HANDLE value=INVALID_HANDLE_VALUE;
  bool owned=false;
  bool closeAttempted=false;
  DWORD closeResult=ERROR_SUCCESS;
  bool closeStatusKnown=false;
  BY_HANDLE_FILE_INFORMATION identity{};
  bool identityKnown=false;
  bool closed()const noexcept{return closeAttempted&&value==INVALID_HANDLE_VALUE;}
};
class FixtureHandle {
  FixtureHandleState* state=nullptr;
public:
  FixtureHandle()=default;
  FixtureHandle(const FixtureHandle&)=delete;
  FixtureHandle& operator=(const FixtureHandle&)=delete;
  void bind(FixtureHandleState& value)noexcept{state=&value;}
  void adopt(HANDLE value){
    require(state&&state->value==INVALID_HANDLE_VALUE&&value!=INVALID_HANDLE_VALUE);
    state->value=value;
    TestFixtureFaults::fidHandle=value;
    // Ownership is recorded before the FID capture boundary. If that query
    // throws, the rollback entry can still remove this created effect safely.
    state->owned=true;
    BY_HANDLE_FILE_INFORMATION identity{};
    fixtureWin32("GetFileInformationByHandle",fixtureIdentityQuery(value,&identity,false));
    state->identity=identity;
    state->identityKnown=true;
    TestFixtureFaults::captureAdoptedFid(state->entryIndex,identity);
  }
  HANDLE get()const noexcept{return state?state->value:INVALID_HANDLE_VALUE;}
  const FixtureHandleState& custodyState()const{
    require(state);
    return *state;
  }
  // This borrower never closes rollback-owned custody. FixtureRollback is
  // declared first and destroyed last, including constructor-failure unwinds.
  ~FixtureHandle()noexcept=default;
};
struct FixtureRollbackEntry {
  FixtureCleanupKind kind=FixtureCleanupKind::close_handle;
  size_t index=0;
  bool active=false,effectRemoved=false;
  bool deleteMarked=false;
  size_t deleteAttempts=0;
  fs::path storagePath,replacementStorage;
  std::wstring replacementText;
  FixtureHandleState handle{};
};
struct FixtureIdentityCheck {
  bool match=false;
  bool queryFailed=false;
  DWORD result=ERROR_SUCCESS;
  bool statusKnown=false;
};
// Rollback owns its handle state. Scoped handles only borrow a pointer into
// this earlier-constructed member, so rollback runs after those handles and
// never dereferences a destroyed Handle. All bookkeeping is reserved before
// the first effect; activating a CREATE_NEW result cannot allocate.
template<size_t Capacity> class FixtureRollback {
  std::array<FixtureRollbackEntry,Capacity> entries{};
  size_t used=0;
  static FixtureCleanupDecision takeCleanup(FixtureCleanupKind kind)noexcept{
    return TestFixtureFaults::cleanupResult(kind);
  }
  static FixtureIdentityCheck identityCheck(FixtureHandleState& handle)noexcept{
    if(handle.value==INVALID_HANDLE_VALUE)
      return {false,false,ERROR_INVALID_HANDLE,true};
    BY_HANDLE_FILE_INFORMATION current{};
    if(!fixtureIdentityQuery(handle.value,&current,true))
      return {false,true,DWORD(GetLastError()),true};
    if(!handle.identityKnown){
      // The first FID capture failed while the created handle remained owned.
      // A later successful query establishes the same held object's identity
      // before disposition; no mutable path fallback is used.
      handle.identity=current;
      handle.identityKnown=true;
      return {true,false,ERROR_SUCCESS,false};
    }
    bool match=current.dwVolumeSerialNumber==handle.identity.dwVolumeSerialNumber
      &&current.nFileIndexHigh==handle.identity.nFileIndexHigh
      &&current.nFileIndexLow==handle.identity.nFileIndexLow;
    if(TestFixtureFaults::identityMismatchOnCleanup){
      TestFixtureFaults::identityMismatchOnCleanup=false;
      match=false;
    }
    return {match,false,ERROR_SUCCESS,false};
  }
  static bool renameForReplacement(FixtureRollbackEntry& entry,bool file,
   const FixtureIdentityCheck& identity)noexcept{
    TestFixtureFaults::replacementAttempted=true;
    if(!identity.match)return false;
    const auto& path=entry.replacementText;
    if(path.size()<5||path.size()>32000)return false;
    struct RenameBuffer {
      FILE_RENAME_INFO info{};
      wchar_t name[32000]{};
    } rename{};
    rename.info.ReplaceIfExists=FALSE;
    rename.info.RootDirectory=nullptr;
    rename.info.FileNameLength=DWORD(path.size()*sizeof(wchar_t));
    std::memcpy(rename.info.FileName,path.data(),path.size()*sizeof(wchar_t));
    if(!SetFileInformationByHandle(entry.handle.value,FileRenameInfo,
      &rename.info,DWORD(sizeof(FILE_RENAME_INFO)+path.size()*sizeof(wchar_t))))
      return false;
    TestFixtureFaults::replacementRenamed=true;
    if(file){
      const HANDLE foreign=CreateFileW(entry.storagePath.c_str(),
        GENERIC_WRITE,0,nullptr,CREATE_NEW,FILE_ATTRIBUTE_NORMAL,nullptr);
      if(foreign==INVALID_HANDLE_VALUE)return false;
      CloseHandle(foreign);
    }else if(!CreateDirectoryW(entry.storagePath.c_str(),nullptr))return false;
    TestFixtureFaults::replacementCreated=true;
    return true;
  }
  void finish(FixtureRollbackEntry& entry,FixtureRollbackReport& report)noexcept{
    if(!entry.active&&!entry.handle.owned)return;
    const bool handlePresent=entry.handle.value!=INVALID_HANDLE_VALUE;
    bool closeApproved=true;
    if(handlePresent){
      auto decision=takeCleanup(FixtureCleanupKind::close_handle);
      closeApproved=decision.result==ERROR_SUCCESS;
      if(!closeApproved)
        report.add("CloseHandle",decision.result,decision.statusKnown,entry.index,
          entry.handle.identity,entry.handle.identityKnown);
    }
    const bool file=entry.kind==FixtureCleanupKind::delete_file;
    const auto kind=file?FixtureCleanupKind::delete_file:FixtureCleanupKind::remove_directory;
    if(!entry.deleteMarked&&!entry.effectRemoved){
      ++entry.deleteAttempts;
      auto decision=takeCleanup(kind);
      auto result=decision.result;
      bool statusKnown=decision.statusKnown;
      const bool injected=decision.result!=ERROR_SUCCESS;
      if(result==ERROR_SUCCESS){
        statusKnown=true;
        const auto identity=identityCheck(entry.handle);
        if(TestFixtureFaults::replacementPoint(file,entry.index,entry.deleteAttempts))
          renameForReplacement(entry,file,identity);
        if(identity.queryFailed){
          result=identity.result;
          statusKnown=identity.statusKnown;
          report.add("GetFileInformationByHandle",result,statusKnown,entry.index,
            entry.handle.identity,entry.handle.identityKnown);
        }else if(!identity.match){
          // A successfully queried but changed FID is not an API failure. It
          // is a distinct unknown/fail-closed custody outcome.
          result=ERROR_ACCESS_DENIED;
          statusKnown=false;
          report.add("FixtureIdentityMismatch",result,statusKnown,entry.index,
            entry.handle.identity,entry.handle.identityKnown);
        }else{
          FILE_DISPOSITION_INFO disposition{TRUE};
          if(SetFileInformationByHandle(entry.handle.value,FileDispositionInfo,
            &disposition,sizeof(disposition))){
            entry.deleteMarked=true;
            entry.effectRemoved=true;
          }else{
            result=GetLastError();
            statusKnown=true;
            report.add("SetFileInformationByHandle",result,statusKnown,entry.index,
              entry.handle.identity,entry.handle.identityKnown);
          }
        }
      }
      if(injected&&result!=ERROR_SUCCESS&&!entry.effectRemoved)
        report.add(file?"DeleteFileW":"RemoveDirectoryW",result,statusKnown,
          entry.index,entry.handle.identity,entry.handle.identityKnown);
    }
    if(handlePresent&&closeApproved&&entry.deleteMarked){
      entry.handle.closeAttempted=true;
      if(CloseHandle(entry.handle.value)){
        entry.handle.closeResult=ERROR_SUCCESS;
        entry.handle.closeStatusKnown=true;
        entry.handle.value=INVALID_HANDLE_VALUE;
      }else{
        entry.handle.closeResult=GetLastError();
        entry.handle.closeStatusKnown=true;
        report.add("CloseHandle",entry.handle.closeResult,true,entry.index,
          entry.handle.identity,entry.handle.identityKnown);
      }
    }
    const bool handleClean=entry.handle.value==INVALID_HANDLE_VALUE;
    entry.active=!(handleClean&&entry.effectRemoved);
    if(handleClean)entry.handle.owned=false;
  }
public:
  FixtureRollbackEntry& reserve(FixtureCleanupKind kind,fs::path storage){
    require(used<Capacity);
    auto& entry=entries[used++];
    entry.index=used-1;
    entry.kind=kind;
    entry.active=false;
    entry.effectRemoved=false;
    entry.deleteMarked=false;
    entry.deleteAttempts=0;
    entry.storagePath=storage;
    entry.handle.entryIndex=entry.index;
    const auto name=storage.filename().wstring()+L".foreign-replacement";
    entry.replacementStorage=storage.parent_path()/name;
    entry.replacementText=entry.replacementStorage.wstring();
    return entry;
  }
  static void activate(FixtureRollbackEntry& entry)noexcept{
    entry.active=true;
    entry.handle.owned=true;
  }
  FixtureRollbackReport cleanup()noexcept{
    FixtureRollbackReport report;
    for(size_t i=Capacity;i>0;--i)
      if(entries[i-1].kind==FixtureCleanupKind::delete_file)finish(entries[i-1],report);
    for(size_t i=Capacity;i>0;--i)
      if(entries[i-1].kind==FixtureCleanupKind::remove_directory)finish(entries[i-1],report);
    for(size_t i=0;i<used&&i<32;i++)
      if(entries[i].active)report.remainingMask|=1u<<i;
    return report;
  }
  ~FixtureRollback()noexcept{observeFixtureCleanup(cleanup());}
};
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
static void diagnoseLongShortcutTarget(const std::vector<uint8_t>& bytes,
 const std::wstring& expected,uint64_t end){
  Microsoft::WRL::ComPtr<IShellLinkW> link;
  shortcutHresult("CoCreateInstance(CLSID_ShellLink)",
    CoCreateInstance(CLSID_ShellLink,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&link)));
  Microsoft::WRL::ComPtr<IPersistStream> persist;
  shortcutHresult("IShellLinkW::QueryInterface(IPersistStream)",link.As(&persist));
  Microsoft::WRL::ComPtr<IStream> stream;
  shortcutHresult("CreateStreamOnHGlobal",CreateStreamOnHGlobal(nullptr,TRUE,&stream));
  ULONG written=0;
  shortcutHresult("IStream::Write",stream->Write(bytes.data(),ULONG(bytes.size()),&written));
  require(written==bytes.size());
  LARGE_INTEGER zero{};
  shortcutHresult("IStream::Seek",stream->Seek(zero,STREAM_SEEK_SET,nullptr));
  shortcutHresult("IPersistStream::Load",persist->Load(stream.Get()));
  const auto actual=exactShortcutTarget(link.Get(),end);
  ScopedPidl pidl;
  shortcutHresult("IShellLinkW::GetIDList",link->GetIDList(&pidl.value));
  ScopedCoTaskMem parsing;
  shortcutHresult("SHGetNameFromIDList(parsing)",
    SHGetNameFromIDList(pidl.value,SIGDN_DESKTOPABSOLUTEPARSING,&parsing.value));
  require(expected.size()<2048&&actual.size()<2048&&parsing.value
    &&wcslen(parsing.value)<2048);
  const std::wstring parsingName(parsing.value);
  std::cerr<<"TEST long serialized target mismatch facts"
    <<" expected_length="<<expected.size()<<" filesystem_length="<<actual.size()
    <<" parsing_length="<<parsingName.size()
    <<" expected_filesystem_equal="<<(expected==actual?"true":"false")
    <<" expected_parsing_equal="<<(expected==parsingName?"true":"false")<<"\n";
}
static void shortcutNamespaceContract(){
  stage("child_shortcut_namespace_spelling");
  require(shortcutDOSParsingName(L"\\\\?\\C:\\TEST\\helper.exe")==L"C:\\TEST\\helper.exe");
  require(shortcutDOSParsingName(L"C:\\TEST\\helper.exe")==L"C:\\TEST\\helper.exe");
  for(const auto* unsupported:{L"\\\\?\\UNC\\server\\share\\helper.exe",
    L"\\\\?\\GLOBALROOT\\Device\\helper.exe",L"\\\\?\\C:\\TEST\\helper.exe:stream"}){
    bool refused=false;
    try{(void)shortcutDOSParsingName(unsupported);}
    catch(const std::runtime_error&){refused=true;}
    require(refused);
  }
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
  require(statusValueText(false,ERROR_ACCESS_DENIED)==std::string("unknown"));
  require(statusValueText(true,DWORD(E_INVALIDARG))
    ==std::to_string(static_cast<unsigned long>(DWORD(E_INVALIDARG))));
  ShortcutContractViolation unknownStatus("TEST unknown status",ERROR_SUCCESS,false);
  require(!unknownStatus.statusKnown
    &&statusValueText(unknownStatus.statusKnown,DWORD(unknownStatus.status))
      ==std::string("unknown"));
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
    throw ShortcutContractViolation("TEST-owned-co-task-memory",E_FAIL,false);
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
    throw ShortcutContractViolation("TEST-owned-pidl",E_FAIL,false);
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
    throw ShortcutContractViolation("TEST-owned-global-lock",E_FAIL,false);
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
static void shortcutApplyContracts(const Generation& g,uint64_t end){
  stage("child_shortcut_private_apply");
  const auto p=fs::path(g.root.wstring().substr(4))/(L"TEST-apply-"+wide(randomID())+L".lnk");
  const fs::path stagePath=p.wstring()+L".stage";
  OperatorDiagnostic diagnostic;
  try{
    applyShortcutAt(g,p,end,&diagnostic);
    require(!strcmp(diagnostic.phase,"shortcut_final_readback"));
    shortcutReadbackAt(g,p,end);
    require(GetFileAttributesW(stagePath.c_str())==INVALID_FILE_ATTRIBUTES
      &&GetLastError()==ERROR_FILE_NOT_FOUND);
    require(win32Result("DeleteFileW",DeleteFileW(p.c_str())));
  }catch(...){
    // Preserve the actual failing production boundary before TEST cleanup.
    diagnostic.emit();
    for(const auto& ownedPath:{p,stagePath}){
      if(!DeleteFileW(ownedPath.c_str())){
        const auto error=GetLastError();
        if(error!=ERROR_FILE_NOT_FOUND)
          registryStatus("TEST private apply cleanup failed",LSTATUS(error));
      }
    }
    throw;
  }
  std::cout<<"TEST private applyShortcut passed effects=private-test-root-only"
    <<" stage_rename=true final_readback=true diagnostic=shortcut_final_readback\n";
}
static std::wstring syntheticLongShortcutTarget(const ShortcutFixture& fixture){
  const auto separator=fixture.target.find_last_of(L'\\');
  require(separator!=std::wstring::npos&&separator>2);
  std::wstring target=fixture.target.substr(0,separator);
  for(size_t part=0;target.size()<=MAX_PATH;part++){
    target+=L'\\';
    target.append(96,wchar_t(L'a'+part%26));
  }
  target+=L"\\helper.exe";
  require(target.size()>MAX_PATH&&target.size()<20000&&target.starts_with(fixture.target.substr(0,3)));
  return target;
}
static fs::path extendedTestPath(const fs::path& path){
  const auto value=path.wstring();
  const auto drive=value.empty()?0:value[0];
  const bool ordinary=value.size()>3
    &&((drive>=65&&drive<=90)||(drive>=97&&drive<=122))
    &&value[1]==58&&value[2]==92&&!value.starts_with(L"\\\\?\\")
    &&value.find(58,2)==std::wstring::npos&&value.find(47)==std::wstring::npos
    &&value.back()!=92;
  if(!ordinary)throw TestFixtureFailure("TEST extendedTestPath prefix",E_INVALIDARG,false);
  return fs::path(L"\\\\?\\"+value);
}
static void requireFixtureStoragePrefix(const fs::path& path,const char* api){
  const auto value=path.wstring();
  const auto drive=value.size()>4?value[4]:0;
  const bool extended=value.size()>6&&value.starts_with(L"\\\\?\\")
    &&((drive>=65&&drive<=90)||(drive>=97&&drive<=122))
    &&value[5]==58&&value[6]==92&&value.find(58,6)==std::wstring::npos
    &&value.find(47)==std::wstring::npos&&value.back()!=92;
  if(!extended)throw TestFixtureFailure(api,E_INVALIDARG,false);
}
static std::wstring ordinaryFixtureTarget(const fs::path& storage){
  requireFixtureStoragePrefix(storage,"TEST fixture ordinary target prefix");
  return storage.wstring().substr(4);
}
static std::string fixturePathFacts(const std::wstring& value){
  const auto kind=value.starts_with(L"\\\\?\\")?"extended-drive"
    :(value.size()>2&&value[1]==58?"ordinary-drive":"other");
  std::string drive="none";
  const auto at=value.starts_with(L"\\\\?\\")?4:0;
  if(value.size()>at+1&&value[at+1]==58)drive=std::string(1,char(value[at]))+":";
  return std::string("namespace=")+kind+" length="+std::to_string(value.size())+" drive="+drive;
}
class ScopedShortcutTargetTree {
  // rollback is declared first so reverse destruction closes borrowed handles
  // before retrying owned-effect removal. Its state outlives those borrowers.
  FixtureRollback<6> rollback;
  std::array<fs::path,4> directoryStorage,directoryTarget;
  std::array<fs::path,2> fileStorage;
  std::array<FixtureHandle,4> heldDirectories;
  std::array<FixtureHandle,2> files;
  std::array<FixtureRollbackEntry*,6> rollbackEntries{};
  SECURITY_ATTRIBUTES* savedAttributes=nullptr;
  void createDirectory(size_t index){
    stage("child_shortcut_long_directory_create");
    fixtureWin32("CreateDirectoryW",
      CreateDirectoryW(directoryStorage[index].c_str(),savedAttributes));
    FixtureRollback<6>::activate(*rollbackEntries[index]);
    TestFixtureFaults::afterEffect(FixtureEffectFault::after_target_tree_directory_effect,
      "TEST after CreateDirectoryW");
    stage("child_shortcut_long_directory_open");
    const HANDLE opened=CreateFileW(directoryStorage[index].c_str(),
      GENERIC_READ|READ_CONTROL|DELETE,FILE_SHARE_READ|FILE_SHARE_WRITE,nullptr,OPEN_EXISTING,
      FILE_FLAG_OPEN_REPARSE_POINT|FILE_FLAG_BACKUP_SEMANTICS,nullptr);
    fixtureWin32("CreateFileW",opened!=INVALID_HANDLE_VALUE);
    heldDirectories[index].adopt(opened);
    stage("child_shortcut_long_directory_custody");
    owned(heldDirectories[index].get(),ownerSID());
  }
  void createTarget(size_t index){
    stage("child_shortcut_long_file_create");
    const HANDLE created=CreateFileW(fileStorage[index].c_str(),
      GENERIC_READ|READ_CONTROL|DELETE,FILE_SHARE_READ,savedAttributes,
      CREATE_NEW,FILE_ATTRIBUTE_NORMAL|FILE_FLAG_OPEN_REPARSE_POINT,nullptr);
    fixtureWin32("CreateFileW",created!=INVALID_HANDLE_VALUE);
    files[index].adopt(created);
    FixtureRollback<6>::activate(*rollbackEntries[index+4]);
    TestFixtureFaults::afterEffect(FixtureEffectFault::after_target_tree_file_effect,
      "TEST after target-tree CreateFileW");
    TestFixtureFaults::syntheticUnwind(FixtureEffectFault::before_target_tree_file_custody,
      "TEST target-tree synthetic unwind before custody");
    stage("child_shortcut_long_file_custody");
    owned(files[index].get(),ownerSID());
    stage("child_shortcut_long_file_physical_name");
    const auto held=physical(files[index].get());
    stage("child_shortcut_long_file_exact_path");
    const auto expected=fileStorage[index].wstring();
    const auto expectedOrdinary=expected.substr(4);
    if(held!=expected&&held!=expectedOrdinary)
      std::cerr<<"TEST long fixture exact path mismatch expected_facts="
        <<fixturePathFacts(expected)<<" held_facts="<<fixturePathFacts(held)<<"\n";
    require(held==expected||held==expectedOrdinary);
  }
public:
  std::wstring expectedTarget,substituteTarget;
  explicit ScopedShortcutTargetTree(const Generation& g,SECURITY_ATTRIBUTES& attributes,
   const wchar_t* leaf=L"shortcut-targets"):savedAttributes(&attributes){
    stage("child_shortcut_long_target_fixture");
    requireFixtureStoragePrefix(g.root,"ScopedShortcutTargetTree::extended_storage_prefix");
    require(leaf&&*leaf&&wcschr(leaf,L'\\')==nullptr&&wcschr(leaf,L':')==nullptr);
    const fs::path ordinaryRoot(g.root.wstring().substr(4));
    directoryTarget[0]=ordinaryRoot/leaf;
    for(size_t part=0;part<3;part++)
      directoryTarget[part+1]=directoryTarget[part]/std::wstring(96,wchar_t(L'a'+part%26));
    for(size_t part=0;part<4;part++){
      const auto text=directoryTarget[part].wstring();
      require(text.size()<32768&&directoryTarget[part].filename().wstring().size()<=255);
      directoryStorage[part]=extendedTestPath(directoryTarget[part]);
    }
    fileStorage[0]=directoryTarget[3]/L"expected-long.exe";
    fileStorage[1]=directoryTarget[3]/L"substituted-long.exe";
    for(auto& path:fileStorage)path=extendedTestPath(path);
    expectedTarget=ordinaryFixtureTarget(fileStorage[0]);
    substituteTarget=ordinaryFixtureTarget(fileStorage[1]);
    for(size_t index=0;index<4;index++)
      rollbackEntries[index]=&rollback.reserve(FixtureCleanupKind::remove_directory,
        directoryStorage[index]);
    for(size_t index=0;index<2;index++)
      rollbackEntries[index+4]=&rollback.reserve(FixtureCleanupKind::delete_file,
        fileStorage[index]);
    for(size_t index=0;index<4;index++)
      heldDirectories[index].bind(rollbackEntries[index]->handle);
    for(size_t index=0;index<2;index++)
      files[index].bind(rollbackEntries[index+4]->handle);
    try{
      for(size_t index=0;index<4;index++)createDirectory(index);
      createTarget(0);
      createTarget(1);
    }catch(...){
      const auto original=std::current_exception();
      observeFixtureCleanup(rollback.cleanup());
      std::rethrow_exception(original);
    }
    stage("child_shortcut_long_target_lengths");
    require(expectedTarget.size()>MAX_PATH&&substituteTarget.size()>MAX_PATH
      &&expectedTarget.size()<32768&&substituteTarget.size()<32768);
  }
  HANDLE expectedCustody()const{return files[0].get();}
  HANDLE substituteCustody()const{return files[1].get();}
  const FixtureHandleState& expectedState()const{return rollbackEntries[4]->handle;}
  const FixtureHandleState& substituteState()const{return rollbackEntries[5]->handle;}
  HANDLE custodyAt(size_t index)const{
    require(index<6);
    return index<4?heldDirectories[index].get():files[index-4].get();
  }
  const FixtureHandleState& stateAt(size_t index)const{
    require(index<6);
    return rollbackEntries[index]->handle;
  }
  const fs::path& storageAt(size_t index)const{
    require(index<6);
    return index<4?directoryStorage[index]:fileStorage[index-4];
  }
  void cleanup(){
    stage("child_shortcut_long_target_cleanup");
    const auto report=rollback.cleanup();
    observeFixtureCleanup(report);
    if(!report.clean())throw FixtureCleanupFailure(report);
  }
};
class ScopedShortcutFile {
  fs::path path;
  FixtureRollback<1> rollback;
  FixtureHandle file;
public:
  std::wstring target;
  explicit ScopedShortcutFile(const fs::path& parent,const wchar_t* name,SECURITY_ATTRIBUTES& attributes)
    :path(parent/name){
    requireFixtureStoragePrefix(path,"ScopedShortcutFile::extended_storage_prefix");
    target=ordinaryFixtureTarget(path);
    auto& entry=rollback.reserve(FixtureCleanupKind::delete_file,path);
    file.bind(entry.handle);
    try{
      const HANDLE created=CreateFileW(path.c_str(),GENERIC_READ|READ_CONTROL|DELETE,FILE_SHARE_READ,
        &attributes,CREATE_NEW,FILE_ATTRIBUTE_NORMAL|FILE_FLAG_OPEN_REPARSE_POINT,nullptr);
      fixtureWin32("CreateFileW",created!=INVALID_HANDLE_VALUE);
      file.adopt(created);
      FixtureRollback<1>::activate(entry);
      TestFixtureFaults::afterEffect(FixtureEffectFault::after_shortcut_file_effect,
        "TEST after shortcut-file CreateFileW");
      TestFixtureFaults::syntheticUnwind(FixtureEffectFault::before_shortcut_file_custody,
        "TEST shortcut-file synthetic unwind before custody");
      owned(file.get(),ownerSID());
      const auto held=physical(file.get());
      const auto expected=path.wstring();
      const auto expectedOrdinary=expected.substr(4);
      if(held!=expected&&held!=expectedOrdinary)
        std::cerr<<"TEST shortcut fixture exact path mismatch expected_facts="
          <<fixturePathFacts(expected)<<" held_facts="<<fixturePathFacts(held)<<"\n";
      require(held==expected||held==expectedOrdinary);
    }catch(...){
      const auto original=std::current_exception();
      observeFixtureCleanup(rollback.cleanup());
      std::rethrow_exception(original);
    }
  }
  HANDLE custody()const{return file.get();}
  const FixtureHandleState& custodyState()const{return file.custodyState();}
  void cleanup(){
    const auto report=rollback.cleanup();
    observeFixtureCleanup(report);
    if(!report.clean())throw FixtureCleanupFailure(report);
  }
};
static bool sameFixtureIdentity(HANDLE left,HANDLE right){
  BY_HANDLE_FILE_INFORMATION a{},b{};
  return GetFileInformationByHandle(left,&a)&&GetFileInformationByHandle(right,&b)
    &&a.dwVolumeSerialNumber==b.dwVolumeSerialNumber
    &&a.nFileIndexHigh==b.nFileIndexHigh&&a.nFileIndexLow==b.nFileIndexLow;
}
static bool fixtureOwnedLive(HANDLE held,const FixtureHandleState& state){
  BY_HANDLE_FILE_INFORMATION info{};
  return held!=INVALID_HANDLE_VALUE&&state.value==held&&state.owned&&!state.closed()
    &&GetFileInformationByHandle(held,&info)!=FALSE
    &&state.identityKnown
    &&info.dwVolumeSerialNumber==state.identity.dwVolumeSerialNumber
    &&info.nFileIndexHigh==state.identity.nFileIndexHigh
    &&info.nFileIndexLow==state.identity.nFileIndexLow;
}
static bool fixturePathIdentity(const fs::path& path,HANDLE held,bool directory){
  Handle probe(CreateFileW(path.c_str(),GENERIC_READ|READ_CONTROL,
    FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE,nullptr,OPEN_EXISTING,
    FILE_FLAG_OPEN_REPARSE_POINT|(directory?FILE_FLAG_BACKUP_SEMANTICS:0),nullptr));
  return probe.h!=INVALID_HANDLE_VALUE&&sameFixtureIdentity(held,probe.h);
}
static bool fixtureAbsent(const fs::path& path){
  const auto result=GetFileAttributesW(path.c_str());
  // Recursive owned cleanup may remove the path's parent before the final
  // absence check. Both missing leaf and missing parent prove no effect remains.
  // Foreign-object safety is checked by held-file identity before any deletion.
  const auto error=result==INVALID_FILE_ATTRIBUTES?GetLastError():ERROR_SUCCESS;
  return result==INVALID_FILE_ATTRIBUTES
    &&(error==ERROR_FILE_NOT_FOUND||error==ERROR_PATH_NOT_FOUND);
}
static std::wstring fixtureName(const wchar_t* stem){
  return std::wstring(stem)+wide(randomID());
}
static void fixtureRollbackContracts(const Generation& g){
  stage("child_fixture_rollback_contracts");
  clearFixtureCleanupObservation();
  TestFixtureFaults::reset();
  Descriptor descriptor(ownACL(L"FA"));
  auto attributes=descriptor.attributes();
  const fs::path ordinaryParent(g.root.wstring().substr(4));

  stage("child_fixture_cleanup_aggregation_overflow_observable");
  FixtureRollbackReport firstAttempt,secondAttempt;
  std::array<BY_HANDLE_FILE_INFORMATION,14> aggregateFids{};
  for(size_t index=0;index<aggregateFids.size();index++){
    aggregateFids[index].dwVolumeSerialNumber=DWORD(0x10203040+index);
    aggregateFids[index].nFileIndexHigh=DWORD(0x50607080+index);
    aggregateFids[index].nFileIndexLow=DWORD(0x90a0b0c0+index);
  }
  firstAttempt.remainingMask=0x15;
  for(size_t index=0;index<7;index++)
    firstAttempt.add("CloseHandle",ERROR_ACCESS_DENIED,false,index,aggregateFids[index],true);
  for(size_t index=7;index<aggregateFids.size();index++)
    secondAttempt.add("DeleteFileW",ERROR_ACCESS_DENIED,false,index,aggregateFids[index],true);
  observeFixtureCleanup(firstAttempt);
  observeFixtureCleanup(secondAttempt);
  bool aggregateOverflow=!firstAttempt.overflow&&!secondAttempt.overflow
    &&fixtureCleanupObservation.known
    &&fixtureCleanupObservation.api==firstAttempt.records[0].api
    &&fixtureCleanupObservation.result==firstAttempt.records[0].result
    &&!fixtureCleanupObservation.statusKnown
    &&fixtureCleanupObservation.attempts==2
    &&fixtureCleanupObservation.count==aggregateFids.size()
    &&fixtureCleanupObservation.report.Capacity==12
    &&fixtureCleanupObservation.report.records.size()==12
    &&fixtureCleanupObservation.report.count==12
    &&fixtureCleanupObservation.report.overflow
    &&!fixtureCleanupObservation.report.complete()
    &&fixtureCleanupObservation.report.remainingMask==0x15;
  for(size_t index=0;aggregateOverflow&&index<12;index++)
    aggregateOverflow=fixtureCleanupObservation.report.matches(
      index,index<7?"CloseHandle":"DeleteFileW",ERROR_ACCESS_DENIED,false,
      index,aggregateFids[index],true);
  require(aggregateOverflow);
  clearFixtureCleanupObservation();
  require(!fixtureCleanupObservation.known
    &&fixtureCleanupObservation.api==nullptr
    &&fixtureCleanupObservation.result==ERROR_SUCCESS
    &&fixtureCleanupObservation.count==0
    &&fixtureCleanupObservation.attempts==0
    &&!fixtureCleanupObservation.statusKnown
    &&fixtureCleanupObservation.report.count==0
    &&!fixtureCleanupObservation.report.overflow
    &&fixtureCleanupObservation.report.remainingMask==0);

  stage("child_fixture_prefix_refused");
  const auto prefixName=fixtureName(L"TEST-invalid-prefix-");
  bool prefixRefused=false;
  try{
    ScopedShortcutFile invalid(ordinaryParent,prefixName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    prefixRefused=!strcmp(error.api,"ScopedShortcutFile::extended_storage_prefix")
      &&error.status==E_INVALIDARG&&!error.apiFailure;
  }
  require(prefixRefused&&fixtureAbsent(ordinaryParent/prefixName));

  stage("child_fixture_foreign_collision_preserved");
  const auto foreignName=fixtureName(L"TEST-foreign-shortcut-");
  const auto foreignPath=g.root/foreignName;
  Handle foreign(CreateFileW(foreignPath.c_str(),GENERIC_READ|GENERIC_WRITE|DELETE,
    FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE,&attributes,CREATE_NEW,
    FILE_ATTRIBUTE_NORMAL|FILE_FLAG_OPEN_REPARSE_POINT,nullptr));
  fixtureWin32("CreateFileW",foreign.h!=INVALID_HANDLE_VALUE);
  BY_HANDLE_FILE_INFORMATION foreignBefore{};
  require(GetFileInformationByHandle(foreign.h,&foreignBefore));
  bool collisionRefused=false;
  try{
    ScopedShortcutFile fixture(g.root,foreignName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    collisionRefused=error.apiFailure&&!strcmp(error.api,"CreateFileW")
      &&(error.status==ERROR_FILE_EXISTS||error.status==ERROR_ALREADY_EXISTS
        ||error.status==ERROR_SHARING_VIOLATION);
  }
  Handle foreignCandidate(CreateFileW(foreignPath.c_str(),GENERIC_READ|READ_CONTROL,
    FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE,nullptr,OPEN_EXISTING,
    FILE_ATTRIBUTE_NORMAL|FILE_FLAG_OPEN_REPARSE_POINT,nullptr));
  fixtureWin32("CreateFileW",foreignCandidate.h!=INVALID_HANDLE_VALUE);
  require(collisionRefused&&sameFixtureIdentity(foreign.h,foreignCandidate.h));
  foreignCandidate.closeChecked();
  foreign.closeChecked();
  fixtureWin32("DeleteFileW",DeleteFileW(foreignPath.c_str()));

  stage("child_fixture_post_effect_rollback");
  const auto postEffectName=fixtureName(L"TEST-post-effect-shortcut-");
  TestFixtureFaults::effect=FixtureEffectFault::after_shortcut_file_effect;
  bool postEffectObserved=false;
  try{
    ScopedShortcutFile fixture(g.root,postEffectName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    postEffectObserved=error.synthetic&&!error.apiFailure
      &&error.status==ERROR_OPERATION_ABORTED
      &&!strcmp(error.api,"TEST after shortcut-file CreateFileW");
  }
  TestFixtureFaults::reset();
  require(postEffectObserved&&fixtureAbsent(g.root/postEffectName));

  stage("child_fixture_synthetic_unwind_rollback");
  const auto unwindName=fixtureName(L"TEST-synthetic-unwind-shortcut-");
  TestFixtureFaults::effect=FixtureEffectFault::before_shortcut_file_custody;
  bool unwindObserved=false;
  try{
    ScopedShortcutFile fixture(g.root,unwindName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    unwindObserved=error.synthetic&&!error.apiFailure
      &&error.status==ERROR_NOT_ENOUGH_MEMORY
      &&!strcmp(error.api,"TEST shortcut-file synthetic unwind before custody");
  }
  TestFixtureFaults::reset();
  require(unwindObserved&&fixtureAbsent(g.root/unwindName));

  stage("child_fixture_fid_capture_failure_preserved");
  const auto fidCaptureName=fixtureName(L"TEST-fid-capture-shortcut-");
  TestFixtureFaults::fidQueryFailure=true;
  bool fidCaptureObserved=false,fidCaptureRemoved=false;
  try{
    ScopedShortcutFile fixture(g.root,fidCaptureName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    const DWORD preserved=error.status;
    fidCaptureObserved=error.apiFailure&&!error.synthetic
      &&!std::strcmp(error.api,"GetFileInformationByHandle")
      &&preserved!=ERROR_SUCCESS&&preserved==TestFixtureFaults::fidQueryStatus
      &&TestFixtureFaults::fidHandle!=INVALID_HANDLE_VALUE
      &&fixtureCleanupObservation.known==false;
    fidCaptureRemoved=fixtureAbsent(g.root/fidCaptureName);
  }
  TestFixtureFaults::reset();
  require(fidCaptureObserved&&fidCaptureRemoved);
  clearFixtureCleanupObservation();

  stage("child_fixture_same_identity_api_failure_preserved");
  const auto sameIdentityName=fixtureName(L"TEST-same-identity-failure-");
  TestFixtureFaults::fidQueryFailureOnCleanup=true;
  bool sameIdentityObserved=false,sameIdentityRetried=false;
  bool sameIdentityObservationComplete=false;
  {
    ScopedShortcutFile fixture(g.root,sameIdentityName.c_str(),attributes);
    const auto sameIdentityFid=fixture.custodyState().identity;
    try{
      fixture.cleanup();
      require(false);
    }catch(const FixtureCleanupFailure& error){
      const DWORD preserved=error.report.records[0].result;
      sameIdentityObserved=error.statusKnown&&error.report.complete()
        &&error.report.count==1
        &&error.report.matches(0,"GetFileInformationByHandle",preserved,true)
        &&error.report.entryMatches(0,0,true)
        &&preserved!=ERROR_SUCCESS&&preserved==TestFixtureFaults::fidQueryStatus
        &&error.report.remainingMask==1;
    }
    fixture.cleanup();
    sameIdentityRetried=fixtureAbsent(g.root/sameIdentityName);
    sameIdentityObservationComplete=fixtureCleanupObservation.attempts==2
      &&fixtureCleanupObservation.report.complete()
      &&fixtureCleanupObservation.report.matches(0,"GetFileInformationByHandle",
        TestFixtureFaults::fidQueryStatus,true,0,sameIdentityFid,true);
  }
  TestFixtureFaults::reset();
  require(sameIdentityObserved&&sameIdentityRetried&&sameIdentityObservationComplete);
  clearFixtureCleanupObservation();

  stage("child_fixture_identity_mismatch_unknown_fail_closed");
  const auto mismatchName=fixtureName(L"TEST-identity-mismatch-");
  TestFixtureFaults::identityMismatchOnCleanup=true;
  bool mismatchObserved=false,mismatchRetried=false;
  {
    ScopedShortcutFile fixture(g.root,mismatchName.c_str(),attributes);
    const auto mismatchFid=fixture.custodyState().identity;
    try{
      fixture.cleanup();
      require(false);
    }catch(const FixtureCleanupFailure& error){
      mismatchObserved=!error.statusKnown&&error.report.complete()
        &&error.report.count==1
        &&error.report.matches(0,"FixtureIdentityMismatch",ERROR_ACCESS_DENIED,false,
          0,mismatchFid,true)
        &&error.report.remainingMask==1;
    }
    fixture.cleanup();
    mismatchRetried=fixtureAbsent(g.root/mismatchName);
  }
  TestFixtureFaults::reset();
  require(mismatchObserved&&mismatchRetried);
  clearFixtureCleanupObservation();

  stage("child_fixture_close_handle_cleanup_failure_observed");
  const auto closeName=fixtureName(L"TEST-close-handle-shortcut-");
  const auto closePath=g.root/closeName;
  bool closeObserved=false,remainingOwnedHandle=false,remainingOwnedEffect=false;
  bool retryClosed=false,retryEffectRemoved=false;
  {
    TestFixtureFaults::failCleanup(FixtureCleanupKind::close_handle);
    ScopedShortcutFile fixture(g.root,closeName.c_str(),attributes);
    const auto ownedHandle=fixture.custody();
    const auto& ownedState=fixture.custodyState();
    require(fixtureOwnedLive(ownedHandle,ownedState)
      &&fixturePathIdentity(closePath,ownedHandle,false));
    try{
      fixture.cleanup();
      require(false);
    }catch(const FixtureCleanupFailure& error){
      closeObserved=!error.statusKnown&&error.report.complete()&&error.count==1
        &&error.matches(0,"CloseHandle",ERROR_ACCESS_DENIED,false)
        &&error.report.remainingMask==1;
      remainingOwnedHandle=fixtureOwnedLive(ownedHandle,ownedState);
      remainingOwnedEffect=!fixtureAbsent(closePath);
    }
    fixture.cleanup();
    retryClosed=ownedState.closed()&&ownedState.closeStatusKnown
      &&ownedState.closeResult==ERROR_SUCCESS&&fixture.custody()==INVALID_HANDLE_VALUE;
    retryEffectRemoved=fixtureAbsent(closePath);
  }
  TestFixtureFaults::reset();
  require(closeObserved&&remainingOwnedHandle&&remainingOwnedEffect
    &&retryClosed&&retryEffectRemoved);
  clearFixtureCleanupObservation();

  stage("child_fixture_cleanup_failure_observed");
  const auto cleanupName=fixtureName(L"TEST-cleanup-shortcut-");
  bool cleanupObserved=false;
  {
    TestFixtureFaults::failCleanup(FixtureCleanupKind::delete_file);
    ScopedShortcutFile fixture(g.root,cleanupName.c_str(),attributes);
    try{
      fixture.cleanup();
      require(false);
    }catch(const FixtureCleanupFailure& error){
      cleanupObserved=!error.statusKnown&&error.report.complete()
        &&!strcmp(error.api,"DeleteFileW")
        &&error.status==ERROR_ACCESS_DENIED&&error.count==1
        &&error.matches(0,"DeleteFileW",ERROR_ACCESS_DENIED,false);
    }
  }
  TestFixtureFaults::reset();
  require(cleanupObserved&&fixtureAbsent(g.root/cleanupName));
  clearFixtureCleanupObservation();

  stage("child_fixture_original_failure_preserved");
  const auto originalName=fixtureName(L"TEST-original-shortcut-");
  TestFixtureFaults::effect=FixtureEffectFault::after_shortcut_file_effect;
  TestFixtureFaults::failCleanup(FixtureCleanupKind::delete_file);
  bool originalObserved=false;
  try{
    ScopedShortcutFile fixture(g.root,originalName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    originalObserved=error.synthetic&&!error.apiFailure
      &&error.status==ERROR_OPERATION_ABORTED
      &&!strcmp(error.api,"TEST after shortcut-file CreateFileW")
      &&fixtureCleanupObservation.known
      &&fixtureCleanupObservation.api
      &&!strcmp(fixtureCleanupObservation.api,"DeleteFileW")
      &&fixtureCleanupObservation.result==ERROR_ACCESS_DENIED
      &&fixtureCleanupObservation.count==1
      &&!fixtureCleanupObservation.statusKnown
      &&fixtureCleanupObservation.report.complete()
      &&fixtureCleanupObservation.report.matches(0,"DeleteFileW",ERROR_ACCESS_DENIED,false)
      &&fixtureCleanupObservation.report.remainingMask==1;
  }
  TestFixtureFaults::reset();
  require(originalObserved&&fixtureAbsent(g.root/originalName));
  clearFixtureCleanupObservation();

  stage("child_fixture_constructor_close_failure_destructor_retry");
  const auto closeConstructorName=fixtureName(L"TEST-tree-close-constructor-");
  TestFixtureFaults::effect=FixtureEffectFault::after_shortcut_file_effect;
  TestFixtureFaults::failCleanup(FixtureCleanupKind::close_handle);
  bool closeConstructorObserved=false;
  try{
    ScopedShortcutFile fixture(g.root,closeConstructorName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    const auto& cleanup=fixtureCleanupObservation;
    closeConstructorObserved=error.synthetic&&!error.apiFailure
      &&error.status==ERROR_OPERATION_ABORTED
      &&!strcmp(error.api,"TEST after shortcut-file CreateFileW")
      &&cleanup.known&&cleanup.count==1&&cleanup.report.complete()
      &&cleanup.report.matches(0,"CloseHandle",ERROR_ACCESS_DENIED,false)
      &&cleanup.report.remainingMask==1;
  }
  TestFixtureFaults::reset();
  require(closeConstructorObserved&&fixtureAbsent(g.root/closeConstructorName));
  clearFixtureCleanupObservation();

  stage("child_fixture_constructor_replacement_refused");
  const auto constructorReplacementName=fixtureName(L"TEST-constructor-replacement-");
  const auto constructorReplacementPath=g.root/constructorReplacementName;
  const auto constructorForeignPath=constructorReplacementPath;
  const auto constructorQuarantinePath=g.root/(constructorReplacementName+L".foreign-replacement");
  TestFixtureFaults::effect=FixtureEffectFault::after_shortcut_file_effect;
  TestFixtureFaults::failCleanup(FixtureCleanupKind::delete_file);
  TestFixtureFaults::replaceFile=true;
  TestFixtureFaults::replaceOnAttempt=2;
  TestFixtureFaults::replaceIndex=0;
  bool constructorReplacementObserved=false;
  bool constructorOriginalObserved=false;
  try{
    ScopedShortcutFile fixture(g.root,constructorReplacementName.c_str(),attributes);
    require(false);
  }catch(const TestFixtureFailure& error){
    constructorOriginalObserved=error.synthetic&&!error.apiFailure
      &&error.status==ERROR_OPERATION_ABORTED
      &&!strcmp(error.api,"TEST after shortcut-file CreateFileW");
    constructorReplacementObserved=TestFixtureFaults::replacementAttempted
      &&TestFixtureFaults::replacementRenamed
      &&TestFixtureFaults::replacementCreated;
  }
  TestFixtureFaults::reset();
  Handle constructorForeign(CreateFileW(constructorForeignPath.c_str(),
    GENERIC_READ|READ_CONTROL,FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE,
    nullptr,OPEN_EXISTING,FILE_ATTRIBUTE_NORMAL,nullptr));
  const bool constructorForeignPreserved=constructorOriginalObserved
    &&constructorReplacementObserved&&constructorForeign.h!=INVALID_HANDLE_VALUE
    &&fixtureAbsent(constructorQuarantinePath);
  constructorForeign.closeChecked();
  fixtureWin32("DeleteFileW",DeleteFileW(constructorForeignPath.c_str()));
  require(constructorForeignPreserved&&fixtureAbsent(constructorForeignPath));

  stage("child_fixture_destructor_replacement_refused");
  const auto destructorReplacementName=fixtureName(L"TEST-destructor-replacement-");
  const auto destructorReplacementPath=g.root/destructorReplacementName;
  const auto destructorForeignPath=destructorReplacementPath;
  const auto destructorQuarantinePath=g.root/(destructorReplacementName+L".foreign-replacement");
  TestFixtureFaults::replaceFile=true;
  TestFixtureFaults::replaceOnAttempt=1;
  TestFixtureFaults::replaceIndex=0;
  {
    ScopedShortcutFile fixture(g.root,destructorReplacementName.c_str(),attributes);
    require(fixtureOwnedLive(fixture.custody(),fixture.custodyState()));
  }
  const bool destructorReplacementObserved=TestFixtureFaults::replacementAttempted
    &&TestFixtureFaults::replacementRenamed&&TestFixtureFaults::replacementCreated;
  TestFixtureFaults::reset();
  Handle destructorForeign(CreateFileW(destructorForeignPath.c_str(),
    GENERIC_READ|READ_CONTROL,FILE_SHARE_READ|FILE_SHARE_WRITE|FILE_SHARE_DELETE,
    nullptr,OPEN_EXISTING,FILE_ATTRIBUTE_NORMAL,nullptr));
  const bool destructorForeignPreserved=destructorReplacementObserved
    &&destructorForeign.h!=INVALID_HANDLE_VALUE
    &&fixtureAbsent(destructorQuarantinePath);
  destructorForeign.closeChecked();
  fixtureWin32("DeleteFileW",DeleteFileW(destructorForeignPath.c_str()));
  require(destructorForeignPreserved&&fixtureAbsent(destructorForeignPath));

  stage("child_fixture_directory_destructor_replacement_refused");
  const auto directoryReplacementName=fixtureName(L"TEST-directory-replacement-");
  const auto directoryReplacementPath=g.root/directoryReplacementName;
  const auto directoryForeignPath=directoryReplacementPath;
  const auto directoryQuarantinePath=g.root/(directoryReplacementName+L".foreign-replacement");
  TestFixtureFaults::replaceDirectory=true;
  TestFixtureFaults::replaceOnAttempt=1;
  TestFixtureFaults::replaceIndex=0;
  {
    ScopedShortcutTargetTree tree(g,attributes,directoryReplacementName.c_str());
    require(tree.custodyAt(0)!=INVALID_HANDLE_VALUE);
  }
  const bool directoryReplacementObserved=TestFixtureFaults::replacementAttempted
    &&TestFixtureFaults::replacementRenamed&&TestFixtureFaults::replacementCreated;
  TestFixtureFaults::reset();
  auto directoryForeign=open(directoryForeignPath,true);
  const bool directoryForeignPreserved=directoryReplacementObserved
    &&directoryForeign.h!=INVALID_HANDLE_VALUE
    &&fixtureAbsent(directoryQuarantinePath);
  directoryForeign.closeChecked();
  fixtureWin32("RemoveDirectoryW",RemoveDirectoryW(directoryForeignPath.c_str()));
  require(directoryForeignPreserved&&fixtureAbsent(directoryForeignPath));

  stage("child_fixture_target_tree_post_effect_rollback");
  const auto treeName=fixtureName(L"TEST-tree-post-effect-");
  TestFixtureFaults::effect=FixtureEffectFault::after_target_tree_directory_effect;
  bool treePostEffectObserved=false;
  try{
    ScopedShortcutTargetTree tree(g,attributes,treeName.c_str());
    require(false);
  }catch(const TestFixtureFailure& error){
    treePostEffectObserved=error.synthetic&&!error.apiFailure
      &&error.status==ERROR_OPERATION_ABORTED
      &&!strcmp(error.api,"TEST after CreateDirectoryW");
  }
  TestFixtureFaults::reset();
  require(treePostEffectObserved&&fixtureCleanupObservation.known
    &&fixtureCleanupObservation.count==2
    &&fixtureCleanupObservation.report.complete()
    &&fixtureCleanupObservation.report.matches(0,"FixtureIdentityMismatch",
      ERROR_ACCESS_DENIED,false)
    &&(fixtureCleanupObservation.report.remainingMask&1u)
    &&!fixtureAbsent(g.root/treeName));
  clearFixtureCleanupObservation();

  stage("child_fixture_target_tree_synthetic_unwind_rollback");
  const auto treeUnwindName=fixtureName(L"TEST-tree-synthetic-unwind-");
  TestFixtureFaults::effect=FixtureEffectFault::before_target_tree_file_custody;
  bool treeUnwindObserved=false;
  try{
    ScopedShortcutTargetTree tree(g,attributes,treeUnwindName.c_str());
    require(false);
  }catch(const TestFixtureFailure& error){
    treeUnwindObserved=error.synthetic&&!error.apiFailure
      &&error.status==ERROR_NOT_ENOUGH_MEMORY
      &&!strcmp(error.api,"TEST target-tree synthetic unwind before custody");
  }
  TestFixtureFaults::reset();
  require(treeUnwindObserved&&fixtureAbsent(g.root/treeUnwindName));

  stage("child_fixture_target_tree_original_failure_preserved");
  const auto treeOriginalName=fixtureName(L"TEST-tree-original-");
  TestFixtureFaults::resetAdoptedFids();
  TestFixtureFaults::effect=FixtureEffectFault::after_target_tree_file_effect;
  TestFixtureFaults::failCleanupAll(10);
  bool treeOriginalObserved=false;
  try{
    ScopedShortcutTargetTree tree(g,attributes,treeOriginalName.c_str());
    require(false);
  }catch(const TestFixtureFailure& error){
    const auto& cleanup=fixtureCleanupObservation;
    bool classified=cleanup.known&&cleanup.count==10
      &&cleanup.report.Capacity==12&&cleanup.report.count==10
      &&cleanup.report.complete()&&!cleanup.report.overflow
      &&cleanup.report.remainingMask==0x1f&&!cleanup.statusKnown;
    for(size_t index=0;classified&&index<5;index++){
      const size_t entryIndex=4-index;
      const char* removal=entryIndex==4?"DeleteFileW":"RemoveDirectoryW";
      classified=cleanup.report.matches(index*2,"CloseHandle",ERROR_ACCESS_DENIED,false,
          entryIndex,TestFixtureFaults::adoptedFids[entryIndex],true)
        &&cleanup.report.matches(index*2+1,removal,ERROR_ACCESS_DENIED,false,
          entryIndex,TestFixtureFaults::adoptedFids[entryIndex],true);
    }
    treeOriginalObserved=TestFixtureFaults::adoptedFidMask==0x3f
      &&error.synthetic&&!error.apiFailure
      &&error.status==ERROR_OPERATION_ABORTED
      &&!strcmp(error.api,"TEST after target-tree CreateFileW")&&classified;
  }
  TestFixtureFaults::reset();
  require(treeOriginalObserved&&fixtureAbsent(g.root/treeOriginalName));
  TestFixtureFaults::resetAdoptedFids();
  clearFixtureCleanupObservation();

  stage("child_fixture_remove_failure_observed");
  const auto removeName=fixtureName(L"TEST-tree-cleanup-");
  bool removeObserved=false;
  {
    TestFixtureFaults::failCleanup(FixtureCleanupKind::remove_directory);
    ScopedShortcutTargetTree tree(g,attributes,removeName.c_str());
    try{
      tree.cleanup();
      require(false);
    }catch(const FixtureCleanupFailure& error){
      removeObserved=!error.statusKnown&&error.report.complete()
        &&!strcmp(error.api,"RemoveDirectoryW")
        &&error.status==ERROR_ACCESS_DENIED&&error.count==4
        &&error.matches(0,"RemoveDirectoryW",ERROR_ACCESS_DENIED,false)
        &&error.matches(1,"SetFileInformationByHandle",ERROR_DIR_NOT_EMPTY,true)
        &&error.matches(2,"SetFileInformationByHandle",ERROR_DIR_NOT_EMPTY,true)
        &&error.matches(3,"SetFileInformationByHandle",ERROR_DIR_NOT_EMPTY,true);
    }
  }
  TestFixtureFaults::reset();
  require(removeObserved&&fixtureAbsent(g.root/removeName));
  clearFixtureCleanupObservation();

  stage("child_fixture_max_cleanup_failure_capacity");
  const auto maxName=fixtureName(L"TEST-tree-max-failures-");
  bool maxObserved=false,maxRetryClosed=false,maxEffectsRemoved=false;
  bool maxObservationComplete=false;
  {
    TestFixtureFaults::failCleanupAll(12);
    ScopedShortcutTargetTree tree(g,attributes,maxName.c_str());
    std::array<BY_HANDLE_FILE_INFORMATION,6> identities{};
    for(size_t index=0;index<6;index++)
      require(fixtureOwnedLive(tree.custodyAt(index),tree.stateAt(index))
        &&GetFileInformationByHandle(tree.custodyAt(index),&identities[index])
        &&fixturePathIdentity(tree.storageAt(index),tree.custodyAt(index),index<4));
    try{
      tree.cleanup();
      require(false);
    }catch(const FixtureCleanupFailure& error){
      bool classified=error.statusKnown==false&&error.count==12
        &&error.report.Capacity==12&&error.report.records.size()==12
        &&error.report.complete()&&!error.report.overflow
        &&error.report.remainingMask==0x3f;
      for(size_t index=0;classified&&index<6;index++){
        const size_t entryIndex=5-index;
        const char* removal=entryIndex>=4?"DeleteFileW":"RemoveDirectoryW";
        classified=error.report.matches(index*2,"CloseHandle",ERROR_ACCESS_DENIED,false,
          entryIndex,identities[entryIndex],true)
          &&error.report.matches(index*2+1,removal,ERROR_ACCESS_DENIED,false,
          entryIndex,identities[entryIndex],true);
      }
      bool stillOwned=true;
      for(size_t index=0;stillOwned&&index<6;index++){
        BY_HANDLE_FILE_INFORMATION current{};
        stillOwned=fixtureOwnedLive(tree.custodyAt(index),tree.stateAt(index))
          &&GetFileInformationByHandle(tree.custodyAt(index),&current)
          &&current.dwVolumeSerialNumber==identities[index].dwVolumeSerialNumber
          &&current.nFileIndexHigh==identities[index].nFileIndexHigh
          &&current.nFileIndexLow==identities[index].nFileIndexLow
          &&fixturePathIdentity(tree.storageAt(index),tree.custodyAt(index),index<4);
      }
      maxObserved=classified&&stillOwned;
    }
    tree.cleanup();
    maxRetryClosed=true;
    maxEffectsRemoved=true;
    for(size_t index=0;index<6;index++){
      maxRetryClosed=maxRetryClosed&&tree.custodyAt(index)==INVALID_HANDLE_VALUE
        &&tree.stateAt(index).closed()&&tree.stateAt(index).closeStatusKnown
        &&tree.stateAt(index).closeResult==ERROR_SUCCESS;
      maxEffectsRemoved=maxEffectsRemoved&&fixtureAbsent(tree.storageAt(index));
    }
    maxObservationComplete=fixtureCleanupObservation.report.complete()
      &&!fixtureCleanupObservation.report.overflow
      &&fixtureCleanupObservation.report.count==12
      &&fixtureCleanupObservation.count==12
      &&fixtureCleanupObservation.attempts==2;
    for(size_t index=0;maxObservationComplete&&index<6;index++){
      const size_t entryIndex=5-index;
      const char* removal=entryIndex>=4?"DeleteFileW":"RemoveDirectoryW";
      maxObservationComplete=fixtureCleanupObservation.report.matches(
        index*2,"CloseHandle",ERROR_ACCESS_DENIED,false,
        entryIndex,identities[entryIndex],true)
        &&fixtureCleanupObservation.report.matches(
        index*2+1,removal,ERROR_ACCESS_DENIED,false,
        entryIndex,identities[entryIndex],true);
    }
  }
  TestFixtureFaults::reset();
  stage("child_fixture_max_cleanup_failure_records");
  require(maxObserved);
  require(maxObservationComplete);
  stage("child_fixture_max_cleanup_failure_retry_closure");
  require(maxRetryClosed);
  stage("child_fixture_max_cleanup_failure_eventual_absence");
  require(maxEffectsRemoved);
  clearFixtureCleanupObservation();

  stage("child_fixture_foreign_directory_preserved");
  const auto foreignDirectoryName=fixtureName(L"TEST-foreign-tree-");
  const auto foreignDirectory=g.root/foreignDirectoryName;
  fixtureWin32("CreateDirectoryW",CreateDirectoryW(foreignDirectory.c_str(),&attributes));
  auto foreignDirectoryHandle=open(foreignDirectory,true);
  BY_HANDLE_FILE_INFORMATION directoryBefore{};
  require(GetFileInformationByHandle(foreignDirectoryHandle.h,&directoryBefore));
  bool foreignDirectoryRefused=false;
  try{
    ScopedShortcutTargetTree tree(g,attributes,foreignDirectoryName.c_str());
    require(false);
  }catch(const TestFixtureFailure& error){
    foreignDirectoryRefused=error.apiFailure&&!strcmp(error.api,"CreateDirectoryW")
      &&(error.status==ERROR_ALREADY_EXISTS||error.status==ERROR_FILE_EXISTS);
  }
  auto directoryCandidate=open(foreignDirectory,true);
  require(foreignDirectoryRefused
    &&sameFixtureIdentity(foreignDirectoryHandle.h,directoryCandidate.h));
  directoryCandidate.closeChecked();
  foreignDirectoryHandle.closeChecked();
  fixtureWin32("RemoveDirectoryW",RemoveDirectoryW(foreignDirectory.c_str()));
  require(!fixtureCleanupObservation.known);
  std::cout<<"TEST fixture rollback contracts passed"
    <<" constructor_failure_cleanup=true post_effect=true"
    <<" synthetic_unwind=true allocator=false"
    <<" rollback_reserved_before_effect=true no_explicit_allocation_after_effect=true"
    <<" cleanup_capacity_bounded=true cleanup_aggregate_count=true"
    <<" cleanup_aggregate_overflow_observable=true cleanup_reset=true"
    <<" fid_capture_failure_preserved=true fid_capture_owned_effect_removed=true"
    <<" same_identity_api_failure_preserved=true same_identity_retry_removed=true"
    <<" identity_mismatch_unknown_fail_closed=true"
    <<" foreign_collision_preserved=true cleanup_failure_observed=true"
    <<" close_handle_failure_observed=true per_owned_handle_identity=true"
    <<" per_owned_handle_closure=true retry_effects_removed=true"
    <<" stored_fid_delete_boundary=true same_name_replacement_refused=true"
    <<" unknown_identity_fail_closed=true"
    <<" constructor_close_failure_destructor_retry=true"
    <<" tree_original_preserved=true tree_cleanup_records_bounded=true"
    <<" max_cleanup_failure_capacity=12 max_cleanup_failure_complete=true"
    <<" tree_cleanup_exact_10_records=true"
    <<" foreign_objects_deleted=false"
    <<" original_failure_preserved=true long_paths=true"
    <<" ordinary_target=true extended_storage=true exactAPI=unknown"
    <<" statusKnown=false fixture_fault_native_status_qualified=false"
    <<" cleanup_injection_synthetic=true cleanup_native_failure_qualification=false\n";
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
  require(unavailable&&GetFileAttributesW(extendedTestPath(target).c_str())==INVALID_FILE_ATTRIBUTES);
  std::cout<<"TEST synthetic shortcut target classified unavailable"
    <<" exists=false api=SHParseDisplayName typed_api_failure=true"
    <<" status_known=true result="<<DWORD(syntheticStatus)
    <<" pidl_fabricated=false exactAPI=SHParseDisplayName\n";
}
static std::vector<uint8_t> legacySetPathShortcutBytes(
 const ShortcutFixture& fixture,uint64_t end){
  shortcutBudget(end);
  Microsoft::WRL::ComPtr<IShellLinkW> link;
  shortcutHresult("CoCreateInstance(CLSID_ShellLink)",
    CoCreateInstance(CLSID_ShellLink,nullptr,CLSCTX_INPROC_SERVER,IID_PPV_ARGS(&link)));
  shortcutHresult("IShellLinkW::SetPath",link->SetPath(fixture.target.c_str()));
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
  shortcutNamespaceContract();
  installedBudget(end);
  shortcutApiFailureContract();
  installedBudget(end);
  shortcutResultClassification();
  installedBudget(end);
  shortcutOwnershipContracts();
  installedBudget(end);
  fixtureRollbackContracts(g);
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
    require(GetFileAttributesW((g.root/L"helper.substituted.exe").c_str())==INVALID_FILE_ATTRIBUTES);
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
    stage("child_shortcut_long_target_serialization");
    const auto longBytes=configuredShortcutBytes(longFixture,end);
    stage("child_shortcut_long_target_readback");
    try{shortcutReadbackBytes(longFixture,longBytes,end);}
    catch(const ShortcutTargetMismatch&){
      std::cerr<<"TEST long readback typed_target_mismatch=true\n";
      diagnoseLongShortcutTarget(longBytes,longFixture.target,end);
      throw;
    }
    installedBudget(end);
    stage("child_shortcut_long_substitution_refused");
    requireTargetSubstitutionRefused(longFixture,longSubstitute,end);
    longTargets.cleanup();
    require(GetFileAttributesW(extendedTestPath(longFixture.target).c_str())==INVALID_FILE_ATTRIBUTES
      &&GetFileAttributesW(extendedTestPath(longSubstitute.target).c_str())==INVALID_FILE_ATTRIBUTES);
  }
  // The former expired-on-entry assertion accepted any std::runtime_error and
  // could therefore turn an unrelated contract failure into a false PASS.
  // The exact post-API tests above retain deterministic deadline coverage and
  // deliberately let every unrelated failure escape.
  installedBudget(end);
  shortcutApplyContracts(g,end);
  installedBudget(end);
  require(!fixtureCleanupObservation.known);
  std::cout<<"TEST shortcut contract passed"
    <<" path=dos+exact-parsing-name+held-custody short_round_trip=true"
    <<" actual_long_round_trip=true synthetic_target_unavailable=true"
    <<" target_substitution_refused=true properties=vt_lpwstring+vt_clsid"
    <<" persistence=ipersiststream legacy_setpath_reader_compatible=true"
    <<" cleanup=co_task_mem+pidl+global_lock"
    <<" test_target_cleanup=true"
   <<" deadline_inside_serialization_after_api=true"
   <<" deadline_inside_readback_after_api=true"
   <<" applyShortcut_private_path=true"
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
      <<" failure_status_known="<<(error.statusKnown?"true":"false")
      <<" failure_api="<<error.api
      <<" failure_status="<<statusValueText(error.statusKnown,DWORD(error.status))
      <<" contract_boundary="<<error.api
      <<" contract_status_known="<<(error.statusKnown?"true":"false")
      <<" contract_status="<<statusValueText(error.statusKnown,DWORD(error.status))
      <<" api_failure="<<(error.apiFailure?"true":"false")
      <<" native_status_qualified="<<(error.statusKnown&&error.apiFailure?"true":"false")
      <<" lastTestAPI="<<lastTestAPI
      <<" lastTestResultKnown="<<(lastTestResultKnown?"true":"false")
      <<" lastTestResult="<<lastTestResult
      <<" exactAPI=unknown"
      <<" reason=shortcut_contract_violation\n";
    return 1;
  }catch(const ShortcutApiFailure& error){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known="<<(error.statusKnown?"true":"false")
      <<" failure_api="<<error.api
      <<" failure_status="<<statusValueText(error.statusKnown,DWORD(error.status))
      <<" failure_code_value="
      <<statusValueText(error.statusKnown,DWORD(error.code().value))
      <<" api_failure="<<(error.apiFailure?"true":"false")
      <<" lastTestAPI="<<lastTestAPI
      <<" lastTestResultKnown="<<(lastTestResultKnown?"true":"false")
      <<" lastTestResult="<<lastTestResult
      <<" exactAPI="<<error.api
      <<" reason=shortcut_api_failure\n";
    return 1;
  }catch(const ShortcutDeadlineExceeded& error){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known=false boundary_api="<<error.api
      <<" native_status_qualified=false"
      <<" lastTestAPI="<<lastTestAPI
      <<" lastTestResultKnown="<<(lastTestResultKnown?"true":"false")
      <<" lastTestResult="<<lastTestResult<<" exactAPI=unknown"
      <<" reason=shortcut_deadline_exceeded boundary="<<error.boundary
      <<" deadline_end="<<error.end<<" deadline_observed="<<error.observed<<"\n";
    return 1;
  }catch(const TestFixtureFailure& error){
    const bool fixtureStatusKnown=error.apiFailure&&!error.synthetic;
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known="<<(fixtureStatusKnown?"true":"false")
      <<" fixture_boundary="<<error.api
      <<" fixture_status="<<error.status
      <<" fixture_status_synthetic="<<(error.synthetic?"true":"false")
      <<" fixture_status_known="<<(fixtureStatusKnown?"true":"false")
      <<" native_status_qualified="<<(fixtureStatusKnown?"true":"false")
      <<" lastTestAPI="<<lastTestAPI
      <<" lastTestResultKnown="<<(lastTestResultKnown?"true":"false")
      <<" lastTestResult="<<lastTestResult
      <<" exactAPI="<<(fixtureStatusKnown?error.api:"unknown")
      <<" api_failure="<<(error.apiFailure?"true":"false")
      <<" reason=test_fixture_failure\n";
    return 1;
  }catch(const FixtureCleanupFailure& error){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known="<<(error.statusKnown?"true":"false")
      <<" cleanup_boundary="<<error.api
      <<" cleanup_status="<<error.status
      <<" cleanup_status_known="<<(error.statusKnown?"true":"false")
      <<" native_status_qualified="<<(error.statusKnown?"true":"false")
      <<" lastTestAPI="<<lastTestAPI
      <<" lastTestResultKnown="<<(lastTestResultKnown?"true":"false")
      <<" lastTestResult="<<lastTestResult
      <<" exactAPI="<<(error.statusKnown?error.api:"unknown")
      <<" cleanup_failure_count="<<error.count
      <<" cleanup_records_complete="<<(error.report.complete()?"true":"false")
      <<" cleanup_injection_synthetic=true"
      <<" cleanup_native_failure_qualification="
      <<(error.report.nativeStatusKnown()?"true":"false")
      <<" reason=fixture_cleanup_failure\n";
    return 1;
  }catch(const std::exception&){
    std::cerr<<"TEST registration contract failed stage="<<testStage
      <<" failure_status_known=false"
      <<" lastTestAPI="<<lastTestAPI
      <<" lastTestResultKnown="<<(lastTestResultKnown?"true":"false")
      <<" lastTestResult="<<lastTestResult<<" exactAPI=unknown"
      <<" cleanup_failure_observed="<<(fixtureCleanupObservation.known?"true":"false")
      <<" cleanup_first_api="<<(fixtureCleanupObservation.known?fixtureCleanupObservation.api:"none")
      <<" cleanup_first_result="<<(fixtureCleanupObservation.known?fixtureCleanupObservation.result:0)
      <<" cleanup_failure_count="<<fixtureCleanupObservation.count
      <<" cleanup_status_known="
      <<(fixtureCleanupObservation.known&&fixtureCleanupObservation.statusKnown?"true":"false")
      <<" cleanup_records_complete="
      <<(fixtureCleanupObservation.known
        &&fixtureCleanupObservation.report.complete()?"true":"false")
      <<" reason=contract_rejected\n";
  return 1;
  }
}
