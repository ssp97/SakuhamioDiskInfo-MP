@echo off
chcp 65001 >nul
echo Building CrystalDiskInfo MP...

go build -trimpath -ldflags="-s -w" -o ./bin/cdi-mp.exe ./cmd/cdi-mp/

if %errorlevel% equ 0 (
    echo Build successful: bin\cdi-mp.exe
) else (
    echo Build failed!
    exit /b 1
)
