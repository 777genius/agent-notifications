@echo off
rem codex-hook-wrapper.cmd - dedicated Codex launcher (Windows).
rem Windows support for the Codex route is NOT declared until the disposable
rem Windows scenario passes; this file ships so the frozen commandWindows
rem identity stays stable from the first release.
rem Observation contract: always exit 0 with empty output.
rem No delayed expansion: every variable is read outside the block that set
rem it, and plain %VAR% expansion keeps paths containing "!" intact.
setlocal
set "CN_PRODUCT=codex"
if not defined PLUGIN_ROOT set "PLUGIN_ROOT=%~dp0.."

set "CN_BIN="
for %%F in ("%~dp0claude-notifications-windows-*.exe") do (
    set "CN_BIN=%%~fF"
    goto :found
)
exit /b 0

:found
rem Old-binary guard (mirrors bin/codex-hook-wrapper.sh, CN_CODEX_MIN_VERSION
rem 1.42.0): pre-Codex binaries silently ignore "--product codex" and would
rem misroute the payload into the Claude decoder. Version output looks like
rem "claude-notifications v1.42.0"; on any parse failure stay silent.
set "CN_BINVER="
for /f "usebackq tokens=2 delims=v" %%V in (`"%CN_BIN%" version 2^>nul`) do set "CN_BINVER=%%V"
if not defined CN_BINVER exit /b 0
set "CN_MAJOR="
set "CN_MINOR="
for /f "tokens=1,2 delims=." %%a in ("%CN_BINVER%") do (
    set "CN_MAJOR=%%a"
    set "CN_MINOR=%%b"
)
if not defined CN_MAJOR exit /b 0
if not defined CN_MINOR exit /b 0
if %CN_MAJOR% GTR 1 goto :run
if %CN_MAJOR% LSS 1 exit /b 0
if %CN_MINOR% LSS 42 exit /b 0

:run
"%CN_BIN%" %* >nul 2>nul
exit /b 0
