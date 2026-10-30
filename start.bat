@echo off
rem ============================================================
rem  start.bat - WorkBuddy2API 启动器入口（Windows）
rem
rem  双击运行，或在 cmd 里执行 .\start.bat
rem  指定运行目录：.\start.bat D:\wb2api
rem
rem  本脚本只负责「找 Go -> 编译启动器 -> 交给启动器」，
rem  其余逻辑（配置/账号自检、端口占用、按需编译、健康检查、
rem  打开面板、Ctrl+C 处理）都在 cmd\launcher 里，三平台共用。
rem ============================================================
chcp 65001 >nul
setlocal

cd /d "%~dp0"

set "TARGET=%~1"
if "%TARGET%"=="" set "TARGET=%CD%"

set "GO_BIN="
if defined WB2API_GO if exist "%WB2API_GO%" set "GO_BIN=%WB2API_GO%"
if not defined GO_BIN for /f "delims=" %%i in ('where go 2^>nul') do if not defined GO_BIN set "GO_BIN=%%i"
if not defined GO_BIN if exist "%ProgramFiles%\Go\bin\go.exe" set "GO_BIN=%ProgramFiles%\Go\bin\go.exe"
if not defined GO_BIN if exist "%LOCALAPPDATA%\Programs\Go\bin\go.exe" set "GO_BIN=%LOCALAPPDATA%\Programs\Go\bin\go.exe"

if not defined GO_BIN (
    echo 未检测到 Go 工具链（编译启动器需要 Go 1.22+）。
    echo   安装: https://go.dev/dl/ 或 winget install GoLang.Go
    echo   已安装但不在 PATH: 设置 WB2API_GO 指向 go.exe 后重试
    pause
    exit /b 1
)

if not exist "bin" mkdir "bin"
set "LAUNCHER=%CD%\bin\wb2api-launcher.exe"

if not exist "%LAUNCHER%" (
    echo 正在编译启动器...
    "%GO_BIN%" build -trimpath -ldflags "-s -w" -o "%LAUNCHER%" .\cmd\launcher
    if errorlevel 1 (
        echo 启动器编译失败，请把上方报错发出来排查。
        pause
        exit /b 1
    )
)

"%LAUNCHER%" -dir "%TARGET%"
if errorlevel 1 pause
