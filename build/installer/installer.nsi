!define APP_NAME "SYG RTSP Virtual Cam Agent"
!define APP_EXE "rtsp-virtual-cam-agent.exe"
!define COMPANY "SYG Medical"
!define INSTALL_DIR "$PROGRAMFILES64\\SYG Medical\\RTSP Virtual Cam Agent"
!define DRIVER_INSTALLER "virtual-camera-installer.exe"

!include "MUI2.nsh"

Name "${APP_NAME}"
OutFile "..\\..\\dist\\${APP_NAME}-Setup.exe"
InstallDir "${INSTALL_DIR}"
RequestExecutionLevel admin
Unicode True

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_RUN "$INSTDIR\\${APP_EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Start ${APP_NAME}"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetOutPath "$INSTDIR"
  File "..\\..\\out\\windows\\${APP_EXE}"

  # Bundle dependencies into third_party
  SetOutPath "$INSTDIR\\third_party\\ffmpeg"
  File "..\\..\\internal\\assets\\third_party\\ffmpeg\\ffmpeg.exe"

  SetOutPath "$INSTDIR\\third_party\\driver"
  File "..\\..\\internal\\assets\\third_party\\driver\\virtual-camera-installer.dll"

  # Register Virtual Camera DLL
  DetailPrint "Registering Virtual Camera Driver..."
  ExecWait 'regsvr32 /s "$INSTDIR\\third_party\\driver\\virtual-camera-installer.dll"'

  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\\Uninstall.exe"
  CreateShortcut "$SMSTARTUP\\${APP_NAME}.lnk" "$INSTDIR\\${APP_EXE}"
  CreateShortcut "$SMPROGRAMS\\${APP_NAME}.lnk" "$INSTDIR\\${APP_EXE}"

  # Add to Add/Remove Programs
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "UninstallString" "$INSTDIR\\Uninstall.exe"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "DisplayIcon" "$INSTDIR\\${APP_EXE}"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "Publisher" "${COMPANY}"
SectionEnd

Section "Uninstall"
  # Unregister Virtual Camera DLL
  DetailPrint "Unregistering Virtual Camera Driver..."
  ExecWait 'regsvr32 /u /s "$INSTDIR\\third_party\\driver\\virtual-camera-installer.dll"'

  Delete "$SMSTARTUP\\${APP_NAME}.lnk"
  Delete "$SMPROGRAMS\\${APP_NAME}.lnk"
  Delete "$INSTDIR\\${APP_EXE}"
  Delete "$INSTDIR\\Uninstall.exe"
  RMDir /r "$INSTDIR\\third_party"
  RMDir "$INSTDIR"

  DeleteRegKey HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}"
SectionEnd

Function .onInit
  # Check for previous installation
  ReadRegStr $R0 HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "UninstallString"
  StrCmp $R0 "" done

  MessageBox MB_OKCANCEL|MB_ICONEXCLAMATION \
  "${APP_NAME} is already installed. $\n$\nClick `OK` to remove the previous version or `Cancel` to cancel this upgrade." \
  IDOK uninst
  Abort

uninst:
  # Run the uninstaller
  ClearErrors
  ExecWait '$R0 /S _?=$INSTDIR' # _?=$INSTDIR tells it to run in place and wait
  IfErrors done
  # The uninstaller might not have deleted everything if it was running, so we manually clean if needed
  # but usually /S _?=$INSTDIR is enough.

done:
FunctionEnd
