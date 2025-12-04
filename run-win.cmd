# run-win.cmd - Runs the Windows binary in a restart loop
# Works from both Windows (cmd.exe) and Linux/WSL (bash)
# Use /api/restart to restart, /api/shutdown or Ctrl-C to stop
# Set JF_EXTERNAL_PORT env var to change port (default 9998)

:<<BATCH
@echo off
echo Running in a loop. Ctrl-C or /api/shutdown to kill, /api/restart to restart
:loop
if defined JF_EXTERNAL_PORT (jf-external-player.exe --port %JF_EXTERNAL_PORT%) else (jf-external-player.exe)
if %errorlevel%==0 goto loop
exit /b %errorlevel%
BATCH

echo "Running in a loop. Ctrl-C or /api/shutdown to kill, /api/restart to restart"
PORT_ARG=""
[ -n "$JF_EXTERNAL_PORT" ] && PORT_ARG="--port $JF_EXTERNAL_PORT"
while ./jf-external-player.exe $PORT_ARG; do :; done
