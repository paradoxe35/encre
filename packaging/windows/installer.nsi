; Built by CI with:
;   makensis /DPRODUCT_VERSION=x.y.z /DSOURCE_EXE=<abs path> /DICON=<abs path> /DOUTFILE=<abs path> installer.nsi
Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"

!define PRODUCT_NAME "Encre"
!define PRODUCT_PUBLISHER "Paradoxe Ng"
!define PRODUCT_WEB_SITE "https://github.com/paradoxe35/encre"
!define PRODUCT_INSTALL_REG "Software\${PRODUCT_PUBLISHER}\${PRODUCT_NAME}"
!define PRODUCT_UNINST_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${PRODUCT_NAME}"

Name "${PRODUCT_NAME}"
OutFile "${OUTFILE}"
InstallDir "$LOCALAPPDATA\${PRODUCT_NAME}"
InstallDirRegKey HKCU "${PRODUCT_INSTALL_REG}" "InstallDir"
RequestExecutionLevel user
SetCompress auto

!define MUI_ABORTWARNING
!define MUI_ICON "${ICON}"
!define MUI_UNICON "${ICON}"

Function .onInit
  SetShellVarContext current
FunctionEnd

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_RUN "$INSTDIR\encre.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Launch Encre now"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_LANGUAGE "English"

Section "Encre" SEC_APP
  SetOutPath "$INSTDIR"
  File /oname=encre.exe "${SOURCE_EXE}"
  File /nonfatal /oname=icon.ico "${ICON}"

  WriteUninstaller "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayName" "${PRODUCT_NAME}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "UninstallString" "$INSTDIR\Uninstall.exe"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayIcon" "$INSTDIR\encre.exe"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "DisplayVersion" "${PRODUCT_VERSION}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "Publisher" "${PRODUCT_PUBLISHER}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "URLInfoAbout" "${PRODUCT_WEB_SITE}"
  WriteRegStr HKCU "${PRODUCT_UNINST_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegDWORD HKCU "${PRODUCT_UNINST_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${PRODUCT_UNINST_KEY}" "NoRepair" 1
  WriteRegStr HKCU "${PRODUCT_INSTALL_REG}" "InstallDir" "$INSTDIR"
  WriteRegStr HKCU "${PRODUCT_INSTALL_REG}" "Version" "${PRODUCT_VERSION}"

  CreateDirectory "$SMPROGRAMS\${PRODUCT_NAME}"
  CreateShortcut "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk" "$INSTDIR\encre.exe"
  CreateShortcut "$SMPROGRAMS\${PRODUCT_NAME}\Uninstall ${PRODUCT_NAME}.lnk" "$INSTDIR\Uninstall.exe"
  CreateShortcut "$DESKTOP\${PRODUCT_NAME}.lnk" "$INSTDIR\encre.exe"
SectionEnd

Section "Uninstall"
  nsExec::ExecToStack 'taskkill /F /IM encre.exe'
  Pop $0

  Delete "$INSTDIR\encre.exe"
  Delete "$INSTDIR\icon.ico"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"

  Delete "$DESKTOP\${PRODUCT_NAME}.lnk"
  Delete "$SMPROGRAMS\${PRODUCT_NAME}\${PRODUCT_NAME}.lnk"
  Delete "$SMPROGRAMS\${PRODUCT_NAME}\Uninstall ${PRODUCT_NAME}.lnk"
  RMDir "$SMPROGRAMS\${PRODUCT_NAME}"

  DeleteRegKey HKCU "${PRODUCT_UNINST_KEY}"
  DeleteRegKey HKCU "${PRODUCT_INSTALL_REG}"
SectionEnd
