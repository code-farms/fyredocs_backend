@echo off
setlocal enabledelayedexpansion

REM Colors for Windows (using ANSI escape codes)
set "RED=[91m"
set "GREEN=[92m"
set "YELLOW=[93m"
set "BLUE=[94m"
set "NC=[0m"

echo.
echo %BLUE%==============================================================
echo ============== Checking requirements...
echo ==============================================================%NC%

REM Check if Docker is installed
where docker >nul 2>nul
if %ERRORLEVEL% NEQ 0 (
    echo %RED%Error: Docker is not installed or not in PATH%NC%
    exit /b 1
)

REM Check if openssl is available
where openssl >nul 2>nul
if %ERRORLEVEL% NEQ 0 (
    echo %YELLOW%Warning: openssl not found in PATH%NC%
    echo %YELLOW%Trying to use Git Bash openssl...%NC%
    set "OPENSSL=C:\Program Files\Git\usr\bin\openssl.exe"
    if not exist "!OPENSSL!" (
        echo %RED%Error: OpenSSL not found. Please install Git for Windows%NC%
        exit /b 1
    )
) else (
    set "OPENSSL=openssl"
)

REM Generate or load JWT secret
set "JWT_SECRET_FILE=.jwt_secret"
if exist "%JWT_SECRET_FILE%" (
    echo %YELLOW%Found existing JWT secret in %JWT_SECRET_FILE%%NC%
    set /p JWT_HS256_SECRET=<"%JWT_SECRET_FILE%"
    echo %GREEN%Loaded JWT secret from file%NC%
) else (
    echo %BLUE%Generating new JWT secret...%NC%
    for /f %%i in ('"%OPENSSL%" rand -hex 32') do set JWT_HS256_SECRET=%%i
    echo !JWT_HS256_SECRET!>"%JWT_SECRET_FILE%"
    echo %GREEN%Generated and saved new JWT secret to %JWT_SECRET_FILE%%NC%
)

REM Validate JWT secret length
set "SECRET_LEN=0"
set "TEMP_SECRET=!JWT_HS256_SECRET!"
:loop
if not "!TEMP_SECRET!"=="" (
    set /a SECRET_LEN+=1
    set "TEMP_SECRET=!TEMP_SECRET:~1!"
    goto loop
)
if %SECRET_LEN% LSS 32 (
    echo %RED%Error: JWT secret is too short%NC%
    exit /b 1
)

echo %GREEN%JWT secret validated (%SECRET_LEN% characters)%NC%

echo.
echo %BLUE%==============================================================
echo ============== Stopping existing containers...
echo ==============================================================%NC%
docker compose down --remove-orphans 2>nul
echo %GREEN%Stopped existing containers%NC%

echo.
echo %BLUE%==============================================================
echo ============== Building services one-by-one (CPU Safety)
echo ==============================================================%NC%

REM Set BuildKit for Windows
set DOCKER_BUILDKIT=1

REM List of services to build sequentially
set "SERVICES=api-gateway auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf cleanup-worker"

for %%s in (%SERVICES%) do (
    echo %YELLOW%🔨 Building %%s...%NC%
    docker compose build %%s
    if !ERRORLEVEL! NEQ 0 (
        echo %RED%Error building %%s. Build aborted.%NC%
        exit /b 1
    )
    echo %GREEN%✓ %%s build complete%NC%
)

echo.
echo %BLUE%==============================================================
echo ============== Starting all services...
echo ==============================================================%NC%
docker compose up -d

echo.
echo %BLUE%==============================================================
echo ============== Waiting for services to be ready...
echo ==============================================================%NC%

echo Waiting for database...
timeout /t 15 /nobreak >nul
docker compose exec -T db pg_isready -U user -d esydocs

echo Waiting for API Gateway...
timeout /t 10 /nobreak >nul

echo.
echo %BLUE%==============================================================
echo ============== Service Status
echo ==============================================================%NC%
docker compose ps

echo.
echo %GREEN%All services started successfully!%NC%
echo.
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo 📋 Service Endpoints:
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo   🌐 API Gateway:        http://localhost:8080
echo   📤 Upload/Job Service: http://localhost:8081
echo   📄 Worker Endpoints:   8082, 8083, 8084, 8085
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

pause