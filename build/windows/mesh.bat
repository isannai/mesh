@echo off
setlocal

REM ===========================================================================
REM  build\windows\mesh.bat  <version>
REM
REM  iSANN Mesh app build (Windows). Self-contained zips under
REM  build\windows\out-mesh\ :
REM    station-windows-amd64.zip  mesh.json + bin\station.exe + conf\station.json
REM    chatbot-windows-amd64.zip  mesh.json + bin\chatbot.exe + conf + docs + public
REM    control-windows-amd64.zip  mesh.json + bin\control.exe + conf + web\control\build
REM
REM  Requires Node/npm (control SPA). Version via ldflags. REQUIRED.
REM    usage:  build\windows\mesh.bat 0.1.1
REM ===========================================================================

if not "%~1"=="" goto :have_version
echo ERROR: version required ^(no build^).
echo   usage:  build\windows\mesh.bat ^<version^>
exit /b 1

:have_version
set "VER=%~1"
cd /d "%~dp0..\.."

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0

REM Station/ControlVersion live in pkg/setup, pulled from GLink via the module
REM replace; ldflags target the real import path so -X resolves as in GLink.
set "PKG=github.com/daesob/http3proxy/pkg/setup"
set "LDFLAGS=-X %PKG%.StationVersion=%VER% -X %PKG%.ControlVersion=%VER%"

set "OUT=build\windows\out-mesh"

if exist "%OUT%" rmdir /s /q "%OUT%"
mkdir "%OUT%"

echo === iSANN mesh build   v%VER%   (windows/amd64) ===
echo.

echo [1/3] station (mesh, zip)...
set "STN=%OUT%\station"
mkdir "%STN%\bin"
mkdir "%STN%\conf"
go build -ldflags "%LDFLAGS%" -o "%STN%\bin\station.exe" ./cmd/station/
if errorlevel 1 goto :error
copy /Y "apps\station\conf\station.json" "%STN%\conf\" >nul
if errorlevel 1 goto :error
copy /Y "apps\station\mesh.json" "%STN%\" >nul
if errorlevel 1 goto :error
powershell -NoProfile -Command "Compress-Archive -Path '%STN%\*' -DestinationPath '%OUT%\station-windows-amd64.zip' -Force"
if errorlevel 1 goto :error

echo [2/3] chatbot (mesh, zip)...
set "CBT=%OUT%\chatbot"
mkdir "%CBT%\bin"
go build -ldflags "-s -w" -o "%CBT%\bin\chatbot.exe" ./cmd/chatbot/
if errorlevel 1 goto :error
xcopy /E /I /Y /Q "apps\chatbot\conf" "%CBT%\conf" >nul
if errorlevel 1 goto :error
copy /Y "apps\chatbot\mesh.json" "%CBT%\" >nul
if errorlevel 1 goto :error
copy /Y "apps\chatbot\README.md" "%CBT%\" >nul
if errorlevel 1 goto :error
xcopy /E /I /Y /Q "apps\chatbot\docs" "%CBT%\docs" >nul
if errorlevel 1 goto :error
xcopy /E /I /Y /Q "apps\chatbot\public" "%CBT%\public" >nul
if errorlevel 1 goto :error
powershell -NoProfile -Command "Compress-Archive -Path '%CBT%\*' -DestinationPath '%OUT%\chatbot-windows-amd64.zip' -Force"
if errorlevel 1 goto :error

echo [3/3] control (mesh + web, zip)...
set "CTL=%OUT%\control"
mkdir "%CTL%\bin"
mkdir "%CTL%\conf"
go build -ldflags "%LDFLAGS%" -o "%CTL%\bin\control.exe" ./cmd/control/
if errorlevel 1 goto :error
pushd web\control
if not exist "node_modules" call npm install
if not errorlevel 1 call npm run build
set "WEBERR=%errorlevel%"
popd
if not "%WEBERR%"=="0" goto :error
copy /Y "apps\control\conf\control.json" "%CTL%\conf\" >nul
if errorlevel 1 goto :error
copy /Y "apps\control\mesh.json" "%CTL%\" >nul
if errorlevel 1 goto :error
xcopy /E /I /Y /Q "web\control\build" "%CTL%\web\control\build" >nul
if errorlevel 1 goto :error
powershell -NoProfile -Command "Compress-Archive -Path '%CTL%\*' -DestinationPath '%OUT%\control-windows-amd64.zip' -Force"
if errorlevel 1 goto :error

echo.
echo ===========================================================================
echo  mesh build complete   v%VER%   at   %OUT%\
echo    station-windows-amd64.zip  (mesh + bin + conf)
echo    chatbot-windows-amd64.zip  (mesh + bin + conf + docs + public)
echo    control-windows-amd64.zip  (mesh + bin + conf + web)
echo ===========================================================================
goto :end

:error
echo.
echo *** BUILD FAILED ***
exit /b 1

:end
endlocal
