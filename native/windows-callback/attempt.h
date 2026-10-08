#pragma once
#include "wire.h"
#include <atomic>
#include <functional>
#include <mutex>
namespace wcb {
using Tick=uint64_t;
constexpr Tick actionBudget=30000,collectionReserve=5000;
struct Result { enum Outcome{Unavailable,Declined,Accepted,Unknown} outcome=Unavailable;bool effectEntered=false; };
struct Attempt {
 Tick entry,deadline;std::string id;bool queryEntered=false,effectEntered=false,published=false;
 std::string reference,snapshotDigest;
 static Tick clipped(Tick entry,Tick lease){require(lease>collectionReserve&&entry<lease-collectionReserve);
 return std::min(entry+actionBudget,lease-collectionReserve);
 }
 void budget(Tick now)const{require(now<deadline);}
};
// Active counts remain held during real asynchronous drain. Closing admission
// cannot decrement them or manufacture quiescence. There is no waiting queue.
class Slots {
 std::mutex lock;unsigned active=0;bool closed=false;
public:
 bool admit(){std::lock_guard guard(lock);if(closed||active==2)return false;++active;return true;}
 void release(){std::lock_guard guard(lock);require(active!=0);--active;}
 void close(){std::lock_guard guard(lock);closed=true;}
 unsigned pending(){std::lock_guard guard(lock);return active;}
};
// Operations expose consumer vocabulary. Only the Windows implementation knows
// WinRT types. query/launch must retain and drain their actual API operation;
// on expiration they publish unknown before drain and never replay effects.
template<class Operations> Result run(Attempt& a,const Record& record,const Snapshot& snapshot,Operations& ops) {
 Result result;
 auto terminal=[&](Result::Outcome outcome){result={outcome,a.effectEntered};if(!a.published){ops.terminal(a,result);a.published=true;}};
 auto unknown=[&]{terminal(Result::Unknown);};
 try {
  a.reference=record.reference;a.snapshotDigest=record.digest;
  a.budget(ops.now());ops.intent(a);a.budget(ops.now());ops.exact(snapshot);a.budget(ops.now());const auto target=uri(record.thread);
  const Tick queryDeadline=std::min(a.deadline,ops.now()+10000);
  ops.queryIntent(a,queryDeadline);
  require(ops.now()<queryDeadline);
  a.queryEntered=true;
  const auto support=ops.query(target,snapshot.family,queryDeadline,unknown);
  if(a.published)return result;
  require(ops.now()<queryDeadline);a.budget(ops.now());
  if(!support){terminal(Result::Declined);return result;}
  ops.exact(snapshot);a.budget(ops.now()); // FullName re-read, still not atomic.
  const Tick launchDeadline=std::min(a.deadline,ops.now()+15000);ops.launchIntent(a,launchDeadline);require(ops.now()<launchDeadline);
  const auto entered=[&]{a.effectEntered=true;};
  const auto accepted=ops.launch(target,snapshot.family,launchDeadline,unknown,entered);
  if(a.published)return result;
  require(ops.now()<launchDeadline);a.budget(ops.now());terminal(accepted?Result::Accepted:Result::Declined);
 }catch(...){terminal(a.queryEntered||a.effectEntered?Result::Unknown:Result::Unavailable);}
 return result;
}
}
