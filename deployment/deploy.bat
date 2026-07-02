@echo off
setlocal enabledelayedexpansion

REM Resolve paths: script lives in deployment/, project root is one level up
set "SCRIPT_DIR=%~dp0"
set "ROOT_DIR=%SCRIPT_DIR%.."
cd /d "%ROOT_DIR%"

REM Set compose file path
set "COMPOSE_FILE=%SCRIPT_DIR%docker-compose.yml"

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
set "SERVICES=api-gateway analytics-service auth-service job-service convert-from-pdf convert-to-pdf organize-pdf optimize-pdf document-service user-service notification-service cleanup-worker"

for %%s in (%SERVICES%) do (
    echo %YELLOW%Building %%s...%NC%
    docker compose build %%s
    if !ERRORLEVEL! NEQ 0 (
        echo %RED%Error building %%s. Build aborted.%NC%
        exit /b 1
    )
    echo %GREEN%%%s build complete%NC%
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
echo Service Endpoints:
echo   App (Caddy edge):     http://localhost  (SPA + APIs under /api, /auth, /admin)
echo   API Gateway:          internal only (caddy -^> api-gateway:8080)
echo   Auth Service:         internal only (auth-service:8086)
echo   Job Service:          internal only (job-service:8081)
echo   PDF Workers:          internal only (convert-from-pdf:8082, convert-to-pdf:8083, organize-pdf:8084, optimize-pdf:8085)
echo   Analytics:            internal only (analytics-service:8087)
echo   Document Service:     internal only (document-service:8089)
echo   User Service:         internal only (user-service:8090)
echo   Notification Service: internal only (notification-service:8091)
echo   Cleanup Worker:       internal only (cleanup-worker:8088, background)
echo   MinIO (S3):           internal (minio:9000); console http://127.0.0.1:9001
echo   NATS / Redis:         internal only (nats:4222 / redis:6379)

pause
