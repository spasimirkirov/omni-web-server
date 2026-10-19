; Omni Web Server installer (NSIS 3). Build with build.sh or:
;   makensis -DVERSION=1.1.0 -DSRCDIR=..\dist\OmniWebServer installer\omni.nsi
;
; Silent install:  OmniWebServer-Setup.exe /S /D=C:\OmniWebServer /WWW=D:\Projects
; (/D must be last and unquoted, as with every NSIS installer)

Unicode true
ManifestDPIAware true
SetCompressor /SOLID lzma

!ifndef VERSION
  !define VERSION "1.1.0"
!endif
!ifndef SRCDIR
  !define SRCDIR "..\dist\OmniWebServer"
!endif
!ifndef OUTFILE
  !define OUTFILE "..\dist\OmniWebServer-${VERSION}-Setup.exe"
!endif

!define APPNAME   "Omni Web Server"
!define APPKEY    "OmniWebServer"
!define APPEXE    "omni.exe"
!define APPEXEW   "omniw.exe" ; no console window: shortcuts, autostart
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPKEY}"

Name "${APPNAME}"
OutFile "${OUTFILE}"
InstallDir "C:\OmniWebServer"
InstallDirRegKey HKLM "${UNINSTKEY}" "InstallLocation"
RequestExecutionLevel admin
BrandingText "${APPNAME} ${VERSION}"

VIProductVersion "${VERSION}.0"
VIAddVersionKey "ProductName" "${APPNAME}"
VIAddVersionKey "FileDescription" "${APPNAME} Setup"
VIAddVersionKey "FileVersion" "${VERSION}"
VIAddVersionKey "ProductVersion" "${VERSION}"
VIAddVersionKey "LegalCopyright" "${APPNAME}"

!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"
!include "x64.nsh"
!include "WinVer.nsh"
!include "WordFunc.nsh"
!include "Sections.nsh"

Var WWWDir
Var Pending
Var StartMenuFolder
Var MigrateKind
Var MigrateRoot
Var MigrateName

!define MUI_ICON "..\favicons\favicon.ico"
!define MUI_UNICON "..\favicons\favicon.ico"
!define MUI_WELCOMEFINISHPAGE_BITMAP "wizard.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "wizard.bmp"
!define MUI_HEADERIMAGE
!define MUI_HEADERIMAGE_RIGHT
!define MUI_HEADERIMAGE_BITMAP "header.bmp"
!define MUI_HEADERIMAGE_UNBITMAP "header.bmp"
!define MUI_ABORTWARNING
!define MUI_COMPONENTSPAGE_SMALLDESC
!define MUI_LANGDLL_REGISTRY_ROOT HKCU
!define MUI_LANGDLL_REGISTRY_KEY "Software\${APPKEY}"
!define MUI_LANGDLL_REGISTRY_VALUENAME "InstallerLanguage"

; ---- pages ----
!insertmacro MUI_PAGE_WELCOME

; 1) where the program goes
!define MUI_PAGE_HEADER_TEXT "$(PageAppTitle)"
!define MUI_PAGE_HEADER_SUBTEXT "$(PageAppSub)"
!define MUI_DIRECTORYPAGE_TEXT_TOP "$(PageAppTop)"
!insertmacro MUI_PAGE_DIRECTORY

; 2) where the projects (www) go - a separate choice
!define MUI_PAGE_HEADER_TEXT "$(PageWWWTitle)"
!define MUI_PAGE_HEADER_SUBTEXT "$(PageWWWSub)"
!define MUI_DIRECTORYPAGE_TEXT_TOP "$(PageWWWTop)"
!define MUI_DIRECTORYPAGE_TEXT_DESTINATION "$(PageWWWDest)"
!define MUI_DIRECTORYPAGE_VARIABLE $WWWDir
!define MUI_PAGE_CUSTOMFUNCTION_PRE WWWPagePre
!insertmacro MUI_PAGE_DIRECTORY

!define MUI_PAGE_CUSTOMFUNCTION_LEAVE ComponentsLeave
!insertmacro MUI_PAGE_COMPONENTS

