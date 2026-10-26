@echo off
rem Runs uefica.vbs against the WMI stubs in uefica-stubs.vbs.
rem Usage: uefica-tests.cmd on Windows; on Linux: UEFICA_WINE=1 wine cmd /c uefica-tests.cmd
setlocal EnableExtensions EnableDelayedExpansion
cd /d "%~dp0"
if not exist run mkdir run
copy /y /b ..\files\uefica.vbs + uefica-stubs.vbs run\uefica-test.vbs >nul
cd run
set "VBS=uefica-test.vbs"
set PASSED=0
set FAILED=0
rem       scenario           input  mode                  code  expected log
call :case nosb              ""     "/check"              10    ""
call :case qemu              ""     "/check"              2     ""
call :case lenovo-on         ""     "/check"              10    ""
call :case lenovo-nopass     ""     "/check"              40    ""
call :case lenovo-nopass     ""     "/apply"              20     lenovo-nopass
call :case lenovo-opcode     ""     "/check"              41    ""
call :case lenovo-opcode     pw1    "/apply /password"    20     lenovo-opcode
call :case lenovo-old        pw1    "/apply /password"    20     lenovo-old
call :case lenovo-denied     bad    "/apply /password"    3     lenovo-denied
call :case lenovo-unknown    ""     "/check"              40    ""
call :case dell-pass         pw1    "/apply /password"    20     dell-pass
rem Wine's VBScript has no ChrW/AscW: set UEFICA_WINE=1 there to skip this one.
if not "%UEFICA_WINE%"=="1" call :case dell-pass-unicode U1 "/apply /password" 20 dell-pass-unicode
call :case dell-array        ""     "/check"              40    ""
call :case lenovo-nobracket  ""     "/check"              40    ""
call :case crash             ""     "/check"              1     ""
call :case hp-nopass         ""     "/apply"              20     hp-nopass
call :case hp-denied         pw1    "/apply /password"    3     hp-denied
if exist uefica-calls.log del uefica-calls.log
if exist uefica.txt del uefica.txt
if exist input.txt del input.txt
del uefica-test.vbs
cd ..
rmdir run
echo.
if "%FAILED%"=="0" (
    echo all %PASSED% passed
    exit /b 0
)
echo %FAILED% failed, %PASSED% passed
exit /b 1

:case
if exist uefica-calls.log del uefica-calls.log
if exist uefica.txt del uefica.txt
if exist input.txt del input.txt
if not "%~2"=="" (>input.txt echo %~2)
cscript //nologo %VBS% /test:%1 %~3 <nul >nul
set GOT=!ERRORLEVEL!
set OK=1
if not "!GOT!"=="%4" set OK=0
if "%~5"=="" (
    if exist uefica-calls.log set OK=0
) else (
    if not exist uefica-calls.log set OK=0
    if exist uefica-calls.log cscript //nologo ..\compare.vbs uefica-calls.log ..\expected\%~5.log
    if errorlevel 1 set OK=0
)
if "!OK!"=="1" (
    set /a PASSED+=1
    echo ok    %1 %~3
) else (
    set /a FAILED+=1
    echo FAIL  %1 %~3: exit !GOT!, want %4
    if exist uefica-calls.log type uefica-calls.log
)
exit /b 0
