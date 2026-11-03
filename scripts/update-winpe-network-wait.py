#!/usr/bin/python3 -I
"""One-time WinPE wait-output upgrade with artifact checksum migration."""
import argparse
import base64
from contextlib import contextmanager
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile

LEGACY_BLOCK = b'wpeutil WaitForNetwork\r\nif errorlevel 1 goto failed\r\n'
OLD_BLOCK = (b'wpeutil WaitForNetwork\r\nset OMB_WAIT_ERROR=%errorlevel%\r\n'
             b'if not "%OMB_WAIT_ERROR%"=="0" echo OMB: network initialization wait returned %OMB_WAIT_ERROR%; retrying installation media connection...\r\n')
NEW_BLOCK = (b'wpeutil WaitForNetwork >"%SystemRoot%\\System32\\omb-network-wait.log" 2>&1\r\n'
             b'set OMB_WAIT_ERROR=%errorlevel%\r\n'
             b'>>"%SystemRoot%\\System32\\omb-network-wait.log" echo WaitForNetwork exit code: %OMB_WAIT_ERROR%\r\n'
             b'if not "%OMB_WAIT_ERROR%"=="0" echo OMB: network is not ready yet; waiting for installation media...\r\n')
MARKER = b'# omb:windrivers-v1 '
TEMPLATE = Path('/usr/share/omb/windows-profiles/omb-setup.cmd.in')
ROOTS = tuple(Path('/srv/omb/tftp')/name for name in ('windows-profiles','windows-answers','windows-driver-configs'))
STATE = Path('/var/lib/omb-winpe-network-wait-output/state.json')
BACKUP_PREFIX = 'omb-winpe-network-wait-output-20261102.'
BOOT_SOCKET = '/run/omb-boot/control.sock'
AGENT_SOCKET = '/run/omb/agent.sock'
SNAPSHOT = Path('/var/lib/omb-boot/menu.ipxe')
ENV = {'PATH':'/usr/sbin:/usr/bin:/sbin:/bin','LC_ALL':'C','LANG':'C'}
LEGACY_TEMPLATE_SHA = '351243d404ec4c3f2fe876e287121c74adebc8b86230424002066b862e9df8b1'
OLD_TEMPLATE_SHA = '430a4cf2ab20461ab94ddcc4da350ac1fc2247b782f5f3cc482b36bab9dc1289'
NEW_TEMPLATE_SHA = '8c070d62d56c08c023dc5b273685ef3efe4a48df3f45ed147ca6fc98e44203b2'


def digest(data):
    return hashlib.sha256(data).hexdigest()


def valid_digest(value):
    return isinstance(value,str) and re.fullmatch('[0-9a-f]{64}',value) is not None


def valid_id(value):
    return isinstance(value,int) and not isinstance(value,bool) and value>0


def upgrade_startup(data):
    if data.count(NEW_BLOCK)==1 and data.count(b'wpeutil WaitForNetwork')==1:
        return data
    for previous in (OLD_BLOCK,LEGACY_BLOCK):
        if data.count(previous)==1 and data.count(b'wpeutil WaitForNetwork')==1:
            return data.replace(previous,NEW_BLOCK,1)
    if b'waitfornetwork' not in data.lower():
        return data
    raise ValueError('unknown or ambiguous network-wait startup block')


def upgrade_manifest(data,config_id,old,new):
    doc=json.loads(data)
    if doc.get('request',{}).get('config_id')!=config_id or doc.get('result',{}).get('script_sha256')!=old:
        raise ValueError('driver manifest identity/hash differs')
    if not valid_digest(old) or not valid_digest(new):
        raise ValueError('invalid startup digest')
    doc['result']['script_sha256']=new
    return json.dumps(doc,separators=(',',':')).encode()


