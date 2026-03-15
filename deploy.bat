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

REM Check if openssl is available (from Git Bash)
where openssl >nul 2>nul
if %ERRORLEVEL% NEQ 0 (
    echo %YELLOW%Warning: openssl not found in PATH%NC%
    echo %YELLOW%Trying to use Git Bash openssl...%NC%
    set "OPENSSL=C:\Program Files\Git\usr\bin\openssl.exe"
    if not exist "!OPENSSL!" (
        echo %RED%Error: OpenSSL not found. Please install Git for Windows or OpenSSL%NC%
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
    echo %YELLOW%IMPORTANT: Keep this file secure and don't commit it to git!%NC%
)

REM Validate JWT secret length
set "SECRET_LEN=0"
for /l %%i in (0,1,100) do if "!JWT_HS256_SECRET:~%%i,1!" neq "" set /a SECRET_LEN+=1

if %SECRET_LEN% LSS 32 (
    echo %RED%Error: JWT secret is too short (must be at least 32 characters)%NC%
    exit /b 1
)

echo %GREEN%JWT secret validated (%SECRET_LEN% characters)%NC%

REM Export the JWT secret for Docker Compose
set JWT_HS256_SECRET=%JWT_HS256_SECRET%

echo.
echo %BLUE%==============================================================
echo ============== Stopping existing containers...
echo ==============================================================%NC%
docker compose down --remove-orphans 2>nul
echo %GREEN%Stopped existing containers%NC%

echo.
echo %BLUE%==============================================================
echo ============== Building and starting all services...
echo ==============================================================%NC%
docker compose up -d --build

echo.
echo %BLUE%==============================================================
echo ============== Waiting for services to be ready...
echo ==============================================================%NC%

echo Waiting for database...
timeout /t 10 /nobreak >nul
echo %GREEN%Database should be ready%NC%

echo Waiting for Redis...
timeout /t 5 /nobreak >nul
echo %GREEN%Redis should be ready%NC%

echo Waiting for API Gateway...
timeout /t 10 /nobreak >nul
echo %GREEN%API Gateway should be ready%NC%

echo Waiting for Upload Service...
timeout /t 10 /nobreak >nul
echo %GREEN%Upload Service should be ready%NC%

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
echo   📤 Upload Service:     http://localhost:8081
echo   🗄️  PostgreSQL:         localhost:5432
echo   🔴 Redis:              localhost:6379
echo.
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo 🔧 Useful Commands:
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo   View logs:             docker compose logs -f
echo   View specific service: docker compose logs -f api-gateway
echo   Restart services:      docker compose restart
echo   Stop all:              docker compose down
echo   Remove all data:       docker compose down -v
echo.
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo 🔐 Security Info:
echo ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
echo   JWT Secret stored in:  %JWT_SECRET_FILE%
echo   ⚠️  Keep this file secure and never commit it!
echo   ⚠️  For production, use proper secret management
echo.

pause
