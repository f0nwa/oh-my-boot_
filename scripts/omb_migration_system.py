#!/usr/bin/env python3
"""Offline, root-only same-host OMB migration. No remote execution."""
import argparse
import base64
import contextlib
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import socket
import stat
import subprocess
import sys
import tarfile
import tempfile
import uuid

ERROR_LOG=None

class Abort(RuntimeError):
    pass

COMPONENTS = ('boot', 'agent', 'agentctl', 'control', 'controlctl', 'images', 'imagesctl', 'winiso-prepare')
SERVICE_NAMES = ('boot', 'agent', 'control', 'images', 'dnsmasq')
ACCOUNTS = ('boot', 'control', 'images', 'iso')
DBS = ('pxe_boot', 'pxe_control', 'pxe_images')
PATH_MAP = {
    '/etc/pxe-platform':'/etc/omb',
    **{'/var/lib/pxe-platform-'+s:'/var/lib/omb-'+s for s in ('boot','agent','control','images')},
    '/usr/share/pxe-platform':'/usr/share/omb', '/srv/pxe':'/srv/omb',
    '/mnt/pxe-platform-smb-upload':'/mnt/omb-smb-upload', '/mnt/pxe-platform-smb':'/mnt/omb-smb',
    '/mnt/pxe-platform-iso':'/mnt/omb-iso', '/opt/pxe-offline':'/opt/omb-offline',
    '/opt/pxe-platform-src':'/opt/omb-src', '/home/pxe-iso':'/home/omb-iso',
    '/run/pxe-platform-boot':'/run/omb-boot', '/run/pxe-platform-images':'/run/omb-images',
    '/run/pxe-platform':'/run/omb',
}
REQUIRED = ('schema_version','source_host','collected_at','table_counts','storages','windows_profiles',
            'windows_answers','driver_configs','menu_versions','menu_integrity','menu_draft_items',
            'profile_configs','recovery','boot_snapshot')

def digest(path):
    h=hashlib.sha256()
    with open(path,'rb') as f:
        for b in iter(lambda:f.read(1024*1024),b''): h.update(b)
    return h.hexdigest()

def private_file(path):
    path=Path(path)
    try: s=path.lstat()
    except FileNotFoundError: raise Abort('Missing protected file: '+str(path))
    if not stat.S_ISREG(s.st_mode) or s.st_uid != os.geteuid() or stat.S_IMODE(s.st_mode)&0o077:
        raise Abort('Requires private owner-only regular file: '+str(path))
    return path

def private_directory(path):
    p=Path(path); s=p.lstat()
    if not stat.S_ISDIR(s.st_mode) or s.st_uid!=os.geteuid() or stat.S_IMODE(s.st_mode)&0o077:
        raise Abort('Requires private owner-only directory: '+str(p))
    return p

def atomic_json(path,value):
    p=Path(path)
    fd,temp=tempfile.mkstemp(prefix='.'+p.name+'.',dir=p.parent)
    try:
        with os.fdopen(fd,'w') as f:
            json.dump(value,f,ensure_ascii=False,indent=2); f.write('\n'); f.flush(); os.fsync(f.fileno())
        os.replace(temp,p)
        d=os.open(p.parent,os.O_RDONLY)
        try: os.fsync(d)
        finally: os.close(d)
    finally:
        if os.path.exists(temp): os.unlink(temp)

class State:
    def __init__(self,manifest): self.path=Path(str(manifest)+'.state.json'); self.lock=Path(str(self.path)+'.lock')
    def __enter__(self):
        fd=os.open(self.lock,os.O_CREAT|os.O_RDWR|os.O_NOFOLLOW,0o600)
        self.f=os.fdopen(fd,'r+'); private_file(self.lock); fcntl.flock(fd,fcntl.LOCK_EX)
        if self.path.exists(): private_file(self.path); self.value=json.loads(self.path.read_text())
        else: self.value={k:False for k in ('backup_complete','backup_verified','system_complete','data_complete','rollback_complete')}
        return self
    def update(self,**fields):
        self.value.update(fields); atomic_json(self.path,self.value)
    def __exit__(self,*exc):
        fcntl.flock(self.f.fileno(),fcntl.LOCK_UN); self.f.close()

def transform_paths(text):
    for old,new in sorted(PATH_MAP.items(),key=lambda kv:-len(kv[0])):
        text=re.sub(r'(?<![\w/])'+re.escape(old)+r'(?=$|[/\s\"\'=:;,\]}])',lambda m:new,text)
    return text

def transform_env(text):
    result=[]
    # Values are opaque unless the setting explicitly denotes an owned path/DB.
    for line in text.splitlines(keepends=True):
        m=re.match(r'^(\s*)(PXE_[A-Z0-9_]+)=(.*?)(\n?)$',line)
        if not m: result.append(line); continue
        space,key,value,nl=m.groups()
        if key.endswith(('_FILE','_DIR','_PATH','_ROOT','_SOCKET')) or key=='PXE_BOOT_UBUNTU_ISO': value=transform_paths(value)
        if key.endswith(('_DB_URL','_DATABASE_URL','_DB_DSN','_DATABASE_DSN')):
            value=transform_dsn(value)
        result.append(space+'OMB_'+key[4:]+'='+value+nl)
    return ''.join(result)

def transform_dsn(value):
    """Change only libpq database/user fields; never rewrite a password token."""
    wrapper=value[0] if len(value)>1 and value[0] in "\"'" and value[-1]==value[0] else ''
    body=value[1:-1] if wrapper else value
    names={'pxe_'+c:'omb_'+c for c in ('boot','control','images')}
    roles={'pxe-'+c:'omb-'+c for c in ('boot','control','images')}
    uri=re.match(r'^(postgres(?:ql)?://)([^/]*)(/[^?#]*)?([?#].*)?$',body)
    if uri:
        scheme,authority,path,tail=uri.groups();path=path or '';tail=tail or ''
        if '@' in authority:
            auth,host=authority.rsplit('@',1);user,sep,password=auth.partition(':')
            authority=roles.get(user,user)+sep+password+'@'+host
        path='/'+names.get(path[1:],path[1:]) if path else path
        # URI parameter keys are delimited; password values remain opaque.
        tail=re.sub(r'([?&])(user|dbname)=([^&#]*)',lambda m:m[1]+m[2]+'='+(roles if m[2]=='user' else names).get(m[3],m[3]),tail)
        result=scheme+authority+path+tail
    else:
        token=re.compile(r"([A-Za-z_][A-Za-z0-9_]*)\s*=\s*('(?:\\.|[^'\\])*'|(?:\\.|[^\s])*)")
        parts=[];end=0
        for match in token.finditer(body):
            if body[end:match.start()].strip(): raise Abort('Unsupported PostgreSQL connection string')
            raw=match[2];quoted=raw.startswith("'") and raw.endswith("'")
            plain=re.sub(r'\\(.)',r'\1',raw[1:-1] if quoted else raw)
            mapping=roles if match[1]=='user' else names if match[1]=='dbname' else {}
            replacement=mapping.get(plain)
            parts.append(body[end:match.start(2)])
            parts.append(("'"+replacement+"'" if quoted else replacement) if replacement else raw)
            end=match.end()
        if body[end:].strip() or not parts: raise Abort('Unsupported PostgreSQL connection string')
        result=''.join(parts)+body[end:]
    return wrapper+result+wrapper

def transform_unit(text):
    text=transform_paths(text)
    for c in (*COMPONENTS,'dnsmasq','iso'):
        text=re.sub(r'(?<![\w-])pxe-'+re.escape(c)+r'(?![\w-])','omb-'+c,text)
    text=re.sub(r'(?m)^(StateDirectory|RuntimeDirectory|CacheDirectory|LogsDirectory)=(.*)$', lambda m:m[1]+'='+re.sub(r'(?<![\w-])pxe-platform(?:-(boot|agent|control|images))?(?![\w-])',lambda x:'omb'+('-'+x[1] if x[1] else ''),m[2]),text)
    text=re.sub(r'\$\{PXE_([A-Z0-9_]+)\}',r'${OMB_\1}',text)
    return re.sub(r'\bPXE_([A-Z0-9_]+)=',r'OMB_\1=',text)