def upgrade_menu(data,changes):
    result=[]
    seen=False
    for line in data.splitlines(keepends=True):
        if not line.startswith(MARKER):
            result.append(line)
            continue
        if seen:raise ValueError('duplicate driver reference marker')
        seen=True
        ending=b'\r\n' if line.endswith(b'\r\n') else b'\n' if line.endswith(b'\n') else b''
        encoded=line[len(MARKER):].rstrip(b'\r\n')
        refs=json.loads(base64.b64decode(encoded+b'='*((-len(encoded))%4),altchars=b'-_',validate=True))
        if not isinstance(refs,list):raise ValueError('invalid saved driver references')
        changed=False
        for ref in refs:
            if not isinstance(ref,dict):raise ValueError('invalid saved driver reference')
            if ref.get('id') in changes:
                old,new=changes[ref['id']]
                if ref.get('script_sha256') not in (old,new):raise ValueError('saved driver reference conflicts with current artifact')
                if ref['script_sha256']==old and old!=new:
                    ref['script_sha256']=new
                    changed=True
        if changed:
            encoded=base64.urlsafe_b64encode(json.dumps(refs,separators=(',',':'),ensure_ascii=False).encode()).rstrip(b'=')
            line=MARKER+encoded+ending
        result.append(line)
    return b''.join(result)


def sql_text(value):
    return "convert_from(decode('"+value.encode().hex()+"','hex'),'UTF8')"


def build_sql(drivers,menus,reverse=False):
    statements=['BEGIN;']
    for row in drivers:
        if not valid_id(row.get('id')) or not valid_digest(row.get('old')) or not valid_digest(row.get('new')):
            raise ValueError('invalid driver migration identity/digest')
        ident=row['id'];old=row['new'] if reverse else row['old'];new=row['old'] if reverse else row['new']
        body="UPDATE winpe_driver_configs SET script_sha256='{}' WHERE id={} AND script_sha256='{}';".format(new,ident,old)
        if reverse:
            guard="IF EXISTS(SELECT 1 FROM winpe_driver_configs WHERE id={} AND script_sha256='{}') THEN {} ELSIF NOT EXISTS(SELECT 1 FROM winpe_driver_configs WHERE id={} AND script_sha256='{}') THEN RAISE EXCEPTION 'driver rollback state changed'; END IF;".format(ident,old,body,ident,new)
        else:
            guard=body+" IF NOT FOUND THEN RAISE EXCEPTION 'driver migration state changed'; END IF;"
        statements.append('DO $omb$ BEGIN '+guard+' END $omb$;')
    for row in menus:
        if not valid_id(row.get('id')) or any(not valid_digest(row.get(key)) for key in ('old_sha','new_sha')):
            raise ValueError('invalid menu migration identity/digest')
        if any(not isinstance(row.get(key),str) for key in ('old_script','new_script')):
            raise ValueError('invalid menu script')
        if digest(row['old_script'].encode())!=row['old_sha'] or digest(row['new_script'].encode())!=row['new_sha']:
            raise ValueError('menu digest does not match script')
        ident=row['id'];old=row['new_sha'] if reverse else row['old_sha'];new=row['old_sha'] if reverse else row['new_sha']
        script=row['old_script'] if reverse else row['new_script']
        body="UPDATE menu_versions SET script={},sha256='{}' WHERE id={} AND sha256='{}';".format(sql_text(script),new,ident,old)
        if reverse:
            guard="IF EXISTS(SELECT 1 FROM menu_versions WHERE id={} AND sha256='{}') THEN {} ELSIF NOT EXISTS(SELECT 1 FROM menu_versions WHERE id={} AND sha256='{}') THEN RAISE EXCEPTION 'menu rollback state changed'; END IF;".format(ident,old,body,ident,new)
        else:
            guard=body+" IF NOT FOUND THEN RAISE EXCEPTION 'menu migration state changed'; END IF;"
        statements.append('DO $omb$ BEGIN '+guard+' END $omb$;')
    return '\n'.join(statements+['COMMIT;',''])


def run(*args,data=None):
    result=subprocess.run(args,input=data,stdout=subprocess.PIPE,stderr=subprocess.PIPE,env=ENV,timeout=180)
    if result.returncode:raise RuntimeError(Path(args[0]).name+' failed (exit '+str(result.returncode)+'); private output withheld')
    return result.stdout


def db(sql):
    return run('/usr/sbin/runuser','-u','postgres','--','/usr/bin/psql','-XqAt','-v','ON_ERROR_STOP=1','--dbname=omb_control',data=sql.encode()).decode().strip()


