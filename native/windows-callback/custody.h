#pragma once
#include "wire.h"
#include <windows.h>
#include <aclapi.h>
#include <sddl.h>
#include <bcrypt.h>
#include <filesystem>
#include <winrt/base.h>
#pragma comment(lib,"Advapi32.lib")
#pragma comment(lib,"Bcrypt.lib")
namespace wcb {
namespace fs=std::filesystem;
struct Handle {
 HANDLE h=INVALID_HANDLE_VALUE;
 explicit Handle(HANDLE value=INVALID_HANDLE_VALUE):h(value){}
 Handle(const Handle&)=delete;Handle& operator=(const Handle&)=delete;
 Handle(Handle&& other)noexcept:h(other.h){other.h=INVALID_HANDLE_VALUE;}
 Handle& operator=(Handle&& other)noexcept{if(h!=INVALID_HANDLE_VALUE)CloseHandle(h);h=other.h;other.h=INVALID_HANDLE_VALUE;return *this;}
 // Destructor cleanup is not a durability acknowledgement. Publication must
 // explicitly close and check before returning to an effect admission boundary.
 void closeChecked(){
  require(h!=INVALID_HANDLE_VALUE);
  const HANDLE closing=h;h=INVALID_HANDLE_VALUE;
  require(CloseHandle(closing)!=FALSE);
 }
 ~Handle(){if(h!=INVALID_HANDLE_VALUE)CloseHandle(h);}
};
inline std::wstring wide(const std::string& s){return std::wstring(winrt::to_hstring(s));}
inline std::string narrow(const std::wstring& s){return winrt::to_string(s);}
inline std::wstring physical(HANDLE h) {
 std::wstring out(32768,L'\0');
 DWORD n=GetFinalPathNameByHandleW(h,out.data(),DWORD(out.size()),FILE_NAME_NORMALIZED|VOLUME_NAME_DOS);
 require(n&&n<out.size());
 out.resize(n);
 return out;
}
inline std::string ownerSID(){Handle token;
require(OpenProcessToken(GetCurrentProcess(),TOKEN_QUERY,&token.h)!=FALSE);
DWORD n=0;
GetTokenInformation(token.h,TokenUser,nullptr,0,&n);
require(n&&n<65536);
std::vector<uint8_t> data(n);
require(GetTokenInformation(token.h,TokenUser,data.data(),n,&n)!=FALSE);
LPWSTR sid=nullptr;
require(ConvertSidToStringSidW(reinterpret_cast<TOKEN_USER*>(data.data())->User.Sid,&sid)!=FALSE);
std::string out=narrow(sid);
LocalFree(sid);
return out;
}
inline void owned(HANDLE h,const std::string& sid) {
 PSID owner=nullptr;PACL dacl=nullptr;PSECURITY_DESCRIPTOR sd=nullptr;
 require(GetSecurityInfo(h,SE_FILE_OBJECT,OWNER_SECURITY_INFORMATION|DACL_SECURITY_INFORMATION,&owner,nullptr,&dacl,nullptr,&sd)==ERROR_SUCCESS);
 struct Free{PSECURITY_DESCRIPTOR p;~Free(){LocalFree(p);}} release{sd};
 LPWSTR actual=nullptr;
 require(owner&&ConvertSidToStringSidW(owner,&actual));
 std::string got=narrow(actual);
 LocalFree(actual);
 require(got==sid&&dacl);
 SECURITY_DESCRIPTOR_CONTROL control{};DWORD revision=0;require(GetSecurityDescriptorControl(sd,&control,&revision)&& (control&SE_DACL_PROTECTED));
 require(dacl->AceCount==2);bool own=false,system=false;
 for(DWORD i=0;i<dacl->AceCount;i++){void* raw=nullptr;
 require(GetAce(dacl,i,&raw));
 auto a=static_cast<ACCESS_ALLOWED_ACE*>(raw);
 require(a->Header.AceType==ACCESS_ALLOWED_ACE_TYPE&&!(a->Header.AceFlags&INHERITED_ACE)&&(a->Mask&FILE_ALL_ACCESS)==FILE_ALL_ACCESS);
 LPWSTR name=nullptr;
 require(ConvertSidToStringSidW(&a->SidStart,&name));
 auto value=narrow(name);
 LocalFree(name);
 if(value==sid&&!own)own=true;
 else if(value=="S-1-5-18"&&!system)system=true;
 else require(false);
 }
 require(own&&system);
}
inline Handle open(const fs::path& p,bool directory) {
 Handle f(CreateFileW(p.c_str(),GENERIC_READ|READ_CONTROL,FILE_SHARE_READ|(directory?FILE_SHARE_WRITE:0),nullptr,OPEN_EXISTING,FILE_FLAG_OPEN_REPARSE_POINT|(directory?FILE_FLAG_BACKUP_SEMANTICS:0),nullptr));
 require(f.h!=INVALID_HANDLE_VALUE);
 BY_HANDLE_FILE_INFORMATION info{};
 require(GetFileInformationByHandle(f.h,&info)&&!(info.dwFileAttributes&FILE_ATTRIBUTE_REPARSE_POINT)&& bool(info.dwFileAttributes&FILE_ATTRIBUTE_DIRECTORY)==directory&&(directory||info.nNumberOfLinks==1));
 return f;
}
inline std::vector<uint8_t> bytes(HANDLE h,size_t limit=maxEnvelope){LARGE_INTEGER size{};
require(GetFileSizeEx(h,&size)&&size.QuadPart>0&&uint64_t(size.QuadPart)<=limit);
std::vector<uint8_t>b(size_t(size.QuadPart));
DWORD n=0;
require(ReadFile(h,b.data(),DWORD(b.size()),&n,nullptr)&&n==b.size());
return b;
}
inline std::string sha(const std::vector<uint8_t>& data){BCRYPT_ALG_HANDLE algorithm=nullptr;
require(BCryptOpenAlgorithmProvider(&algorithm,BCRYPT_SHA256_ALGORITHM,nullptr,0)==0);
std::array<uint8_t,32> digest{};
auto status=BCryptHash(algorithm,nullptr,0,const_cast<PUCHAR>(data.data()),ULONG(data.size()),digest.data(),ULONG(digest.size()));
BCryptCloseAlgorithmProvider(algorithm,0);
require(status==0);
const char* hex="0123456789abcdef";
std::string out;
for(auto b:digest){out+=hex[b>>4];
out+=hex[b&15];
}return out;
}
inline std::string randomID(){std::array<uint8_t,16>b{};
require(BCryptGenRandom(nullptr,b.data(),ULONG(b.size()),BCRYPT_USE_SYSTEM_PREFERRED_RNG)==0);
const char* hex="0123456789abcdef";
std::string out;
for(auto c:b){out+=hex[c>>4];
out+=hex[c&15];
}return out;
}
struct Generation {
 fs::path root;std::string sid,snapshotDigest;Snapshot snapshot;
 std::vector<Handle> guards;Handle snapshotFile,helperFile,recordDirectory,attemptDirectory;
 Generation():sid(ownerSID()) {
  std::wstring module(32768,L'\0');
  DWORD n=GetModuleFileNameW(nullptr,module.data(),DWORD(module.size()));
  require(n&&n<module.size());
  module.resize(n);
  helperFile=open(module,false);
  root=fs::path(physical(helperFile.h)).parent_path();
  require(rootGrammar(narrow(root.wstring()))&&fs::path(module).filename()==L"helper.exe");
  // Hold each ancestor against replacement; reject junctions before trusting root.
  fs::path path=root.root_path();
  guards.push_back(open(path,true));
  for(const auto& part:root.relative_path()){path/=part;
  guards.push_back(open(path,true));
  }
  require(physical(guards.back().h)==root.wstring());owned(guards.back().h,sid);owned(helperFile.h,sid);
  snapshotFile=open(root/L"generation.wne",false);
  owned(snapshotFile.h,sid);
  require(physical(snapshotFile.h)==(root/L"generation.wne").wstring());
  auto data=bytes(snapshotFile.h);
  snapshotDigest=sha(data);
  snapshot=Snapshot::read(data);
  require(snapshot.root==narrow(root.wstring())&&snapshot.sid==sid&&physical(helperFile.h)==(root/L"helper.exe").wstring());
  require(sha(bytes(helperFile.h,16*1024*1024))==snapshot.digest);
  recordDirectory=open(root/L"records",true);attemptDirectory=open(root/L"attempts",true);owned(recordDirectory.h,sid);owned(attemptDirectory.h,sid);
 }
 Record record(const std::string& reference){require(hex(reference,32));
 auto file=open(root/L"records"/(wide(reference)+L".wne"),false);
 owned(file.h,sid);
 require(physical(file.h)==(root/L"records"/(wide(reference)+L".wne")).wstring());
 auto r=Record::read(bytes(file.h));
 r.bind(snapshot,snapshotDigest);
 require(r.reference==reference);
 return r;
 }
 void publish(const std::string& attempt,const wchar_t* suffix,const std::string& value) {
  require(hex(attempt,32)&&value.size()<4096);
  PSECURITY_DESCRIPTOR sd=nullptr;
  auto descriptor=L"O:"+wide(sid)+L"D:P(A;;FA;;;"+wide(sid)+L")(A;;FA;;;SY)";
  require(ConvertStringSecurityDescriptorToSecurityDescriptorW(descriptor.c_str(),SDDL_REVISION_1,&sd,nullptr));
  SECURITY_ATTRIBUTES attrs{sizeof(attrs),sd,FALSE};
  Handle file(CreateFileW((root/L"attempts"/(wide(attempt)+suffix)).c_str(),GENERIC_WRITE|READ_CONTROL,0,&attrs,CREATE_NEW,FILE_FLAG_WRITE_THROUGH|FILE_FLAG_OPEN_REPARSE_POINT,nullptr));
  LocalFree(sd);
  require(file.h!=INVALID_HANDLE_VALUE);
  owned(file.h,sid);
  DWORD n=0;
  require(WriteFile(file.h,value.data(),DWORD(value.size()),&n,nullptr)&&n==value.size()&&FlushFileBuffers(file.h));
  file.closeChecked();
 }
};
}
