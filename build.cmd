@echo off
rem Double-click to build on Windows: runs build.sh with Git Bash.
setlocal
set "BASH="
for /f "delims=" %%G in ('where git 2^>nul') do (
  if not defined BASH if exist "%%~dpG..\bin\bash.exe" set "BASH=%%~dpG..\bin\bash.exe"
)
if not defined BASH if exist "%ProgramFiles%\Git\bin\bash.exe" set "BASH=%ProgramFiles%\Git\bin\bash.exe"
if not defined BASH (
  echo Git Bash not found. Install Git for Windows: https://git-scm.com/download/win
  pause
  exit /b 1
)
"%BASH%" "%~dp0build.sh"
set "RC=%ERRORLEVEL%"
pause
exit /b %RC%