def mount_unit(path):
    raw=path.strip('/').encode(); out=''
    for i,c in enumerate(raw):
        if c==47: out+='-'
        elif (48<=c<=57 or 65<=c<=90 or 97<=c<=122 or c in (58,95,46)) and not (i==0 and c==46): out+=chr(c)
        else: out+='\\x%02x'%c
    return (out or '-')+'.mount'

def validate_roles(roles):
    if any(r.get('password_format')=='md5' for r in roles):
        raise Abort('MD5 role password would be erased by rename; resolve authentication explicitly before apply')

def require_forward_state(s):
    if s.get('rollback_complete'): raise Abort('Migration was rolled back; collect a new manifest for another attempt')
    if s.get('system_journal') and not s.get('system_complete'): raise Abort('Partial system migration: run rollback; do not retry apply-system')
    if not s.get('backup_complete') or not s.get('backup_verified'): raise Abort('Complete backup and successful verify-backup required')

def validate_archive(path):
    with tarfile.open(path) as t:
        members=t.getmembers(); links=set()
        for m in members:
            p=PurePosixPath(m.name)
            if p.is_absolute() or '..' in p.parts or m.isdev() or m.isfifo(): raise Abort('Unsafe backup member')
            if m.issym(): links.add(str(p))
            if m.islnk():
                q=PurePosixPath(m.linkname)
                if q.is_absolute() or '..' in q.parts: raise Abort('Unsafe backup hardlink')
        for m in members:
            if any(str(p) in links for p in PurePosixPath(m.name).parents): raise Abort('Backup member descends through symlink')

def log_failure(argv,stderr):
    if ERROR_LOG is None: return
    fd=os.open(ERROR_LOG,os.O_CREAT|os.O_APPEND|os.O_WRONLY|os.O_NOFOLLOW,0o600)
    with os.fdopen(fd,'a') as f:
        private_file(ERROR_LOG)
        f.write(datetime.datetime.now(datetime.timezone.utc).isoformat()+' '+repr(argv)+'\n')
        f.write(stderr.decode(errors='replace') if isinstance(stderr,bytes) else stderr or '')
        f.write('\n'); f.flush(); os.fsync(f.fileno())

def run(argv,input=None,output=None,allowed=(0,)):
    # Never relay stderr: pg_dump, mount or Samba diagnostics can contain secrets.
    try:
        p=subprocess.run(argv,input=input,stdout=output or subprocess.PIPE,stderr=subprocess.PIPE,text=output is None,check=False)
    except OSError: raise Abort('Required local command unavailable: '+argv[0])
    if p.returncode not in allowed:
        log_failure(argv,p.stderr)
        raise Abort('Local command failed: '+argv[0]+' (exit '+str(p.returncode)+'); inspect protected local logs, no automatic retry')
    return p.stdout if output is None else ''

def pg(db,sql,runner=run):
    return runner(['runuser','-u','postgres','--','psql','--host=/var/run/postgresql','--username=postgres','--no-password','-XAt','-v','ON_ERROR_STOP=1','-d',db],input=sql+'\n').strip()

def rows(db,sql,runner=run):
    return json.loads(pg(db,"SELECT coalesce(json_agg(r),'[]'::json) FROM ("+sql+") r;",runner))

def qi(value): return '"'+value.replace('"','""')+'"'
def qs(value): return "'"+value.replace("'","''")+"'"

def assert_stopped(runner,units):
    for u in units:
        state=runner(['systemctl','show',u,'--property=ActiveState','--value']).strip()
        if state not in ('inactive','failed'): raise Abort('Stop source writers, DHCP and mounts first: '+u)

def owned_unit(name):
    return bool(re.fullmatch(r'pxe-(boot|agent|control|images|dnsmasq)\.service|pxe-winiso@(?:\d*)\.service',name)) or (name.startswith('mnt-pxe\\x2dplatform') and name.endswith('.mount'))

def systemd_words(value):
    # systemctl quotes and doubles backslashes in list properties. Preserve
    # literal systemd path escapes (\x2d), rather than decoding them to '-'.
    result=[]
    for match in re.finditer(r'"((?:\\.|[^"\\])*)"|(\S+)',value):
        if match[1] is not None:
            result.append(re.sub(r'\\([\\"])',r'\1',match[1]))
        else:
            if '"' in match[2]: raise Abort('Malformed systemd list property')
            result.append(match[2])
    return result

def unit_inventory(unit_directory=Path('/etc/systemd/system')):
    names=set('pxe-'+s+'.service' for s in SERVICE_NAMES)
    names.add('pxe-winiso@.service')
    for line in run(['systemctl','list-units','--all','--plain','--no-legend','--no-pager','pxe-*','mnt-pxe*']).splitlines():
        if line and not line.split()[0].startswith('system-pxe'): names.add(line.split()[0])
    for p in unit_directory.glob('*pxe*'):
        if p.name.endswith(('.service','.mount')): names.add(p.name)
    out=[]
    for name in sorted(names):
        if not owned_unit(name): raise Abort('Unsupported source unit dependency: '+name)
        props={}
        if '@.service' in name:
            # A template has files but no runtime instance. Recent systemd
            # rejects `show service@.service`; loaded instances are separate.
            fragment=unit_directory/name
            if not fragment.is_file(): raise Abort('Missing source unit template: '+name)
            dropins=sorted((unit_directory/(name+'.d')).glob('*.conf'))
            props={'Id':name,'ActiveState':'inactive','UnitFileState':run(['systemctl','is-enabled',name],allowed=(0,1,4)).strip(),'FragmentPath':str(fragment),'DropInPaths':' '.join(str(p) for p in dropins)}
            for source in [fragment,*dropins]:
                for line in source.read_text().splitlines():
                    if '=' in line:
                        key,value=line.split('=',1)
                        if key in ('Requires','Wants','Before','After'): props[key]=props.get(key,'')+' '+value
        else:
            for line in run(['systemctl','show',name,'--property=Id,ActiveState,UnitFileState,FragmentPath,DropInPaths,Where,What,Type,Requires,Wants,Before,After,User,Group,ReadWritePaths']).splitlines():
                if '=' in line: k,v=line.split('=',1); props[k]=v
        for dependency in ('Requires','Wants','Before','After'):
            for d in systemd_words(props.get(dependency,'')):
                if 'pxe' in d and not owned_unit(d) and d!='system-pxe\\x2dwiniso.slice':
                    raise Abort('Unsupported source dependency: '+d)
        fragment=props.get('FragmentPath','')
        paths=([fragment] if fragment else [])+systemd_words(props.get('DropInPaths',''))
        props['files']=[file_info(p) for p in paths]
        # Effective contents are secret-bearing; only hashes enter the manifest.
        props['effective_sha256']=hashlib.sha256(run(['systemctl','cat',name]).encode()).hexdigest() if fragment else ''
        out.append(props)
    return out

def file_info(path):
    p=Path(path); s=p.lstat()
    xattrs={k:base64.b64encode(os.getxattr(p,k,follow_symlinks=False)).decode() for k in os.listxattr(p,follow_symlinks=False)} if hasattr(os,'listxattr') else {}
    x={'xattrs':xattrs,'path':str(p),'uid':s.st_uid,'gid':s.st_gid,'mode':oct(stat.S_IMODE(s.st_mode)),'size':s.st_size,'mtime_ns':s.st_mtime_ns}
    if p.is_symlink(): x['link']=os.readlink(p)
    elif stat.S_ISREG(s.st_mode): x['sha256']=digest(p)
    return x

def walk_local(paths):
    out=[]; seen=set()
    mountpoints={m['target'] for m in all_mounts()}
    def visit(p,device):
        if str(p) in seen: return
        seen.add(str(p)); s=p.lstat()
        if stat.S_ISSOCK(s.st_mode): return
        out.append(file_info(p))
        if stat.S_ISDIR(s.st_mode) and s.st_dev==device and str(p) not in mountpoints:
            for child in sorted(p.iterdir()): visit(child,device)
    for raw in paths:
        p=Path(raw)
        if p.exists() or p.is_symlink(): visit(p,p.lstat().st_dev)
    return out

