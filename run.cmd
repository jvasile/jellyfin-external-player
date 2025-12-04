:<<BATCH
echo Running in a loop. Ctrl-C or SIGINT or /api/shutdown to kill, /api/restart to restart
@echo off
:loop
embyfin-kiosk.exe --port 9998
if %errorlevel%==0 goto loop
exit /b %errorlevel%
BATCH
echo "Running in a loop. Ctrl-C or /api/shutdown to kill, /api/restart to restart"
while ./embyfin-kiosk.exe --port 9998; do :; done