!define MUI_STARTMENUPAGE_DEFAULTFOLDER "${APPNAME}"
!define MUI_STARTMENUPAGE_REGISTRY_ROOT HKLM
!define MUI_STARTMENUPAGE_REGISTRY_KEY "${UNINSTKEY}"
!define MUI_STARTMENUPAGE_REGISTRY_VALUENAME "StartMenuFolder"
!insertmacro MUI_PAGE_STARTMENU Application $StartMenuFolder

!insertmacro MUI_PAGE_INSTFILES

!define MUI_FINISHPAGE_RUN "$INSTDIR\${APPEXEW}"
!define MUI_FINISHPAGE_RUN_TEXT "$(FinishRun)"
!define MUI_FINISHPAGE_SHOWREADME "$INSTDIR\README.md"
!define MUI_FINISHPAGE_SHOWREADME_NOTCHECKED
!define MUI_FINISHPAGE_SHOWREADME_TEXT "$(FinishReadme)"
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "Bulgarian"
!insertmacro MUI_RESERVEFILE_LANGDLL

; ---- strings (English / Bulgarian) ----
LangString PageAppTitle ${LANG_ENGLISH} "Program folder"
LangString PageAppTitle ${LANG_BULGARIAN} "Папка на програмата"
LangString PageAppSub ${LANG_ENGLISH} "Where Omni Web Server, Apache, PHP and MySQL are installed."
LangString PageAppSub ${LANG_BULGARIAN} "Къде се инсталират Omni Web Server, Apache, PHP и MySQL."
LangString PageAppTop ${LANG_ENGLISH} "Omni Web Server keeps the web server, PHP versions, databases and logs in this folder. Choose a short path without spaces, e.g. C:\OmniWebServer (Program Files is not recommended: it is read-only for normal users)."
LangString PageAppTop ${LANG_BULGARIAN} "Omni Web Server пази уеб сървъра, PHP версиите, базите данни и логовете в тази папка. Изберете кратък път без интервали, напр. C:\OmniWebServer (Program Files не се препоръчва: там обикновените потребители нямат право на запис)."
LangString PageWWWTitle ${LANG_ENGLISH} "Projects folder (www)"
LangString PageWWWTitle ${LANG_BULGARIAN} "Папка с проектите (www)"
LangString PageWWWSub ${LANG_ENGLISH} "Where your websites live. It can be anywhere, also on another drive."
LangString PageWWWSub ${LANG_BULGARIAN} "Къде са вашите сайтове. Може да е навсякъде, включително на друг диск."
LangString PageWWWTop ${LANG_ENGLISH} "Every folder inside becomes http://<folder>.local automatically (Laravel projects are served from public/). Uninstalling never deletes this folder."
LangString PageWWWTop ${LANG_BULGARIAN} "Всяка папка вътре автоматично става http://<папка>.local (Laravel проектите се обслужват от public/). Деинсталирането никога не изтрива тази папка."
LangString PageWWWDest ${LANG_ENGLISH} "Projects folder"
LangString PageWWWDest ${LANG_BULGARIAN} "Папка с проекти"
LangString FinishRun ${LANG_ENGLISH} "Start Omni Web Server"
LangString FinishRun ${LANG_BULGARIAN} "Стартирай Omni Web Server"
LangString FinishReadme ${LANG_ENGLISH} "Show the README"
LangString FinishReadme ${LANG_BULGARIAN} "Покажи README"
LangString UnDeleteData ${LANG_ENGLISH} "Also delete databases, downloaded components (Apache, PHP, MySQL, ...), certificates, logs and settings?$\r$\n$\r$\nYour projects folder ($WWWDir) is never deleted."
LangString UnDeleteData ${LANG_BULGARIAN} "Да се изтрият ли и базите данни, изтеглените компоненти (Apache, PHP, MySQL, ...), сертификатите, логовете и настройките?$\r$\n$\r$\nПапката с проекти ($WWWDir) никога не се изтрива."
LangString NeedWin10 ${LANG_ENGLISH} "Omni Web Server needs 64-bit Windows 10 or 11."
LangString NeedWin10 ${LANG_BULGARIAN} "Omni Web Server изисква 64-битов Windows 10 или 11."
LangString VCRedist ${LANG_ENGLISH} "Installing the Microsoft Visual C++ Redistributable..."
LangString VCRedist ${LANG_BULGARIAN} "Инсталиране на Microsoft Visual C++ Redistributable..."