def all_mounts():
    data=json.loads(run(['findmnt','-J','-l','-o','TARGET,SOURCE,FSTYPE']))
    return data.get('filesystems',[])

def schema_digest(db):
    dump=run(['runuser','-u','postgres','--','pg_dump','--host=/var/run/postgresql','--username=postgres','--no-password','--schema-only','--no-owner','--no-privileges','--dbname='+db])
    dump='\n'.join(line for line in dump.splitlines() if not line.startswith(('\\restrict ','\\unrestrict ')))
    return hashlib.sha256(dump.encode()).hexdigest()

def tables(db):
    result={}
    for r in rows(db,"SELECT schemaname,tablename FROM pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY 1,2"):
        schema,table=r['schemaname'],r['tablename']
        key=table if schema=='public' else schema+'.'+table
        relation=qi(schema)+'.'+qi(table)
        # Canonical row JSON hashes cover all data, including opaque auth tables, without exposing content.
        count=int(pg(db,'SELECT count(*) FROM '+relation+';'))
        fingerprint=pg(db,"SELECT md5(coalesce(string_agg(h, '' ORDER BY h),'')) FROM (SELECT md5(row_to_json(t)::text) h FROM "+relation+" t) q;")
        result[key]={'count':count,'identity_md5':fingerprint}
    return result

def inspect(manifest):
    if manifest.exists() or manifest.is_symlink(): raise Abort('Manifest already exists; use a fresh private path')
    out={'schema_version':1,'source_host':socket.gethostname(),'collected_at':datetime.datetime.now(datetime.timezone.utc).isoformat()}
    out['system_schema_version']=1
    out['machine_id_sha256']=digest('/etc/machine-id')
    out['system_units']=unit_inventory()
    out['accounts']=run(['getent','passwd',*['pxe-'+c for c in ACCOUNTS]]).strip()
    out['groups']=run(['getent','group',*['pxe-'+c for c in ACCOUNTS]]).strip()
    out['roles']=rows('postgres',"SELECT oid,rolname,rolcanlogin,CASE WHEN rolpassword IS NULL THEN 'none' WHEN rolpassword LIKE 'md5%' THEN 'md5' WHEN rolpassword LIKE 'SCRAM-SHA-256$%' THEN 'scram' ELSE 'unsupported' END password_format FROM pg_authid WHERE rolname LIKE 'pxe-%' OR rolname LIKE 'omb-%' ORDER BY rolname")
    out['databases']=rows('postgres',"SELECT d.oid,d.datname,r.rolname owner FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname IN ('pxe_boot','pxe_control','pxe_images','omb_boot','omb_control','omb_images') ORDER BY d.datname")
    out['pg_auth']=rows('postgres',"SELECT line_number,type,database,user_name,address,auth_method,error FROM pg_hba_file_rules ORDER BY line_number")
    out['pg_ident']=rows('postgres',"SELECT map_number,line_number,map_name,sys_name,pg_username,error FROM pg_ident_file_mappings ORDER BY line_number")
    out['schema_fingerprints']={db:schema_digest(db) for db in DBS}
    out['table_fingerprints']={db:tables(db) for db in DBS}
    out['table_counts']={db:{k:v['count'] for k,v in ts.items()} for db,ts in out['table_fingerprints'].items()}
    queries={
      'storages':('pxe_images','SELECT id,name,kind,root,storage_key,server,share,subpath,state FROM storages ORDER BY id'),
      'windows_profiles':('pxe_control','SELECT id,storage_id,path,storage_root,size_bytes,modified_at,state FROM windows_profiles ORDER BY id'),
      'windows_answers':('pxe_control','SELECT id,profile_id,sha256,size_bytes,state FROM windows_answer_variants ORDER BY id'),
      'driver_configs':('pxe_control','SELECT id,profile_id,answer_id,state,wim_key,wim_sha256,script_sha256 FROM winpe_driver_configs ORDER BY id'),
      'menu_versions':('pxe_control','SELECT id,active,sha256,octet_length(script) bytes,created_at FROM menu_versions ORDER BY id'),
      'menu_draft_items':('pxe_control','SELECT position,installation_id,folder,title FROM menu_draft_items ORDER BY position')}
    for k,(db,sql) in queries.items(): out[k]=rows(db,sql)
    out['menu_integrity']=[{'id':r['id'],'stored_sha256':r['sha256'],'actual_sha256':hashlib.sha256(r['script'].encode()).hexdigest()} for r in rows('pxe_control','SELECT id,sha256,script FROM menu_versions ORDER BY id')]
    out['profile_configs']=[json.loads(p.read_text()) for p in sorted(Path('/etc/pxe-platform/winiso').glob('*.json'))]
    out['recovery']=json.loads(Path('/etc/pxe-platform/storage-recovery.json').read_text())
    out['boot_snapshot']=file_info('/var/lib/pxe-platform-boot/menu.ipxe')
    out['mounts']=[m for m in all_mounts() if m['target'].startswith('/mnt/pxe-platform-')]
    paths=source_paths(out)
    out['files']=walk_local(paths)
    out['env_keys']={str(p):[line.split('=',1)[0].strip() for line in p.read_text().splitlines() if re.match(r'\s*PXE_[A-Z0-9_]+=',line)] for p in Path('/etc/pxe-platform').glob('*.env')}
    out['network']={'state':json.loads(Path('/var/lib/pxe-platform-agent/network.json').read_text())}
    out['samba_accounts']=run(['pdbedit','-L']).splitlines() # No hashes (-w is deliberately forbidden).
    out['dependency_scan']=scan_dependencies()
    atomic_json(manifest,out)

def source_paths(m):
    paths=[p for p in PATH_MAP if not p.startswith('/run/')]
    paths+=['/usr/local/bin/pxe-'+c for c in COMPONENTS]
    paths+=['/etc/samba/smb.conf','/var/lib/samba/private/passdb.tdb','/etc/passwd','/etc/group','/etc/shadow','/etc/gshadow']
    for u in m['system_units']:
        paths += [f['path'] for f in u['files']]
    return sorted(set(p for p in paths if Path(p).exists() or Path(p).is_symlink()))

def scan_dependencies():
    result=[]
    for raw in ('/etc/crontab','/etc/fstab','/etc/rc.local','/etc/sudoers','/etc/nftables.conf','/etc/iptables/rules.v4','/etc/iptables/rules.v6'):
        p=Path(raw)
        if p.is_file() and b'pxe' in p.read_bytes(): result.append(raw)
    roots=('/etc/cron.d','/etc/cron.daily','/etc/sudoers.d','/etc/logrotate.d','/etc/udev/rules.d','/etc/tmpfiles.d','/etc/systemd/system')
    for root in roots:
        if Path(root).exists():
            for p in Path(root).rglob('*'):
                if p.is_file() and not p.is_symlink() and b'pxe' in p.read_bytes():
                    if root=='/etc/systemd/system' and ('.bak-' in p.name or owned_unit(p.name) or owned_unit(p.parent.name.removesuffix('.d'))): continue
                    result.append(str(p))
    return result

def load_manifest(path):
    private_file(path); m=json.loads(path.read_text())
    if any(k not in m for k in REQUIRED) or m['schema_version']!=1 or m.get('system_schema_version')!=1:
        raise Abort('Fresh enriched inspect manifest required (system_schema_version=1)')
    if m['source_host']!=socket.gethostname() or m['machine_id_sha256']!=digest('/etc/machine-id'):
        raise Abort('Manifest belongs to another host')
    return m

def bind_state(state,manifest):
    value=digest(manifest)
    if state.value.get('manifest_sha256',value)!=value: raise Abort('Manifest digest differs from bound state')
    state.update(manifest_sha256=value)

