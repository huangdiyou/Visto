#!/usr/bin/env python3
"""Portable module tests; scheduled tasks and native ACL remain Windows acceptance."""
import os
import json
import shutil
import tempfile
import socket
from pathlib import Path
import subprocess
import unittest
ROOT = Path(__file__).resolve().parents[2]
PWSH = os.environ.get('VISTO_TEST_PWSH', 'pwsh')
class Readiness(unittest.TestCase):
    def run_check(self, mode):
        script = r'''
$ErrorActionPreference='Stop'
$global:count=0
function global:Start-Sleep { param($Seconds) }
function global:Invoke-RestMethod {
 param($Uri,$TimeoutSec)
 $global:count++
 if ($env:MODE -eq 'delayed' -and $global:count -lt 3) {throw 'connection refused'}
 return @{status='ready';dependencies=@{ffmpeg=($env:MODE -ne 'missing-video');ffprobe=$true}}
}
function global:Invoke-WebRequest {
 param($Uri,$TimeoutSec,[switch]$UseBasicParsing)
 return @{Content=$(if ($env:MODE -eq 'directory-listing') {'<a>index.html</a>'} else {'<div id="root"></div>'})}
}
Import-Module $env:MODULE -Force
Wait-VistoInstallationReady -Address '127.0.0.1:8799'
Write-Output "ready attempts=$global:count"
'''
        env=os.environ.copy();env.update(MODE=mode,MODULE=str(ROOT/'scripts/server-win/VistoServer.Install.psm1'))
        return subprocess.run([PWSH,'-NoProfile','-Command',script],env=env,capture_output=True,text=True)
    def test_ready_and_delayed_start(self):
        for mode in ['ready','delayed']:
            r=self.run_check(mode);self.assertEqual(r.returncode,0,r.stderr)
            if mode=='delayed':self.assertIn('attempts=3',r.stdout)
    def test_directory_listing_is_failure(self):
        r=self.run_check('directory-listing');self.assertNotEqual(r.returncode,0);self.assertIn('-Repair',r.stderr)
    def test_missing_video_is_failure(self):
        r=self.run_check('missing-video');self.assertNotEqual(r.returncode,0)
    def test_task_repair_preserves_identity_and_rejects_changed_material(self):
        with tempfile.TemporaryDirectory() as directory:
            root=Path(directory);package=root/'package';package.mkdir();data=root/'data'
            for name in ['Install-VistoServer.ps1','Start-VistoServer.ps1','VistoServer.Install.psm1']:
                shutil.copy2(ROOT/'scripts/server-win'/name,package/name)
            installer=package/'Install-VistoServer.ps1'
            body=installer.read_text(encoding='utf-8-sig')
            start=body.index('$administrator = ');end=body.index('$PackageRoot = ',start)
            installer.write_text(body[:start]+'# Portable harness supplies elevated identity; no OS task/ACL API is used.\n'+body[end:],encoding='utf-8-sig')
            (package/'bin').mkdir()
            (package/'web').mkdir();(package/'web/index.html').write_text('<div id="root"></div>')
            (package/'runtime').mkdir();(package/'runtime/ffmpeg.exe').write_text('fixture')
            for name in ['visto-core.exe','visto-server.exe']:(package/'bin'/name).write_text('fixture')
            with socket.socket() as sock:
                sock.bind(('127.0.0.1',0));address='127.0.0.1:'+str(sock.getsockname()[1])
            (package/'visto-server.json').write_text(json.dumps({'address':address,'dataDir':str(data),'webDir':'web','version':'1.0.4-install-preview.3'}))
            (package/'VistoServer.Security.psm1').write_text("function Protect-VistoManagedDirectory {param($Path) New-Item -ItemType Directory -Force $Path|Out-Null; return $Path}; function Protect-VistoSecretFile {param($Path)}; Export-ModuleMember -Function *")
            wrapper=root/'run.ps1'
            wrapper.write_text(r'''
$ErrorActionPreference='Stop'
function global:Get-ScheduledTask {param($TaskName,$ErrorAction)
 if ($env:EXISTING -eq '1') {
  return @{State='Ready';Actions=@(@{Execute='powershell.exe';Arguments=("-NoProfile -ExecutionPolicy Bypass -File `""+(Join-Path $env:PACKAGE 'Start-VistoServer.ps1')+"`" -Foreground")})}
 }
 return $null
}
function global:New-ScheduledTaskAction {param($Execute,$Argument) return @{}}
function global:New-ScheduledTaskTrigger {param([switch]$AtStartup) return @{}}
function global:New-ScheduledTaskSettingsSet {param($RestartCount,$RestartInterval,[switch]$StartWhenAvailable,$ExecutionTimeLimit) return @{}}
function global:New-ScheduledTaskPrincipal {param($UserId,$LogonType,$RunLevel) return @{}}
function global:Register-ScheduledTask {param($TaskName,$Action,$Trigger,$Settings,$Principal,$Description) return @{}}
function global:Start-ScheduledTask {param($TaskName)}
function global:Stop-ScheduledTask {param($TaskName,$ErrorAction)}
function global:Start-Sleep {param($Seconds)}
function global:Invoke-RestMethod {param($Uri,$TimeoutSec) return @{status='ready';dependencies=@{ffmpeg=$true;ffprobe=$true}}}
function global:Invoke-WebRequest {param($Uri,$TimeoutSec,[switch]$UseBasicParsing) return @{Content='<div id="root"></div>'}}
& (Join-Path $env:PACKAGE 'Install-VistoServer.ps1') -Repair:($env:REPAIR -eq '1')
''')
            env=os.environ.copy();env.update(PACKAGE=str(package),EXISTING='0',REPAIR='0')
            def run():return subprocess.run([PWSH,'-NoProfile','-File',str(wrapper)],env=env,capture_output=True,text=True)
            first=run();self.assertEqual(first.returncode,0,first.stderr)
            config=(package/'visto-server.json').read_bytes();token=(data/'host-management-token.txt').read_bytes()
            env['EXISTING']='1'
            blocked=run();self.assertNotEqual(blocked.returncode,0);self.assertIn('already exists',blocked.stderr)
            env['REPAIR']='1'
            repaired=run();self.assertEqual(repaired.returncode,0,repaired.stderr)
            self.assertEqual(config,(package/'visto-server.json').read_bytes());self.assertEqual(token,(data/'host-management-token.txt').read_bytes())
            (package/'bin/visto-core.exe').write_text('changed')
            changed=run();self.assertNotEqual(changed.returncode,0);self.assertIn('changed since installation',changed.stderr)
            self.assertEqual(token,(data/'host-management-token.txt').read_bytes())

    def test_all_scripts_parse(self):
        script=r'''$bad=@(); Get-ChildItem $env:SOURCE -Include *.ps1,*.psm1 -Recurse | ForEach-Object {$t=$null;$e=$null;[System.Management.Automation.Language.Parser]::ParseFile($_.FullName,[ref]$t,[ref]$e)|Out-Null;$bad+=@($e)}; if ($bad.Count) {throw ($bad|Out-String)}'''
        env=os.environ.copy();env['SOURCE']=str(ROOT/'scripts/server-win')
        r=subprocess.run([PWSH,'-NoProfile','-Command',script],env=env,capture_output=True,text=True);self.assertEqual(r.returncode,0,r.stderr)
if __name__=='__main__':unittest.main()
