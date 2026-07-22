@echo off
setlocal

"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" ^
  -NoLogo ^
  -NoProfile ^
  -NonInteractive ^
  -ExecutionPolicy Bypass ^
  -File "%~dp0ensure-revyl.ps1" ^
  -InstallRoot "%~dp0.."

if errorlevel 1 echo {}
exit /b 0