LangString DescCore ${LANG_ENGLISH} "Dashboard, CLI (omni), automatic vhosts, wildcard DNS and HTTPS."
LangString DescCore ${LANG_BULGARIAN} "Табло, команден ред (omni), автоматични vhost-ове, wildcard DNS и HTTPS."
LangString DescVC ${LANG_ENGLISH} "Runtime needed by Apache, PHP and MySQL (skipped when already installed)."
LangString DescVC ${LANG_BULGARIAN} "Нужен на Apache, PHP и MySQL (пропуска се, ако вече е инсталиран)."
LangString DescStack ${LANG_ENGLISH} "Downloaded automatically on the first start, so you get a working server right away."
LangString DescStack ${LANG_BULGARIAN} "Изтеглят се автоматично при първото стартиране, за да имате работещ сървър веднага."
LangString DescApps ${LANG_ENGLISH} "Windows apps installed with winget on the first start."
LangString DescApps ${LANG_BULGARIAN} "Windows приложения, инсталирани с winget при първото стартиране."
LangString DescAdmin ${LANG_ENGLISH} "Shortcuts start Omni Web Server as administrator, so hosts/DNS changes need no prompts."
LangString DescAdmin ${LANG_BULGARIAN} "Преките пътища стартират Omni Web Server като администратор, без допълнителни въпроси за hosts/DNS."
LangString DescDesktop ${LANG_ENGLISH} "Desktop shortcut."
LangString DescDesktop ${LANG_BULGARIAN} "Пряк път на работния плот."
LangString DescAutorun ${LANG_ENGLISH} "Start Omni Web Server in the background when you sign in."
LangString DescAutorun ${LANG_BULGARIAN} "Стартирай Omni Web Server във фонов режим при влизане в Windows."

LangString ImportFrom ${LANG_ENGLISH} "Import from"
LangString ImportFrom ${LANG_BULGARIAN} "Импорт от"
LangString DescImport ${LANG_ENGLISH} "Copy the projects and databases of your current local server and replace it. You will see every step before anything happens."
LangString DescImport ${LANG_BULGARIAN} "Копира проектите и базите данни от сегашния ви локален сървър и заема мястото му. Ще видите всяка стъпка, преди да започне."
LangString ImportSteps ${LANG_ENGLISH} "On its first start Omni Web Server will import from $MigrateName ($MigrateRoot):$\r$\n$\r$\n1. Install what is missing: Apache, the PHP version $MigrateName uses, and MySQL.$\r$\n2. Stop $MigrateName and its servers. Its Windows services are set to manual start.$\r$\n3. Copy its projects to your projects folder (projects that already exist are skipped).$\r$\n4. Export its databases with its own MySQL and import them, keeping the same root password so your .env files keep working.$\r$\n5. Remove its hosts entries, its PHP/MySQL/Composer PATH entries and its startup entry.$\r$\n6. Start Apache and MySQL on the standard ports 80 and 3306.$\r$\n$\r$\nNothing is deleted: $MigrateName stays installed with its data, and you can uninstall it afterwards.$\r$\n$\r$\nContinue?"
LangString ImportSteps ${LANG_BULGARIAN} "При първото стартиране Omni Web Server ще импортира от $MigrateName ($MigrateRoot):$\r$\n$\r$\n1. Инсталира липсващото: Apache, същата PHP версия като в $MigrateName и MySQL.$\r$\n2. Спира $MigrateName и неговите сървъри. Windows услугите му се превключват на ръчно стартиране.$\r$\n3. Копира проектите му в папката с проекти (вече съществуващите се пропускат).$\r$\n4. Експортира базите данни със собствения му MySQL и ги импортира, като запазва същата root парола, за да работят .env файловете ви.$\r$\n5. Премахва неговите записи в hosts, PHP/MySQL/Composer пътищата му от PATH и автоматичното му стартиране.$\r$\n6. Стартира Apache и MySQL на стандартните портове 80 и 3306.$\r$\n$\r$\nНищо не се изтрива: $MigrateName остава инсталиран с данните си и можете да го деинсталирате после.$\r$\n$\r$\nПродължаване?"
; ---- helpers ----