def assert_source_quiet(m):
    units=[u['Id'] for u in m['system_units'] if '@.' not in u['Id']]
    assert_stopped(run,units)
    if any(x['target'].startswith(tuple(p for p in PATH_MAP if p.startswith('/mnt/'))) for x in all_mounts()):
        raise Abort('Source product mounts are still mounted')
    # Samba JSON is version-dependent: unknown shape fails closed.
    status=json.loads(run(['smbstatus','--json']))
    if not isinstance(status.get('tcons'),dict): raise Abort('Cannot verify Samba installshare client state')
    if any(t.get('service','').lower() in ('pxeisos','ombisos') for t in status['tcons'].values()):
        raise Abort('Active installation-share clients must disconnect')
    sessions=rows('postgres',"SELECT datname,pid FROM pg_stat_activity WHERE datname IN ('pxe_boot','pxe_control','pxe_images','omb_boot','omb_control','omb_images') AND pid<>pg_backend_pid()")
    if sessions: raise Abort('Product PostgreSQL sessions remain; do not terminate clients automatically')

OPERATIONAL_TABLES={
    'pxe_boot':{'boot_events'},
    'pxe_control':{'audit_events','machines','sessions','mail_outbox','mail_tokens','oidc_flows','oidc_logout_jti','recovery_rate_limits'},
    'pxe_images':set(),
}

def refresh_operational_manifest(manifest,m,state):
    """Freeze post-quiesce journals without losing pre-stop service activity."""
    if state.value.get('manifest_sha256') or state.value.get('backup_started'):
        return
    assert_source_quiet(m)
    if {db:schema_digest(db) for db in DBS}!=m['schema_fingerprints']:
        raise Abort('Database schema changed since inspect')
    fresh={db:tables(db) for db in DBS}
    for db,current in fresh.items():
        original=m['table_fingerprints'][db]
        if set(current)!=set(original): raise Abort('Database table inventory changed since inspect')
        for table,observation in current.items():
            if observation!=original[table] and table not in OPERATIONAL_TABLES[db]:
                raise Abort('Database immutable object changed since inspect: '+db+'.'+table)
    if digest(m['boot_snapshot']['path'])!=m['boot_snapshot']['sha256']:
        raise Abort('Boot snapshot changed since inspect')
    m['table_fingerprints']=fresh
    m['table_counts']={db:{name:v['count'] for name,v in ts.items()} for db,ts in fresh.items()}
    m['quiesced_at']=datetime.datetime.now(datetime.timezone.utc).isoformat()
    atomic_json(manifest,m)

