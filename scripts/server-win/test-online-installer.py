#!/usr/bin/env python3
"""Portable PowerShell preflight tests. These do not replace Windows installation."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import zipfile

ROOT = Path(__file__).resolve().parents[2]
PWSH = os.environ.get('VISTO_TEST_PWSH', 'pwsh')


class WindowsOnlinePreflight(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root=Path(self.tmp.name)
        self.requests=self.root/'requests'
        self.wrapper=self.root/'test.ps1'
        self.wrapper.write_text(r'''
$ErrorActionPreference='Stop'
$env:OS='Windows_NT'
$env:PROCESSOR_ARCHITECTURE='AMD64'
$env:PROCESSOR_ARCHITEW6432=''
$env:ProgramFiles=Join-Path $env:FIXTURE_ROOT 'programs'
function Invoke-RestMethod {
 param($Uri,$Headers,$TimeoutSec)
 Add-Content -LiteralPath (Join-Path $env:FIXTURE_ROOT 'requests') -Value $Uri
 return (Get-Content -LiteralPath (Join-Path $env:FIXTURE_ROOT 'release.json') -Raw | ConvertFrom-Json)
}
function Invoke-WebRequest {
 param($Uri,$OutFile,$TimeoutSec,[switch]$UseBasicParsing)
 Add-Content -LiteralPath (Join-Path $env:FIXTURE_ROOT 'requests') -Value $Uri
 $name=[IO.Path]::GetFileName(([Uri]$Uri).AbsolutePath)
 Copy-Item -LiteralPath (Join-Path $env:FIXTURE_ROOT $name) -Destination $OutFile
}
function Get-ScheduledTask {throw 'CheckOnly must not touch scheduled tasks.'}
$tokens=$null; $errors=$null
[System.Management.Automation.Language.Parser]::ParseFile($env:INSTALLER,[ref]$tokens,[ref]$errors) | Out-Null
if ($errors.Count) {throw ($errors | Out-String)}
& $env:INSTALLER -CheckOnly
''')
        self.env=os.environ.copy()
        self.env.update(FIXTURE_ROOT=str(self.root),INSTALLER=str(ROOT/'scripts/server-win/Install-VistoOnline.ps1'),DOTNET_SYSTEM_GLOBALIZATION_INVARIANT='1')
        self.make_package()

    def make_package(self, version='1.0.3', metadata=None, extra=None, omit=None, legacy_hash=False):
        name=f'Visto-Server_{version}_windows-x64.zip'
        archive=self.root/name
        files={
          'Install-VistoServer.ps1':'throw "Never execute in CheckOnly"',
          'VistoServer.Security.psm1':'throw "Never import in CheckOnly"',
          'visto-server.json':json.dumps({'version':metadata or version,'address':'127.0.0.1:8787'}),
          'bin/visto-core.exe':'fixture','web/index.html':'fixture',
          'runtime/ffmpeg/ffmpeg.exe':'fixture','runtime/ffmpeg/ffprobe.exe':'fixture'}
        if omit: files.pop(omit)
        with zipfile.ZipFile(archive,'w') as z:
            for path,body in files.items():z.writestr(path,body)
            if extra:z.writestr(extra,'unsafe')
        digest=hashlib.sha256(archive.read_bytes()).hexdigest()
        text=f'SHA256  {name}\n{digest}\n' if legacy_hash else f'{digest}  {name}\n'
        Path(str(archive)+'.sha256').write_text(text)
        (self.root/'release.json').write_text(json.dumps({'tag_name':'v'+version,'draft':False,'prerelease':False,'assets':[{'name':name}]}))
        return archive

    def run_check(self):
        return subprocess.run([PWSH,'-NoLogo','-NoProfile','-File',str(self.wrapper)],env=self.env,text=True,capture_output=True)

    def test_latest_changes_are_resolved_without_host_mutations(self):
        for version in ['1.0.3','1.0.4']:
            self.make_package(version)
            r=self.run_check();self.assertEqual(r.returncode,0,r.stderr)
            self.assertIn(version,r.stdout)
            self.assertIn(f'/releases/download/v{version}/Visto-Server_{version}_windows-x64.zip',self.requests.read_text())
            self.assertFalse((self.root/'programs').exists())
        self.assertEqual(self.requests.read_text().count('/releases/latest'),2)

    def test_legacy_two_line_checksum_is_supported(self):
        self.make_package(legacy_hash=True)
        r=self.run_check();self.assertEqual(r.returncode,0,r.stderr)

    def test_local_candidate_is_checked_without_latest(self):
        archive=self.make_package('1.0.4-install-preview.3')
        self.wrapper.write_text(self.wrapper.read_text().replace('& $env:INSTALLER -CheckOnly', '& $env:INSTALLER -CheckOnly -Package $env:PACKAGE'))
        self.env['PACKAGE']=str(archive)
        r=self.run_check();self.assertEqual(r.returncode,0,r.stderr)
        self.assertFalse(self.requests.exists())

    def test_checksum_mismatch_is_rejected(self):
        (self.root/'Visto-Server_1.0.3_windows-x64.zip.sha256').write_text('0'*64+'  Visto-Server_1.0.3_windows-x64.zip\n')
        r=self.run_check();self.assertNotEqual(r.returncode,0)
        self.assertNotIn('已检查 Visto',r.stdout)

    def test_zip_traversal_duplicates_and_normalized_paths_are_rejected(self):
        for path in ['../escape','bin/../escape','BIN/VISTO-CORE.EXE','dir../file','bin/file:stream']:
            self.make_package(extra=path)
            r=self.run_check();self.assertNotEqual(r.returncode,0,path)
            self.assertNotIn('已检查 Visto',r.stdout)

    def test_missing_runtime_and_mismatched_version_are_rejected(self):
        self.make_package(omit='runtime/ffmpeg/ffprobe.exe')
        self.assertNotEqual(self.run_check().returncode,0)
        self.make_package(metadata='1.0.4')
        self.assertNotEqual(self.run_check().returncode,0)

    def test_prerelease_and_missing_latest_platform_stop_before_download(self):
        for change in [{'prerelease':True},{'assets':[]}]:
            release=json.loads((self.root/'release.json').read_text());release.update(change)
            (self.root/'release.json').write_text(json.dumps(release))
            r=self.run_check();self.assertNotEqual(r.returncode,0)
            self.assertNotIn('/releases/download/',self.requests.read_text())
            self.make_package()


if __name__=='__main__':unittest.main()
