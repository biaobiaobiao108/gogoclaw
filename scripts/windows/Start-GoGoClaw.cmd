@echo off
setlocal
title GoGoClaw
cd /d "%~dp0"

if not exist "gogoclaw-launcher.exe" (
  echo GoGoClaw Launcher was not found next to this script.
  echo Run "make build-launcher" from the repository root.
  pause
  exit /b 1
)

"gogoclaw-launcher.exe" %*
set "EXIT_CODE=%ERRORLEVEL%"

if not "%EXIT_CODE%"=="0" (
  echo.
  echo GoGoClaw exited with code %EXIT_CODE%.
  echo Check the messages above and the logs under %%USERPROFILE%%\.gogoclaw\logs.
  pause
)

exit /b %EXIT_CODE%
