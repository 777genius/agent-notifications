import copy,hashlib,json
SOURCE_CONTRACT_SHA256="f22d32cf6be5701923877c6201b11ae0b915dff259421b43d7f1b30b88e9d79e"
COMMAND="printf P0_OWNED_TEST"

def joined_rejection(native,history,pending,session,call,tool,command):
    """Pure causal comparison. Running/reply/error alone never proves execution."""
    if any(e.get('type')=='session.status' and e.get('properties',{}).get('status',{}).get('type')=='retry' for e in native):return None
    if tool!='bash' or not isinstance(pending,dict) or pending.get('sessionID')!=session or pending.get('permission')!=tool:return None
    ref=pending.get('tool',{});assistant=ref.get('messageID');request=pending.get('id')
    if not isinstance(assistant,str) or not assistant or ref.get('callID')!=call or not isinstance(request,str) or not request:return None
    asked=[(i,e.get('properties',{})) for i,e in enumerate(native) if e.get('type')=='permission.asked' and e.get('properties',{}).get('id')==request]
    replied=[(i,e.get('properties',{})) for i,e in enumerate(native) if e.get('type')=='permission.replied' and e.get('properties',{}).get('requestID')==request]
    if len(asked)!=1 or len(replied)!=1:return None
    ai,a=asked[0];ri,r=replied[0]
    if not ai<ri or any(a.get(k)!=pending.get(k) for k in ('id','sessionID','permission','tool')) or r.get('sessionID')!=session or r.get('reply')!='reject':return None
    parts=[(i,e.get('properties',{}).get('part',{})) for i,e in enumerate(native) if e.get('type')=='message.part.updated' and e.get('properties',{}).get('part',{}).get('callID')==call]
    if not parts or any(p.get('type')!='tool' or p.get('sessionID')!=session or p.get('messageID')!=assistant or p.get('tool')!=tool for _,p in parts):return None
    if any(p.get('state',{}).get('status')=='completed' or 'output' in p.get('state',{}) for _,p in parts):return None
    running=[(i,p) for i,p in parts if p.get('state',{}).get('status')=='running' and p.get('state',{}).get('input',{}).get('command')==command]
    errors=[(i,p) for i,p in parts if p.get('state',{}).get('status')=='error']
    if not running or not errors:return None
    _,before=running[-1];ei,error=errors[-1];state=error['state'];birth=before['state'].get('time',{}).get('start')
    part_id=before.get('id')
    if not isinstance(part_id,str) or not part_id or any(p.get('id')!=part_id for _,p in parts):return None
    if not running[-1][0]<ri<ei or state.get('input')!=before['state'].get('input') or state.get('time',{}).get('start')!=birth or type(birth) is not int or birth<=0:return None
    end=state.get('time',{}).get('end')
    if type(end) is not int or end<birth or not isinstance(state.get('error'),str) or not state['error']:return None
    rows=[row for row in history if isinstance(row,dict) and row.get('info',row).get('id')==assistant]
    if len(rows)!=1:return None
    row=rows[0];info=row.get('info',row);final=row.get('parts',[])
    if info.get('role')!='assistant' or info.get('sessionID')!=session or not info.get('parentID') or info.get('summary') or info.get('error'):return None
    if type(info.get('time',{}).get('completed')) is not int or info['time']['completed']<=0:return None
    answers=[e.get('properties',{}).get('info',{}) for e in native if e.get('type')=='message.updated' and e.get('properties',{}).get('info',{}).get('id')==assistant]
    if not any(a.get('sessionID')==session and a.get('role')=='assistant' and a.get('parentID')==info['parentID'] and a.get('time')==info.get('time') and not a.get('error') for a in answers):return None
    actual=[p for p in final if isinstance(p,dict) and p.get('type')=='tool' and p.get('callID')==call]
    if len(actual)!=1:return None
    compared_error=error;actual_state=actual[0].get('state')
    # Stock failToolCall retains optional undefined metadata; capture marks it,
    # while JSON history omits it. Normalize only this same-running-part case.
    if state.get('metadata')=='[unsupported]' and 'metadata' not in before['state'] and isinstance(actual_state,dict) and 'metadata' not in actual_state:
        compared_error=copy.deepcopy(error);compared_error['state'].pop('metadata')
    if any(actual[0].get(k)!=compared_error.get(k) for k in ('id','sessionID','messageID','callID','tool','state')):return None
    users=[e.get('properties',{}).get('info',{}) for e in native if e.get('type')=='message.updated' and e.get('properties',{}).get('info',{}).get('role')=='user']
    if not any(u.get('id')==info['parentID'] and u.get('sessionID')==session for u in users):return None
    return {'sessionID':session,'requestID':request,'assistantID':assistant,'userID':info['parentID'],'callID':call,'tool':tool,'nativeAskedIndex':ai,'nativeReplyIndex':ri,'nativeErrorIndex':ei,'sourceContractSHA256':SOURCE_CONTRACT_SHA256,'matchingFinalPartSHA256':hashlib.sha256(json.dumps(actual[0],sort_keys=True,separators=(',',':')).encode()).hexdigest(),'genuineWrappedError':state['error'],'finalFinish':info.get('finish'),'requestedCommandHMAC':command,'nativeRejectionJoined':True,'commandNonexecution': 'pending_complete_host_close_recording'}

def native_join(g,base,project,enc,native,session,call,tool,headers,key,pending):
    history=g['read_history'](base,project,enc,False,headers)
    if not isinstance(history,list) or len(history)>48:return None
    # Capture and history use identical privacy redaction; decode only this closed status enum.
    history=g['private_redact'](history,key);native=copy.deepcopy(native)
    for event in native:
        state=event.get('properties',{}).get('part',{}).get('state',{})
        if state.get('status')==g['private_redact']('error',key):state['status']='error'
    for row in history:
        for part in row.get('parts',[]):
            state=part.get('state',{})
            if state.get('status')==g['private_redact']('error',key):state['status']='error'
    return joined_rejection(native,history,pending,session,call,tool,g['private_redact'](COMMAND,key))
