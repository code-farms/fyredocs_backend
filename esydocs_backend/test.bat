@echo off
setlocal enabledelayedexpansion
REM Run Go tests for esydocs_backend services.
REM
REM Usage:
REM   test.bat              - run all services
REM   test.bat -v           - run all services (verbose)
REM   test.bat shared       - run only the shared package tests
REM   test.bat -v api-gateway auth-service  - verbose, multiple services

cd /d "%~dp0"

set "VERBOSE="
set "HAS_TARGETS=0"
set "FAIL=0"

REM Parse -v flag and collect targets
set "TARGETS="
for %%a in (%*) do (
    if "%%a"=="-v" (
        set "VERBOSE=-v"
    ) else if "%%a"=="--verbose" (
        set "VERBOSE=-v"
    ) else if "%%a"=="-h" (
        goto :help
    ) else if "%%a"=="--help" (
        goto :help
    ) else (
        set "TARGETS=!TARGETS! %%a"
        set "HAS_TARGETS=1"
    )
)

if "!HAS_TARGETS!"=="0" (
    set "TARGETS=shared api-gateway auth-service job-service convert-to-pdf convert-from-pdf organize-pdf optimize-pdf cleanup-worker"
)

for %%s in (!TARGETS!) do (
    echo --- %%s ---
    go test !VERBOSE! ./%%s/...
    if errorlevel 1 set "FAIL=1"
)

echo.
if "!FAIL!"=="1" (
    echo SOME TESTS FAILED
    exit /b 1
)
echo ALL TESTS PASSED
exit /b 0

:help
echo Usage: %~nx0 [-v^|--verbose] [service ...]
echo.
echo Services: shared api-gateway auth-service job-service
echo           convert-to-pdf convert-from-pdf organize-pdf optimize-pdf
echo           cleanup-worker
echo.
echo Examples:
echo   %~nx0                     test everything
echo   %~nx0 shared              test shared only
echo   %~nx0 -v api-gateway      verbose, api-gateway only
exit /b 0
