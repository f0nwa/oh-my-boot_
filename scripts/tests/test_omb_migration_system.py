import importlib.util
import json
import os
from pathlib import Path
import tempfile
import unittest

SOURCE = Path(__file__).parents[1] / 'omb_migration_system.py'

class MigrationSafetyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec = importlib.util.spec_from_file_location('migration', SOURCE)
        cls.m = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(cls.m)

    def test_component_paths_leave_remote_and_similar_names_unchanged(self):
        self.assertEqual(self.m.transform_paths('/srv/pxe/tftp /srv/pxe-extra //nas/pxe/iso /etc/pxe-platform/control.env'), '/srv/omb/tftp /srv/pxe-extra //nas/pxe/iso /etc/omb/control.env')

    def test_environment_changes_keys_and_owned_paths_but_never_secret_values(self):
        source = 'PXE_CONTROL_SETTINGS_KEY_FILE=/var/lib/pxe-platform-control/integrations.key\nPXE_PASSWORD=PXE_abc/srv/pxe/secret\nPXE_DB_URL=postgres:///pxe_control?host=/var/run/postgresql\n'
        self.assertEqual(self.m.transform_env(source), 'OMB_CONTROL_SETTINGS_KEY_FILE=/var/lib/omb-control/integrations.key\nOMB_PASSWORD=PXE_abc/srv/pxe/secret\nOMB_DB_URL=postgres:///omb_control?host=/var/run/postgresql\n')

    def test_shared_state_atomic_merge_preserves_data_fields(self):
        with tempfile.TemporaryDirectory() as d:
            path = Path(d)/'manifest.json'; path.write_text('{}'); path.chmod(0o600)
            with self.m.State(path) as state: state.update(data_complete=True, phase='data')
            with self.m.State(path) as state: state.update(backup_complete=True)
            actual = json.loads(Path(str(path)+'.state.json').read_text())
            self.assertTrue(actual['data_complete']); self.assertTrue(actual['backup_complete']); self.assertEqual(actual['phase'], 'data')
            self.assertEqual(Path(str(path)+'.state.json').stat().st_mode & 0o777, 0o600)

    def test_private_file_rejects_symlinks_and_public_permissions(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'manifest'; p.write_text('{}'); p.chmod(0o644)
            with self.assertRaises(self.m.Abort): self.m.private_file(p)
            p.chmod(0o600); link=Path(d)/'link'; link.symlink_to(p)
            with self.assertRaises(self.m.Abort): self.m.private_file(link)

    def test_archive_rejects_traversal_and_symlink_parent(self):
        import tarfile, io
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'bad.tar'
            with tarfile.open(p,'w') as t:
                a=tarfile.TarInfo('etc/linked'); a.type=tarfile.SYMTYPE; a.linkname='/etc'; t.addfile(a)
                b=tarfile.TarInfo('etc/linked/passwd'); b.size=1; t.addfile(b,io.BytesIO(b'x'))
            with self.assertRaises(self.m.Abort): self.m.validate_archive(p)

    def test_partial_system_state_refuses_forward_execution(self):
        with self.assertRaisesRegex(self.m.Abort,'rollback'):
            self.m.require_forward_state({'backup_complete':True,'backup_verified':True,'system_journal':[{'status':'intent'}]})

    def test_md5_role_password_refuses_apply(self):
        with self.assertRaisesRegex(self.m.Abort,'MD5'):
            self.m.validate_roles([{'rolname':'pxe-control','password_format':'md5'}])

    def test_systemd_mount_filename_escapes_literal_hyphen(self):
        self.assertEqual(self.m.mount_unit('/mnt/omb-smb/key'), r'mnt-omb\x2dsmb-key.mount')

    def test_unit_transformation_preserves_restrictions(self):
        source='[Service]\nUser=pxe-boot\nReadWritePaths=/srv/pxe/tftp/ubuntu-autoinstall\nNoNewPrivileges=yes\nEnvironmentFile=/etc/pxe-platform/boot.env\n'
        target=self.m.transform_unit(source)
        self.assertIn('User=omb-boot',target); self.assertIn('NoNewPrivileges=yes',target); self.assertIn('ReadWritePaths=/srv/omb/tftp/ubuntu-autoinstall',target)

    def test_backup_requires_stopped_proof_before_runner_writes(self):
        calls=[]
        class Runner:
            def __call__(self,argv,**kw):
                calls.append(argv)
                return 'active\n' if argv[:2]==['systemctl','show'] else ''
        with self.assertRaises(self.m.Abort): self.m.assert_stopped(Runner(),['pxe-boot.service'])
        self.assertEqual(len(calls),1)

if __name__=='__main__': unittest.main()

class ApplyContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec=importlib.util.spec_from_file_location('migration',SOURCE); cls.m=importlib.util.module_from_spec(spec); spec.loader.exec_module(cls.m)
    def test_samba_changes_only_owned_share_and_account(self):
        source='[global]\ninterfaces = lo ens19\nbind interfaces only = yes\n[pxeisos]\npath = /mnt/pxe-platform-iso/profiles\nvalid users = pxe-iso\n[other]\npath = /srv/pxe-archive\nvalid users = someone\n'
        result=self.m.transform_samba(source)
        self.assertIn('[ombisos]',result); self.assertIn('valid users = omb-iso',result); self.assertIn('interfaces = lo ens19',result); self.assertIn('path = /srv/pxe-archive',result)
    def test_unexpected_samba_path_fails_before_modification(self):
        with self.assertRaises(self.m.Abort): self.m.transform_samba('[pxeisos]\npath = /somewhere/else\n')
    def test_bundle_inventory_rejects_symlink_binaries(self):
        with tempfile.TemporaryDirectory() as d:
            root=Path(d); (root/'bin').mkdir(); (root/'bin/omb-boot').symlink_to('/bin/sh')
            with self.assertRaises(self.m.Abort): self.m.bundle_inventory(root)
    def test_unit_expansions_are_converted_without_rewriting_literal_secret(self):
        self.assertIn('${OMB_CONTROL_LISTEN}',self.m.transform_unit('ExecStart=/usr/local/bin/pxe-control -listen ${PXE_CONTROL_LISTEN}\n'))

class RollbackContractTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec=importlib.util.spec_from_file_location('migration',SOURCE); cls.m=importlib.util.module_from_spec(spec); spec.loader.exec_module(cls.m)
    def test_intent_rename_without_mutation_is_safe_noop(self):
        with tempfile.TemporaryDirectory() as d:
            old=Path(d)/'old'; new=Path(d)/'new'; old.mkdir(); st=old.stat()
            self.assertEqual(self.m.locate_rename({'old':str(old),'new':str(new),'device':st.st_dev,'inode':st.st_ino}), 'old')
    def test_ambiguous_rename_refuses_rollback(self):
        with tempfile.TemporaryDirectory() as d:
            old=Path(d)/'old'; new=Path(d)/'new'; old.mkdir(); new.mkdir(); st=old.stat()
            with self.assertRaises(self.m.Abort): self.m.locate_rename({'old':str(old),'new':str(new),'device':st.st_dev,'inode':st.st_ino})
    def test_file_metadata_detects_xattr_loss(self):
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'f'; p.write_text('preserved')
            try: os.setxattr(p,'user.omb-test',b'test')
            except (OSError,AttributeError): self.skipTest('xattrs unavailable')
            self.assertIn('user.omb-test',self.m.file_info(p)['xattrs'])
            self.m.write_preserving(p,b'changed')
            self.assertEqual(os.getxattr(p,'user.omb-test'),b'test')
    def test_unit_does_not_rename_nas_share_name(self):
        self.assertIn('//nas/pxe-platform-archive',self.m.transform_unit('What=//nas/pxe-platform-archive\nWhere=/mnt/pxe-platform-smb/key\n'))

class RestoreRehearsalTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec=importlib.util.spec_from_file_location('migration',SOURCE); cls.m=importlib.util.module_from_spec(spec); spec.loader.exec_module(cls.m)
    def test_database_rehearsal_uses_unique_owned_names_and_drops_exact_names(self):
        from unittest.mock import patch
        import types
        with tempfile.TemporaryDirectory() as d:
            directory=Path(d); (directory/'files.json').write_text('[]')
            for db in self.m.DBS: (directory/(db+'.dump')).write_bytes(b'dump')
            manifest=directory/'manifest'; manifest.write_text('{}'); manifest.chmod(0o600)
            commands=[]
            def pg(db,sql): commands.append((db,sql)); return ''
            m={'table_fingerprints':{db:{} for db in self.m.DBS},'schema_fingerprints':{db:'schema' for db in self.m.DBS}}
            with self.m.State(manifest) as state:
                with patch.object(self.m,'verify_artifacts',return_value=directory), patch.object(self.m,'run',return_value=''), patch.object(self.m,'pg',side_effect=pg), patch.object(self.m,'tables',return_value={}), patch.object(self.m,'schema_digest',return_value='schema'), patch.object(self.m.subprocess,'run',return_value=types.SimpleNamespace(returncode=0)):
                    self.m.verify_backup(manifest,m,state)
                self.assertTrue(state.value['backup_verified'])
            creates=[sql for _,sql in commands if sql.startswith('CREATE')]; drops=[sql for _,sql in commands if sql.startswith('DROP')]
            self.assertEqual(len(creates),3); self.assertEqual(len(set(creates)),3)
            for create,drop in zip(creates,drops): self.assertEqual(create.split('"')[1],drop.split('"')[1]); self.assertTrue(drop.startswith('DROP DATABASE "_omb_migration_check_'))
    def test_failed_restore_never_marks_verified_or_drops_database(self):
        from unittest.mock import patch
        import types
        with tempfile.TemporaryDirectory() as d:
            directory=Path(d); (directory/'files.json').write_text('[]')
            for db in self.m.DBS: (directory/(db+'.dump')).write_bytes(b'dump')
            manifest=directory/'manifest'; manifest.write_text('{}'); manifest.chmod(0o600); commands=[]
            with self.m.State(manifest) as state:
                with patch.object(self.m,'verify_artifacts',return_value=directory), patch.object(self.m,'run',return_value=''), patch.object(self.m,'pg',side_effect=lambda db,sql:commands.append(sql)), patch.object(self.m.subprocess,'run',return_value=types.SimpleNamespace(returncode=1,stderr=b'restore failed')):
                    with self.assertRaises(self.m.Abort): self.m.verify_backup(manifest,{},state)
                self.assertFalse(state.value['backup_verified']); self.assertTrue(state.value['verification_pending_db'].startswith('_omb_migration_check_'))
                self.assertFalse(any(s.startswith('DROP') for s in commands))
    def test_pg_sends_sql_only_through_stdin(self):
        calls=[]
        self.m.pg('postgres','SELECT 1;',lambda argv,**kw:calls.append((argv,kw)) or '1\n')
        self.assertNotIn('SELECT 1;',calls[0][0]); self.assertEqual(calls[0][1]['input'],'SELECT 1;\n')
    def test_corrupt_backup_is_rejected_before_restore(self):
        with tempfile.TemporaryDirectory() as d:
            directory=Path(d); directory.chmod(0o700); manifest=directory/'manifest'; manifest.write_text('{}'); manifest.chmod(0o600)
            with self.m.State(manifest) as state:
                state.update(backup_complete=True,backup_source_stop_verified=True,backup_dir=d,backup_artifacts={})
                with self.assertRaisesRegex(self.m.Abort,'Incomplete backup'): self.m.verify_artifacts(manifest,{},state)

class GeneratedMountTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec=importlib.util.spec_from_file_location('migration',SOURCE); cls.m=importlib.util.module_from_spec(spec); spec.loader.exec_module(cls.m)
    def test_kernel_iso_mount_maps_without_nonexistent_fragment(self):
        source={'Id':r'mnt-pxe\x2dplatform\x2diso-profiles-1.mount','FragmentPath':'','Where':'/mnt/pxe-platform-iso/profiles/1'}
        self.assertEqual(self.m.unit_target(source),r'mnt-omb\x2diso-profiles-1.mount')

class SQLFingerprintTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec=importlib.util.spec_from_file_location('migration',SOURCE); cls.m=importlib.util.module_from_spec(spec); spec.loader.exec_module(cls.m)
    def test_row_fingerprint_defines_inner_row_alias(self):
        from unittest.mock import patch
        statements=[]
        with patch.object(self.m,'rows',return_value=[{'schemaname':'public','tablename':'auth_users'}]), patch.object(self.m,'pg',side_effect=lambda db,sql:statements.append(sql) or ('0' if 'count(*)' in sql else 'hash')):
            self.m.tables('pxe_control')
        self.assertIn('FROM "public"."auth_users" t)',statements[1])

class LiveIntegrationRegressionTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        spec=importlib.util.spec_from_file_location('migration',SOURCE);cls.m=importlib.util.module_from_spec(spec);spec.loader.exec_module(cls.m)
    def test_owned_database_role_is_renamed_without_modifying_secret(self):
        source='PXE_CONTROL_DATABASE_URL="host=/var/run/postgresql dbname=pxe_control user=pxe-control password=pxe-control-secret"\n'
        self.assertEqual(self.m.transform_env(source),'OMB_CONTROL_DATABASE_URL="host=/var/run/postgresql dbname=omb_control user=omb-control password=pxe-control-secret"\n')
    def test_uri_role_is_renamed_without_modifying_password_or_host(self):
        source='PXE_BOOT_DATABASE_URL=postgresql://pxe-boot:pxe-boot-secret@db.example/pxe_boot?sslmode=require\n'
        self.assertEqual(self.m.transform_env(source),'OMB_BOOT_DATABASE_URL=postgresql://omb-boot:pxe-boot-secret@db.example/omb_boot?sslmode=require\n')
    def test_ubuntu_iso_owned_path_is_mapped(self):
        self.assertEqual(self.m.transform_env('PXE_BOOT_UBUNTU_ISO=/srv/pxe/tftp/ubuntu/ubuntu.iso\n'),'OMB_BOOT_UBUNTU_ISO=/srv/omb/tftp/ubuntu/ubuntu.iso\n')
    def test_quiet_refresh_preserves_original_service_states(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'manifest';p.write_text('{}');p.chmod(0o600)
            before={'pxe_boot':{'boot_events':{'count':1,'identity_md5':'old'}},'pxe_control':{'users':{'count':6,'identity_md5':'same'}},'pxe_images':{}}
            after={'pxe_boot':{'boot_events':{'count':2,'identity_md5':'new'}},'pxe_control':before['pxe_control'],'pxe_images':{}}
            m={'table_fingerprints':before,'table_counts':{db:{t:x['count'] for t,x in ts.items()} for db,ts in before.items()},'schema_fingerprints':{db:'schema' for db in self.m.DBS},'boot_snapshot':{'path':'boot','sha256':'same'},'system_units':[{'ActiveState':'active'}]}
            with self.m.State(p) as state,patch.object(self.m,'assert_source_quiet'),patch.object(self.m,'schema_digest',return_value='schema'),patch.object(self.m,'tables',side_effect=lambda db:after[db]),patch.object(self.m,'digest',return_value='same'):
                self.m.refresh_operational_manifest(p,m,state)
            self.assertEqual(m['table_counts']['pxe_boot']['boot_events'],2)
            self.assertEqual(m['system_units'][0]['ActiveState'],'active')
            self.assertIn('quiesced_at',json.loads(p.read_text()))
    def test_quiet_refresh_refuses_immutable_changes(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'manifest';p.write_text('{}');p.chmod(0o600)
            m={'table_fingerprints':{db:{'users':{'count':1,'identity_md5':'old'}} for db in self.m.DBS},'schema_fingerprints':{db:'schema' for db in self.m.DBS},'boot_snapshot':{'path':'boot','sha256':'same'}}
            with self.m.State(p) as state,patch.object(self.m,'assert_source_quiet'),patch.object(self.m,'schema_digest',return_value='schema'),patch.object(self.m,'tables',return_value={'users':{'count':2,'identity_md5':'new'}}):
                with self.assertRaisesRegex(self.m.Abort,'immutable'):self.m.refresh_operational_manifest(p,m,state)
            self.assertEqual(p.read_text(),'{}')
    def test_database_like_tokens_inside_password_stay_opaque(self):
        source="PXE_CONTROL_DATABASE_URL=host=/var/run/postgresql dbname='pxe_control' user='pxe-control' password='dbname=pxe_control user=pxe-control'\n"
        self.assertEqual(self.m.transform_env(source),"OMB_CONTROL_DATABASE_URL=host=/var/run/postgresql dbname='omb_control' user='omb-control' password='dbname=pxe_control user=pxe-control'\n")
    def test_bound_manifest_never_refreshes_again(self):
        from unittest.mock import patch
        with tempfile.TemporaryDirectory() as d:
            p=Path(d)/'manifest';p.write_text('unchanged');p.chmod(0o600)
            with self.m.State(p) as state:
                state.update(manifest_sha256='bound')
                with patch.object(self.m,'assert_source_quiet',side_effect=AssertionError('must not refresh bound state')):
                    self.m.refresh_operational_manifest(p,{},state)
            self.assertEqual(p.read_text(),'unchanged')
