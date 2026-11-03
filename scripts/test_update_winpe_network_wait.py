import base64
import hashlib
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

SOURCE=Path(__file__).with_name('update-winpe-network-wait.py')
spec=importlib.util.spec_from_file_location('winpe_network_update',SOURCE)
m=importlib.util.module_from_spec(spec)
spec.loader.exec_module(m)
OLD=b'wpeutil WaitForNetwork\r\nif errorlevel 1 goto failed\r\n'
PREVIOUS=(b'wpeutil WaitForNetwork\r\nset OMB_WAIT_ERROR=%errorlevel%\r\n'
          b'if not "%OMB_WAIT_ERROR%"=="0" echo OMB: network initialization wait returned %OMB_WAIT_ERROR%; retrying installation media connection...\r\n')
NEW=(b'wpeutil WaitForNetwork >"%SystemRoot%\\System32\\omb-network-wait.log" 2>&1\r\n'
     b'set OMB_WAIT_ERROR=%errorlevel%\r\n'
     b'>>"%SystemRoot%\\System32\\omb-network-wait.log" echo WaitForNetwork exit code: %OMB_WAIT_ERROR%\r\n'
     b'if not "%OMB_WAIT_ERROR%"=="0" echo OMB: network is not ready yet; waiting for installation media...\r\n')

def sha(data):return hashlib.sha256(data).hexdigest()

class StartupMigrationTests(unittest.TestCase):
 def test_upgrades_current_deployed_wait_without_changing_retry_body(self):
  body=b'set /a OMB_ATTEMPT=0 >nul\r\n:connect_media\r\nnet use Y: \\\\server\\share\r\n'
  self.assertEqual(m.upgrade_startup(b'@echo off\r\n'+PREVIOUS+body),b'@echo off\r\n'+NEW+body)
 def test_upgrade_preserves_credential_and_setup_binding(self):
  script=b'@echo off\r\nset OMB_ISO_PASSWORD=test-secret-not-real\r\n'+OLD+b'net use Y: \\\\server\\share %OMB_ISO_PASSWORD%\r\nY:\\17\\sources\\setup.exe /Unattend:X:\\answer.xml\r\n'
  fixed=m.upgrade_startup(script)
  self.assertEqual(fixed,script.replace(OLD,NEW))
  self.assertEqual(m.upgrade_startup(fixed),fixed)
 def test_rejects_ambiguous_or_unknown_wait_flow(self):
  for script in (OLD+OLD,b'wpeutil WaitForNetwork\r\ngoto unknown\r\n'):
   with self.assertRaises(ValueError):m.upgrade_startup(script)
 def test_leaves_legacy_script_without_wait_untouched(self):
  script=b'@echo off\r\nwpeinit\r\nnet use Y: \\\\server\\share\r\n'
  self.assertEqual(m.upgrade_startup(script),script)
 def test_manifest_changes_only_script_digest(self):
  before=b'@echo off\r\n'+OLD;after=before.replace(OLD,NEW)
  doc={'request':{'config_id':7,'profile_id':17,'answer_id':4,'package_infs':[{'package_sha256':'a'*64,'inf_path':'net.inf'}]},'result':{'wim_key':'b'*64,'wim_sha256':'c'*64,'script_sha256':sha(before)}}
  original=json.dumps(doc).encode();fixed=json.loads(m.upgrade_manifest(original,7,sha(before),sha(after)))
  self.assertEqual(fixed['request'],doc['request'])
  self.assertEqual(fixed['result'],dict(doc['result'],script_sha256=sha(after)))
  with self.assertRaises(ValueError):m.upgrade_manifest(original,8,sha(before),sha(after))
  with self.assertRaises(ValueError):m.upgrade_manifest(original,7,'d'*64,sha(after))
 def test_menu_updates_matching_reference_and_preserves_other_content(self):
  old='a'*64;new='b'*64
  refs=[{'id':7,'profile_id':17,'answer_id':0,'answer_sha256':'','wim_key':'c'*64,'wim_sha256':'d'*64,'script_sha256':old},{'id':9,'script_sha256':'e'*64}]
  encoded=base64.urlsafe_b64encode(json.dumps(refs,separators=(',',':')).encode()).rstrip(b'=')
  menu=b'#!ipxe\n# omb:windrivers-v1 '+encoded+b'\nmenu Keep title\nitem install_7 Keep selection\n'
  fixed=m.upgrade_menu(menu,{7:(old,new)})
  got=json.loads(base64.urlsafe_b64decode(fixed.splitlines()[1].split(b' ',2)[2]+b'==='))
  self.assertEqual(got[0],dict(refs[0],script_sha256=new));self.assertEqual(got[1],refs[1])
  self.assertTrue(fixed.endswith(b'\nmenu Keep title\nitem install_7 Keep selection\n'))
  self.assertEqual(m.upgrade_menu(fixed,{7:(old,new)}),fixed)
 def test_rejects_conflicting_saved_menu_reference(self):
  refs=[{'id':7,'script_sha256':'f'*64}]
  encoded=base64.urlsafe_b64encode(json.dumps(refs).encode()).rstrip(b'=')
  with self.assertRaises(ValueError):m.upgrade_menu(b'#!ipxe\n# omb:windrivers-v1 '+encoded+b'\n',{7:('a'*64,'b'*64)})
 def test_menu_without_driver_references_is_unchanged(self):
  menu=b'#!ipxe\nmenu Ubuntu\n';self.assertEqual(m.upgrade_menu(menu,{7:('a'*64,'b'*64)}),menu)
 def test_sql_rejects_untrusted_id_or_digest(self):
  for change in ({'id':'1;DROP TABLE users','old':'a'*64,'new':'b'*64},{'id':1,'old':"bad'",'new':'b'*64}):
   with self.assertRaises(ValueError):m.build_sql([change],[])