def api(socket,path,data=None):
    args=['/usr/bin/curl','--fail','--silent','--show-error','--max-time','120','--unix-socket',socket]
    if data is not None:args+=['-X','POST','-H','Content-Type: application/json','--data-binary','@-']
    return run(*(args+['http://local'+path]),data=data)


def read_regular(path):
    path=Path(path);info=path.lstat()
    if not stat.S_ISREG(info.st_mode) or path.resolve()!=path or info.st_size>1<<20:
        raise ValueError('unsafe or oversized startup asset: '+str(path))
    return path.read_bytes(),info


def atomic_install(path,data,info):
    fd,name=tempfile.mkstemp(prefix='.omb-winpe-',dir=path.parent)
    try:
        with os.fdopen(fd,'wb') as stream:
            os.fchown(stream.fileno(),info['uid'],info['gid'])
            os.fchmod(stream.fileno(),info['mode'])
            stream.write(data);stream.flush();os.fsync(stream.fileno())
        os.replace(name,path)
        sync_directory(path.parent)
    finally:
        if os.path.exists(name):os.unlink(name)


def sync_directory(path):
    fd=os.open(path,os.O_RDONLY|os.O_DIRECTORY)
    try:os.fsync(fd)
    finally:os.close(fd)


def metadata(path,old,new):
    _,info=read_regular(path)
    return {'path':str(path),'old':old,'new':new,'old_sha':digest(old),'new_sha':digest(new),
            'uid':info.st_uid,'gid':info.st_gid,'mode':stat.S_IMODE(info.st_mode)}


def collect_plan():
    if db("SELECT count(*) FROM winpe_driver_configs WHERE state='preparing'")!='0':
        raise ValueError('a driver build is active; retry after it finishes')
    template,_=read_regular(TEMPLATE)
    if digest(template) not in (LEGACY_TEMPLATE_SHA,OLD_TEMPLATE_SHA,NEW_TEMPLATE_SHA):raise ValueError('installed template differs from the verified baseline')
    rows=json.loads(db("SELECT COALESCE(json_agg(x),'[]') FROM (SELECT id,state,script_sha256 FROM winpe_driver_configs ORDER BY id) x"))
    driver_rows={r['id']:r for r in rows};files=[];mapping={};driver_changes=[];checks=[]
    new_template=upgrade_startup(template)
    if new_template!=template:files.append(metadata(TEMPLATE,template,new_template))
    for root in ROOTS:
        if root.resolve()!=root or not root.is_dir():raise ValueError('startup root unavailable: '+str(root))
        for parent in sorted(root.iterdir()):
            if not re.fullmatch('[1-9][0-9]*',parent.name):continue
            if parent.resolve()!=parent or not parent.is_dir():raise ValueError('unsafe numeric asset directory')
            path=parent/'omb-setup.cmd';old,_=read_regular(path);new=upgrade_startup(old)
            if old==new:continue
            files.append(metadata(path,old,new))
            if root==ROOTS[2]:
                ident=int(parent.name);manifest_path=parent/'build.json';manifest,_=read_regular(manifest_path)
                changed=upgrade_manifest(manifest,ident,digest(old),digest(new))
                files.append(metadata(manifest_path,manifest,changed));mapping[ident]=(digest(old),digest(new))
                if ident not in driver_rows:raise ValueError('changed driver artifact has no database row')
                row=driver_rows[ident]
                if row['script_sha256']==digest(old):driver_changes.append({'id':ident,'old':digest(old),'new':digest(new)})
                elif row['state']=='ready' or row['script_sha256']!='':raise ValueError('driver database/artifact digest mismatch')
                if row['state']=='ready':
                    doc=json.loads(manifest);checks.append({'before':doc,'after':json.loads(changed)})
    versions=json.loads(db("SELECT COALESCE(json_agg(x),'[]') FROM (SELECT id,script,sha256,active FROM menu_versions ORDER BY id) x"))
    menus=[];active=[v for v in versions if v['active']]
    old_menu=api(BOOT_SOCKET,'/menu');snapshot,_=read_regular(SNAPSHOT)
    if old_menu!=snapshot:raise ValueError('Boot memory and saved menu differ')
    if active and (len(active)!=1 or active[0]['script'].encode()!=old_menu):raise ValueError('active database and Boot menu differ')
    for row in versions:
        old=row['script'].encode();new=upgrade_menu(old,mapping)
        if new==old:continue
        if digest(old)!=row['sha256']:raise ValueError('saved menu digest is invalid')
        menus.append({'id':row['id'],'old_script':row['script'],'new_script':new.decode(),'old_sha':row['sha256'],'new_sha':digest(new)})
    new_menu=upgrade_menu(old_menu,mapping)
    return {'files':files,'drivers':driver_changes,'menus':menus,'checks':checks,'old_menu':old_menu,'new_menu':new_menu}


