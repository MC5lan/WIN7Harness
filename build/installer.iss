; WIN7 Harness 通用安装器（Inno Setup 6.7.x）
;
; 一个安装包，两种形态：
;   标准安装：程序装到本机（当前用户 / 所有用户），数据仍在用户目录；
;   便携安装：程序和数据放进 U 盘，不写注册表、不建快捷方式。
;
; 编译：
;   ISCC.exe /DMyAppVersion=1.38.1 /DMySourceDir=..\dist installer.iss
;
; 注意：锁定 Inno Setup 6.7.x。7.x 面向更新的系统，不要拿来出 Win7 包。

#ifndef MyAppVersion
  #define MyAppVersion "1.38.1"
#endif

#ifndef MySourceDir
  #define MySourceDir AddBackslash(SourcePath) + "..\dist"
#endif

#ifndef MyOutputDir
  #define MyOutputDir AddBackslash(SourcePath) + "..\dist"
#endif

#ifndef MyIcon
  #define MyIcon MySourceDir + "\win7harness.ico"
#endif

#ifndef MyLangFile
  #define MyLangFile AddBackslash(SourcePath) + "installer-lang\ChineseSimplified.isl"
#endif

[Setup]
AppId={{8E4C2F7A-3B1D-4E6A-9C2F-7D5A1B8E3C40}
AppName=WIN7 Harness
AppVersion={#MyAppVersion}
AppVerName=WIN7 Harness {#MyAppVersion}
AppPublisher=WIN7 Harness
AppComments=Windows 7 SP1 上运行的本地 AI 编程智能体
DefaultDirName={code:GetDefaultDir}
DefaultGroupName=WIN7 Harness
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
PrivilegesRequiredOverridesAllowed=dialog commandline
ArchitecturesAllowed=x86 x64os
ArchitecturesInstallIn64BitMode=x64os
OutputDir={#MyOutputDir}
OutputBaseFilename=WIN7Harness-v{#MyAppVersion}-Setup
SetupIconFile={#MyIcon}
UninstallDisplayIcon={app}\win7harness.ico
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
MinVersion=6.1sp1
CloseApplications=yes
RestartApplications=no
UsePreviousAppDir=yes
UsePreviousTasks=yes
UsePreviousPrivileges=yes
Uninstallable=not IsPortableMode
CreateUninstallRegKey=not IsPortableMode
VersionInfoCompany=WIN7 Harness
VersionInfoDescription=WIN7 Harness 安装程序
VersionInfoProductName=WIN7 Harness
VersionInfoProductVersion={#MyAppVersion}
VersionInfoVersion={#MyAppVersion}.0
ShowLanguageDialog=no
LanguageDetectionMethod=uilanguage
SetupLogging=no
ChangesAssociations=no

[Languages]
Name: "chinesesimplified"; MessagesFile: "{#MyLangFile}"

[Messages]
PrivilegesRequiredOverrideTitle=选择安装范围
PrivilegesRequiredOverrideInstruction=这一步只影响“标准安装”。便携安装不需要管理员，选“仅安装给我”即可。
PrivilegesRequiredOverrideText1=%1 可以安装给所有用户（需要管理员权限），也可以只安装给你自己。
PrivilegesRequiredOverrideText2=%1 可以只安装给你自己，也可以安装给所有用户（需要管理员权限）。
PrivilegesRequiredOverrideAllUsers=安装给所有用户(&A)
PrivilegesRequiredOverrideAllUsersRecommended=安装给所有用户(&A)（推荐）
PrivilegesRequiredOverrideCurrentUser=仅安装给我(&M)
PrivilegesRequiredOverrideCurrentUserRecommended=仅安装给我(&M)（推荐）

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加任务："; Flags: checkedonce
Name: "startmenu"; Description: "创建开始菜单快捷方式"; GroupDescription: "附加任务："; Flags: checkedonce

[Files]
Source: "{#MySourceDir}\WIN7Harness-amd64.exe"; DestDir: "{app}"; DestName: "Win7Harness.exe"; Check: IsWin64; Flags: ignoreversion
Source: "{#MySourceDir}\WIN7Harness-386.exe"; DestDir: "{app}"; DestName: "Win7Harness.exe"; Check: not IsWin64; Flags: ignoreversion
Source: "{#MyIcon}"; DestDir: "{app}"; Flags: ignoreversion
Source: "{#MySourceDir}\README.txt"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist
Source: "{#MySourceDir}\version.txt"; DestDir: "{app}"; Flags: ignoreversion skipifsourcedoesntexist

[Dirs]
; 便携模式：整个程序目录和 data 目录允许本机其他用户修改。
; FAT32/exFAT 会忽略 ACL；NTFS U 盘换电脑时这一步很重要。
Name: "{app}"; Permissions: users-modify; Check: IsPortableMode
Name: "{app}\data"; Permissions: users-modify; Check: IsPortableMode

[Icons]
Name: "{group}\WIN7 Harness"; Filename: "{app}\Win7Harness.exe"; WorkingDir: "{app}"; IconFilename: "{app}\win7harness.ico"; Tasks: startmenu; Check: not IsPortableMode
Name: "{group}\卸载 WIN7 Harness"; Filename: "{uninstallexe}"; Tasks: startmenu; Check: not IsPortableMode
Name: "{userdesktop}\WIN7 Harness"; Filename: "{app}\Win7Harness.exe"; WorkingDir: "{app}"; IconFilename: "{app}\win7harness.ico"; Tasks: desktopicon; Check: not IsPortableMode and not IsAdminInstallMode
Name: "{commondesktop}\WIN7 Harness"; Filename: "{app}\Win7Harness.exe"; WorkingDir: "{app}"; IconFilename: "{app}\win7harness.ico"; Tasks: desktopicon; Check: not IsPortableMode and IsAdminInstallMode

[Run]
Filename: "{app}\Win7Harness.exe"; Description: "启动 WIN7 Harness"; WorkingDir: "{app}"; Flags: nowait postinstall skipifsilent runasoriginaluser

[UninstallDelete]
Type: files; Name: "{app}\install-mode.txt"

[Code]

var
  ModePage: TWizardPage;
  LocalRadio, PortableRadio: TNewRadioButton;
  LocalDesc, PortableDesc: TNewStaticText;
  DriveLabel, SubLabel: TNewStaticText;
  DriveCombo: TNewComboBox;
  SubEdit: TNewEdit;
  PathInfo: TNewStaticText;
  RefreshBtn: TNewButton;
  Mode: Integer;
  SetupOnRemovable: Boolean;
  DriveListLoaded: Boolean;
  LastDrive: String;
  StandardDir: String;
  SkipLegacy: Boolean;
  HasDirParam: Boolean;
  DirParam: String;

function GetDriveTypeW(lpRootPathName: String): Cardinal;
  external 'GetDriveTypeW@kernel32.dll stdcall';

function GetVolumeInformationW(
  lpRootPathName: String;
  lpVolumeNameBuffer: String;
  nVolumeNameSize: DWORD;
  var lpVolumeSerialNumber: DWORD;
  var lpMaximumComponentLength: DWORD;
  var lpFileSystemFlags: DWORD;
  lpFileSystemNameBuffer: String;
  nFileSystemNameSize: DWORD): Boolean;
  external 'GetVolumeInformationW@kernel32.dll stdcall';

function IsPortableMode: Boolean;
begin
  Result := Mode = 1;
end;

function TrimSlash(const S: String): String;
begin
  Result := S;
  while (Length(Result) > 0) and
        ((Result[Length(Result)] = '\') or (Result[Length(Result)] = '/')) do
    Delete(Result, Length(Result), 1);
end;

function GetDefaultDir(Param: String): String;
begin
  if IsAdminInstallMode then
    Result := ExpandConstant('{pf}\WIN7Harness')
  else
    Result := ExpandConstant('{localappdata}\Programs\WIN7Harness');
end;

function GetPortableDrive: String;
begin
  if (DriveCombo <> nil) and (DriveCombo.ItemIndex >= 0) and
     (Length(DriveCombo.Text) >= 2) then
    Result := Copy(DriveCombo.Text, 1, 2)
  else
    Result := '';
end;

function GetPortablePath: String;
begin
  if GetPortableDrive = '' then
    Result := ''
  else
    Result := AddBackslash(GetPortableDrive) + Trim(SubEdit.Text);
end;

function DirWritable(const Dir: String): Boolean;
var
  TestFile: String;
begin
  Result := False;
  if Trim(Dir) = '' then
    Exit;
  if not ForceDirectories(Dir) then
    Exit;
  TestFile := AddBackslash(Dir) + '.wh-write-test';
  if SaveStringToFile(TestFile, 'x', False) then
  begin
    DeleteFile(TestFile);
    Result := True;
  end;
end;

function ValidSubFolder(const S: String): Boolean;
var
  I: Integer;
  Bad: String;
begin
  Bad := '<>:"/\|?*';
  Result := (Trim(S) <> '') and (Pos('..', S) = 0);
  if not Result then
    Exit;
  for I := 1 to Length(Bad) do
    if Pos(Bad[I], S) > 0 then
    begin
      Result := False;
      Exit;
    end;
end;

function ValidateTargetDir(const Dir: String; Portable: Boolean): Boolean;
var
  Drive: String;
  DT: Cardinal;
  FreeBytes, TotalBytes: Int64;
begin
  Result := False;
  if Trim(Dir) = '' then
  begin
    MsgBox('请先选择安装位置。', mbError, MB_OK);
    Exit;
  end;
  if Length(Dir) > 200 then
  begin
    MsgBox('路径太长。Win7 的路径上限是 260 个字符，请换一个更短的目录。', mbError, MB_OK);
    Exit;
  end;
  Drive := ExtractFileDrive(Dir);
  if Drive = '' then
  begin
    MsgBox('请选择带盘符的完整路径，例如 E:\WIN7Harness。', mbError, MB_OK);
    Exit;
  end;
  DT := GetDriveTypeW(Drive + '\');
  if (DT <> 2) and (DT <> 3) then
  begin
    MsgBox('所选位置不是可写磁盘（可能是光驱、网络盘或无效盘符）。请重新选择。', mbError, MB_OK);
    Exit;
  end;
  if Portable and (Uppercase(Drive) = Uppercase(ExpandConstant('{sd}'))) then
  begin
    MsgBox('不能把便携版装在系统盘 C:。请选择 U 盘或移动硬盘。', mbError, MB_OK);
    Exit;
  end;
  if not GetSpaceOnDisk64(Drive + '\', FreeBytes, TotalBytes) then
  begin
    MsgBox('读不到磁盘剩余空间，请确认盘符可用。', mbError, MB_OK);
    Exit;
  end;
  if Portable and (FreeBytes < 268435456) then
  begin
    MsgBox('空间不足：便携安装至少需要 256 MB 可用空间，建议留 1 GB 以上。', mbError, MB_OK);
    Exit;
  end;
  if (not Portable) and
     (Uppercase(TrimSlash(Dir)) = Uppercase(TrimSlash(ExpandConstant('{localappdata}\Win7Harness')))) then
  begin
    MsgBox('这个目录是 WIN7 Harness 的数据目录（会话和日志）。' + #13#10 +
           '请换一个程序目录，例如默认的 LocalAppData\Programs\WIN7Harness。', mbError, MB_OK);
    Exit;
  end;
  if not DirWritable(Dir) then
  begin
    MsgBox('当前用户对所选目录没有写权限。' + #13#10 + #13#10 +
           '请换一个目录；如果要装到 Program Files，请重新运行安装包并选择“所有用户”。',
           mbError, MB_OK);
    Exit;
  end;
  Result := True;
end;

procedure UpdatePortableInfo;
var
  Drive, Path: String;
  DT: Cardinal;
  FreeBytes, TotalBytes: Int64;
begin
  if PathInfo = nil then
    Exit;
  Drive := GetPortableDrive;
  if Drive = '' then
  begin
    PathInfo.Caption := '未检测到可用的 U 盘/移动硬盘。插好后点“刷新”。';
    PathInfo.Font.Color := clRed;
    Exit;
  end;
  if not GetSpaceOnDisk64(Drive + '\', FreeBytes, TotalBytes) then
  begin
    PathInfo.Caption := '读不到 ' + Drive + ' 的剩余空间。';
    PathInfo.Font.Color := clRed;
    Exit;
  end;
  Path := GetPortablePath;
  DT := GetDriveTypeW(Drive + '\');
  PathInfo.Caption := '将安装到 ' + Path + '（' + IntToStr(FreeBytes div 1048576) + ' MB 可用）';
  if DT = 3 then
    PathInfo.Caption := PathInfo.Caption + ' · 这是本地磁盘，不是可移动盘';
  if FreeBytes < 268435456 then
    PathInfo.Font.Color := clRed
  else
    PathInfo.Font.Color := clWindowText;
end;

procedure ShowPortableControls(Show: Boolean);
begin
  DriveLabel.Visible := Show;
  DriveCombo.Visible := Show;
  RefreshBtn.Visible := Show;
  SubLabel.Visible := Show;
  SubEdit.Visible := Show;
  PathInfo.Visible := Show;
end;

procedure ModeChanged(Sender: TObject);
begin
  if PortableRadio.Checked then
  begin
    Mode := 1;
    ShowPortableControls(True);
    UpdatePortableInfo;
  end
  else
  begin
    Mode := 0;
    ShowPortableControls(False);
    if (StandardDir <> '') and (WizardForm <> nil) then
      WizardForm.DirEdit.Text := StandardDir;
  end;
end;

procedure DriveChanged(Sender: TObject);
begin
  LastDrive := GetPortableDrive;
  UpdatePortableInfo;
end;

procedure SubFolderChanged(Sender: TObject);
begin
  UpdatePortableInfo;
end;

procedure RefreshDriveList; forward;

procedure RefreshClick(Sender: TObject);
begin
  LastDrive := GetPortableDrive;
  DriveListLoaded := False;
  RefreshDriveList;
end;

procedure AddDrives(WantType: Cardinal);
var
  C: Char;
  I: Integer;
  Drive, Vol, Fs, Kind, Item: String;
  DT: Cardinal;
  FreeBytes, TotalBytes: Int64;
  MaxLen, Flags: DWORD;
  Serial: DWORD;
  P: Integer;
begin
  for I := Ord('D') to Ord('Z') do
  begin
    C := Chr(I);
    Drive := C + ':';
    DT := GetDriveTypeW(Drive + '\');
    if DT <> WantType then
      Continue;
    if not GetSpaceOnDisk64(Drive + '\', FreeBytes, TotalBytes) then
      Continue;
    SetLength(Vol, 256);
    SetLength(Fs, 256);
    MaxLen := 0;
    Flags := 0;
    if not GetVolumeInformationW(Drive + '\', Vol, 256, Serial,
                                 MaxLen, Flags, Fs, 256) then
    begin
      Vol := '';
      Fs := '';
    end;
    P := Pos(#0, Vol);
    if P > 0 then
      Vol := Copy(Vol, 1, P - 1);
    P := Pos(#0, Fs);
    if P > 0 then
      Fs := Copy(Fs, 1, P - 1);
    Vol := Trim(Vol);
    Fs := Trim(Fs);
    if Vol = '' then
      Vol := '无卷标';
    if Fs = '' then
      Fs := '未知格式';
    if WantType = 2 then
      Kind := '可移动'
    else
      Kind := '本地磁盘';
    Item := Drive + '  ' + Kind + '  ' + Vol + '  (' + Fs + ', ' +
            IntToStr(FreeBytes div 1048576) + ' MB 可用)';
    DriveCombo.Items.Add(Item);
  end;
end;

procedure RefreshDriveList;
var
  I, Idx, SetupIdx: Integer;
  SetupDrive: String;
begin
  if DriveCombo = nil then
    Exit;
  DriveCombo.Items.Clear;
  AddDrives(2); // 可移动盘优先
  AddDrives(3); // USB 移动硬盘常被识别成本地盘

  if DriveCombo.Items.Count = 0 then
  begin
    DriveCombo.ItemIndex := -1;
    UpdatePortableInfo;
    DriveListLoaded := True;
    Exit;
  end;

  Idx := 0;
  if LastDrive <> '' then
    for I := 0 to DriveCombo.Items.Count - 1 do
      if Copy(DriveCombo.Items[I], 1, 2) = LastDrive then
      begin
        Idx := I;
        Break;
      end;

  SetupDrive := ExtractFileDrive(ParamStr(0));
  SetupIdx := -1;
  if SetupOnRemovable and (SetupDrive <> '') then
    for I := 0 to DriveCombo.Items.Count - 1 do
      if Copy(DriveCombo.Items[I], 1, 2) = SetupDrive then
      begin
        SetupIdx := I;
        Break;
      end;
  if SetupIdx >= 0 then
    Idx := SetupIdx;

  DriveCombo.ItemIndex := Idx;
  LastDrive := GetPortableDrive;
  UpdatePortableInfo;
  DriveListLoaded := True;
end;

procedure InitializeWizard;
begin
  StandardDir := WizardForm.DirEdit.Text;
  if StandardDir = '' then
    StandardDir := GetDefaultDir('');

  ModePage := CreateCustomPage(wpWelcome, '选择安装方式',
    '标准安装装到这台电脑；便携安装把程序和数据放进 U 盘，换电脑即插即用。');

  LocalRadio := TNewRadioButton.Create(ModePage);
  LocalRadio.Parent := ModePage.Surface;
  LocalRadio.Left := 0;
  LocalRadio.Top := 0;
  LocalRadio.Width := ModePage.SurfaceWidth;
  LocalRadio.Caption := '标准安装（推荐）';
  LocalRadio.OnClick := @ModeChanged;

  LocalDesc := TNewStaticText.Create(ModePage);
  LocalDesc.Parent := ModePage.Surface;
  LocalDesc.Left := ScaleX(20);
  LocalDesc.Top := ScaleY(22);
  LocalDesc.Width := ModePage.SurfaceWidth - ScaleX(20);
  LocalDesc.Caption := '装到本机，创建桌面/开始菜单快捷方式，可在“程序和功能”里卸载。';

  PortableRadio := TNewRadioButton.Create(ModePage);
  PortableRadio.Parent := ModePage.Surface;
  PortableRadio.Left := 0;
  PortableRadio.Top := ScaleY(58);
  PortableRadio.Width := ModePage.SurfaceWidth;
  PortableRadio.Caption := '便携安装（U 盘 / 移动硬盘）';
  PortableRadio.OnClick := @ModeChanged;

  PortableDesc := TNewStaticText.Create(ModePage);
  PortableDesc.Parent := ModePage.Surface;
  PortableDesc.Left := ScaleX(20);
  PortableDesc.Top := ScaleY(80);
  PortableDesc.Width := ModePage.SurfaceWidth - ScaleX(20);
  PortableDesc.Caption := '程序和数据放在同一个文件夹；不写注册表，不建快捷方式。';

  DriveLabel := TNewStaticText.Create(ModePage);
  DriveLabel.Parent := ModePage.Surface;
  DriveLabel.Left := ScaleX(20);
  DriveLabel.Top := ScaleY(116);
  DriveLabel.Caption := 'U 盘 / 移动硬盘';

  DriveCombo := TNewComboBox.Create(ModePage);
  DriveCombo.Parent := ModePage.Surface;
  DriveCombo.Left := ScaleX(20);
  DriveCombo.Top := ScaleY(136);
  DriveCombo.Width := ModePage.SurfaceWidth - ScaleX(130);
  DriveCombo.Style := csDropDownList;
  DriveCombo.OnChange := @DriveChanged;

  RefreshBtn := TNewButton.Create(ModePage);
  RefreshBtn.Parent := ModePage.Surface;
  RefreshBtn.Left := ModePage.SurfaceWidth - ScaleX(90);
  RefreshBtn.Top := ScaleY(134);
  RefreshBtn.Width := ScaleX(70);
  RefreshBtn.Caption := '刷新';
  RefreshBtn.OnClick := @RefreshClick;

  SubLabel := TNewStaticText.Create(ModePage);
  SubLabel.Parent := ModePage.Surface;
  SubLabel.Left := ScaleX(20);
  SubLabel.Top := ScaleY(170);
  SubLabel.Caption := 'U 盘里的子目录';

  SubEdit := TNewEdit.Create(ModePage);
  SubEdit.Parent := ModePage.Surface;
  SubEdit.Left := ScaleX(20);
  SubEdit.Top := ScaleY(190);
  SubEdit.Width := ScaleX(200);
  SubEdit.Text := 'WIN7Harness';
  SubEdit.OnChange := @SubFolderChanged;

  PathInfo := TNewStaticText.Create(ModePage);
  PathInfo.Parent := ModePage.Surface;
  PathInfo.Left := ScaleX(20);
  PathInfo.Top := ScaleY(228);
  PathInfo.Width := ModePage.SurfaceWidth - ScaleX(20);
  PathInfo.AutoSize := False;
  PathInfo.Height := ScaleY(40);
  PathInfo.Caption := '';

  if (Mode = 1) or SetupOnRemovable then
  begin
    Mode := 1;
    PortableRadio.Checked := True;
  end
  else
  begin
    Mode := 0;
    LocalRadio.Checked := True;
  end;
  ShowPortableControls(Mode = 1);
  RefreshDriveList;
end;

function CmdLineParamExists(const Value: String): Boolean;
var
  I: Integer;
begin
  Result := False;
  for I := 1 to ParamCount do
    if CompareText(ParamStr(I), Value) = 0 then
    begin
      Result := True;
      Exit;
    end;
end;

function GetCmdLineValue(const Prefix: String): String;
var
  I: Integer;
  S: String;
begin
  Result := '';
  for I := 1 to ParamCount do
  begin
    S := ParamStr(I);
    if CompareText(Copy(S, 1, Length(Prefix)), Prefix) = 0 then
    begin
      Result := Copy(S, Length(Prefix) + 1, Length(S));
      if (Length(Result) >= 2) and (Result[1] = '"') and
         (Result[Length(Result)] = '"') then
        Result := Copy(Result, 2, Length(Result) - 2);
      Exit;
    end;
  end;
end;

function InitializeSetup: Boolean;
var
  Root: String;
begin
  Result := True;
  Mode := 0;
  DriveListLoaded := False;
  LastDrive := '';
  if CmdLineParamExists('/PORTABLE') then
    Mode := 1;
  SkipLegacy := CmdLineParamExists('/SKIPLEGACY');
  DirParam := GetCmdLineValue('/DIR=');
  HasDirParam := DirParam <> '';
  Root := ExtractFileDrive(ParamStr(0));
  SetupOnRemovable := (Root <> '') and
    (GetDriveTypeW(Root + '\') = 2);
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  Dir: String;
begin
  Result := True;
  if (ModePage <> nil) and (CurPageID = ModePage.ID) then
  begin
    if IsPortableMode then
    begin
      if HasDirParam then
      begin
        if not ValidateTargetDir(DirParam, True) then
        begin
          Result := False;
          Exit;
        end;
        WizardForm.DirEdit.Text := DirParam;
      end
      else
      begin
        if not ValidSubFolder(SubEdit.Text) then
        begin
          MsgBox('子目录名不合法：不能为空，不能包含 < > : " / \ | ? *，也不能包含 ..。', mbError, MB_OK);
          Result := False;
          Exit;
        end;
        if not ValidateTargetDir(GetPortablePath, True) then
        begin
          Result := False;
          Exit;
        end;
        WizardForm.DirEdit.Text := GetPortablePath;
      end;
    end
    else
    begin
      if (StandardDir <> '') and (WizardForm.DirEdit.Text = '') then
        WizardForm.DirEdit.Text := StandardDir;
    end;
    Exit;
  end;

  if CurPageID = wpSelectDir then
  begin
    Dir := WizardForm.DirEdit.Text;
    if IsPortableMode then
    begin
      if not ValidSubFolder(ExtractFileName(Dir)) then
      begin
        MsgBox('便携目录的最后一层名字不合法：不能包含 < > : " / \ | ? *，也不能包含 ..。', mbError, MB_OK);
        Result := False;
        Exit;
      end;
      if not ValidateTargetDir(Dir, True) then
      begin
        Result := False;
        Exit;
      end;
    end
    else
    begin
      if not ValidateTargetDir(Dir, False) then
      begin
        Result := False;
        Exit;
      end;
      StandardDir := Dir;
    end;
  end;
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := False;
  if IsPortableMode and (PageID = wpSelectTasks) then
    Result := True;
end;

procedure RemoveLegacyInstall;
var
  OldDir: String;
begin
  if SkipLegacy then
    Exit;
  OldDir := ExpandConstant('{localappdata}\Win7Harness');
  // 只删旧 setup.bat 装进去的程序文件，绝不整目录删除：
  // 这个目录同时是会话/日志目录。
  DeleteFile(OldDir + '\Win7Harness.exe');
  DeleteFile(OldDir + '\win7harness.ico');
  DeleteFile(OldDir + '\README.txt');
  DeleteFile(OldDir + '\uninstall.bat');
  DeleteFile(OldDir + '\shortcuts.txt');
  DeleteFile(ExpandConstant('{userdesktop}\WIN7 Harness.lnk'));
  DeleteFile(ExpandConstant('{userprograms}\WIN7 Harness.lnk'));
  RegDeleteKeyIncludingSubkeys(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Uninstall\Win7Harness');
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  OldExe: String;
begin
  Result := '';
  NeedsRestart := False;
  if IsPortableMode or SkipLegacy then
    Exit;
  OldExe := ExpandConstant('{localappdata}\Win7Harness\Win7Harness.exe');
  if FileExists(OldExe) then
  begin
    if not DeleteFile(OldExe) then
      Result := '检测到旧版 WIN7 Harness 正在运行。' + #13#10 +
                '请先退出旧版程序，再重新运行安装包。';
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  ModeFile, FlagFile: String;
begin
  if CurStep <> ssPostInstall then
    Exit;
  ModeFile := ExpandConstant('{app}\install-mode.txt');
  if IsPortableMode then
  begin
    SaveStringToFile(ModeFile, 'portable', False);
    FlagFile := ExpandConstant('{app}\portable.flag');
    SaveStringToFile(FlagFile, '', False);
    ForceDirectories(ExpandConstant('{app}\data'));
  end
  else if IsAdminInstallMode then
    SaveStringToFile(ModeFile, 'standard-all', False)
  else
    SaveStringToFile(ModeFile, 'standard-user', False);

  if not IsPortableMode then
    RemoveLegacyInstall;
end;









