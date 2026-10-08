#pragma once
#include "custody.h"
namespace wcb {
  inline uint64_t little(const std::vector<uint8_t>& b,size_t at){
    uint64_t n=0;
    for(unsigned j=0;j<8;j++)n|=uint64_t(b[at+j])<<(j*8);
    return n;
  }
  // WCAP0001 is a fixed charged ledger, shared with the Go producer. No absent
  // file or failed API observation reduces a charged reservation.
  struct CapacityState {
    uint64_t records,recordBytes,attempts,attemptBytes;
    static CapacityState read(const std::vector<uint8_t>& b){
      require(b.size()==72&&std::string(b.begin(),b.begin()+8)=="WCAP0001");
      auto digest=sha(std::vector<uint8_t>(b.begin(),b.begin()+40));
      const char* h="0123456789abcdef";
      std::string actual;
      for(size_t i=40;i<72;i++){
        actual+=h[b[i]>>4];
        actual+=h[b[i]&15];
      }
      require(actual==digest);
      CapacityState c{
        little(b,8),little(b,16),little(b,24),little(b,32)
      };
      require(c.records<=1024&&c.attempts<=2048&&c.recordBytes==c.records*65536&&c.attemptBytes==c.attempts*32768&&c.recordBytes<=67108864&&c.attemptBytes<=67108864);
      return c;
    }
    std::vector<uint8_t> nextAttempt()const{
      require(attempts<2048&&attemptBytes<=67108864-32768);
      std::vector<uint8_t> b(40);
      const char* magic="WCAP0001";
      std::copy(magic,magic+8,b.begin());
      const uint64_t values[]={
        records,recordBytes,attempts+1,attemptBytes+32768
      };
      for(unsigned i=0;i<4;i++)for(unsigned j=0;j<8;j++)b[8+i*8+j]=uint8_t(values[i]>>(8*j));
      const auto digest=sha(b);
      for(unsigned i=0;i<32;i++)b.push_back(uint8_t(std::stoi(digest.substr(i*2,2),nullptr,16)));
      return b;
    }
  };
  inline void exclusiveFile(const fs::path& p,const std::string& sid,const std::vector<uint8_t>& b){
    PSECURITY_DESCRIPTOR sd=nullptr;
    auto sddl=L"O:"+wide(sid)+L"D:P(A;;FA;;;"+wide(sid)+L")(A;;FA;;;SY)";
    require(ConvertStringSecurityDescriptorToSecurityDescriptorW(sddl.c_str(),SDDL_REVISION_1,&sd,nullptr));
    SECURITY_ATTRIBUTES sa{
      sizeof(sa),sd,FALSE
    };
    Handle file(CreateFileW(p.c_str(),GENERIC_WRITE|READ_CONTROL,0,&sa,CREATE_NEW,FILE_FLAG_WRITE_THROUGH|FILE_FLAG_OPEN_REPARSE_POINT,nullptr));
    LocalFree(sd);
    require(file.h!=INVALID_HANDLE_VALUE);
    owned(file.h,sid);
    DWORD n=0;
    require(WriteFile(file.h,b.data(),DWORD(b.size()),&n,nullptr)&&n==b.size()&&FlushFileBuffers(file.h));
    file.closeChecked();
  }
  inline void reserveAttempt(Generation& g,const std::string& id,uint64_t end){
    Handle lock;
    for(;;){
      require(GetTickCount64()<end);
      lock=Handle(CreateFileW((g.root/L"capacity.lock").c_str(),GENERIC_READ|READ_CONTROL,0,nullptr,OPEN_EXISTING,FILE_FLAG_OPEN_REPARSE_POINT,nullptr));
      if(lock.h!=INVALID_HANDLE_VALUE)break;
      require(GetLastError()==ERROR_SHARING_VIOLATION);
      Sleep(1);
    }
    BY_HANDLE_FILE_INFORMATION info{
    };
    require(GetFileInformationByHandle(lock.h,&info)&&!(info.dwFileAttributes&(FILE_ATTRIBUTE_DIRECTORY|FILE_ATTRIBUTE_REPARSE_POINT))&&info.nNumberOfLinks==1&&physical(lock.h)==(g.root/L"capacity.lock").wstring());
    owned(lock.h,g.sid);
    const auto pending=g.root/L"capacity.pending";
    require(GetFileAttributesW(pending.c_str())==INVALID_FILE_ATTRIBUTES&&GetLastError()==ERROR_FILE_NOT_FOUND);
    auto state=open(g.root/L"capacity.state",false);
    owned(state.h,g.sid);
    auto c=CapacityState::read(bytes(state.h,128));
    state.closeChecked();
    auto b=c.nextAttempt();
    require(GetTickCount64()<end);
    exclusiveFile(pending,g.sid,std::vector<uint8_t>(id.begin(),id.end()));
    require(GetTickCount64()<end);
    exclusiveFile(g.root/L"capacity.next",g.sid,b);
    require(GetTickCount64()<end);
    require(MoveFileExW((g.root/L"capacity.next").c_str(),(g.root/L"capacity.state").c_str(),MOVEFILE_REPLACE_EXISTING|MOVEFILE_WRITE_THROUGH));
    require(GetTickCount64()<end);
    require(DeleteFileW(pending.c_str()));
    require(GetTickCount64()<end);
  }
}
