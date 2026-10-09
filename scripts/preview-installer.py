"""Preview the real macOS installer with installed CLIs in a retained, fresh TEST profile."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import stat
import subprocess
import sys
import tarfile
import tempfile

REPO = '777genius/agent-notifications'
TOOLS = ('bash sh env python3 node tar gzip unzip zip mktemp rm cat cp mv chmod mkdir ln '
         'uname tr wc head cmp grep sed awk dirname basename find sort sha256sum shasum '
         'cut xargs sleep date stat diff touch readlink dd od git').split()
PRIVATE_KEYS = ('HOME USERPROFILE APPDATA LOCALAPPDATA XDG_CONFIG_HOME XDG_CACHE_HOME '
                'XDG_DATA_HOME XDG_STATE_HOME XDG_RUNTIME_DIR XDG_CONFIG_DIRS XDG_DATA_DIRS '
                'CODEX_HOME CLAUDE_HOME CLAUDE_CONFIG_DIR TMPDIR TMP TEMP '
                'OPENCODE_CONFIG_DIR GEMINI_CLI_HOME').split()


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def private_directory(path):
    if path.is_symlink():
        raise ValueError('TEST state directory must not be a symlink')
    path.mkdir(parents=True, exist_ok=True, mode=0o700)
    identity = path.stat()
    if identity.st_uid != os.getuid() or stat.S_IMODE(identity.st_mode) & 0o077:
        raise ValueError('TEST state directory must be owned by you with mode 700: '+str(path))
    return path.resolve()


def source_identity(root, expected=None):
    env = {'PATH': '/usr/bin:/bin', 'GIT_CONFIG_NOSYSTEM': '1', 'GIT_CONFIG_GLOBAL': '/dev/null'}
    sha = subprocess.check_output(['/usr/bin/git', '-C', str(root), 'rev-parse', 'HEAD'], env=env, text=True).strip()
    if not re.fullmatch('[0-9a-f]{40}', sha) or expected and sha != expected:
        raise ValueError('Repository HEAD changed during preview preparation')
    subprocess.run(['/usr/bin/git', '-C', str(root), 'diff', '--quiet', 'HEAD', '--'], env=env, check=True)
    return sha


def committed_tree(root, state, sha):
    # Build exactly the committed tree: ignored go.work/vendor and untracked Go
    # files in the caller's checkout must never acquire a false HEAD stamp.
    sources = private_directory(state/'sources')
    source = Path(tempfile.mkdtemp(prefix=sha+'-', dir=sources))
    archive = source/'committed-source.tar'
    subprocess.run(['/usr/bin/git', '-C', str(root), 'archive', '--format=tar',
                    '--output', str(archive), sha], check=True)
    tree = private_directory(source/'tree')
    with tarfile.open(archive) as committed:
        members = committed.getmembers()
        links = []
        for member in members:
            destination = tree/member.name
            if (not destination.resolve().is_relative_to(tree) or
                    not (member.isfile() or member.isdir() or member.issym())):
                raise ValueError('Unsafe committed source archive member: '+member.name)
            if member.issym():
                target = Path(member.linkname)
                if target.is_absolute() or not (destination.parent/target).resolve().is_relative_to(tree):
                    raise ValueError('Unsafe committed source symlink: '+member.name)
                links.append((destination, target))
        # Materialize committed files first; symlinks cannot redirect extraction.
        options = {"filter": "fully_trusted"} if sys.version_info >= (3, 12) else {}
        committed.extractall(tree, members=[m for m in members if not m.issym()], **options)
        for destination, target in links:
            destination.symlink_to(target)
        for destination, _ in links:
            try:
                resolved = destination.resolve(strict=True)
            except FileNotFoundError:
                resolved = destination.resolve(strict=False)
            except (OSError, RuntimeError) as error:
                raise ValueError('Invalid committed source symlink chain: '+str(destination)) from error
            if not resolved.is_relative_to(tree):
                raise ValueError('Committed source symlink escapes tree: '+str(destination))
    return tree


def build_binary(root, state, sha, go):
    output = state/'binaries'/sha/'claude-notifications'
    private_directory(output.parent)
    if output.is_file():
        return output
    compiler = shutil.which(go)
    if not compiler:
        raise ValueError('Go is required unless --binary supplies an exact-source build')
    env = {'PATH': '/usr/bin:/bin', 'GOMAXPROCS': '2', 'GOTELEMETRY': 'off', 'GOWORK': 'off'}
    for key, leaf in (('HOME','build-home'),('GOCACHE','go-cache'),('GOMODCACHE','go-modules'),('TMPDIR','build-tmp')):
        env[key] = str(private_directory(state/leaf))
    tree = committed_tree(root, state, sha)
    fd, temporary = tempfile.mkstemp(prefix='build-', dir=output.parent)
    os.close(fd)
    try:
        print('Building clean source '+sha+' (private reusable Go cache)...', flush=True)
        subprocess.run([compiler, 'build', '-mod=readonly', '-ldflags', '-s -w -X main.selectorSourceCommit='+sha,
                        '-o', temporary, './cmd/claude-notifications'], cwd=tree, env=env, check=True)
        source_identity(root, sha)
        os.replace(temporary, output)
    finally:
        Path(temporary).unlink(missing_ok=True)
    return output


def native_archive(state, supplied):
    if supplied:
        return supplied.resolve(strict=True)
    gh = shutil.which('gh')
    if not gh:
        raise ValueError('Provide --native-zip or install/authenticate GitHub CLI (gh)')
    release = json.loads(subprocess.check_output([gh, 'release', 'view', '--repo', REPO,
                                                 '--json', 'tagName'], text=True))['tagName']
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', release):
        raise ValueError('Unexpected public release tag')
    cache = private_directory(state/'native'/release)
    archive, manifest = cache/'ClaudeNotifier.app.zip', cache/'checksums.txt'
    if not archive.exists() or not manifest.exists():
        subprocess.run([gh, 'release', 'download', release, '--repo', REPO, '--dir', str(cache),
                        '--pattern', 'ClaudeNotifier.app.zip', '--pattern', 'checksums.txt', '--clobber'], check=True)
    matches = [line.split()[0] for line in manifest.read_text().splitlines()
               if len(line.split()) == 2 and line.split()[1] == 'ClaudeNotifier.app.zip']
    if matches != [digest(archive)]:
        raise ValueError('Public native archive checksum mismatch')
    return archive


def build_portable(root, state, sha, go, binary, version, arch):
    # The official builder embeds the committed skills/manifest and supplied
    # exact-source executable; no hand-written substitute package is accepted.
    output = state/'portable'/sha/('agent-notify-portable-darwin-'+arch+'.zip')
    private_directory(output.parent)
    proof = output.with_suffix('.json')
    expected = {'SourceCommit': sha, 'BinarySHA256': digest(binary)}
    if output.is_file() and proof.is_file():
        cached = json.loads(proof.read_text())
        if cached == dict(expected, ArchiveSHA256=digest(output)):
            return output
    compiler = shutil.which(go)
    if not compiler:
        raise ValueError('Go is required to build the real portable package')
    tree = committed_tree(root, state, sha)
    env = {'PATH': '/usr/bin:/bin', 'GOMAXPROCS': '2', 'GOTELEMETRY': 'off', 'GOWORK': 'off'}
    for key, leaf in (('HOME','build-home'),('GOCACHE','go-cache'),('GOMODCACHE','go-modules'),('TMPDIR','build-tmp')):
        env[key] = str(private_directory(state/leaf))
    subprocess.run([compiler,'run','-mod=readonly','./cmd/build-portable-package',
                    '-version',version[1:],'-os','darwin','-arch',arch,
                    '-executable',str(binary),'-output',str(output),
                    '-workdir',str(output.parent/'expanded')], cwd=tree, env=env,check=True)
    source_identity(root,sha)
    proof.write_text(json.dumps(dict(expected, ArchiveSHA256=digest(output))))
    return output


def discover_clis(args):
    result = {}
    for name in ('claude','codex','opencode','gemini'):
        found = str(args.gemini_cli) if name == 'gemini' and args.gemini_cli else shutil.which(name)
        if found:
            path = Path(found).resolve(strict=True)
            if not path.is_file() or not os.access(path,os.X_OK):
                raise ValueError('CLI must be an actual executable: '+str(path))
            result[name] = str(path)
    return result


def local_git_snapshot(root, lab, sha, env):
    snapshot = lab/'source.git'
    git_env = {'PATH':'/usr/bin:/bin','HOME':env['HOME'],
               'GIT_CONFIG_NOSYSTEM':'1','GIT_CONFIG_GLOBAL':'/dev/null',
               'GIT_CONFIG_SYSTEM':'/dev/null','GIT_TERMINAL_PROMPT':'0'}
    subprocess.run(['/usr/bin/git','-c','init.defaultBranch=main','init','--quiet','--bare',str(snapshot)],env=git_env,check=True,stdout=subprocess.DEVNULL)
    subprocess.run(['/usr/bin/git','-C',str(snapshot),'-c','protocol.file.allow=always',
                    'fetch','--quiet','--depth=1',str(root),sha],env=git_env,check=True)
    for ref in ('refs/tags/dist/platform-source/'+sha,'refs/heads/release/platform-macos'):
        subprocess.run(['/usr/bin/git','-C',str(snapshot),'update-ref',ref,sha],env=git_env,check=True)
    subprocess.run(['/usr/bin/git','-C',str(snapshot),'symbolic-ref','HEAD','refs/heads/release/platform-macos'],env=git_env,check=True)
    config = lab/'gitconfig'
    urls = ('https://github.com/'+REPO+'.git','https://github.com/'+REPO,
            'git@github.com:'+REPO+'.git','git@github.com:'+REPO,
            'ssh://git@github.com/'+REPO+'.git','ssh://git@github.com/'+REPO)
    config.write_text('[protocol]\n\tallow = never\n[protocol "file"]\n\tallow = always\n'
                      '[credential]\n\thelper =\n[core]\n\thooksPath = /dev/null\n'+
                      '[url "'+snapshot.as_uri()+'"]\n'+''.join('\tinsteadOf = '+url+'\n' for url in urls))
    env.update(GIT_CONFIG_GLOBAL=str(config),GIT_CONFIG_SYSTEM='/dev/null',
               GIT_CONFIG_NOSYSTEM='1',GIT_TERMINAL_PROMPT='0',GIT_ASKPASS='/usr/bin/false',
               SSH_ASKPASS='/usr/bin/false')


def cli_proxies(commands, lab, installed, env, sha):
    if not Path('/usr/bin/sandbox-exec').is_file():
        raise ValueError('Real CLI preview requires macOS sandbox-exec')
    home = Path.home()
    blocked = [home/leaf for leaf in ('.claude','.claude.json','.codex','.gemini','.config',
                                    '.gitconfig','.git-credentials','.ssh','.aws','.npmrc','.local/share/opencode',
                                    'Library/Keychains','Library/Application Support/Claude',
                                    'Library/Application Support/Codex')]
    blocked.append(Path('/Library/Keychains'))
    # Cover the spelling a CLI may open and the actual target of host symlinks.
    blocked = sorted({str(path) for entry in blocked for path in (entry, entry.resolve())})
    profile = lab/'cli.sb'
    quoted = lambda value: json.dumps(str(value))
    profile.write_text('(version 1)\n(allow default)\n(deny network*)\n'
                       '(deny process-exec (literal "/usr/bin/security"))\n'
                       '(deny mach-lookup (global-name "com.apple.securityd") '
                       '(global-name "com.apple.securityd.xpc") (global-name "com.apple.KeychainCircleNotification") '
                       '(global-name-regex #".*(securityd|SecurityAgent|keychain).*"))\n'
                       '(deny file-write* (require-not (subpath '+quoted(lab)+')))\n'+
                       ''.join('(deny file-read* file-write* (subpath '+quoted(p)+'))\n' for p in blocked))
    # Empty Gemini system/user inputs prevent upward .env/settings/trust search.
    for directory in (lab,Path(env['HOME']),Path(env['GEMINI_CLI_HOME']),Path(env['HOME'])/'.gemini'):
        private_directory(directory)
        (directory/'.env').write_text('')
    for key,leaf in (('GEMINI_CLI_SYSTEM_SETTINGS_PATH','gemini-system.json'),
                     ('GEMINI_CLI_SYSTEM_DEFAULTS_PATH','gemini-defaults.json'),
                     ('GEMINI_CLI_TRUSTED_FOLDERS_PATH','gemini-trust.json')):
        path = lab/leaf
        path.write_text('{}\n')
        env[key] = str(path)
    env.update(DISABLE_AUTOUPDATER='1',CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC='1',
               DISABLE_TELEMETRY='1',DO_NOT_TRACK='1',OTEL_SDK_DISABLED='true',
               GEMINI_TELEMETRY_ENABLED='false',OPENCODE_DISABLE_AUTOUPDATE='1')
    data = {'lab':str(lab),'installed':installed,'env':env,'profile':str(profile),
            'sha':sha,'repo':REPO}
    settings = lab/'cli-proxy.json'
    settings.write_text(json.dumps(data))
    code = r'''import hashlib, json, os, pathlib, re, subprocess, sys
D = json.loads(pathlib.Path(SETTINGS).read_text())
lab = pathlib.Path(D['lab'])
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
def local(value):
    p = pathlib.Path(value)
    return p.is_absolute() and p.resolve().is_relative_to(lab)
def marketplace(value):
    if not local(value): return False
    root = pathlib.Path(value)
    path = root/'.github/plugin/marketplace.json'
    try:
        doc = json.loads(path.read_text())
        expected = 'agentplugins-'+hashlib.sha256(root.name.encode()).hexdigest()[:12]
        if doc.get('name') != expected: return False
        if [p.get('name') for p in doc.get('plugins',[])] != ['agent-notify']: return False
        known.add(expected)
        registry.write_text(json.dumps(sorted(known)))
        return True
    except (OSError,ValueError,KeyError): return False
registry = lab/'allowed-codex-marketplaces.json'
known = set(json.loads(registry.read_text())) if registry.exists() else set()
allowed = args == ['--version']
if name == 'claude':
    exact = 'claude-notifications-go@claude-notifications-go'
    allowed |= args in [['plugin','list','--json'],['plugin','marketplace','list','--json']]
    allowed |= args in [['plugin',verb,exact] for verb in ('install','update','uninstall')]
    check = args
    if len(check) > 2 and check[0] == '--settings' and local(check[1]): check = check[2:]
    sources = [D['repo']+'#'+ref for ref in ('dist/platform-source/'+D['sha'],'release/platform-macos')]
    allowed |= check in [['plugin','marketplace','add',source] for source in sources]
if name == 'codex':
    allowed |= args == ['plugin','list','--json']
    if len(args) == 5 and args[:3] == ['plugin','marketplace','add'] and args[4] == '--json':
        allowed |= marketplace(args[3])
    allowed |= args in [['plugin','marketplace',verb,key,'--json'] for verb in ('update','remove') for key in known]
    allowed |= args in [['plugin',verb,'agent-notify@'+key,'--json'] for verb in ('add','remove') for key in known]
if not allowed or name not in D['installed']:
    print('Rejected TEST CLI command: '+name+' '+repr(args),file=sys.stderr)
    sys.exit(99)
# SDK native adapters intentionally filter ambient environment. Restore only
# this harness's fixed private environment, preserving their private profile.
env = dict(D['env'])
for key in ('HOME','CODEX_HOME','CLAUDE_CONFIG_DIR','GEMINI_CLI_HOME','TMPDIR'):
    if key in os.environ:
        if not local(os.environ[key]): sys.exit('Rejected non-TEST CLI profile')
        env[key] = os.environ[key]
with (lab/'cli-commands.jsonl').open('a') as log:
    log.write(json.dumps({'cli':name,'executable':D['installed'][name],'args':args})+'\n')
try:
    result = subprocess.run(['/usr/bin/sandbox-exec','-f',D['profile'],D['installed'][name]]+args,
                            cwd=lab,env=env,timeout=60)
    sys.exit(result.returncode)
except subprocess.TimeoutExpired:
    sys.exit('TEST CLI command exceeded 60 seconds')
'''.replace('SETTINGS',repr(str(settings)))
    for name in installed:
        path = commands/name
        path.write_text('#!'+str(Path(sys.executable).resolve())+' -I\n'+code)
        path.chmod(0o700)


def offline_transport(commands, assets, root, sha, version, name, requests):
    raw = 'https://raw.githubusercontent.com/'+REPO+'/'+sha
    mapping = {raw+'/bin/'+leaf: root/'bin'/leaf
               for leaf in ('setup.sh','bootstrap.sh','release-channel.sh','install.sh')}
    mapping[raw+'/release-channels.tsv'] = assets/'channels.tsv'
    mapping['https://github.com/'+REPO+'/archive/'+sha+'.tar.gz'] = assets/'source.tar.gz'
    base = 'https://preview-installer.invalid/releases/download/'+version+'/'
    mapping.update({base+leaf: assets/leaf for leaf in ('checksums.txt',name,'ClaudeNotifier.app.zip','agent-notify-portable-darwin-'+('arm64' if os.uname().machine == 'arm64' else 'amd64')+'.zip')})
    # Copy source inputs so every acquisition sees the verified source snapshot.
    for url, source in list(mapping.items()):
        if source.is_relative_to(root):
            destination = assets/source.name
            shutil.copyfile(source, destination)
            mapping[url] = destination
    clauses = '\n'.join(' '+shlex.quote(url)+') source='+shlex.quote(str(file))+' ;;'
                        for url,file in mapping.items())
    script = '''#!/bin/bash
set -eu
out=""; url=""; accept=""
while [ "$#" -gt 0 ]; do
 case "$1" in
  -o|-H|--connect-timeout|--max-time)
   [ "$#" -ge 2 ] || exit 99
   case "$1" in -o) out=$2 ;; -H) accept=$2 ;; esac
   shift 2 ;;
  -fsSL|-fSL|-sS|-s|-f|-L) shift ;;
  -*) echo "Unsupported TEST curl option: $1" >&2; exit 99 ;;
  *) [ -z "$url" ] || exit 99; url=$1; shift ;;
 esac
done
printf '%s\\n' "$url" >> REQUESTS
case "$url" in
 API_MAIN|API_TAG)
  [ "$accept" = 'Accept: application/vnd.github.sha' ] && [ -n "$out" ] || exit 99
  printf '%s' COMMIT > "$out"; exit 0 ;;
 CLAUSES
 *) echo "Unexpected TEST download: $url" >&2; exit 99 ;;
esac
if [ -n "$out" ]; then cp "$source" "$out"; else cat "$source"; fi
'''.replace('REQUESTS', shlex.quote(str(requests))).replace('API_MAIN', shlex.quote('https://api.github.com/repos/'+REPO+'/commits/main')).replace('API_TAG', shlex.quote('https://api.github.com/repos/'+REPO+'/commits/'+version)).replace('COMMIT', shlex.quote(sha)).replace('CLAUSES', clauses)
    (commands/'curl').write_text(script)
    for path in commands.iterdir():
        path.chmod(0o700)


def preview(args):
    if sys.platform != 'darwin' or not os.isatty(0) or not os.isatty(1):
        raise ValueError('This macOS preview requires an interactive terminal')
    root = Path(__file__).resolve().parent.parent
    sha = source_identity(root)
    state = private_directory(args.state_dir or Path(tempfile.gettempdir())/('agent-notifications-preview-TEST-'+str(os.getuid())))
    if state.is_relative_to(root):
        raise ValueError('Keep TEST state outside the source checkout')
    binary = args.binary.resolve(strict=True) if args.binary else build_binary(root,state,sha,args.go)
    archive = native_archive(state,args.native_zip)
    installed = discover_clis(args)
    lab = Path(tempfile.mkdtemp(prefix='AllAgents-TEST-',dir=state))
    print('Retained TEST profile: '+str(lab), flush=True)
    commands = private_directory(lab/'fixture-tools')
    trusted = private_directory(lab/'trusted-tools')
    assets = private_directory(lab/'assets')
    env = {key:os.environ[key] for key in ('TERM','COLORTERM','TERM_PROGRAM','NO_COLOR','LANG','LC_CTYPE') if key in os.environ}
    env['PATH'] = str(commands)+os.pathsep+str(trusted)
    for tool in TOOLS:
        source = shutil.which(tool)
        if source:
            (trusted/tool).symlink_to(source)
    for key in PRIVATE_KEYS:
        env[key] = str(private_directory(lab/key))
    arch = 'arm64' if os.uname().machine == 'arm64' else 'amd64'
    name = 'claude-notifications-darwin-'+arch
    staged = assets/name
    shutil.copyfile(binary,staged)
    staged.chmod(0o700)
    version = subprocess.check_output([str(staged),'--version'],env=env,text=True).strip().split()[-1]
    if not re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+',version):
        raise ValueError('Unexpected candidate binary version')
    shutil.copyfile(archive,assets/'ClaudeNotifier.app.zip')
    portable = build_portable(root, state, sha, args.go, staged, version, arch)
    shutil.copyfile(portable, assets/portable.name)
    subprocess.run(['/usr/bin/git','-C',str(root),'archive','--format=tar.gz',
                    '--prefix=agent-notifications-'+sha+'/', '--output',str(assets/'source.tar.gz'),sha],check=True)
    (assets/'checksums.txt').write_text(''.join(digest(assets/leaf)+'  '+leaf+'\n' for leaf in (name,'ClaudeNotifier.app.zip',portable.name)))
    rows = ['# agent-notifications-platform-channels-v1']
    for platform, cpu in (('darwin','amd64'),('darwin','arm64'),('linux','amd64'),('linux','arm64'),('windows','amd64')):
        ref = 'release/platform-macos' if platform == 'darwin' else 'release/platform-linux-windows'
        rows.append('\t'.join([platform,cpu,version,sha,sha,ref]))
    (assets/'channels.tsv').write_text('\n'.join(rows)+'\n')
    offline_transport(commands,assets,root,sha,version,name,lab/'acquisitions.log')
    local_git_snapshot(root, lab, sha, env)
    cli_proxies(commands, lab, installed, env, sha)
    env['BOOTSTRAP_RELEASES_BASE_URL'] = 'https://preview-installer.invalid/releases'
    # Supported read-only intent verifies the executable, not its filename/cache key.
    intent = assets/'source-proof.json'
    result = subprocess.run([str(staged),'setup-products','prepare','--products','opencode',
                             '--intent-file',str(intent),'--plain'],cwd=lab,env=env,
                            text=True,capture_output=True,timeout=40)
    if result.returncode or result.stdout != 'prepared\n':
        raise ValueError('Read-only source verification failed: '+result.stdout+result.stderr)
    proof = json.loads(intent.read_text())['provenance']
    if proof['SourceCommit'] != sha or proof['SHA256'] != digest(staged):
        raise ValueError('Binary SourceCommit/SHA256 does not match clean repository HEAD')
    source_identity(root,sha)
    print('Actual installed CLIs (missing CLIs remain absent): '+json.dumps(installed),flush=True)
    print('Select the installed clients. Fresh Desktop on/Webhook off.',flush=True)
    print('CLI registration uses a local source snapshot; CLI network and host credentials are denied.',flush=True)
    print('This previews installation only; it does not run agents or verify notification delivery.',flush=True)
    command = [str(trusted/'bash'),str(assets/'setup.sh')]+(['--plain'] if args.plain else ['--ui','auto'])
    try:
        return subprocess.run(command,cwd=lab,env=env).returncode
    finally:
        print('\nTEST artifacts retained: '+str(lab),flush=True)
        print('Keep this directory while its native app registration may exist; no automatic deletion.',flush=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__, epilog='Requires macOS, Python 3.9+, Bash, system signing/sandbox tools, and Go (also for the portable package). Without --native-zip, gh downloads the latest signed public helper and verifies checksums. No agent/account/provider execution. TEST profiles are retained.')
    parser.add_argument('--binary',type=Path,help='prebuilt binary whose supported SourceCommit matches clean HEAD')
    parser.add_argument('--native-zip',type=Path,help='existing signed ClaudeNotifier.app.zip; installer verifies signature/attestation')
    parser.add_argument('--state-dir',type=Path,help='private mode-700 TEST directory outside checkout for caches/profiles (default: OS temporary cache by UID)')
    parser.add_argument('--go',default='go',help='Go executable for source build (default: go from PATH)')
    parser.add_argument('--gemini-cli',type=Path,help='explicit real Gemini CLI executable for TEST only (for example an isolated 0.62.0 install)')
    parser.add_argument('--plain',action='store_true',help='explicit accessible line prompts instead of automatic terminal UI')
    args = parser.parse_args()
    try:
        return preview(args)
    except (ValueError,OSError,subprocess.SubprocessError) as error:
        print('Preview failed: '+str(error),file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
