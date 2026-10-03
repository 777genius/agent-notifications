"""Frozen ROOT TEST consumers only: root-owned cgroup launch and held netns witness."""
import fcntl
import hashlib
import os
from pathlib import Path
import socket
import subprocess
import time
import uuid

CGROUP = Path('/sys/fs/cgroup')
TEST_BASES = [Path('/var/tmp'), Path('/srv/workers/jobs/universal-agent-plugins/opencode-dual-20261001/sandboxes')]
NS_GET_NSTYPE, CLONE_NEWNET = 0xb703, 0x40000000


def require(value, reason):
    if not value: raise ValueError(reason)


def private_netns(host_fd):
    require(isinstance(host_fd, int) and host_fd >= 3, 'actual held external netns FD required')
    require(fcntl.ioctl(host_fd, NS_GET_NSTYPE) == CLONE_NEWNET, 'held FD is not a network namespace')
    outside = os.fstat(host_fd)
    fd = os.open('/proc/self/ns/net', os.O_RDONLY | os.O_CLOEXEC)
    try:
        require(fcntl.ioctl(fd, NS_GET_NSTYPE) == CLONE_NEWNET, 'current FD is not a network namespace')
        current = os.fstat(fd)
    finally: os.close(fd)
    require((outside.st_dev, outside.st_ino) != (current.st_dev, current.st_ino), 'external and current network namespaces are identical')
    require({name for _, name in socket.if_nameindex()} == {'lo'}, 'owned namespace must have only loopback')
    return {'externalDevice':outside.st_dev,'externalInode':outside.st_ino,
            'privateDevice':current.st_dev,'privateInode':current.st_ino,'onlyLoopback':True}


def run_owned(argv, test_root, log_path, timeout=900, net_fd_flag='--host-netns-fd'):
    require(os.geteuid() == 0, 'only the root parent may launch a TEST cgroup')
    require(net_fd_flag in ('--host-netns-fd','--host-net-fd'), 'closed consumer NET FD flag required')
    root, log = Path(test_root), Path(log_path)
    require(root.is_dir() and not root.is_symlink() and root.resolve() == root and root.name.startswith('TEST-') and root.parent in TEST_BASES, 'canonical allocated TEST root required')
    require(log.parent == root and not log.exists() and not log.is_symlink(), 'fresh private TEST log required')
    require(isinstance(argv,list) and argv and all(isinstance(x,str) and '\0' not in x for x in argv) and 0 < timeout <= 900, 'closed trusted argv/budget required')
    require(CGROUP.resolve() == CGROUP and (CGROUP/'cgroup.controllers').is_file(), 'canonical unified cgroup hierarchy required')
    mounts=Path('/proc/self/mountinfo').read_text().splitlines()
    require(any(x.split()[4] == str(CGROUP) and ' - cgroup2 ' in x and 'rw' in x.split()[5].split(',') for x in mounts), 'writable cgroup2 mount required')
    leaf=CGROUP/('TEST-uap-cli-reader-'+uuid.uuid4().hex);leaf.mkdir(mode=0o755)
    identity=leaf.stat();key=(identity.st_dev,identity.st_ino);host_fd=None;p=None
    record={'status':'unqualified','cgroupPath':str(leaf),'directoryIdentity':{'device':key[0],'inode':key[1]},
            'naturalWait':False,'leaderReaped':False,'populatedZero':False,'forcedKillUsed':False,'leafRemoved':False}
    def checked():
        st=leaf.stat(follow_symlinks=False)
        require(not leaf.is_symlink() and st.st_uid == 0 and (st.st_dev,st.st_ino) == key, 'owned leaf identity changed')
    def events():
        checked();fields=dict(x.split() for x in (leaf/'cgroup.events').read_text().splitlines())
        require(fields.get('populated') in ('0','1'), 'actual populated state missing');return fields
    def move_before_exec():
        checked();fd=os.open(leaf/'cgroup.procs',os.O_WRONLY | os.O_NOFOLLOW)
        try:
            value=str(os.getpid()).encode();require(os.write(fd,value) == len(value),'child cgroup assignment incomplete')
        finally:os.close(fd)
        require(Path('/proc/self/cgroup').read_text().splitlines() == ['0::/'+leaf.name], 'child membership not bound before exec')
    env={'PATH':'/usr/bin:/bin','LANG':'C.UTF-8','TZ':'UTC','PYTHONDONTWRITEBYTECODE':'1'}
    try:
        require(all((leaf/name).is_file() and not (leaf/name).is_symlink() for name in ['cgroup.events','cgroup.kill','cgroup.procs']), 'owned descendant controls unavailable')
        require(all((leaf/name).stat().st_uid == 0 and (leaf/name).stat().st_mode & 0o022 == 0 for name in ['cgroup.events','cgroup.kill','cgroup.procs']),'child must not write cgroup authority')
        record['initialEvents']=events();require(record['initialEvents']['populated'] == '0','fresh leaf not empty')
        host_fd=os.open('/proc/self/ns/net',os.O_RDONLY | os.O_CLOEXEC)
        require(fcntl.ioctl(host_fd,NS_GET_NSTYPE) == CLONE_NEWNET,'outer FD is not a netns')
        host=os.fstat(host_fd);record['externalNetns']={'device':host.st_dev,'inode':host.st_ino,'heldFd':host_fd}
        actual=[*argv,net_fd_flag,str(host_fd)];record.update(argv=actual,environment=env,timeoutSeconds=timeout,netFdFlag=net_fd_flag)
        with log.open('xb') as out:
            p=subprocess.Popen(actual,cwd=root,env=env,stdin=subprocess.DEVNULL,stdout=out,stderr=subprocess.STDOUT,
                               start_new_session=True,pass_fds=(host_fd,),preexec_fn=move_before_exec)
            record['ownedPid']=p.pid;record['exitCode']=p.wait(timeout=timeout);record['naturalWait']=True
        record['completedEvents']=events()
        require(record['exitCode'] == 0 and record['completedEvents']['populated'] == '0', 'child failed or detached descendants remain')
        record['status']='completed'
    except Exception as error:record['failure']=type(error).__name__+': '+str(error)
    finally:
        began=time.monotonic();end=began+10
        try:
            if events()['populated'] != '0':
                require(record['status'] != 'completed','success cannot require cgroup.kill')
                checked();fd=os.open(leaf/'cgroup.kill',os.O_WRONLY | os.O_NOFOLLOW)
                try:require(os.write(fd,b'1') == 1,'atomic owned cgroup.kill incomplete');record['forcedKillUsed']=True
                finally:os.close(fd)
            if p is not None and p.poll() is None:
                remaining=end-time.monotonic();require(remaining>0,'shared cleanup budget exhausted');p.wait(timeout=remaining)
            while events()['populated'] != '0' and time.monotonic()<end:time.sleep(min(.02,max(0,end-time.monotonic())))
            record['finalEvents']=events();record['populatedZero']=record['finalEvents']['populated'] == '0'
            require(record['populatedZero'],'owned descendants unresolved within shared 10s cleanup budget')
            checked();leaf.rmdir();record['leafRemoved']=True
        except Exception as error:record.update(status='unqualified',cleanupFailure=type(error).__name__+': '+str(error))
        if host_fd is not None:os.close(host_fd);record['externalFdReleased']=True
        record['leaderReaped']=p is not None and p.poll() is not None
        if p is not None:record['exitCode']=p.returncode
        record['cleanupSeconds']=round(time.monotonic()-began,4)
        if log.is_file():
            with log.open('rb') as stream:record['logSHA256']=hashlib.file_digest(stream,'sha256').hexdigest()
    return record