; Set the "Run as administrator" flag of a .lnk file (byte 0x15, bit 0x20).
!macro SetLnkRunAsAdmin LNK
  Push $0
  Push $1
  FileOpen $0 "${LNK}" a
  ${If} $0 != ""
    FileSeek $0 0x15 SET
    FileReadByte $0 $1
    IntOp $1 $1 | 0x20
    FileSeek $0 0x15 SET
    FileWriteByte $0 $1
    FileClose $0
  ${EndIf}
  Pop $1
  Pop $0
!macroend

!macro AddPending ITEM
  ${If} $Pending == ""
    StrCpy $Pending "${ITEM}"
  ${Else}
    StrCpy $Pending "$Pending,${ITEM}"
  ${EndIf}
!macroend

Function .onInit
  ${IfNot} ${RunningX64}
  ${OrIfNot} ${AtLeastWin10}
    MessageBox MB_ICONSTOP "$(NeedWin10)"
    Abort
  ${EndIf}
  SetRegView 64
  !insertmacro MUI_LANGDLL_DISPLAY
  ; Upgrades keep the previous projects folder.
  ReadRegStr $WWWDir HKCU "Software\${APPKEY}" "WWWDir"
  ; /WWW=<folder> on the command line (silent installs)
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "/WWW=" $1
  ${IfNot} ${Errors}
    StrCpy $WWWDir $1
  ${EndIf}
  Call DetectOldStack
  ; Upgrades keep "Start with Windows" if it was on.
  ReadRegStr $0 HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${APPKEY}"
  ${If} $0 != ""
    !insertmacro SelectSection ${SecAutorun}
  ${EndIf}
FunctionEnd

Function WWWPagePre
  ${If} $WWWDir == ""
    StrCpy $WWWDir "$INSTDIR\www"
  ${EndIf}
FunctionEnd

; ---- sections ----

Section "!Omni Web Server" SecCore
  SectionIn RO
  SetShellVarContext current
  ${If} $WWWDir == ""
    StrCpy $WWWDir "$INSTDIR\www"
  ${EndIf}

  ; Upgrade: stop a running instance so omni.exe can be replaced.
  ${If} ${FileExists} "$INSTDIR\${APPEXE}"
    nsExec::Exec '"$INSTDIR\${APPEXE}" quit'
    Pop $0
    Sleep 2500
  ${EndIf}

  SetOutPath "$INSTDIR"
  File "${SRCDIR}\${APPEXE}"
  File "${SRCDIR}\${APPEXEW}"
  File "${SRCDIR}\README.md"
  File "${SRCDIR}\LICENSE"
  File "${SRCDIR}\CREDITS.md"
  File /oname=omni.ico "..\favicons\favicon.ico"
  WriteUninstaller "$INSTDIR\uninstall.exe"

  ; Programs and Features entry
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTKEY}" "Publisher" "${APPNAME}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\omni.ico"
  WriteRegStr HKLM "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINSTKEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegStr HKLM "${UNINSTKEY}" "URLInfoAbout" "http://localhost:8800/"
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "${UNINSTKEY}" "EstimatedSize" "$0"
SectionEnd

Section "Visual C++ Redistributable (x64)" SecVC
  ${IfNot} ${FileExists} "$SYSDIR\vcruntime140_1.dll"
    DetailPrint "$(VCRedist)"
    nsExec::ExecToLog `powershell -NoProfile -ExecutionPolicy Bypass -Command "$$ProgressPreference='SilentlyContinue'; Invoke-WebRequest -UseBasicParsing https://aka.ms/vs/17/release/vc_redist.x64.exe -OutFile '$TEMP\vc_redist.x64.exe'"`
    Pop $0
    ExecWait '"$TEMP\vc_redist.x64.exe" /install /quiet /norestart' $0
    Delete "$TEMP\vc_redist.x64.exe"
  ${EndIf}
SectionEnd

