Unicode true

!define INFO_PROJECTNAME "zdp-desktop-talos"
!define INFO_COMPANYNAME "0disoft"
!define INFO_PRODUCTNAME "Talos Agent"
!define INFO_PRODUCTVERSION "0.35.2"
!define INFO_COPYRIGHT "Copyright (c) 2026 0disoft"
!define PRODUCT_EXECUTABLE "talos-desktop.exe"
!define UNINST_KEY_NAME "0disoftTalosAgent"
!define WAILS_INSTALL_SCOPE "user"
!define REQUEST_EXECUTION_LEVEL "user"

!ifndef ARG_TALOS_WORKER_BINARY
  !error "ARG_TALOS_WORKER_BINARY is required"
!endif
!ifndef ARG_TALOS_CLI_BINARY
  !error "ARG_TALOS_CLI_BINARY is required"
!endif
!ifndef ARG_TALOS_INSTALLER_OUTPUT
  !error "ARG_TALOS_INSTALLER_OUTPUT is required"
!endif

!include "wails_tools.nsh"
!include "MUI.nsh"

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName" "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion" "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright" "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName" "${INFO_PRODUCTNAME}"

ManifestDPIAware true
SetCompressor /SOLID lzma
SetOverwrite on

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_NOAUTOCLOSE

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

!ifdef ARG_TALOS_SIGN_SCRIPT
  !uninstfinalize 'powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "${ARG_TALOS_SIGN_SCRIPT}" -Path "%1"'
!endif

Name "${INFO_PRODUCTNAME}"
OutFile "${ARG_TALOS_INSTALLER_OUTPUT}"
InstallDir "$LOCALAPPDATA\Programs\0disoft\${INFO_PRODUCTNAME}"
ShowInstDetails show
ShowUninstDetails show

Function Talos.CheckWebView2
  SetRegView 64
  ReadRegStr $0 HKLM "SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  StrCmp $0 "" 0 webview_ready
  ReadRegStr $0 HKCU "Software\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" "pv"
  StrCmp $0 "" 0 webview_ready
  SetErrorLevel 66
  IfSilent webview_abort 0
  MessageBox MB_ICONSTOP|MB_OK "Microsoft Edge WebView2 Runtime is required before installing ${INFO_PRODUCTNAME}."
  webview_abort:
  Abort
  webview_ready:
FunctionEnd

Function .onInit
  !insertmacro wails.checkArchitecture
  Call Talos.CheckWebView2
FunctionEnd

Section "Talos Agent" SEC_TALOS
  !insertmacro wails.setShellContext
  SetOutPath "$INSTDIR"

  !insertmacro wails.files
  File "/oname=talos-worker.exe" "${ARG_TALOS_WORKER_BINARY}"
  File "/oname=talosctl.exe" "${ARG_TALOS_CLI_BINARY}"

  CreateDirectory "$SMPROGRAMS\0disoft"
  CreateShortcut "$SMPROGRAMS\0disoft\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
  CreateShortcut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

  !insertmacro wails.writeUninstaller
SectionEnd

Section "Uninstall"
  !insertmacro wails.setShellContext

  Delete "$SMPROGRAMS\0disoft\${INFO_PRODUCTNAME}.lnk"
  RMDir "$SMPROGRAMS\0disoft"
  Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

  Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
  Delete "$INSTDIR\talos-worker.exe"
  Delete "$INSTDIR\talosctl.exe"

  !insertmacro wails.deleteUninstaller
  RMDir "$INSTDIR"
SectionEnd
