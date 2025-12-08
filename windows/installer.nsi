; NSIS Installer Script for Jellyfin External Player
; Build with: makensis windows/installer.nsi (from project root)

!define APPNAME "Jellyfin External Player"
!define EXENAME "jellyfin-external-player.exe"
!define INSTDIR_REG_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"

Name "${APPNAME}"
OutFile "jellyfin-external-player-setup.exe"
InstallDir "$LOCALAPPDATA\${APPNAME}"
RequestExecutionLevel user

!include "MUI2.nsh"

; UI settings
!define MUI_ABORTWARNING
!define MUI_ICON "${NSISDIR}\Contrib\Graphics\Icons\modern-install.ico"
!define MUI_UNICON "${NSISDIR}\Contrib\Graphics\Icons\modern-uninstall.ico"

; Pages
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\${EXENAME}"
!define MUI_FINISHPAGE_RUN_TEXT "Launch ${APPNAME}"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Install"
    ; Kill any running instance before overwriting
    nsExec::ExecToLog 'taskkill /F /IM jellyfin-external-player.exe'

    SetOutPath "$INSTDIR"

    ; Install files (paths relative to .nsi file location)
    File "jellyfin-external-player.exe"
    File "../dist/jellyfin-external-player.js"

    ; Create uninstaller
    WriteUninstaller "$INSTDIR\uninstall.exe"

    ; Add to Start Menu
    CreateDirectory "$SMPROGRAMS\${APPNAME}"
    CreateShortCut "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk" "$INSTDIR\${EXENAME}"
    CreateShortCut "$SMPROGRAMS\${APPNAME}\Uninstall.lnk" "$INSTDIR\uninstall.exe"

    ; Add to Startup (auto-start on login)
    CreateShortCut "$SMSTARTUP\${APPNAME}.lnk" "$INSTDIR\${EXENAME}"

    ; Add uninstall info to registry
    WriteRegStr HKCU "${INSTDIR_REG_KEY}" "DisplayName" "${APPNAME}"
    WriteRegStr HKCU "${INSTDIR_REG_KEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
    WriteRegStr HKCU "${INSTDIR_REG_KEY}" "InstallLocation" "$INSTDIR"
    WriteRegStr HKCU "${INSTDIR_REG_KEY}" "DisplayIcon" "$INSTDIR\${EXENAME}"
    WriteRegStr HKCU "${INSTDIR_REG_KEY}" "Publisher" "jellyfin-external-player"
    WriteRegDWORD HKCU "${INSTDIR_REG_KEY}" "NoModify" 1
    WriteRegDWORD HKCU "${INSTDIR_REG_KEY}" "NoRepair" 1
SectionEnd

Section "Uninstall"
    ; Kill running process
    nsExec::ExecToLog 'taskkill /F /IM ${EXENAME}'

    ; Remove files
    Delete "$INSTDIR\jellyfin-external-player.exe"
    Delete "$INSTDIR\jellyfin-external-player.js"
    Delete "$INSTDIR\uninstall.exe"
    RMDir "$INSTDIR"

    ; Remove Start Menu items
    Delete "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk"
    Delete "$SMPROGRAMS\${APPNAME}\Uninstall.lnk"
    RMDir "$SMPROGRAMS\${APPNAME}"

    ; Remove from Startup
    Delete "$SMSTARTUP\${APPNAME}.lnk"

    ; Remove registry keys
    DeleteRegKey HKCU "${INSTDIR_REG_KEY}"
SectionEnd