SectionGroup /e "Web stack (downloaded on first start)" SecStack
  Section "Apache 2.4" SecApache
    !insertmacro AddPending "apache"
  SectionEnd
  Section "PHP (latest)" SecPHP
    !insertmacro AddPending "php"
  SectionEnd
  Section "MySQL 8.4" SecMySQL
    !insertmacro AddPending "mysql"
  SectionEnd
  Section /o "MariaDB 11.4 (instead of MySQL)" SecMariaDB
    !insertmacro AddPending "mariadb"
  SectionEnd
  Section "Composer" SecComposer
    !insertmacro AddPending "composer"
  SectionEnd
  Section "Node.js LTS" SecNode
    !insertmacro AddPending "node"
  SectionEnd
  Section "Mailpit (mail catcher)" SecMailpit
    !insertmacro AddPending "mailpit"
  SectionEnd
  Section /o "Redis" SecRedis
    !insertmacro AddPending "redis"
  SectionEnd
  Section /o "Adminer (database web UI)" SecAdminer
    !insertmacro AddPending "adminer"
  SectionEnd
SectionGroupEnd

SectionGroup "Developer apps (winget)" SecApps
  Section "Git for Windows" SecGit
    !insertmacro AddPending "tool:git"
  SectionEnd
  Section /o "Visual Studio Code" SecVSCode
    !insertmacro AddPending "tool:vscode"
  SectionEnd
  Section /o "HeidiSQL" SecHeidi
    !insertmacro AddPending "tool:heidisql"
  SectionEnd
  Section /o "Cloudflare Tunnel (Share button)" SecCloudflared
    !insertmacro AddPending "tool:cloudflared"
  SectionEnd
SectionGroupEnd

Section /o "Import from Laragon / XAMPP" SecImport
  !insertmacro AddPending "migrate:$MigrateKind"
SectionEnd

Section "Run as administrator" SecAdmin
  ; read in the shortcut section below
SectionEnd

Section "Desktop shortcut" SecDesktop
SectionEnd

Section /o "Start with Windows" SecAutorun
  WriteRegStr HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${APPKEY}" '"$INSTDIR\${APPEXEW}" serve --no-browser'
SectionEnd

; Runs last: shortcuts, then settings + PATH + registry through omni init.
Section "-Finalize"
  SetShellVarContext current
  !insertmacro MUI_STARTMENU_WRITE_BEGIN Application
    CreateDirectory "$SMPROGRAMS\$StartMenuFolder"
    CreateShortCut "$SMPROGRAMS\$StartMenuFolder\${APPNAME}.lnk" "$INSTDIR\${APPEXEW}" "" "$INSTDIR\omni.ico"
    CreateShortCut "$SMPROGRAMS\$StartMenuFolder\Projects (www).lnk" "$WWWDir"
    CreateShortCut "$SMPROGRAMS\$StartMenuFolder\Uninstall ${APPNAME}.lnk" "$INSTDIR\uninstall.exe"
    ${If} ${SectionIsSelected} ${SecAdmin}
      !insertmacro SetLnkRunAsAdmin "$SMPROGRAMS\$StartMenuFolder\${APPNAME}.lnk"
    ${EndIf}
  !insertmacro MUI_STARTMENU_WRITE_END
  ${If} ${SectionIsSelected} ${SecDesktop}
    CreateShortCut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\${APPEXEW}" "" "$INSTDIR\omni.ico"
    ${If} ${SectionIsSelected} ${SecAdmin}
      !insertmacro SetLnkRunAsAdmin "$DESKTOP\${APPNAME}.lnk"
    ${EndIf}
  ${EndIf}

  CreateDirectory "$WWWDir"
  ; Creates omni.json, the www folder, bin\shims on PATH and HKCU\Software\OmniWebServer.
  nsExec::ExecToLog '"$INSTDIR\${APPEXE}" --root "$INSTDIR" init --www "$WWWDir" --install "$Pending"'
  Pop $0
  WriteRegStr HKCU "Software\${APPKEY}" "WWWDir" "$WWWDir"
SectionEnd

