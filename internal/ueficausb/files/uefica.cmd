@echo off
rem Enables the Microsoft UEFI CA key in the BIOS (Lenovo, Dell, HP).
rem All key presses are read here; uefica.vbs only reads the BIOS password.
rem uefica.vbs exits 20 after a change; only that restarts the machine.
setlocal EnableExtensions EnableDelayedExpansion
title Microsoft UEFI CA key
echo Starting, please wait...
wpeinit
wpeutil SetKeyboardLayout 0409:00000409 >nul
set "VBS=%~dp0uefica.vbs"
if exist X:\uefica.txt del /q X:\uefica.txt
cls
cscript //nologo "%VBS%" /check
set CODE=%ERRORLEVEL%
if "%CODE%"=="40" goto confirm
if "%CODE%"=="41" goto confirm
goto finish

:confirm
echo.
echo If this disk has BitLocker and its Windows must keep working, have the
echo recovery key ready: Windows will ask for it once after this change.
echo.
set "ANSWER="
set /p "ANSWER=Enable the Microsoft UEFI CA key now? Type Y or N and press Enter: "
if /i not "!ANSWER!"=="Y" goto declined
if "%CODE%"=="41" goto password
cscript //nologo "%VBS%" /apply
set CODE=%ERRORLEVEL%
if not "%CODE%"=="3" goto finish
echo.
echo The BIOS refused the change without a password.

:password
set TRY=1
:passloop
echo.
echo Type the BIOS administrator (supervisor) password and press Enter.
echo Attempt %TRY% of 2. The screen is cleared after you press Enter.
cscript //nologo "%VBS%" /apply /password
set CODE=%ERRORLEVEL%
cls
if exist X:\uefica.txt type X:\uefica.txt
if not "%CODE%"=="3" goto finish
if "%TRY%"=="2" goto finish
echo.
echo Password not accepted, 1 attempt left.
set TRY=2
goto passloop

:declined
echo.
echo Nothing changed.
goto menu

:finish
echo.
if "%CODE%"=="20" goto restart
for %%c in (1 2 3 10) do if "%CODE%"=="%%c" goto menu
echo Unexpected result (code %CODE%). Nothing was restarted.

:menu
echo.
set "ANSWER="
set /p "ANSWER=Type R and press Enter to restart, or just press Enter for a command prompt: "
if /i "!ANSWER!"=="R" wpeutil reboot
cmd /k
goto :eof

:restart
echo Restarting in 10 seconds. Turn the computer off now to stop the restart.
cscript //nologo "%VBS%" /wait
wpeutil reboot
