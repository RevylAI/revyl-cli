@echo off
setlocal

"%SystemRoot%\System32\WindowsPowerShell\v1.0\powershell.exe" ^
  -NoLogo ^
  -NoProfile ^
  -NonInteractive ^
  -ExecutionPolicy Bypass ^
  -File "%~dp0launch-revyl.ps1" %*

exit /b %ERRORLEVEL%
