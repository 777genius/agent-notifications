// Retained classic unpackaged callback and fixed explicit installed operator.
#define NOMINMAX
#include <windows.h>
#include <notificationactivationcallback.h>
#include <wrl.h>
#include <wtsapi32.h>
#include <winrt/Windows.Foundation.h>
#include <winrt/Windows.Foundation.Collections.h>
#include <winrt/Windows.ApplicationModel.h>
#include <winrt/Windows.Management.Deployment.h>
#include <winrt/Windows.System.h>
#include "custody.h"
#include "attempt.h"
#include "installed.h"
#include <thread>
#include <cwchar>
#include <exception>
#include <memory>
#pragma comment(lib,"Windowsapp.lib")
#pragma comment(lib,"Wtsapi32.lib")
namespace wcb {
using namespace winrt::Windows::System;
inline void desktop(){DWORD session=0;
require(ProcessIdToSessionId(GetCurrentProcessId(),&session)&&session!=0);
LPWSTR value=nullptr;
DWORD bytes=0;
require(WTSQuerySessionInformationW(WTS_CURRENT_SERVER_HANDLE,session,WTSConnectState,&value,&bytes));
bool active=bytes==sizeof(WTS_CONNECTSTATE_CLASS)&&*reinterpret_cast<WTS_CONNECTSTATE_CLASS*>(value)==WTSActive;
WTSFreeMemory(value);
require(active);
auto input=OpenInputDesktop(0,FALSE,DESKTOP_READOBJECTS);
require(input!=nullptr);
wchar_t name[128]{},current[128]{};
DWORD needed=0;
bool same=GetUserObjectInformationW(input,UOI_NAME,name,sizeof(name),&needed)&&GetUserObjectInformationW(GetThreadDesktop(GetCurrentThreadId()),UOI_NAME,current,sizeof(current),&needed)&&std::wstring(name)==L"Default"&&std::wstring(name)==current;
CloseDesktop(input);
require(same);
}
struct SDKOperations {
 Generation& generation;
 bool queryEntered=false,queryCompletionKnown=false;
 bool launchEntered=false,launchCompletionKnown=false;
 bool operationsComplete()const {
  return (!queryEntered||queryCompletionKnown)&&(!launchEntered||launchCompletionKnown);
 }
 Tick now(){return GetTickCount64();}
 Tick metadataEnd=0;
 void exact(const Snapshot& s){
  require(now()<metadataEnd);winrt::Windows::Management::Deployment::PackageManager manager;unsigned count=0;
  require(now()<metadataEnd);const auto packages=manager.FindPackagesForUser(L"",wide(s.family));
  for(auto const& package:packages){require(now()<metadataEnd&&++count<=8);const auto id=package.Id();
   require(now()<metadataEnd&&id.Name()==wide(s.name));
   require(now()<metadataEnd&&id.Publisher()==wide(s.publisher));
   require(now()<metadataEnd&&id.FamilyName()==wide(s.family));
   require(now()<metadataEnd&&id.FullName()==wide(s.full));
   require(now()<metadataEnd&&!package.IsResourcePackage());
   require(now()<metadataEnd&&id.Architecture()==winrt::Windows::System::ProcessorArchitecture::X64);
  }
  require(now()<metadataEnd&&count==1);
 }
 void intent(const Attempt& a){generation.publish(a.id,L".intent","WinAttempt1 "+a.id+" "+std::to_string(a.entry)+" "+std::to_string(a.deadline)+" "+a.reference+" "+a.snapshotDigest+"\n");
 }
 void queryIntent(const Attempt& a,Tick end){generation.publish(a.id,L".query","query "+std::to_string(end)+"\n");}
 void launchIntent(const Attempt& a,Tick end){generation.publish(a.id,L".launch","launch "+std::to_string(end)+"\n");}
 void terminal(const Attempt& a,Result r){
  // A missing/late publication never grants retry. Outcome is written once.
  auto outcome=r.outcome;if(now()>=a.deadline)outcome=Result::Unknown;
  const char* names[]={"unavailable","declined","accepted","unknown"};
  generation.publish(a.id,L".result",std::string(names[outcome])+" effect_entered="+(r.effectEntered?"1":"0")+" target_confirmed=0 full_name_atomic=0\n");
  if(now()>=a.deadline)generation.publish(a.id,L".late","unknown terminal_publication_deadline=expired\n");
 }
 template<class Operation> auto await(const Operation& op,Tick end,const std::function<void()>& unknown,bool& completionKnown){
  bool expired=false;std::exception_ptr publicationFailure;
  auto expire=[&]{if(!expired){expired=true;try{unknown();}catch(...){publicationFailure=std::current_exception();}}};
  for(;;){
   winrt::Windows::Foundation::AsyncStatus status;
   try{status=op.Status();}catch(...){
    expire();
    // Without status readback completion is unproved. Hold this operation and
    // slot until own-incarnation lease collection; never label it drained.
    for(;;)Sleep(10);
   }
   if(status==winrt::Windows::Foundation::AsyncStatus::Completed ||
      status==winrt::Windows::Foundation::AsyncStatus::Canceled ||
      status==winrt::Windows::Foundation::AsyncStatus::Error){
    // Only an actual terminal SDK status proves this operation has completed.
    completionKnown=true;break;
   }
   if(status!=winrt::Windows::Foundation::AsyncStatus::Started){
    expire();for(;;)Sleep(10); // Unknown status cannot certify completion.
   }
   if(now()>=end)expire();Sleep(10);
  }
  if(now()>=end)expire();
  winrt::check_hresult(op.ErrorCode());require(op.Status()==winrt::Windows::Foundation::AsyncStatus::Completed);
  auto result=op.GetResults();if(now()>=end)expire();
  if(publicationFailure)std::rethrow_exception(publicationFailure);
  return result;
 }
 bool query(const std::string& target,const std::string& family,Tick end,const std::function<void()>& unknown){
  const winrt::Windows::Foundation::Uri uri(wide(target));
  const winrt::hstring selectedFamily=winrt::to_hstring(family);
  require(now()<end);queryEntered=true;
  auto op=Launcher::QueryUriSupportAsync(uri,LaunchQuerySupportType::Uri,selectedFamily);
  return await(op,end,unknown,queryCompletionKnown)==LaunchQuerySupportStatus::Available;
 }
 bool launch(const std::string& target,const std::string& family,Tick end,const std::function<void()>& unknown,const std::function<void()>& entered){
  LauncherOptions options;options.TargetApplicationPackageFamilyName(wide(family));options.FallbackUri(nullptr);require(now()<end);
  const winrt::Windows::Foundation::Uri uri(wide(target));require(now()<end);launchEntered=true;entered();
  auto op=Launcher::LaunchUriAsync(uri,options);return await(op,end,unknown,launchCompletionKnown);
 }
};
 static int operatorMain(const std::wstring& mode,uint64_t end,const std::string& nonce){
  bool showEntered=false;
  std::string attempt;
  try{
   installedBudget(end);
   Generation generation;
   Handle permit;
   if(mode==L"show"||mode.rfind(L"apply-",0)==0||mode.rfind(L"restore-",0)==0){
    permit=open(generation.root/L"operator.pending",false);
    owned(permit.h,generation.sid);
    const auto fields=decode(bytes(permit.h,4096),4,4);
    require(fields[0]==nonce&&fields[1]==narrow(mode)&&fields[2]==std::to_string(end)&&fields[3]==generation.snapshotDigest);
   }
   desktop();
   SDKOperations ops{
    generation
   }
   ;
   ops.metadataEnd=end;
   ops.exact(generation.snapshot);
   installedBudget(end);
   if(mode==L"observe"){
    require(absentKey(classPath(generation))&&absentKey(appPath(generation)));
    auto p=shortcutPath(generation);
    require(GetFileAttributesW(p.c_str())==INVALID_FILE_ATTRIBUTES&&GetLastError()==ERROR_FILE_NOT_FOUND);
   }
   else if(mode==L"verify-clsid")classReadback(generation);
   else if(mode==L"verify-aumid")appReadback(generation);
   else if(mode==L"verify-shortcut")shortcutReadback(generation);
   else if(mode==L"apply-clsid")applyClass(generation,end);
   else if(mode==L"apply-aumid")applyApp(generation,end);
   else if(mode==L"apply-shortcut")applyShortcut(generation,end);
   else if(mode==L"restore-clsid")restoreClass(generation,end);
   else if(mode==L"restore-aumid")restoreApp(generation,end);
   else if(mode==L"restore-shortcut")restoreShortcut(generation,end);
   else if(mode==L"readback"||mode==L"ready"||mode==L"show"){
    registryReadback(generation);
    shortcutReadback(generation);
    installedBudget(end);
    if(mode==L"readback"){
     std::cout<<"WCB1 ready not_checked 0\n";
     return 0;
    }
    using namespace winrt::Windows::UI::Notifications;
    auto notifier=ToastNotificationManager::CreateToastNotifier(wide(generation.snapshot.aumid));
    installedBudget(end);
    std::string permission="enabled";
    try{
     const auto setting=notifier.Setting();
     installedBudget(end);
     switch(setting){
      case NotificationSetting::Enabled:break;
      case NotificationSetting::DisabledForApplication:case NotificationSetting::DisabledForUser:
      case NotificationSetting::DisabledByGroupPolicy:case NotificationSetting::DisabledByManifest:
      std::cout<<"WCB1 declined disabled 0\n";
      return 2;
      default:require(false);
     }
    }
    catch(const winrt::hresult_error& e){
     require(e.code().value==static_cast<HRESULT>(0x80070490));
     permission="permission_unknown";
    }
    installedBudget(end);
    if(mode==L"ready"){
     std::cout<<"WCB1 ready "<<permission<<" 0\n";
     return 0;
    }
    const auto fields=operatorFields();
    require(hex(fields[0],32)&&text(fields[1],4096)&&(fields[2].empty()||text(fields[2],4096))&&(fields[3]=="0"||fields[3]=="1"));
    generation.record(fields[0]);
    ops.exact(generation.snapshot);
    installedBudget(end);
    attempt=randomID();
    reserveAttempt(generation,attempt,end);
    const std::wstring launch=L"WinEnvelope1:open_thread:"+wide(fields[0]);
    const std::wstring payload=L"<toast launch=\""+launch+L"\"><visual><binding template=\"ToastGeneric\"><text>"+xml(fields[1])+L"</text><text>"+xml(fields[2])+L"</text></binding></visual><audio silent=\""+(fields[3]=="1"?L"true":L"false")+L"\"/></toast>";
    winrt::Windows::Data::Xml::Dom::XmlDocument doc;
    doc.LoadXml(payload);
    ToastNotification toast(doc);
    installedBudget(end);
    generation.publish(attempt,L".intent","WinShow1 "+attempt+" "+fields[0]+" "+generation.snapshotDigest+" "+std::to_string(end)+"\n");
    installedBudget(end);
    showEntered=true;
    notifier.Show(toast);
    installedBudget(end);
    generation.publish(attempt,L".result","show_returned=1 effect_entered=1\n");
    installedBudget(end);
    std::cout<<"WCB1 submitted "<<permission<<" 1\n";
    return 0;
   }
   else require(false);
   installedBudget(end);
   std::cout<<"WCB1 ready not_checked 0\n";
   return 0;
  }
  catch(...){
   std::cout<<(showEntered?"WCB1 unknown unknown 1\n":"WCB1 unavailable not_checked 0\n");
   return 2;
  }
 }
struct Server {
 std::shared_ptr<Generation> generation;Tick lease;Slots slots;
 Server(std::shared_ptr<Generation> value,Tick end):generation(std::move(value)),lease(end){}
};
using namespace Microsoft::WRL;
class Callback final:public RuntimeClass<RuntimeClassFlags<ClassicCom>,INotificationActivationCallback> {
 std::shared_ptr<Server> server;
public:
 explicit Callback(std::shared_ptr<Server> value):server(std::move(value)){}
 HRESULT STDMETHODCALLTYPE Activate(LPCWSTR app,LPCWSTR args,const NOTIFICATION_USER_INPUT_DATA*,ULONG count)override{
  const Tick entry=GetTickCount64();bool admitted=false;
  try {
   // Count validation as in-flight before touching generation state. Closing
   // admission rejects immediately; held COM objects retain their server safely.
   require(server->slots.admit());admitted=true;
   const Tick end=Attempt::clipped(entry,server->lease);require(app&&args&&count==0);
   require(wcsnlen_s(app,129)<=128&&wcsnlen_s(args,128)<128&&wide(server->generation->snapshot.aumid)==app);
   const std::wstring argument(args),prefix=L"WinEnvelope1:open_thread:";require(argument.rfind(prefix,0)==0);
   const auto reference=narrow(argument.substr(prefix.size()));require(hex(reference,32));
   // Two nonqueued guard/work slots; a full route rejects before admission.
   auto record=server->generation->record(reference);require(GetTickCount64()<end);desktop();require(GetTickCount64()<end);
   Attempt attempt{entry,end,randomID()};reserveAttempt(*server->generation,attempt.id,end);auto state=server;
   std::thread([state,attempt=std::move(attempt),record=std::move(record)]()mutable{
    // Keep completion knowledge outside publication/init exception scopes.
    // An entered SDK creation that throws still owns this worker and slot.
    SDKOperations ops{*state->generation};ops.metadataEnd=attempt.deadline;
    try{winrt::init_apartment(winrt::apartment_type::multi_threaded);
    run(attempt,record,state->generation->snapshot,ops);
    state->generation->publish(attempt.id,L".worker-returned",std::string("worker_returned=1 operations_completion_known=")+(ops.operationsComplete()?"1":"0")+"\n");
    if(ops.operationsComplete())
     state->generation->publish(attempt.id,L".drained","actual_operation_completion=1 collected_boot_ms="+std::to_string(GetTickCount64())+" deadline_boot_ms="+std::to_string(attempt.deadline)+"\n");
    // Read the same absolute deadline after checked close. A collector must
    // treat any late marker, missing completion or collection uncertainty as
    // unknown. Publication is not clock-atomic across failure/crash boundaries.
    if(GetTickCount64()>=attempt.deadline)
     state->generation->publish(attempt.id,L".collection-late","unknown collection_publication_deadline=expired\n");
    }catch(...){/* Durable intents remain unknown;
    never retry. */}
    if(!ops.operationsComplete()){
     // Creation threw without a returned operation/status, or collection lost
     // completion evidence. Retain this exact worker/slot through the existing
     // own-incarnation hard lease; do not admit new work or exit as drained.
     // Publication exceptions above cannot erase this completion knowledge.
     for(;;)Sleep(10);
    }
    state->slots.release();
   }).detach();return S_OK; // Admission acknowledged, not selected target success.
  }catch(...){if(admitted)server->slots.release();return E_INVALIDARG;}
 }
};
class Factory final:public RuntimeClass<RuntimeClassFlags<ClassicCom>,IClassFactory>{
 std::shared_ptr<Server> server;
public:
 explicit Factory(std::shared_ptr<Server> value):server(std::move(value)){}
 HRESULT STDMETHODCALLTYPE CreateInstance(IUnknown* outer,REFIID iid,void** out)override{if(outer)return CLASS_E_NOAGGREGATION;
 auto callback=Make<Callback>(server);if(!callback)return E_OUTOFMEMORY;return callback->QueryInterface(iid,out);
 }
 HRESULT STDMETHODCALLTYPE LockServer(BOOL)override{return S_OK;}
};
}
int WINAPI wWinMain(HINSTANCE,HINSTANCE,PWSTR command,int){
 // Only the retained, exact LocalServer32 command may compose a callback server.
 // Explicit setup operators have a separate bounded vocabulary and deadline.
 const auto lease=GetTickCount64()+65000;
 const bool callback=std::wstring(command)==L"--callback -Embedding";
 // Establish the hard own-incarnation collection lease before any guard or
 // COM publication. Blocking SDK/COM calls cannot extend it. This never
 // certifies global quiescence; unresolved durable intents remain unknown.
 std::thread([lease]{while(GetTickCount64()<lease)Sleep(10);TerminateProcess(GetCurrentProcess(),124);}).detach();
 try{
  winrt::init_apartment(winrt::apartment_type::multi_threaded);
 if(!callback){
  int count=0;
  auto args=CommandLineToArgvW(GetCommandLineW(),&count);
  wcb::require(args&&count==5&&std::wstring(args[1])==L"--operator");
  const std::wstring mode=args[2],number=args[3];
  const auto nonce=wcb::narrow(args[4]);
  wcb::require(wcb::hex(nonce,32));
  wcb::require(number.size()<=20&&!number.empty()&&number.find_first_not_of(L"0123456789")==std::wstring::npos);
  const auto end=std::stoull(number);
  LocalFree(args);
  wcb::require(end>GetTickCount64()&&end<=lease-65000+wcb::actionBudget);
  return wcb::operatorMain(mode,end,nonce);
 }
  auto generation=std::make_shared<wcb::Generation>();
  wcb::desktop();
  wcb::require(GetTickCount64()<lease-wcb::collectionReserve);
  auto server=std::make_shared<wcb::Server>(generation,lease);GUID clsid{};winrt::check_hresult(CLSIDFromString(wcb::wide(generation->snapshot.clsid).c_str(),&clsid));
  auto factory=Microsoft::WRL::Make<wcb::Factory>(server);DWORD cookie=0;
  winrt::check_hresult(CoRegisterClassObject(clsid,factory.Get(),CLSCTX_LOCAL_SERVER,REGCLS_MULTIPLEUSE|REGCLS_SUSPENDED,&cookie));
  winrt::check_hresult(CoResumeClassObjects());
  while(GetTickCount64()<lease-wcb::collectionReserve)Sleep(10);
  server->slots.close();CoRevokeClassObject(cookie);
  while(server->slots.pending()&&GetTickCount64()<lease)Sleep(10);
  if(server->slots.pending()){
   // Kill only this incarnation at its lease. Unknown retains intent/records.
   // This is bounded collection, explicitly NOT an async/global drain claim.
   TerminateProcess(GetCurrentProcess(),124);return 124;
  }
  return 0;
 }catch(...){return 2;}
}