def backup(manifest,m,state,backup_dir):
    if state.value.get('backup_complete'): verify_artifacts(manifest,m,state); return
    if state.value.get('backup_started'): raise Abort('Partial backup: use new manifest and new directory; source is unchanged')
    assert_source_quiet(m)
    if m['dependency_scan']: raise Abort('Unmapped custom dependencies: '+', '.join(m['dependency_scan']))
    if backup_dir.exists(): raise Abort('Backup directory must be new')
    backup_dir.mkdir(mode=0o700); private_directory(backup_dir)
    state.update(backup_started=True,backup_dir=str(backup_dir.resolve()))
    files=walk_local(source_paths(m))
    # Explicit --files-from list avoids descending into NAS/ISO and excludes Unix sockets.
    names=[f['path'].lstrip('/') for f in files]
    with tempfile.TemporaryFile() as listing:
        listing.write(b'\0'.join(n.encode() for n in names)+b'\0'); listing.seek(0)
        p=subprocess.run(['tar','--create','--file',str(backup_dir/'files.tar'),'--directory','/','--numeric-owner','--acls','--xattrs','--xattrs-include=*','--one-file-system','--no-recursion','--null','--files-from','-'],stdin=listing,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        if p.returncode:
            log_failure(['tar','backup'],p.stderr)
            raise Abort('File archive failed; backup remains incomplete')
    (backup_dir/'files.tar').chmod(0o600)
    atomic_json(backup_dir/'files.json',files)
    atomic_json(backup_dir/'manifest.json',m)
    for db in DBS:
        p=backup_dir/(db+'.dump')
        fd=os.open(p,os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
        with os.fdopen(fd,'wb') as f: run(['runuser','-u','postgres','--','pg_dump','--host=/var/run/postgresql','--username=postgres','--no-password','--format=custom','--dbname='+db],output=f)
    fd=os.open(backup_dir/'globals.sql',os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
    with os.fdopen(fd,'wb') as f: run(['runuser','-u','postgres','--','pg_dumpall','--host=/var/run/postgresql','--username=postgres','--no-password','--globals-only'],output=f)
    fd=os.open(backup_dir/'journals.export',os.O_CREAT|os.O_EXCL|os.O_WRONLY,0o600)
    with os.fdopen(fd,'wb') as f:
        run(['journalctl','--no-pager','--output=export',*['--unit='+u['Id'] for u in m['system_units']]],output=f)
    fingerprints={db:tables(db) for db in DBS}
    if {db:schema_digest(db) for db in DBS}!=m['schema_fingerprints']: raise Abort('Database schema changed since inspect')
    if fingerprints!=m['table_fingerprints']: raise Abort('Database changed since inspect; collect new manifest')
    if digest(m['boot_snapshot']['path'])!=m['boot_snapshot']['sha256']: raise Abort('Boot snapshot changed since inspect')
    assert_source_quiet(m)
    artifacts={p.name:digest(p) for p in backup_dir.iterdir() if p.is_file()}
    atomic_json(backup_dir/'checksums.json',artifacts)
    state.update(backup_complete=True,backup_artifacts=artifacts,backup_source_stop_verified=True)

def verify_artifacts(manifest,m,state):
    if not state.value.get('backup_complete') or not state.value.get('backup_source_stop_verified'): raise Abort('No complete stopped-source backup')
    directory=private_directory(state.value['backup_dir'])
    expected=state.value.get('backup_artifacts',{})
    required={'files.tar','files.json','manifest.json','globals.sql','journals.export',*(db+'.dump' for db in DBS)}
    if set(expected)!=required: raise Abort('Incomplete backup artifact inventory')
    for name,sha in expected.items():
        p=private_file(directory/name)
        if digest(p)!=sha: raise Abort('Backup checksum mismatch: '+name)
    if json.loads((directory/'manifest.json').read_text())!=m: raise Abort('Backup manifest mismatch')
    validate_archive(directory/'files.tar')
    return directory

def verify_backup(manifest,m,state):
    directory=verify_artifacts(manifest,m,state)
    if state.value.get('backup_verified'): return
    if state.value.get('verification_pending_db'): raise Abort('Previous rehearsal DB remains: '+state.value['verification_pending_db']+'; inspect and remove ONLY this recorded temporary DB before retry')
    with tempfile.TemporaryDirectory(prefix='omb-migration-restore-') as root:
        os.chmod(root,0o700)
        run(['tar','--extract','--file',str(directory/'files.tar'),'--directory',root,'--numeric-owner','--same-owner','--acls','--xattrs','--xattrs-include=*'])
        verify_files(root,json.loads((directory/'files.json').read_text()))
    # pg_restore reads dump via root-open stdin; no directory permission weakening for postgres.
    for db in DBS:
        temporary='_omb_migration_check_'+uuid.uuid4().hex
        state.update(verification_pending_db=temporary)
        pg('postgres','CREATE DATABASE '+qi(temporary)+' TEMPLATE template0;')
        try:
            with open(directory/(db+'.dump'),'rb') as dump:
                p=subprocess.run(['runuser','-u','postgres','--','pg_restore','--host=/var/run/postgresql','--username=postgres','--no-password','--exit-on-error','--dbname='+temporary],stdin=dump,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
            if p.returncode:
                log_failure(['pg_restore',temporary],p.stderr)
                raise Abort('Restore rehearsal failed; temporary DB retained: '+temporary)
            if schema_digest(temporary)!=m['schema_fingerprints'][db]: raise Abort('Restored schema differs: '+temporary)
            if tables(temporary)!=m['table_fingerprints'][db]: raise Abort('Restored schema/table identity mismatch: '+temporary)
            pg('postgres','DROP DATABASE '+qi(temporary)+';')
            state.update(verification_pending_db=None)
        except BaseException:
            # Do not mask restore failures or broadly remove databases.
            raise
    state.update(backup_verified=True,backup_verification={'files':True,'database_restores':list(DBS),'verified_at':datetime.datetime.now(datetime.timezone.utc).isoformat()})

def verify_files(root,files):
    for expected in files:
        p=Path(root)/expected['path'].lstrip('/')
        actual=file_info(p)
        for k in ('uid','gid','mode','sha256','link','xattrs'):
            if k in expected and actual.get(k)!=expected[k]: raise Abort('Restored file metadata/hash differs: '+expected['path'])
        if 'sha256' in expected and actual['size']!=expected['size']: raise Abort('Restored file size differs')

def transform_samba(text):
    parts=re.split(r'(?m)^(\s*\[[^]\n]+\]\s*)$',text)
    found=False
    for i in range(1,len(parts),2):
        section=parts[i].strip().lower()
        if section=='[ombisos]': raise Abort('Target Samba section already exists')
        if section!='[pxeisos]': continue
        if found: raise Abort('Duplicate source Samba section')
        found=True; body=parts[i+1]
        paths=re.findall(r'(?im)^\s*path\s*=\s*(.*?)\s*$',body)
        if paths!=['/mnt/pxe-platform-iso/profiles']: raise Abort('Unexpected source installation-share path')
        # Own account references only inside the known product share.
        parts[i]=parts[i].replace('pxeisos','ombisos')
        body=transform_paths(body)
        body=re.sub(r'(?<![\w-])pxe-iso(?![\w-])','omb-iso',body)
        parts[i+1]=body
    if not found: raise Abort('Missing source Samba installation share')
    return ''.join(parts)

def bundle_inventory(root):
    root=Path(root).resolve()
    names=['bin/omb-'+c for c in (*COMPONENTS,'migrate-data')]
    names+=['deploy/systemd/omb-'+s+'.service' for s in (*SERVICE_NAMES,'winiso@')]
    names+=['deploy/boot-assets/windows-profiles/omb-setup.cmd.in','deploy/boot-assets/windows-profiles/winpeshl.ini','deploy/samba/smb.conf']
    result={}
    for name in names:
        p=root/name
        if not p.exists() or p.is_symlink() or not p.is_file() or p.resolve().is_relative_to(root) is False:
            raise Abort('Missing/unsafe bundle artifact: '+name)
        result[name]=digest(p)
    return result

def write_preserving(path,data,mode=None):
    p=Path(path); old=p.stat() if p.exists() else None
    if p.is_symlink(): raise Abort('Refuse to overwrite symlink: '+str(p))
    p.parent.mkdir(parents=True,exist_ok=True)
    fd,tmp=tempfile.mkstemp(prefix='.omb-migrate-',dir=p.parent)
    try:
        with os.fdopen(fd,'wb') as f:
            f.write(data); f.flush(); os.fsync(f.fileno())
            if old:
                os.fchown(f.fileno(),old.st_uid,old.st_gid)
                if hasattr(os,'listxattr'):
                    for key in os.listxattr(p): os.setxattr(f.fileno(),key,os.getxattr(p,key))
            os.fchmod(f.fileno(),mode if mode is not None else stat.S_IMODE(old.st_mode) if old else 0o600)
        os.replace(tmp,p)
    finally:
        if os.path.exists(tmp): os.unlink(tmp)

def mapped_unit_name(name):
    if name.endswith('.mount'):
        raise Abort('Mount names must be derived from Where=, not textual substitution')
    return 'omb-'+name[4:]

def unit_target(u):
    name=u['Id']
    if name.endswith('.mount'):
        where=[u['Where']] if u.get('Where') else []
        if not where and u.get('FragmentPath'):
            content=Path(u['FragmentPath']).read_text()
            where=re.findall(r'(?m)^Where=(.*)$',content)
        if len(where)!=1 or not where[0].startswith('/mnt/pxe-platform-'): raise Abort('Unsupported product mount Where')
        return mount_unit(transform_paths(where[0]))
    return mapped_unit_name(name)

def preflight(m,bundle):
    validate_roles(m['roles'])
    if any(r.get('password_format') not in ('none','scram') for r in m['roles']): raise Abort('Unsupported PG role password format')
    if any(r['rolname'].startswith('omb-') for r in m['roles']): raise Abort('Target PostgreSQL role exists')
    if {r['rolname'] for r in m['roles']} != {'pxe-'+c for c in ('boot','control','images')}: raise Abort('Unexpected source PostgreSQL roles')
    if {r['datname'] for r in m['databases']}!=set(DBS): raise Abort('Target database exists or source database missing')
    if m.get('dependency_scan') or scan_dependencies(): raise Abort('Unmapped host dependencies need an explicit reviewed implementation')
    for rule in m['pg_auth']:
        if rule.get('error'): raise Abort('Invalid PostgreSQL authentication rule')
        # Explicit old names require pg_hba/pg_ident editing, deliberately not guessed.
        if any('pxe' in str(v) for k,v in rule.items() if k in ('database','user_name')):
            raise Abort('Explicit source names in pg_hba require reviewed authentication mapping')
    if any('pxe' in str(v) for r in m['pg_ident'] for k,v in r.items() if k in ('sys_name','pg_username')):
        raise Abort('Explicit source identity map requires reviewed authentication mapping')
    if not any(r.get('type')=='local' and r.get('auth_method')=='peer' and 'all' in (r.get('database') or []) and 'all' in (r.get('user_name') or []) for r in m['pg_auth']):
        raise Abort('Expected local all/all peer authentication is absent')
    for old,new in PATH_MAP.items():
        if old.startswith('/run/'): continue
        if Path(new).exists() or Path(new).is_symlink(): raise Abort('Target path conflict: '+new)
        if Path(old).is_symlink(): raise Abort('Owned directory is a symlink: '+old)
    for field,database in (('accounts','passwd'),('groups','group')):
        parsed=[line.split(':') for line in m[field].splitlines()]
        if {x[0] for x in parsed}!={'pxe-'+c for c in ACCOUNTS} or len(parsed)!=4: raise Abort('Incomplete source account/group inventory')
        for fields in parsed:
            if run(['getent',database,fields[0]]).strip()!=':'.join(fields): raise Abort('Source account/group changed since inspect')
    current_roles=rows('postgres',"SELECT oid,rolname FROM pg_roles WHERE rolname IN ('pxe-boot','pxe-control','pxe-images')")
    if {(int(r['oid']),r['rolname']) for r in current_roles}!={(int(r['oid']),r['rolname']) for r in m['roles']}: raise Abort('Source PG roles changed')
    for c in ACCOUNTS:
        if run(['getent','passwd','omb-'+c],allowed=(0,2)).strip() or run(['getent','group','omb-'+c],allowed=(0,2)).strip(): raise Abort('Target account/group exists')
    for c in (*COMPONENTS,'migrate-data'):
        if Path('/usr/local/bin/omb-'+c).exists() or Path('/usr/local/bin/omb-'+c).is_symlink(): raise Abort('Target executable exists')
    for u in m['system_units']:
        target=unit_target(u)
        if '@.service' in target:
            if any((Path(base)/target).exists() for base in ('/etc/systemd/system','/run/systemd/system','/usr/lib/systemd/system','/lib/systemd/system')): raise Abort('Target unit already exists: '+target)
        elif run(['systemctl','show',target,'--property=LoadState','--value']).strip()!='not-found': raise Abort('Target unit already exists: '+target)
        for f in u['files']:
            if file_info(f['path'])!=f: raise Abort('Source unit metadata changed since inspect: '+f['path'])
    # Validate transformations and JSON without touching the source.
    transform_samba(Path('/etc/samba/smb.conf').read_text())
    password=private_file('/etc/pxe-platform/pxe-iso-password').read_text().rstrip('\r\n')
    if not password or '\n' in password: raise Abort('Invalid private source Samba password')
    samba_names={line.split(':',1)[0] for line in run(['pdbedit','-L']).splitlines()}
    if 'pxe-iso' not in samba_names or 'omb-iso' in samba_names: raise Abort('Samba account conflict')
    for p in Path('/etc/pxe-platform').glob('*.env'): transform_env(p.read_text())
    # Main source unit content and local restrictions remain authoritative; bundle units are validation inputs.
    bundle_inventory(bundle)
    assert_source_quiet(m)

def mutate(state,kind,**fields):
    record={'kind':kind,'status':'intent',**fields}
    journal=state.value.get('system_journal',[])+[record]
    state.update(system_journal=journal)
    execute_mutation(record)
    record['status']='done'; state.update(system_journal=journal)

def execute_mutation(r):
    kind=r['kind']
    if kind=='rename_path': os.rename(r['old'],r['new'])
    elif kind=='rename_group': run(['groupmod','--new-name',r['new'],r['old']])
    elif kind=='rename_user':
        run(['usermod','--login',r['new'],'--home',r['new_home'],r['old']])
    elif kind=='rename_role': pg('postgres','ALTER ROLE '+qi(r['old'])+' RENAME TO '+qi(r['new'])+';')
    elif kind=='rename_db': pg('postgres','ALTER DATABASE '+qi(r['old'])+' RENAME TO '+qi(r['new'])+';')
    elif kind=='write':
        data=Path(r['payload']).read_bytes()
        if hashlib.sha256(data).hexdigest()!=r['sha256']: raise Abort('Protected operation payload hash mismatch')
        write_preserving(r['path'],data,r['mode'])
    elif kind=='samba_add':
        password=Path(r['password_file']).read_text().rstrip('\r\n')
        if '\n' in password or not password: raise Abort('Invalid private Samba password file')
        run(['smbpasswd','-s','-a',r['account']],input=password+'\n'+password+'\n')
    elif kind=='samba_delete': run(['smbpasswd','-x',r['account']])
    elif kind=='unit_disable': run(['systemctl','disable',r['unit']])
    elif kind=='unit_enable': run(['systemctl','enable',r['unit']])
    elif kind=='unlink': Path(r['path']).unlink()
    else: raise Abort('Unknown internal operation; manual recovery required')

def journal_write(state,path,data,mode=None):
    directory=private_directory(state.value['backup_dir'])/'operations'
    directory.mkdir(mode=0o700,exist_ok=True); private_directory(directory)
    n=len(state.value.get('system_journal',[])); payload=directory/(str(n)+'.new')
    payload.write_bytes(data); payload.chmod(0o600)
    p=Path(path); preimage=None
    if p.exists():
        if not p.is_file() or p.is_symlink(): raise Abort('Unsafe target file type: '+str(p))
        old=directory/(str(n)+'.old'); shutil.copyfile(p,old); old.chmod(0o600)
        s=p.stat(); preimage={'path':str(old),'sha256':digest(old),'uid':s.st_uid,'gid':s.st_gid,'mode':stat.S_IMODE(s.st_mode)}
    mutate(state,'write',path=str(p),payload=str(payload),sha256=digest(payload),mode=mode,preimage=preimage)

def apply_system(manifest,m,state,bundle):
    require_forward_state(state.value); directory=verify_artifacts(manifest,m,state)
    artifacts=bundle_inventory(bundle)
    if state.value.get('bundle_artifacts',artifacts)!=artifacts: raise Abort('Bundle differs from bound migration bundle')
    if state.value.get('system_complete'): verify_system(manifest,m,state); return
    preflight(m,bundle)
    state.update(bundle_dir=str(bundle.resolve()),bundle_artifacts=artifacts)
    for line in m['groups'].splitlines():
        fields=line.split(':'); old=fields[0]
        if old not in ['pxe-'+c for c in ACCOUNTS]: raise Abort('Unexpected account group')
        mutate(state,'rename_group',old=old,new='omb-'+old[4:],gid=int(fields[2]))
    for line in m['accounts'].splitlines():
        f=line.split(':'); old=f[0]
        if old not in ['pxe-'+c for c in ACCOUNTS]: raise Abort('Unexpected account')
        mutate(state,'rename_user',old=old,new='omb-'+old[4:],uid=int(f[2]),old_home=f[5],new_home=transform_paths(f[5]))
    for role in m['roles']: mutate(state,'rename_role',old=role['rolname'],new='omb-'+role['rolname'][4:],oid=int(role['oid']))
    for db in m['databases']: mutate(state,'rename_db',old=db['datname'],new='omb_'+db['datname'][4:],oid=int(db['oid']))
    for old,new in PATH_MAP.items():
        if old.startswith('/run/') or not Path(old).exists(): continue
        s=Path(old).stat(); mutate(state,'rename_path',old=old,new=new,device=s.st_dev,inode=s.st_ino)
    for p in Path('/etc/omb').glob('*.env'): journal_write(state,p,transform_env(p.read_text()).encode())
    # Exact runtime configuration documents only; opaque credentials, TLS, keys and auth-migrations stay untouched.
    json_paths=list(Path('/etc/omb').glob('network*.json'))+[Path('/var/lib/omb-agent/network.json')]
    for p in json_paths:
        if p.exists():
            obj=json.loads(p.read_text())
            if 'tftp_root' in obj: obj['tftp_root']=transform_paths(obj['tftp_root'])
            journal_write(state,p,(json.dumps(obj,ensure_ascii=False,indent=2)+'\n').encode())
    recovery=Path('/etc/omb/storage-recovery.json'); obj=json.loads(recovery.read_text()); obj['enabled']=False
    journal_write(state,recovery,(json.dumps(obj,indent=2)+'\n').encode())
    dnsmasq=Path('/etc/omb/dnsmasq.conf')
    lines=[]
    for line in dnsmasq.read_text().splitlines(keepends=True):
        lines.append(transform_paths(line) if re.match(r'\s*(tftp-root|conf-file|conf-dir|dhcp-hostsfile|dhcp-optsfile|log-facility|pid-file)=',line) else line)
    journal_write(state,dnsmasq,''.join(lines).encode())
    password=Path('/etc/omb/pxe-iso-password')
    if password.exists():
        mutate(state,'rename_path',old=str(password),new='/etc/omb/omb-iso-password',device=password.stat().st_dev,inode=password.stat().st_ino)
    else: raise Abort('Private source Samba password file missing; rollback required')
    for c in (*COMPONENTS,'migrate-data'):
        journal_write(state,Path('/usr/local/bin/omb-'+c),(bundle/('bin/omb-'+c)).read_bytes(),0o755)
    for name in ('omb-setup.cmd.in','winpeshl.ini'):
        journal_write(state,Path('/usr/share/omb/windows-profiles')/name,(bundle/'deploy/boot-assets/windows-profiles'/name).read_bytes(),0o644)
    old_template=Path('/usr/share/omb/windows-profiles/pxe-setup.cmd.in')
    if old_template.exists(): mutate(state,'unlink',path=str(old_template))
    new_units=[]
    for u in m['system_units']:
        # Instances share the template; list generated units once below.
        if not u['FragmentPath']: continue
        target=unit_target(u); new_units.append({'old':u['Id'],'new':target,'active':u['ActiveState'],'enabled':u.get('UnitFileState','')})
        for f in u['files']:
            source=Path(f['path'])
            if source==Path(u['FragmentPath']): dest=Path('/etc/systemd/system')/(mapped_unit_name(source.name) if source.name.startswith('pxe-') else target)
            else:
                # Explicitly retain each source local dropin, including nonstandard basename.
                parent=source.parent.name
                if not parent.endswith('.d'): raise Abort('Unsupported dropin path')
                dest=Path('/etc/systemd/system')/(mapped_unit_name(parent[:-2])+'.d' if parent.startswith('pxe-') else target+'.d')/source.name
            content=transform_unit(source.read_text())
            if source.suffix=='.mount':
                content=re.sub(r'(?m)^Where=(.*)$',lambda x:'Where='+transform_paths(x[1]),content)
            journal_write(state,dest,content.encode(),int(f['mode'],8))
        if u.get('UnitFileState')=='enabled': mutate(state,'unit_disable',unit=u['Id'])
    state.update(mapped_units=new_units)
    for source in sorted({f['path'] for u in m['system_units'] for f in u['files']} | {'/usr/local/bin/pxe-'+c for c in COMPONENTS}):
        if Path(source).is_file() and not Path(source).is_symlink(): mutate(state,'unlink',path=source)
    samba=Path('/etc/samba/smb.conf'); journal_write(state,samba,transform_samba(samba.read_text()).encode())
    mutate(state,'samba_add',account='omb-iso',password_file='/etc/omb/omb-iso-password')
    mutate(state,'samba_delete',account='pxe-iso')
    run(['testparm','-s','--suppress-prompt']); run(['dnsmasq','--test','--conf-file=/etc/omb/dnsmasq.conf'])
    run(['systemctl','daemon-reload']); run(['systemctl','reload','smbd'])
    # New units remain stopped and disabled until data rebuild + acceptance; DHCP is never started here.
    state.update(system_complete=True,recovery_original=m['recovery'],system_verified=False)
    verify_system(manifest,m,state)

def verify_system(manifest,m,state):
    verify_artifacts(manifest,m,state)
    if not state.value.get('system_complete'): raise Abort('System migration is incomplete; rollback required')
    for r in state.value['system_journal']:
        if r['status']!='done': raise Abort('Incomplete system operation; rollback required')
        if r['kind']=='rename_user':
            f=run(['getent','passwd',r['new']]).strip().split(':')
            if int(f[2])!=r['uid']: raise Abort('User numeric identity changed')
        elif r['kind']=='rename_group':
            f=run(['getent','group',r['new']]).strip().split(':')
            if int(f[2])!=r['gid']: raise Abort('Group numeric identity changed')
        elif r['kind']=='rename_role':
            if pg('postgres','SELECT oid FROM pg_roles WHERE rolname='+qs(r['new'])+';')!=str(r['oid']): raise Abort('PG role OID changed')
        elif r['kind']=='rename_db':
            if pg('postgres','SELECT oid FROM pg_database WHERE datname='+qs(r['new'])+';')!=str(r['oid']): raise Abort('PG database OID changed')
    if json.loads(Path('/etc/omb/storage-recovery.json').read_text()).get('enabled'): raise Abort('Recovery must remain disabled during migration')
    for name,sha in state.value['bundle_artifacts'].items():
        if name.startswith('bin/') and digest('/usr/local/'+name)!=sha: raise Abort('Installed bundle binary differs')
    units=['omb-'+c+'.service' for c in SERVICE_NAMES]+['pxe-'+c+'.service' for c in SERVICE_NAMES]
    assert_stopped(run,units)
    run(['testparm','-s','--suppress-prompt']); run(['dnsmasq','--test','--conf-file=/etc/omb/dnsmasq.conf'])
    state.update(system_verified=True)

def locate_rename(r):
    old=Path(r['old']); new=Path(r['new'])
    exists=lambda p:p.exists() or p.is_symlink()
    if exists(old)==exists(new): raise Abort('Ambiguous rename recovery; inspect '+str(old)+' and '+str(new))
    side='old' if exists(old) else 'new'; p=old if side=='old' else new
    s=p.lstat()
    if s.st_dev!=r['device'] or s.st_ino!=r['inode']: raise Abort('Rename object identity changed: '+str(p))
    return side

def reverse_identity(r):
    kind=r['kind']
    if kind in ('rename_user','rename_group'):
        database='passwd' if kind=='rename_user' else 'group'; idx=2
        old=run(['getent',database,r['old']],allowed=(0,2)).strip()
        new=run(['getent',database,r['new']],allowed=(0,2)).strip()
        if bool(old)==bool(new): raise Abort('Ambiguous account rollback: '+r['old'])
        if int((new or old).split(':')[idx])!=r['uid' if kind=='rename_user' else 'gid']: raise Abort('Account ID mismatch; manual recovery required')
        if new:
            if kind=='rename_user': run(['usermod','--login',r['old'],'--home',r['old_home'],r['new']])
            else: run(['groupmod','--new-name',r['old'],r['new']])
    else:
        catalog='pg_roles' if kind=='rename_role' else 'pg_database'; name='rolname' if kind=='rename_role' else 'datname'
        old=pg('postgres','SELECT oid FROM '+catalog+' WHERE '+name+'='+qs(r['old'])+';')
        new=pg('postgres','SELECT oid FROM '+catalog+' WHERE '+name+'='+qs(r['new'])+';')
        if bool(old)==bool(new) or (new or old)!=str(r['oid']): raise Abort('Ambiguous PostgreSQL identity rollback: '+r['old'])
        if new: pg('postgres','ALTER '+('ROLE' if kind=='rename_role' else 'DATABASE')+' '+qi(r['new'])+' RENAME TO '+qi(r['old'])+';')

def rollback(manifest,m,state):
    directory=verify_artifacts(manifest,m,state)
    if state.value.get('rollback_complete'): return
    # Stop only product services. DHCP first; unmount deepest paths before CIFS parents.
    all_units={u['Id'] for u in m['system_units'] if '@.' not in u['Id']}
    all_units.update(x['new'] for x in state.value.get('mapped_units',[]) if '@.' not in x['new'])
    all_units.update('omb-'+c+'.service' for c in SERVICE_NAMES)
    order=sorted(all_units,key=lambda u:(0 if 'dnsmasq' in u else 2 if u.endswith('.mount') else 1,-u.count('-')))
    for unit in order:
        if run(['systemctl','show',unit,'--property=LoadState','--value']).strip()!='not-found': run(['systemctl','stop',unit])
    known_mounts={x['target'] for x in m['mounts']}
    known_mounts.update(transform_paths(x['target']) for x in m['mounts'])
    known_mounts.update(x['target'] for x in state.value.get('storage_journal',[]) if x['kind']=='iso')
    for mount in sorted(all_mounts(),key=lambda x:-len(x['target'])):
        if mount['target'] in known_mounts: run(['umount','--',mount['target']])
    assert_source_quiet(m)
    journal=state.value.get('system_journal',[])
    # Reverse each operation by verified identity. Intent entries are reconciled from actual old/new objects.
    for i in range(len(journal)-1,-1,-1):
        r=journal[i]
        if r.get('rollback_status')=='done': continue
        kind=r['kind']
        if kind.startswith('rename_') and kind!='rename_path': reverse_identity(r)
        elif kind=='rename_path':
            quarantine=r.get('quarantine')
            if quarantine and Path(quarantine).exists():
                q=Path(quarantine).stat()
                if Path(r['old']).exists() or Path(r['new']).exists() or (q.st_dev,q.st_ino)!=(r['device'],r['inode']): raise Abort('Ambiguous rollback quarantine')
            elif locate_rename(r)=='new':
                if r['old'] in PATH_MAP:
                    # Quarantine migrated local trees on the SAME filesystem. No recursive deletion of data.
                    quarantine=r['new']+'.omb-rollback-'+state.value['manifest_sha256'][:12]
                    if Path(quarantine).exists(): raise Abort('Rollback quarantine already exists: '+quarantine)
                    r['quarantine']=quarantine; state.update(system_journal=journal)
                    os.rename(r['new'],quarantine)
                else:
                    os.rename(r['new'],r['old'])
        elif kind=='write':
            p=Path(r['path']); prior=r['preimage']
            if prior:
                private_file(prior['path'])
                if digest(prior['path'])!=prior['sha256']: raise Abort('Rollback preimage corrupted')
                write_preserving(p,Path(prior['path']).read_bytes(),prior['mode']); os.chown(p,prior['uid'],prior['gid'])
            elif p.exists():
                if p.is_symlink() or digest(p)!=r['sha256']: raise Abort('Created target changed; preserve/inspect before removing: '+str(p))
                p.unlink()
        elif kind=='samba_add':
            names={line.split(':',1)[0] for line in run(['pdbedit','-L']).splitlines()}
            if r['account'] in names: run(['smbpasswd','-x',r['account']])
        elif kind=='unit_enable': run(['systemctl','disable',r['unit']])
        elif kind in ('unit_disable','unlink','samba_delete'): pass # Original files/states restored below.
        else: raise Abort('Unknown journal operation in rollback; manual recovery required')
        r['rollback_status']='done'; state.update(system_journal=journal)
    if not state.value.get('rollback_files_restored'):
        # Account databases and shared Samba passdb are never overwritten wholesale.
        excluded=('etc/passwd','etc/group','etc/shadow','etc/gshadow','var/lib/samba/private/passdb.tdb')
        argv=['tar','--extract','--file',str(directory/'files.tar'),'--directory','/','--numeric-owner','--same-owner','--acls','--xattrs','--xattrs-include=*']
        argv += ['--exclude='+p for p in excluded]
        run(argv)
        expected=[f for f in json.loads((directory/'files.json').read_text()) if f['path'].lstrip('/') not in excluded]
        verify_files('/',expected)
        state.update(rollback_files_restored=True)
    if any(r['kind']=='samba_delete' for r in journal):
        names={line.split(':',1)[0] for line in run(['pdbedit','-L']).splitlines()}
        if 'pxe-iso' not in names:
            password=private_file('/etc/pxe-platform/pxe-iso-password').read_text().rstrip('\r\n')
            run(['smbpasswd','-s','-a','pxe-iso'],input=password+'\n'+password+'\n')
    # pg_restore --clean only drops objects present in the saved archive; unknown extras refuse completion.
    for db in DBS:
        key='rollback_restored_'+db
        if state.value.get(key): continue
        with open(directory/(db+'.dump'),'rb') as dump:
            p=subprocess.run(['runuser','-u','postgres','--','pg_restore','--host=/var/run/postgresql','--username=postgres','--no-password','--exit-on-error','--clean','--if-exists','--dbname='+db],stdin=dump,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        if p.returncode:
            log_failure(['pg_restore',db],p.stderr)
            raise Abort('Original DB restore failed: '+db+'; keep source writers stopped and rerun rollback after resolving the reported local PostgreSQL error')
        if schema_digest(db)!=m['schema_fingerprints'][db]: raise Abort('Original DB schema differs after restore: '+db)
        if tables(db)!=m['table_fingerprints'][db]: raise Abort('Original DB differs after restore: '+db+'; writers remain stopped')
        state.update(**{key:True})
    if digest(m['boot_snapshot']['path'])!=m['boot_snapshot']['sha256']: raise Abort('Original Boot snapshot hash mismatch; DHCP remains stopped')
    run(['testparm','-s','--suppress-prompt']); run(['dnsmasq','--test','--conf-file=/etc/pxe-platform/dnsmasq.conf'])
    run(['systemctl','daemon-reload']); run(['systemctl','reload','smbd'])
    for u in m['system_units']:
        if u.get('UnitFileState')=='enabled': run(['systemctl','enable',u['Id']])
    # Prior mounts and profile instances before app writers, old DHCP strictly LAST.
    units=[u for u in m['system_units'] if u.get('ActiveState')=='active' and '@.' not in u['Id'] and (u.get('FragmentPath') or not u['Id'].endswith('.mount'))]
    def start_order(u):
        n=u['Id']
        return (5 if 'dnsmasq' in n else 0 if n.endswith('.mount') and 'smb' in n else 1 if n.endswith('.mount') else 2 if 'winiso@' in n else 3 if 'boot' in n or 'images' in n else 4,n)
    for u in sorted(units,key=start_order): run(['systemctl','start',u['Id']])
    state.update(rollback_complete=True,system_complete=False,data_complete=False)

def reconnect_storage(manifest,m,state):
    require_forward_state(state.value); verify_system(manifest,m,state)
    journal=state.value.get('storage_journal',[])
    if journal:
        if state.value.get('storage_complete'): return
        raise Abort('Partial storage reconnect: rollback before continuing')
    for u in state.value['mapped_units']:
        if u['new'].endswith('.mount') and 'smb' in u['new']:
            record={'kind':'smb','unit':u['new'],'status':'intent'}; journal.append(record); state.update(storage_journal=journal)
            run(['systemctl','start',u['new']]); record['status']='done'; state.update(storage_journal=journal)
    for p in m['windows_profiles']:
        if p['state']!='ready': continue
        root=transform_paths(p['storage_root']); relative=PurePosixPath(p['path'])
        if relative.is_absolute() or '..' in relative.parts: raise Abort('Invalid ISO relative path')
        source=Path(root)/str(relative); destination=Path('/mnt/omb-iso/profiles')/str(p['id'])
        if source.stat().st_size!=p['size_bytes']: raise Abort('Source ISO size changed')
        if any(x['target']==str(destination) for x in all_mounts()): raise Abort('Target ISO mount conflict')
        destination.mkdir(parents=True,exist_ok=True)
        record={'kind':'iso','source':str(source),'target':str(destination),'status':'intent'}; journal.append(record); state.update(storage_journal=journal)
        # Match the application's ISO mounter: autodetect the original
        # filesystem and keep its non-executable read-only restrictions.
        run(['mount','-o','loop,ro,nosuid,nodev,noexec','--',str(source),str(destination)])
        record['status']='done'; state.update(storage_journal=journal)
    state.update(storage_complete=True)

def activate_system(manifest,m,state):
    if state.value.get('activated'): return
    if not state.value.get('data_complete') or not state.value.get('system_complete') or not state.value.get('storage_complete'):
        raise Abort('System, read-only storage and data migration must complete before activation')
    if state.value.get('activation_started'): raise Abort('Partial activation: rollback required')
    verify_system(manifest,m,state)
    state.update(activation_started=True)
    recovery=Path('/etc/omb/storage-recovery.json'); recovery_obj=json.loads(recovery.read_text()); recovery_obj['enabled']=m['recovery']['enabled']
    journal_write(state,recovery,(json.dumps(recovery_obj,indent=2)+'\n').encode())
    for u in state.value['mapped_units']:
        if u['enabled']=='enabled': mutate(state,'unit_enable',unit=u['new'])
    # Profile content was rebuilt by data migration. Keep pure read-only mounts; do not run obsolete builders.
    active={u['old'] for u in state.value['mapped_units'] if u['active']=='active'}
    for c in ('boot','images','agent','control','dnsmasq'):
        if 'pxe-'+c+'.service' in active: run(['systemctl','start','omb-'+c+'.service'])
    state.update(activated=True)

def main(argv=None):
    global ERROR_LOG
    parser=argparse.ArgumentParser(description=__doc__)
    sub=parser.add_subparsers(dest='command',required=True)
    for command in ('inspect','backup','verify-backup','apply-system','verify-system','reconnect-storage','activate-system','rollback'):
        p=sub.add_parser(command); p.add_argument('--manifest',required=True,type=Path)
        if command=='backup': p.add_argument('--backup-dir',required=True,type=Path)
        if command=='apply-system': p.add_argument('--bundle',required=True,type=Path)
    args=parser.parse_args(argv)
    if os.geteuid()!=0: raise Abort('Run locally as root; this tool never invokes SSH or sudo')
    if sys.platform!='linux': raise Abort('Live operations require Linux; local unit tests require no privileges')
    os.umask(0o077)
    manifest=args.manifest.absolute()
    ERROR_LOG=Path(str(manifest)+'.errors.log')
    if args.command=='inspect': inspect(manifest)
    else:
        m=load_manifest(manifest)
        with State(manifest) as state:
            if args.command=='backup': refresh_operational_manifest(manifest,m,state)
            bind_state(state,manifest)
            if args.command=='backup': backup(manifest,m,state,args.backup_dir.absolute())
            elif args.command=='verify-backup': verify_backup(manifest,m,state)
            elif args.command=='apply-system': apply_system(manifest,m,state,args.bundle.absolute())
            elif args.command=='verify-system': verify_system(manifest,m,state)
            elif args.command=='rollback': rollback(manifest,m,state)
            elif args.command=='reconnect-storage': reconnect_storage(manifest,m,state)
            elif args.command=='activate-system': activate_system(manifest,m,state)
    print(args.command+': complete (local checks only; client boot acceptance remains manual)')

if __name__=='__main__':
    try: main()
    except (Abort,OSError,ValueError,KeyError) as e:
        print('ABORT: '+(str(e) if isinstance(e,Abort) else 'Malformed input or local filesystem error; inspect private state and use rollback for any partial system apply'),file=sys.stderr)
        if ERROR_LOG is not None: print('Protected diagnostics: '+str(ERROR_LOG)+'; keep writers stopped after a partial apply and run rollback with the same manifest',file=sys.stderr)
        sys.exit(1)
