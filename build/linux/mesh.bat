@echo off
setlocal

REM ===========================================================================
REM  build\linux\mesh.bat  <version>   (cross-compiled from Windows, GOOS=linux)
REM
REM  iSANN Mesh app build (Linux). Self-contained tarballs under
REM  build\linux\out-mesh\ :
REM    station-linux-amd64.tar.gz  mesh.json + bin/station + conf/station.json
REM    chatbot-linux-amd64.tar.gz  mesh.json + bin/chatbot + conf + docs + public
REM    control-linux-amd64.tar.gz  mesh.json + bin/control + conf + web/control/build
REM
REM  .tar.gz (not .zip): tar is on every Linux box (no unzip needed) and preserves
REM  the exec bit on bin/<svc>. `isann app pull` content-sniffs gzip magic, so it
REM  unpacks these regardless of the published url's extension.
REM
REM  Requires Node/npm (control SPA). CGO_ENABLED=0. Version via ldflags. REQUIRED.
REM    usage:  build\linux\mesh.bat 0.1.1
REM ===========================================================================

if not "%~1"=="" goto :have_version
echo ERROR: version required ^(no build^).
echo   usage:  build\linux\mesh.bat ^<version^>
exit /b 1

:have_version
set "VER=%~1"
cd /d "%~dp0..\.."

set GOOS=linux
set GOARCH=amd64
set CGO_ENABLED=0

REM Station/ControlVersion live in pkg/setup, pulled from GLink via the module
REM replace; ldflags target the real import path so -X resolves as in GLink.
set "PKG=github.com/isannai/mesh/pkg/setup"
set "LDFLAGS=-X %PKG%.StationVersion=%VER% -X %PKG%.ControlVersion=%VER%"

set "OUT=build\linux\out-mesh"

if exist "%OUT%" rmdir /s /q "%OUT%"
mkdir "%OUT%"

echo === iSANN mesh build   v%VER%   (linux/amd64) ===
echo.

echo [1/3] station (mesh, tar.gz)...
set "STN=%OUT%\station"
mkdir "%STN%\bin"
mkdir "%STN%\conf"
go build -ldflags "%LDFLAGS%" -o "%STN%\bin\station" ./cmd/station/
if errorlevel 1 goto :error
copy /Y "apps\station\conf\station.json" "%STN%\conf\" >nul
if errorlevel 1 goto :error
copy /Y "apps\station\mesh.json" "%STN%\" >nul
if errorlevel 1 goto :error
tar -czf "%OUT%\station-linux-amd64.tar.gz" -C "%STN%" .
if errorlevel 1 goto :error

echo [2/3] chatbot (mesh, tar.gz)...
set "CBT=%OUT%\chatbot"
mkdir "%CBT%\bin"
go build -ldflags "-s -w" -o "%CBT%\bin\chatbot" ./cmd/chatbot/
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
tar -czf "%OUT%\chatbot-linux-amd64.tar.gz" -C "%CBT%" .
if errorlevel 1 goto :error

echo [3/3] control (mesh + web, tar.gz)...
set "CTL=%OUT%\control"
mkdir "%CTL%\bin"
mkdir "%CTL%\conf"
go build -ldflags "%LDFLAGS%" -o "%CTL%\bin\control" ./cmd/control/
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
tar -czf "%OUT%\control-linux-amd64.tar.gz" -C "%CTL%" .
if errorlevel 1 goto :error

echo.
echo === probe (mesh, tar.gz) ===
REM The faucet prober. No isannd.servers - it only dials out, so unlike
REM station/control it opens no public listener.
set "PRB=%OUT%\probe"
mkdir "%PRB%\bin"
mkdir "%PRB%\conf"
go build -ldflags "%LDFLAGS%" -o "%PRB%\bin\probe" ./cmd/probe/
if errorlevel 1 goto :error
copy /Y "apps\probe\conf\probe.json" "%PRB%\conf\" >nul
if errorlevel 1 goto :error
copy /Y "apps\probe\mesh.json" "%PRB%\" >nul
if errorlevel 1 goto :error
copy /Y "apps\probe\README.md" "%PRB%\" >nul
if errorlevel 1 goto :error
tar -czf "%OUT%\probe-linux-amd64.tar.gz" -C "%PRB%" .
if errorlevel 1 goto :error

echo.
echo  writing per-app bundle manifests...
call :bundle station "iSANN Station"
call :bundle chatbot "iSANN Chatbot"
call :bundle control "iSANN Control"
call :bundle probe "iSANN Faucet Prober"

echo.
echo ===========================================================================
echo  mesh build complete   v%VER%   at   %OUT%\
echo    station-linux-amd64.tar.gz  (mesh + bin + conf)      + bundle-station.json
echo    chatbot-linux-amd64.tar.gz  (mesh + bin + conf + docs + public) + bundle-chatbot.json
echo    control-linux-amd64.tar.gz  (mesh + bin + conf + web)  + bundle-control.json
echo    probe-linux-amd64.tar.gz    (mesh + bin + conf)      + bundle-probe.json
echo ===========================================================================
goto :end

:error
echo.
echo *** BUILD FAILED ***
exit /b 1

REM ---------------------------------------------------------------------------
REM  :bundle <app> <summary>  — writes bundle-<app>.json next to the archives.
REM  Per-app manifest for `isann app pull`; the {os}-{arch}.{ar} template makes
REM  one file valid for both platforms (windows zip + linux tar.gz).
REM ---------------------------------------------------------------------------
:bundle
set "APP=%~1"
set "SUM=%~2"
> "%OUT%\bundle-%APP%.json" (
  echo {
  echo   "type": "mesh",
  echo   "version": "%VER%",
  echo   "summary": "%SUM%",
  echo   "platforms": [
  echo     { "os": "windows", "arch": "amd64", "ar": "zip" },
  echo     { "os": "linux", "arch": "amd64", "ar": "tar.gz" }
  echo   ],
  echo   "files": [
  echo     { "path": "%APP%-{os}-{arch}.{ar}" }
  echo   ],
  echo   "metadata": { "author": "isann", "name": "%APP%" }
  echo }
)
exit /b 0

:end
endlocal
