# Instructions

 * Don't advertise in commits
 * Re-read CLAUDE.md after compacting
 * After making changes, rebuild if necessary
 * After rebuilding, restart the server via the /api/restart endpoint
 * The userscript should be a stub that loads javascript served by our server,
   so we don't need to re-load the userscript ever again. Cache the javascript so it
   only gets reloaded if I shift-reload.  When the javascript gets requested
   from the server, check whether it needs to be remade from the template on
   disk.  This allows us to make changes to the javascript without rebuilding
   the server.
 * We are running in WSL. The windows side is available at /mnt/c.  The server
   logs to a tmp file on the windows side.  My username is jvasi on the Windows
   side.  Find that file and read it as needed to see server logs.