class RecoveryTests(unittest.TestCase):
 def test_failed_automatic_recovery_keeps_control_stopped(self):
  self.check_apply_recovery(False)
 def test_successful_automatic_recovery_restarts_control(self):
  self.check_apply_recovery(True)
 def check_apply_recovery(self,recovered):
  with tempfile.TemporaryDirectory() as temporary:
   backup=Path(temporary);(backup/'plan.json').write_text('{}')
   calls=[];plan={'files':[{'path':str(backup/'startup.cmd')}],'drivers':[],'menus':[],'new_menu':b'#!ipxe\n'}
   def run(*args,**kwargs):calls.append(args);return b''
   def fail_install(*args):raise RuntimeError('injected write failure')
   with patch.object(m,'STATE',backup/'state.json'),patch.object(m,'run',run),patch.object(m,'collect_plan',return_value=plan),patch.object(m,'check_driver_assets'),patch.object(m,'save_backup',return_value=backup),patch.object(m,'set_state'),patch.object(m,'read_regular',fail_install),patch.object(m,'restore',side_effect=None if recovered else RuntimeError('injected restore failure')):
    with self.assertRaises(RuntimeError):m.apply()
   self.assertIn(('/usr/bin/systemctl','stop','omb-control'),calls)
   self.assertEqual(('/usr/bin/systemctl','start','omb-control') in calls,recovered)
 def test_failed_explicit_rollback_keeps_control_stopped(self):
  calls=[]
  with patch.object(m,'load_backup',return_value=({},Path('/root/backup'),{})),patch.object(m,'run',side_effect=lambda *a,**kw:calls.append(a)),patch.object(m,'restore',side_effect=RuntimeError('injected rollback failure')):
   with self.assertRaises(RuntimeError):m.rollback()
  self.assertNotIn(('/usr/bin/systemctl','start','omb-control'),calls)
 def test_operations_are_exclusive(self):
  with tempfile.TemporaryDirectory() as temporary,patch.object(m,'STATE',Path(temporary)/'state.json'):
   with m.operation_lock():
    with self.assertRaises(RuntimeError):
     with m.operation_lock():pass

if __name__=='__main__':unittest.main()
