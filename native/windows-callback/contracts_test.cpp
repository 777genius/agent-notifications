// Inert wire + lifecycle tests. No registration, SDK target or UI effects; finite fixture/hash operations.
#define NOMINMAX
#include "wire.h"
#include "attempt.h"
#include "capacity.h"
#include <fstream>
#include <iterator>
#include <iostream>
#include <thread>
#include <condition_variable>
#include <cstdlib>
using namespace wcb;
static void check(bool ok){if(!ok)throw std::runtime_error("contract regression");}
static std::vector<uint8_t> fixture(const std::string& path){std::ifstream file(path,std::ios::binary);
check(bool(file));
return {std::istreambuf_iterator<char>(file),{}};
}
struct Operations {
 Tick clock=101;unsigned queries=0,launches=0,intents=0,terminals=0,exactReads=0;bool support=true,accept=true,expireQuery=false,expireLaunch=false,failIntent=false;
 Result terminalResult;Tick queryEnd=0,launchEnd=0;
 Tick now(){return clock;}
 void intent(const Attempt& a){check(a.reference==std::string(32,'b')&&a.snapshotDigest==std::string(64,'c'));++intents;if(failIntent)throw std::runtime_error("intent publication failed");}
 void exact(const Snapshot&){++exactReads;}
 void queryIntent(const Attempt&,Tick end){queryEnd=end;}
 void launchIntent(const Attempt&,Tick end){launchEnd=end;}
 void terminal(const Attempt&,Result r){++terminals;terminalResult=r;}
 bool query(const std::string& u,const std::string& family,Tick end,const std::function<void()>& unknown){++queries;
 check(u=="codex://threads/a%2Fb%252F%3F%23%E9%9B%AA"&&family=="OpenAI.Codex_2p2nqsd0c76g0");
 if(expireQuery){clock=end;
 unknown();
 }return support;
 }
 bool launch(const std::string& target,const std::string& family,Tick end,const std::function<void()>& unknown,const std::function<void()>& entered){check(target=="codex://threads/a%2Fb%252F%3F%23%E9%9B%AA"&&family=="OpenAI.Codex_2p2nqsd0c76g0");entered();
 ++launches;
 if(expireLaunch){clock=end;
 unknown();
 }return accept;
 }
};
struct Blocking:Operations {
 std::mutex mutex;std::condition_variable cv;bool entered=false,finish=false;std::string target;
 bool query(const std::string& value,const std::string&,Tick end,const std::function<void()>& unknown){target=value;++queries;
 clock=end;
 unknown();
 std::unique_lock lock(mutex);
 entered=true;
 cv.notify_all();
 cv.wait(lock,[&]{return finish;});
 return true;
 }
 void wait(){std::unique_lock lock(mutex);cv.wait(lock,[&]{return entered;});}
 void drain(){std::lock_guard lock(mutex);finish=true;cv.notify_all();}
};
int main(int argc,char** argv){try{
 check(argc==2);
 const std::string base=argv[1];
 auto rb=fixture(base+"/record.wne"),sb=fixture(base+"/snapshot.wne");
 auto record=Record::read(rb);
 auto snapshot=Snapshot::read(sb);
 // Independent bytes detect URI corruption and reader incompatibility in both codecs.
 check(record.thread=="a/b%2F?#雪"&&record.reference==std::string(32,'b'));
 check(encode(2,record.fields())==rb&&encode(1,snapshot.fields())==sb);
 check(uri(record.thread)=="codex://threads/a%2Fb%252F%3F%23%E9%9B%AA");
 record.bind(snapshot,std::string(64,'c'));
 for(unsigned mutation=0;mutation<5;mutation++){auto bad=rb;
 if(mutation==0)bad[8]=2;
 if(mutation==1)bad[10]=1;
 if(mutation==2)bad[11]=1;
 if(mutation==3)bad.push_back(0);
 if(mutation==4)bad.pop_back();
 bool rejected=false;
 try{Record::read(bad);
 }catch(...){rejected=true;
 }check(rejected);
 }
 for(const std::string bad:{std::string("\xc0\xaf"),std::string("\xed\xa0\x80"),std::string("\xf4\x90\x80\x80"),std::string("\0",1),std::string("\xc2\x80")})check(!text(bad,4096));
 for(const std::string id:{std::string("."),std::string("..")}) {
  bool uriRejected=false,recordRejected=false;try{uri(id);}catch(...){uriRejected=true;}
  auto bad=record;bad.thread=id;try{bad.validate();}catch(...){recordRejected=true;}
  check(uriRejected&&recordRejected);
 }
 check(uri("a.b")=="codex://threads/a.b"&&uri("../x")=="codex://threads/..%2Fx");
 auto foreign=record;
 foreign.generation=std::string(32,'b');
 bool rejected=false;
 try{foreign.bind(snapshot,std::string(64,'c'));
 }catch(...){rejected=true;
 }check(rejected);
 // Deadline is immutable and fresh for each activation of the same record.
 check(Attempt::clipped(100,65000)==30100&&Attempt::clipped(200,65000)==30200&&Attempt::clipped(59000,65000)==60000);
 for(unsigned scenario=0;scenario<5;scenario++){
  Attempt attempt{100,30100,"fresh"};
  Operations ops;
  ops.support=scenario!=1;
  ops.accept=scenario!=2;
  ops.expireQuery=scenario==3;
  ops.expireLaunch=scenario==4;
  auto result=run(attempt,record,snapshot,ops);check(ops.intents==1&&ops.queries==1&&ops.terminals==1&&ops.launches<=1&&attempt.deadline==30100);
  if(scenario==0)check(result.outcome==Result::Accepted&&result.effectEntered&&ops.exactReads==2&&ops.queryEnd==10101&&ops.launchEnd==15101);
  if(scenario==1)check(result.outcome==Result::Declined&&!result.effectEntered&&ops.launches==0);
  if(scenario==2)check(result.outcome==Result::Declined&&result.effectEntered);
  if(scenario==3)check(result.outcome==Result::Unknown&&!result.effectEntered&&ops.launches==0);
  if(scenario==4)check(result.outcome==Result::Unknown&&result.effectEntered&&ops.launches==1);
 }
 // Breakage: a failed durable intent admits SDK queries or launches. This
 // exercises the existing effect-admission seam, not a test-only Win32 hook.
 {
  Attempt attempt{100,30100,"failed-intent"};Operations ops;ops.failIntent=true;
  const auto result=run(attempt,record,snapshot,ops);
  check(result.outcome==Result::Unavailable&&!result.effectEntered&&ops.intents==1&&ops.queries==0&&ops.launches==0);
 }
 // Expiration is not cancellation/quiescence. Both independent slots remain
 // held through reverse completion of actual drain; no third queued target.
 Slots slots;check(slots.admit()&&slots.admit()&&!slots.admit());Blocking first,second;Attempt a{100,30100,"first"},b{100,30100,"second"};
 std::thread one([&]{run(a,record,snapshot,first);slots.release();});auto secondRecord=record;secondRecord.thread="second/target";
 std::thread two([&]{run(b,secondRecord,snapshot,second);slots.release();});
 first.wait();
 second.wait();
 check(first.target=="codex://threads/a%2Fb%252F%3F%23%E9%9B%AA"&&second.target=="codex://threads/second%2Ftarget");
 check(slots.pending()==2&&first.terminals==1&&second.terminals==1&&first.launches==0&&second.launches==0);
 slots.close();
 check(!slots.admit());
 second.drain();
 two.join();
 check(slots.pending()==1);
 first.drain();
 one.join();
 check(slots.pending()==0);
 // Breakage: concurrent producer/callback charge limits disagree, or a valid
 // independent overflow ledger is accepted. These fixtures were authored from
 // the numeric wire layout independently of both production codecs.
 const auto quota=CapacityState::read(fixture(std::string(argv[1])+"/capacity.wcap"));
 check(quota.records==1023&&quota.attempts==2047&&quota.attemptBytes==67076096);
 const auto full=CapacityState::read(quota.nextAttempt());check(full.attempts==2048&&full.attemptBytes==67108864);
 bool refused=false;try{full.nextAttempt();}catch(...){refused=true;}check(refused);
 refused=false;try{CapacityState::read(fixture(std::string(argv[1])+"/capacity-overflow.wcap"));}catch(...){refused=true;}check(refused);
 std::cout<<"inert WinEnvelope1 and callback lifecycle contracts passed\n";return 0;
 }catch(const std::exception& e){std::cerr<<e.what()<<'\n';return 1;}}
