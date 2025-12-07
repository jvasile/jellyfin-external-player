# run-win.cmd - Runs the Windows binary in a restart loop
# Works from both Windows (cmd.exe) and Linux/WSL (bash)
# Use /api/restart to restart, /api/shutdown or Ctrl-C to stop
# Set JELLYFIN_EXTERNAL_PORT env var to change port (default 9998)

:<<BATCH
@echo off
echo Running in a loop. Ctrl-C or /api/shutdown to kill, /api/restart to restart
:loop
if defined JELLYFIN_EXTERNAL_PORT (jellyfin-external-player.exe --port %JELLYFIN_EXTERNAL_PORT%) else (jellyfin-external-player.exe)
if %errorlevel%==0 goto loop
exit /b %errorlevel%
BATCH

echo "Running in a loop. Ctrl-C or /api/shutdown to kill, /api/restart to restart"
PORT_ARG=""
[ -n "$JELLYFIN_EXTERNAL_PORT" ] && PORT_ARG="--port $JELLYFIN_EXTERNAL_PORT"
while ./jellyfin-external-player.exe $PORT_ARG; do :; done
