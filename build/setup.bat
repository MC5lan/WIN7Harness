@echo off
rem ============================================================
rem  WIN7 Harness - installer (no external tools required)
rem  Copies the right binary for this machine, creates shortcuts,
rem  registers an uninstall entry under HKCU. Nothing else is touched.
rem ============================================================
setlocal EnableExtensions
title WIN7 Harness Setup
color 0F

set "APPNAME=WIN7 Harness"
set "APPID=Win7Harness"
set "VER=__VERSION__"
set "SRCDIR=%~dp0"

echo.
echo   WIN7 Harness v%VER%  -  Setup
echo   ---------------------------------------------
echo.

rem ---------- 1. pick the matching binary ----------
set "EXE=WIN7Harness-386.exe"
if /i "%PROCESSOR_ARCHITECTURE%"=="AMD64" set "EXE=WIN7Harness-amd64.exe"
if defined PROCESSOR_ARCHITEW6432 set "EXE=WIN7Harness-amd64.exe"
if not exist "%SRCDIR%%EXE%" (
  if exist "%SRCDIR%WIN7Harness-386.exe" set "EXE=WIN7Harness-386.exe"
)
if not exist "%SRCDIR%%EXE%" goto :noexe
echo   [1/4] binary: %EXE%

rem ---------- 2. copy files ----------
rem Keep the program directory separate from the data directory:
rem %LOCALAPPDATA%\Win7Harness holds sessions/logs, so never put the exe there.
set "DEST=%LOCALAPPDATA%\Programs\%APPID%"
if not defined LOCALAPPDATA set "DEST=%APPDATA%\Programs\%APPID%"
if not exist "%DEST%" mkdir "%DEST%" >nul 2>nul
if not exist "%DEST%" (
  echo   [x] cannot create "%DEST%"
  goto :fail
)
if exist "%DEST%\unins000.exe" (
  echo   [x] An installer-based WIN7 Harness installation already exists in:
  echo       "%DEST%"
  echo       Please use WIN7Harness-v*-Setup.exe or Control Panel to upgrade/uninstall.
  echo       setup.bat is only a fallback for machines that cannot run the installer.
  pause
  exit /b 1
)
taskkill /f /im Win7Harness.exe >nul 2>nul
copy /y "%SRCDIR%%EXE%" "%DEST%\Win7Harness.exe" >nul
if errorlevel 1 (
  echo   [x] copy failed. Is WIN7 Harness still running?
  goto :fail
)
if exist "%SRCDIR%win7harness.ico" copy /y "%SRCDIR%win7harness.ico" "%DEST%\win7harness.ico" >nul
if exist "%SRCDIR%README.txt" copy /y "%SRCDIR%README.txt" "%DEST%\README.txt" >nul
if exist "%SRCDIR%version.txt" copy /y "%SRCDIR%version.txt" "%DEST%\version.txt" >nul
rem Old setup.bat installed the exe into the data directory; remove only program files.
set "OLDDIR=%LOCALAPPDATA%\Win7Harness"
if /i not "%DEST%"=="%OLDDIR%" (
  del "%OLDDIR%\Win7Harness.exe" >nul 2>nul
  del "%OLDDIR%\win7harness.ico" >nul 2>nul
  del "%OLDDIR%\README.txt" >nul 2>nul
  del "%OLDDIR%\uninstall.bat" >nul 2>nul
  del "%OLDDIR%\shortcuts.txt" >nul 2>nul
)
echo   [2/4] installed to: %DEST%

rem ---------- 3. shortcuts ----------
rem Use WScript.Shell SpecialFolders so a OneDrive-redirected desktop
rem (common on Win10/11) still gets the shortcut in the right place.
set "VBS=%TEMP%\wh_mklink.vbs"
set "LNKS=%DEST%\shortcuts.txt"
del "%LNKS%" >nul 2>nul
> "%VBS%" echo Set sh = CreateObject("WScript.Shell")
>>"%VBS%" echo Set fso = CreateObject("Scripting.FileSystemObject")
>>"%VBS%" echo t = WScript.Arguments(0)
>>"%VBS%" echo w = WScript.Arguments(1)
>>"%VBS%" echo i = WScript.Arguments(2)
>>"%VBS%" echo l = WScript.Arguments(3)
>>"%VBS%" echo If l = "@desktop" Then l = sh.SpecialFolders("Desktop") ^& "\WIN7 Harness.lnk"
>>"%VBS%" echo If l = "@programs" Then l = sh.SpecialFolders("Programs") ^& "\WIN7 Harness.lnk"
>>"%VBS%" echo Set s = sh.CreateShortcut(l)
>>"%VBS%" echo s.TargetPath = t
>>"%VBS%" echo s.WorkingDirectory = w
>>"%VBS%" echo If fso.FileExists(i) Then s.IconLocation = i
>>"%VBS%" echo s.Description = "WIN7 Harness"
>>"%VBS%" echo s.Save
>>"%VBS%" echo Set lg = fso.OpenTextFile(WScript.Arguments(4), 8, True)
>>"%VBS%" echo lg.WriteLine l
>>"%VBS%" echo lg.Close
cscript //nologo "%VBS%" "%DEST%\Win7Harness.exe" "%DEST%" "%DEST%\win7harness.ico" "@desktop" "%LNKS%" >nul 2>nul
cscript //nologo "%VBS%" "%DEST%\Win7Harness.exe" "%DEST%" "%DEST%\win7harness.ico" "@programs" "%LNKS%" >nul 2>nul
del "%VBS%" >nul 2>nul
echo   [3/4] shortcuts: desktop + start menu