def summary(plan):
    return {'changed_files':len(plan['files']),'driver_ids':[r['id'] for r in plan['drivers']],
            'menu_version_ids':[r['id'] for r in plan['menus']],'live_menu_changes':plan['old_menu']!=plan['new_menu']}


def check_driver_assets(plan,after):
    for item in plan['checks']:
        doc=item['after' if after else 'before']
        # Matches DriverBuildCheck: manifest fields are request and result.
        api(AGENT_SOCKET,'/winpe-drivers/check-config',json.dumps(doc,separators=(',',':')).encode())


def save_backup(plan):
    backup=Path(tempfile.mkdtemp(prefix=BACKUP_PREFIX,dir='/root'));os.chmod(backup,0o700)
    (backup/'files').mkdir(mode=0o700);(backup/'new').mkdir(mode=0o700)
    doc={k:v for k,v in plan.items() if k not in ('files','old_menu','new_menu')};doc['files']=[]
    for index,item in enumerate(plan['files']):
        name=str(index);(backup/'files'/name).write_bytes(item['old']);(backup/'new'/name).write_bytes(item['new'])
        doc['files'].append({k:v for k,v in item.items() if k not in ('old','new')})
    (backup/'menu-before.ipxe').write_bytes(plan['old_menu']);(backup/'menu-after.ipxe').write_bytes(plan['new_menu'])
    (backup/'plan.json').write_text(json.dumps(doc,separators=(',',':')))
    (backup/'omb_control.dump').write_bytes(run('/usr/sbin/runuser','-u','postgres','--','/usr/bin/pg_dump','-Fc','--dbname=omb_control'))
    run('/usr/bin/pg_restore','--list',str(backup/'omb_control.dump'))
    # A durable prepared state must never precede its recovery data.
    for path in backup.rglob('*'):
        if path.is_file():
            with path.open('rb') as stream:os.fsync(stream.fileno())
    for path in (backup/'files',backup/'new',backup,backup.parent):sync_directory(path)
    return backup


def set_state(backup,stage):
    STATE.parent.mkdir(mode=0o700,exist_ok=True);os.chmod(STATE.parent,0o700)
    sync_directory(STATE.parent.parent)
    info={'uid':0,'gid':0,'mode':0o600}
    atomic_install(STATE,json.dumps({'backup':str(backup),'stage':stage}).encode(),info)


@contextmanager
def operation_lock():
    STATE.parent.mkdir(mode=0o700,exist_ok=True)
    fd=os.open(STATE.parent/'operation.lock',os.O_CREAT|os.O_RDWR|os.O_NOFOLLOW,0o600)
    try:
        try:fcntl.flock(fd,fcntl.LOCK_EX|fcntl.LOCK_NB)
        except BlockingIOError:raise RuntimeError('another WinPE network-wait operation is running') from None
        yield
    finally:os.close(fd)


def load_backup():
    state=json.loads(STATE.read_text());backup=Path(state['backup'])
    if backup.parent!=Path('/root') or not backup.name.startswith(BACKUP_PREFIX) or backup.resolve()!=backup:
        raise ValueError('invalid backup path')
    doc=json.loads((backup/'plan.json').read_text())
    return state,backup,doc


