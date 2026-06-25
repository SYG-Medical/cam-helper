!define APP_NAME "NystaVision"
!define APP_EXE "SYG Medical - NystaVision.exe"
!define COMPANY "SYG Medical"
!define INSTALL_DIR "$PROGRAMFILES64\SYG Medical\NystaVision"

!define MUI_ICON "..\\..\\internal\\tray\\resources\\icon.ico"
!include "MUI2.nsh"


Name "${APP_NAME}"
OutFile "..\\..\\dist\\${APP_NAME}-Setup.exe"
InstallDir "${INSTALL_DIR}"
RequestExecutionLevel admin
Unicode True

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_COMPONENTS
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_RUN "$INSTDIR\\${APP_EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Start ${APP_NAME}"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Section "Main Application" SecMain
  SectionIn RO
  SetOutPath "$INSTDIR"
  File "..\\..\\out\\windows\\${APP_EXE}"
  File "..\\..\\internal\\tray\\resources\\icon.ico"

  # Bundle ffmpeg
  SetOutPath "$INSTDIR\\third_party\\ffmpeg"
  File "..\\..\\internal\\assets\\third_party\\ffmpeg\\ffmpeg.exe"

  SetOutPath "$INSTDIR"
  WriteUninstaller "$INSTDIR\\Uninstall.exe"

  # Add to Add/Remove Programs
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "DisplayName" "${APP_NAME}"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "UninstallString" "$INSTDIR\\Uninstall.exe"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "DisplayIcon" "$INSTDIR\\icon.ico"
  WriteRegStr HKLM "Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${APP_NAME}" "Publisher" "${COMPANY}"
SectionEnd

Section "Create Desktop Shortcut" SecDesktopShortcut
  CreateShortcut "$DESKTOP\\${APP_NAME}.lnk" "$INSTDIR\\${APP_EXE}" "" "$INSTDIR\\icon.ico" 0
SectionEnd

Section "Create Start Menu Shortcut" SecStartMenuShortcut
  CreateShortcut "$SMPROGRAMS\\${APP_NAME}.lnk" "$INSTDIR\\${APP_EXE}" "" "$INSTDIR\\icon.ico" 0
SectionEnd

# Description of sections
!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecMain} "Main application and required components."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktopShortcut} "Create a shortcut for NystaVision on your Desktop."
  !insertmacro MUI_DESCRIPTION_TEXT ${SecStartMenuShortcut} "Create a NystaVision shortcut in your Start Menu."
!insertmacro MUI_FUNCTION_DESCRIPTION_END

Section "Uninstall"
  Delete "$SMPROGRAMS\\${APP_NAME}.lnk"
  Delete "$DESKTOP\\${APP_NAME}.lnk"
  Delete "$INSTDIR\\${APP_EXE}"
  Delete "$INSTDIR\\icon.ico"
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
  ClearErrors
  ExecWait '$R0 /S _?=$INSTDIR'
  IfErrors done

done:
FunctionEnd