!insertmacro MUI_FUNCTION_DESCRIPTION_BEGIN
  !insertmacro MUI_DESCRIPTION_TEXT ${SecCore} "$(DescCore)"
  !insertmacro MUI_DESCRIPTION_TEXT ${SecVC} "$(DescVC)"
  !insertmacro MUI_DESCRIPTION_TEXT ${SecStack} "$(DescStack)"
  !insertmacro MUI_DESCRIPTION_TEXT ${SecApps} "$(DescApps)"
  !insertmacro MUI_DESCRIPTION_TEXT ${SecImport} "$(DescImport)"
  !insertmacro MUI_DESCRIPTION_TEXT ${SecAdmin} "$(DescAdmin)"
  !insertmacro MUI_DESCRIPTION_TEXT ${SecDesktop} "$(DescDesktop)"
  !insertmacro MUI_DESCRIPTION_TEXT ${SecAutorun} "$(DescAutorun)"
!insertmacro MUI_FUNCTION_DESCRIPTION_END

; ---- Laragon / XAMPP import ----

; Shows the import option only when omni.exe finds Laragon or XAMPP
; ("omni migrate detect" prints "kind|root" per installation).
Function DetectOldStack
  SectionSetText ${SecImport} ""
  InitPluginsDir
  File /oname=$PLUGINSDIR\omni-detect.exe "${SRCDIR}\${APPEXE}"
  nsExec::ExecToStack '"$PLUGINSDIR\omni-detect.exe" --root "$PLUGINSDIR" migrate detect'
  Pop $0
  Pop $1
  ${If} $0 != 0
  ${OrIf} $1 == ""
    Return
  ${EndIf}
  ${WordFind} "$1" "$\n" "+1" $2
  ${WordFind} "$2" "$\r" "+1" $2
  ${WordFind} "$2" "|" "+1" $MigrateKind
  ${WordFind} "$2" "|" "+2" $MigrateRoot
  ${If} $MigrateKind == "xampp"
    StrCpy $MigrateName "XAMPP"
  ${ElseIf} $MigrateKind == "laragon"
    StrCpy $MigrateName "Laragon"
  ${Else}
    Return
  ${EndIf}
  SectionSetText ${SecImport} "$(ImportFrom) $MigrateName ($MigrateRoot)"
  ; Preselected on a fresh install; an upgrade does not import again by itself.
  ${IfNot} ${FileExists} "$INSTDIR\omni.json"
    !insertmacro SelectSection ${SecImport}
  ${EndIf}
FunctionEnd

; Lists every step before the user commits to the import.
Function ComponentsLeave
  ${If} ${SectionIsSelected} ${SecImport}
    MessageBox MB_OKCANCEL|MB_ICONINFORMATION "$(ImportSteps)" IDOK +2
    Abort
  ${EndIf}
FunctionEnd

; ---- uninstaller ----

Function un.onInit
  SetRegView 64
  !insertmacro MUI_UNGETLANGUAGE
  ReadRegStr $WWWDir HKCU "Software\${APPKEY}" "WWWDir"
FunctionEnd

Section "Uninstall"
  SetShellVarContext current
  ; Stops the server and removes hosts entries, the DNS rule, the PATH entry and registry values.
  nsExec::ExecToLog '"$INSTDIR\${APPEXE}" --root "$INSTDIR" cleanup'
  Pop $0

  MessageBox MB_YESNO|MB_ICONQUESTION|MB_DEFBUTTON2 "$(UnDeleteData)" /SD IDNO IDNO keepdata
    RMDir /r "$INSTDIR\bin"
    RMDir /r "$INSTDIR\etc"
    RMDir /r "$INSTDIR\data"
    RMDir /r "$INSTDIR\logs"
    RMDir /r "$INSTDIR\tmp"
    Delete "$INSTDIR\omni.json"
  keepdata:

  Delete "$INSTDIR\${APPEXE}"
  Delete "$INSTDIR\${APPEXEW}"
  Delete "$INSTDIR\README.md"
  Delete "$INSTDIR\LICENSE"
  Delete "$INSTDIR\CREDITS.md"
  Delete "$INSTDIR\omni.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir "$INSTDIR\www"   ; only when empty
  RMDir "$INSTDIR"       ; only when empty

  !insertmacro MUI_STARTMENU_GETFOLDER Application $StartMenuFolder
  RMDir /r "$SMPROGRAMS\$StartMenuFolder"
  Delete "$DESKTOP\${APPNAME}.lnk"
  DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "${APPKEY}"
  DeleteRegKey HKCU "Software\${APPKEY}"
  DeleteRegKey HKLM "${UNINSTKEY}"
SectionEnd