def restore(backup,doc):
    old_menu=(backup/'menu-before.ipxe').read_bytes();new_menu=(backup/'menu-after.ipxe').read_bytes()
    if api(BOOT_SOCKET,'/menu') not in (old_menu,new_menu):raise ValueError('Boot menu changed after update; automatic rollback stopped')
    for index,item in enumerate(doc['files']):
        current,_=read_regular(Path(item['path']))
        if digest(current) not in (item['old_sha'],item['new_sha']):raise ValueError('asset changed after update; automatic rollback stopped')
        if digest((backup/'files'/str(index)).read_bytes())!=item['old_sha']:raise ValueError('backup digest differs')
    db(build_sql(doc['drivers'],doc['menus'],True))
    for index,item in enumerate(doc['files']):atomic_install(Path(item['path']),(backup/'files'/str(index)).read_bytes(),item)
    api(BOOT_SOCKET,'/publish',old_menu)
    check_driver_assets(doc,False)
    set_state(backup,'rolled_back')


def apply():
    if STATE.exists():
        state,_,_=load_backup()
        if state['stage']=='applied':status();return
        if state['stage']=='prepared':raise ValueError('incomplete update; run rollback before retrying')
    for service in ('omb-control','omb-agent','omb-boot'):
        run('/usr/bin/systemctl','is-active','--quiet',service)
    run('/usr/bin/systemctl','stop','omb-control')
    backup=None
    restart_control=True
    try:
        plan=collect_plan()
        if not plan['files']:
            print('NO_CHANGES');return
        check_driver_assets(plan,False)
        backup=save_backup(plan);set_state(backup,'prepared')
        for item in plan['files']:
            if read_regular(Path(item['path']))[0]!=item['old']:raise ValueError('startup changed during preflight')
            atomic_install(Path(item['path']),item['new'],item)
        db(build_sql(plan['drivers'],plan['menus']))
        api(BOOT_SOCKET,'/publish',plan['new_menu'])
        if api(BOOT_SOCKET,'/menu')!=plan['new_menu']:raise ValueError('published menu verification failed')
        check_driver_assets(plan,True)
        db("INSERT INTO audit_events(action,result,detail) VALUES('system.winpe_network_wait_output','ok','recoverable wait output moved to WinPE log; backup "+str(backup)+"')")
        set_state(backup,'applied')
        print('APPLIED '+json.dumps(summary(plan))+' backup='+str(backup))
    except Exception:
        if backup is not None:
            restart_control=False
            doc=json.loads((backup/'plan.json').read_text());restore(backup,doc)
            restart_control=True
        raise
    finally:
        if restart_control:run('/usr/bin/systemctl','start','omb-control')
    status()


def rollback():
    _,backup,doc=load_backup()
    run('/usr/bin/systemctl','stop','omb-control')
    restored=False
    try:
        restore(backup,doc)
        restored=True
        db("INSERT INTO audit_events(action,result,detail) VALUES('system.winpe_network_wait_output','rollback','startup wait output restored')")
        print('ROLLED_BACK backup='+str(backup))
    finally:
        if restored:run('/usr/bin/systemctl','start','omb-control')


def status():
    result={'template_sha256':digest(read_regular(TEMPLATE)[0])}
    if STATE.exists():
        state,backup,doc=load_backup();result.update(state);result['changed_files']=len(doc['files'])
        result['current_hashes_match']=all(digest(read_regular(Path(item['path']))[0])==item['new_sha' if state['stage']=='applied' else 'old_sha'] for item in doc['files'])
    print(json.dumps(result))
    print(run('/usr/bin/systemctl','show','omb-control','omb-agent','omb-boot','omb-dnsmasq','-p','Id','-p','ActiveState','-p','MainPID').decode())


def main(argv=None):
    parser=argparse.ArgumentParser(description=__doc__);parser.add_argument('action',choices=('preflight','apply','status','rollback'));args=parser.parse_args(argv)
    if os.geteuid()!=0:raise ValueError('install this helper root-owned and invoke with authorized sudo')
    os.umask(0o077)
    with operation_lock():
        if args.action=='preflight':
            plan=collect_plan();check_driver_assets(plan,False);print('PREFLIGHT '+json.dumps(summary(plan)))
        elif args.action=='apply':apply()
        elif args.action=='rollback':rollback()
        else:status()


if __name__=='__main__':
    try:main()
    except Exception as error:
        print('ERROR: '+str(error),file=sys.stderr);sys.exit(1)
