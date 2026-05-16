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
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Install"
  SetOutPath "$INSTDIR"

  File "..\\..\\out\\windows\\${APP_EXE}"
  File /r "..\\..\\internal\\assets\\third_party\\ffmpeg"
  File /r "..\\..\\internal\\assets\\third_party\\driver"

  IfFileExists "$INSTDIR\\third_party\\driver\\${DRIVER_INSTALLER}" 0 +3
    ExecWait '"$INSTDIR\\third_party\\driver\\${DRIVER_INSTALLER}" /S'
    DetailPrint "Virtual camera installer executed."

  WriteUninstaller "$INSTDIR\\Uninstall.exe"
  CreateShortcut "$SMSTARTUP\\${APP_NAME}.lnk" "$INSTDIR\\${APP_EXE}"
  CreateShortcut "$SMPROGRAMS\\${APP_NAME}.lnk" "$INSTDIR\\${APP_EXE}"
SectionEnd

Section "Uninstall"
  Delete "$SMSTARTUP\\${APP_NAME}.lnk"
  Delete "$SMPROGRAMS\\${APP_NAME}.lnk"
  Delete "$INSTDIR\\${APP_EXE}"
  Delete "$INSTDIR\\Uninstall.exe"
  RMDir /r "$INSTDIR\\third_party\\ffmpeg"
  RMDir /r "$INSTDIR\\third_party\\driver"
  RMDir "$INSTDIR"
SectionEnd
