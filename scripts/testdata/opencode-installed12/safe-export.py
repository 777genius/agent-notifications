"""TEST closed scalar/digest projection, never raw histories/config/claims/logs."""
import argparse,hashlib,json,pathlib,re,stat
P=pathlib.Path
HEX_FIELDS={'manifestSHA256','gateSHA256','SDKArchiveSHA256','embeddedSHA256','claimSHA256','claimCorrelation','nativeEntryWitnessSHA256','privateNativeHistorySHA256','acceptedPrimitiveReceiptsSHA256','hostPrivateLogSHA256'}
BOOL_FIELDS={'FULLQualified','GUIQualified','sourceEpochQualified','platformLifetimeQualified','privateANFrameIntercepted','internalANProfileCallIntercepted','nativeCompletionObserved','nativeEntryNaturalExit','ownedLeaderWaitObserved','ownedOutputEOF','POSIXOwnedGroupAbsent','ConPTYClosedAfterZeroClients','hostForcedCleanupUsed','cleanupUncertainty','fixtureCleanupUncertainty','sealedInputsUnchanged'}
COUNT_FIELDS={'plannedProviderTransactions','actualProviderHTTPRequestAttempts','actualProviderTransactions','actualIndependentWebhookSubmissions','actualToolCalls','actualDesktopSubmissions','actualWebhookSubmissions','actualClosedEventChildren','policyGeneration','originalNativeCompletedTime','hostPrivateLogBytes'}
def need(v,c):
 if not v:raise ValueError(c)
def main(a):
 need(not any(p.is_symlink() for p in (a.cases,*a.cases.parents)),'private_case_root')
 roots=list(a.cases.glob('TEST-installed-*'));need(len(roots)<=1,'one_planned_case_no_retry')
 result={'schema':1,'purpose':'TEST installed12 bounded SAFE result','status':'unqualified_before_result','cell':{'os':a.os,'arch':a.arch,'version':a.version,'entry':a.entry},'qualificationGranted':False}
 if roots and (roots[0]/'result.json').exists():
  p=roots[0]/'result.json';need(not any(q.is_symlink() for q in (p,*p.parents)) and stat.S_ISREG(p.lstat().st_mode) and 0<p.stat().st_size<=1048576,'bounded_regular_result')
  raw=p.read_bytes();d=json.loads(raw);need(d['cell']==result['cell'] and d['status'] in ('unqualified','bounded_installed_local_completion_observed'),'actual_closed_cell')
  result.update(status=d['status'],resultSHA256=hashlib.sha256(raw).hexdigest())
  for k in HEX_FIELDS:
   if k in d:need(re.fullmatch('[0-9a-f]{64}',str(d[k])),'safe_digest');result[k]=d[k]
  for k in BOOL_FIELDS:
   if k in d:need(type(d[k]) is bool,'safe_boolean');result[k]=d[k]
  for k in COUNT_FIELDS:
   if k in d:need(type(d[k]) is int and 0<=d[k]<=2**63-1,'safe_integer');result[k]=d[k]
  need(re.fullmatch('[0-9a-f]{40}',d['candidateCommit']),'safe_final_head');result['candidateCommit']=d['candidateCommit']
  need(re.fullmatch('[0-9a-f]{40}',d['sourceTree']),'safe_source_tree');result['sourceTree']=d['sourceTree']
  for k,values in {'timePolicy':{'unverified_original_date','accepted_exact_cell_policy'},'desktopEvidence':{'independent_private_dbus','closed_backend_submitted_receipt'},'nativeEntryTransport':{'run','tui'},'ConPTYOutputEOFReason':{'broken_pipe_109','read_zero'}}.items():
   if k in d:need(d[k] in values,'safe_enum');result[k]=d[k]
  if 'firstFailedStage' in d:need(d['firstFailedStage'] in {'preflight','actual_install','direct_stock_entry','settled_natural_oracle','independent_callback_readiness','native_TUI_input_readiness','one_installed_completion_settlement','native_entry_natural_finish'},'safe_failure_stage');result['firstFailedStage']=d['firstFailedStage']
 need(not a.output.exists() and not any(p.is_symlink() for p in (a.output,*a.output.parents)),'fresh_SAFE_export')
 a.output.parent.mkdir(parents=True,exist_ok=True)
 with a.output.open('x') as out:json.dump(result,out,sort_keys=True);out.write('\n')
 a.output.chmod(0o644)
if __name__=='__main__':
 p=argparse.ArgumentParser()
 for n in ('cases','output'):p.add_argument('--'+n,type=P,required=True)
 for n in ('os','arch','version','entry'):p.add_argument('--'+n,required=True)
 main(p.parse_args())
