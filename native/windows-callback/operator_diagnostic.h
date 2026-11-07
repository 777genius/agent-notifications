#pragma once
#include "wire.h"
#include <windows.h>
#include <winrt/base.h>
#include <cstdio>
#include <stdexcept>
namespace wcb {
enum class RegistryRole { Software, Classes, SharedContainer, Resolved, OwnedLeaf };
struct RegistryPhases { const char* query; const char* linkValue; const char* otherValue; };
inline RegistryPhases registryPhases(RegistryRole role)noexcept{
 switch(role){
 case RegistryRole::Software:
  return {"registry_software_query","registry_software_link_value","registry_software_other_value"};
 case RegistryRole::Classes:
  return {"registry_classes_query","registry_classes_link_value","registry_classes_other_value"};
 case RegistryRole::SharedContainer:
  return {"registry_shared_query","registry_shared_link_value","registry_shared_other_value"};
 case RegistryRole::Resolved:
  return {"registry_resolved_query","registry_resolved_link_value","registry_resolved_other_value"};
 case RegistryRole::OwnedLeaf:
  return {"registry_owned_query","registry_owned_link_value","registry_owned_other_value"};
 }
 return {"registry_link_guard","registry_link_guard","registry_link_guard"};
}
struct RegistryFailure : std::runtime_error {
 const char* phase;DWORD code;
 RegistryFailure(const char* p,DWORD value):std::runtime_error("registry operation rejected"),phase(p),code(value){}
};
struct OperatorDiagnostic {
 const char* phase="entry_apartment";
 void enter(const char* value)noexcept{phase=value;}
 void registry(const char* entered,LSTATUS actual,LSTATUS expected=ERROR_SUCCESS){
  enter(entered);
  if(actual!=expected&&actual!=ERROR_SUCCESS)throw RegistryFailure(entered,DWORD(actual));
  require(actual==expected);
 }
 // Called only from an active operator catch. No payload, disk, SDK or wait.
 void emit()const noexcept{
  const char* at=phase;const char* kind="other";DWORD code=0;
  try{throw;}
  catch(const RegistryFailure& error){at=error.phase;kind="win32";code=error.code;}
  catch(const winrt::hresult_error& error){kind="hresult";code=DWORD(error.code().value);}
  catch(...){}
  char line[256]{};
  const int count=code?_snprintf_s(line,sizeof(line),_TRUNCATE,"WCD1 %s %s %lu\n",at,kind,static_cast<unsigned long>(code)):
   _snprintf_s(line,sizeof(line),_TRUNCATE,"WCD1 %s other -\n",at);
  if(count>0&&count<static_cast<int>(sizeof(line))){DWORD written=0;
   WriteFile(GetStdHandle(STD_ERROR_HANDLE),line,DWORD(count),&written,nullptr);
  }
 }
};
inline void registryResult(OperatorDiagnostic* diagnostic,const char* phase,LSTATUS actual,LSTATUS expected=ERROR_SUCCESS){
 if(diagnostic)diagnostic->registry(phase,actual,expected);
 else require(actual==expected);
}
}