rem ---------- 4. uninstaller ----------
set "UNINS=%DEST%\uninstall.bat"
> "%UNINS%" echo @echo off
>>"%UNINS%" echo setlocal EnableExtensions
>>"%UNINS%" echo title WIN7 Harness Uninstall
>>"%UNINS%" echo echo.
>>"%UNINS%" echo echo   Removing WIN7 Harness ...
>>"%UNINS%" echo taskkill /f /im Win7Harness.exe ^>nul 2^>nul
>>"%UNINS%" echo if exist "%LNKS%" for /f "usebackq delims=" %%%%p in ("%LNKS%") do del "%%%%p" ^>nul 2^>nul
>>"%UNINS%" echo del "%USERPROFILE%\Desktop\WIN7 Harness.lnk" ^>nul 2^>nul
>>"%UNINS%" echo del "%APPDATA%\Microsoft\Windows\Start Menu\Programs\WIN7 Harness.lnk" ^>nul 2^>nul
>>"%UNINS%" echo reg delete "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /f ^>nul 2^>nul
>>"%UNINS%" echo cd /d "%%TEMP%%"
>>"%UNINS%" echo echo   Removing program files ...
>>"%UNINS%" echo del "%DEST%\Win7Harness.exe" ^>nul 2^>nul
>>"%UNINS%" echo del "%DEST%\win7harness.ico" ^>nul 2^>nul
>>"%UNINS%" echo del "%DEST%\README.txt" ^>nul 2^>nul
>>"%UNINS%" echo del "%DEST%\version.txt" ^>nul 2^>nul
>>"%UNINS%" echo del "%DEST%\shortcuts.txt" ^>nul 2^>nul
>>"%UNINS%" echo del "%DEST%\uninstall.bat" ^>nul 2^>nul
>>"%UNINS%" echo echo   Note: your conversations and config are kept.
>>"%UNINS%" echo echo   Config: %APPDATA%\WIN7Harness
>>"%UNINS%" echo echo   Sessions/logs: %LOCALAPPDATA%\WIN7Harness
>>"%UNINS%" echo echo   Delete those folders by hand if you want a clean wipe.
>>"%UNINS%" echo ping -n 2 127.0.0.1 ^>nul
>>"%UNINS%" echo exit

reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v DisplayName /t REG_SZ /d "%APPNAME%" /f >nul
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v DisplayVersion /t REG_SZ /d "%VER%" /f >nul
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v Publisher /t REG_SZ /d "WIN7 Harness" /f >nul
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v InstallLocation /t REG_SZ /d "%DEST%" /f >nul
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v DisplayIcon /t REG_SZ /d "%DEST%\win7harness.ico" /f >nul
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v UninstallString /t REG_SZ /d "%DEST%\uninstall.bat" /f >nul
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v NoModify /t REG_DWORD /d 1 /f >nul
reg add "HKCU\Software\Microsoft\Windows\CurrentVersion\Uninstall\%APPID%" /v NoRepair /t REG_DWORD /d 1 /f >nul
echo   [4/4] uninstaller: "%DEST%\uninstall.bat"

echo.
echo   Done. Launching WIN7 Harness ...
echo.
echo   Tip: run "%DEST%\Win7Harness.exe" -selftest once to verify
echo        this machine (a report lands in %%APPDATA%%\Win7Harness\selftest\reports).
start "" "%DEST%\Win7Harness.exe"
ping -n 2 127.0.0.1 >nul
exit /b 0

:noexe
echo   [x] WIN7Harness-*.exe not found next to setup.bat.
echo       Unzip the whole package first, then run setup.bat again.
echo.
pause
exit /b 1

:fail
echo.
pause
exit /b 1
