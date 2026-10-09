#pragma once
#include <algorithm>
#include <array>
#include <cstdint>
#include <stdexcept>
#include <string>
#include <vector>
namespace wcb {
constexpr size_t maxEnvelope = 65536;
inline void require(bool ok) { if (!ok) throw std::runtime_error("callback contract rejected"); }
inline bool hex(const std::string& s, size_t n) {
 if (s.size()!=n) return false;
 return std::all_of(s.begin(),s.end(),[](char c){return (c>='0'&&c<='9')||(c>='a'&&c<='f');});
}
// Validate scalars independently of WinRT conversion (which may replace bad UTF-8).
inline bool text(const std::string& s,size_t maximum) {
 if (s.empty() || s.size()>maximum) return false;
 for (size_t i=0;i<s.size();) {
  const auto first=static_cast<unsigned char>(s[i++]); uint32_t cp=first; unsigned count=0;
  if (first>=0xc2 && first<=0xdf) {cp=first&31;count=1;}
  else if(first>=0xe0 && first<=0xef){cp=first&15;count=2;}
  else if(first>=0xf0 && first<=0xf4){cp=first&7;count=3;}
  else if(first>=0x80) return false;
  for(unsigned j=0;j<count;j++){if(i==s.size())return false;
  auto c=static_cast<unsigned char>(s[i++]);
  if((c&0xc0)!=0x80)return false;
  cp=(cp<<6)|(c&63);
  }
  if((count==1&&cp<0x80)||(count==2&&cp<0x800)||(count==3&&cp<0x10000)||cp>0x10ffff||(cp>=0xd800&&cp<=0xdfff)||cp<32||(cp>=127&&cp<=159))return false;
 }
 return true;
}
inline std::vector<std::string> decode(const std::vector<uint8_t>& b,uint8_t kind,size_t count) {
 require(b.size()>=12&&b.size()<=maxEnvelope&&std::string(b.begin(),b.begin()+8)=="WNCB0001"&&b[8]==1&&b[9]==0&&b[10]==kind&&b[11]==0);
 size_t at=12;std::vector<std::string> fields;
 for(size_t i=0;i<count;i++){require(at+4<=b.size());
 uint32_t n=0;
 for(unsigned j=0;j<4;j++)n|=uint32_t(b[at++])<<(8*j);
 require(n<=b.size()-at);
 fields.emplace_back(b.begin()+at,b.begin()+at+n);
 at+=n;
 }
 require(at==b.size());return fields;
}
inline std::vector<uint8_t> encode(uint8_t kind,const std::vector<std::string>& fields) {
 std::vector<uint8_t> b={'W','N','C','B','0','0','0','1',1,0,kind,0};
 for(const auto& f:fields){auto n=uint32_t(f.size());for(unsigned j=0;j<4;j++)b.push_back(uint8_t(n>>(8*j)));b.insert(b.end(),f.begin(),f.end());}
 require(b.size()<=maxEnvelope);return b;
}
inline bool rootGrammar(const std::string& root) {
 if(!text(root,32768)||root.rfind("\\\\?\\",0)!=0||root.find('/')!=std::string::npos||root.back()=='\\')return false;
 // Retained generations are local drive paths; UNC/device/relative roots reject.
 if(root.size()<7||!((root[4]>='A'&&root[4]<='Z')||(root[4]>='a'&&root[4]<='z'))||root[5]!=':'||root[6]!='\\')return false;
 size_t at=7;
 while(at<root.size()){auto end=root.find('\\',at);
 auto part=root.substr(at,end-at);
 if(part.empty()||part=="."||part==".."||part.back()=='.'||part.back()==' '||part.find_first_of(":*?\"<>|")!=std::string::npos)return false;
 if(end==std::string::npos)break;
 at=end+1;
 }return true;
}
struct Snapshot {
 std::string generation,root,sid,digest,aumid,clsid,name,publisher,family,full;
 std::vector<std::string> fields() const{return {generation,root,sid,digest,aumid,clsid,name,publisher,family,full};}
 void validate()const {
  require(hex(generation,32)&&rootGrammar(root)&&text(sid,256)&&sid.rfind("S-1-",0)==0&&hex(digest,64)&&text(aumid,128));
  require(clsid.size()==38&&clsid.front()=='{'&&clsid.back()=='}');
  for(size_t i=1;i<37;i++)require(i==9||i==14||i==19||i==24?clsid[i]=='-':hex(clsid.substr(i,1),1));
  require(name=="OpenAI.Codex"&&publisher=="CN=50BDFD77-8903-4850-9FFE-6E8522F64D5B"&&family=="OpenAI.Codex_2p2nqsd0c76g0"&&text(full,256)&&full.rfind(name+"_",0)==0&&full.size()>std::string("__2p2nqsd0c76g0").size()&&full.ends_with("__2p2nqsd0c76g0"));
 }
 static Snapshot read(const std::vector<uint8_t>& b){auto f=decode(b,1,10);
 Snapshot s{f[0],f[1],f[2],f[3],f[4],f[5],f[6],f[7],f[8],f[9]};
 s.validate();
 return s;
 }
};
struct Record {
 std::string generation,reference,provider,thread,digest;
 void validate()const{require(hex(generation,32)&&hex(reference,32)&&provider=="codex"&&text(thread,4096)&&thread!="."&&thread!=".."&&hex(digest,64));}
 void bind(const Snapshot& s,const std::string& sha)const{validate();s.validate();require(generation==s.generation&&digest==sha);}
 static Record read(const std::vector<uint8_t>& b){auto f=decode(b,2,5);Record r{f[0],f[1],f[2],f[3],f[4]};r.validate();return r;}
 std::vector<std::string> fields()const{return {generation,reference,provider,thread,digest};}
};
inline std::string uri(const std::string& id) {
 require(text(id,4096)&&id!="."&&id!="..");const char* h="0123456789ABCDEF";std::string out="codex://threads/";
 for(unsigned char c:id){if((c>='a'&&c<='z')||(c>='A'&&c<='Z')||(c>='0'&&c<='9')||c=='-'||c=='.'||c=='_'||c=='~')out+=char(c);
 else{out+='%';
 out+=h[c>>4];
 out+=h[c&15];
 }}return out;
}
}
