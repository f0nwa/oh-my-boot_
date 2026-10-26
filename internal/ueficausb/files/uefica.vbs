' Enables the Microsoft UEFI CA key ("3rd party UEFI CA") in the BIOS of
' Lenovo, Dell and HP machines from WinPE. Port of the vendor logic of
' scripts/windows/Enable-MicrosoftUefiCA.ps1 (Set-LenovoUefiCA, Set-DellUefiCA,
' Set-HPUefiCA).
'
'   cscript uefica.vbs /check              show the machine, exit 10/2/1/40/41
'   cscript uefica.vbs /apply [/password]  change the option, exit 20/10/2/3/1
'   cscript uefica.vbs /wait               wait 10 seconds
'   add /test:<scenario> to a copy with testdata/uefica-stubs.vbs appended
'   (testdata/uefica-tests.cmd builds it)
'
' Exit codes: 20 changed, 10 nothing to do, 2 not supported or no option,
' 3 password not accepted, 1 error, 40 can change (no password known),
' 41 can change (BIOS password set). "Changed" is not 0 on purpose: a script
' that stops on an error may leave 0, and that must never restart the machine.
'
' The BIOS password is read from standard input into the variable "secret"
' and is never printed, logged or written to a file.
Option Explicit

Const LENOVO_NS = "root\wmi"
Const DELL_NS = "root\dcim\sysman\biosattributes"
Const DELL_SECURITY_NS = "root\dcim\sysman\wmisecurity"
Const HP_NS = "root\HP\InstrumentedBIOS"
Const SECURE_BOOT_KEY = "HKLM\SYSTEM\CurrentControlSet\Control\SecureBoot\State\UEFISecureBootEnabled"

Dim fso, mode, testName, wantPassword, resultPath, logPath, resultText, arg, argIndex
Set fso = CreateObject("Scripting.FileSystemObject")
mode = ""
testName = ""
wantPassword = False
For argIndex = 0 To WScript.Arguments.Count - 1
    arg = WScript.Arguments(argIndex)
    Select Case LCase(arg)
        Case "/check": mode = "check"
        Case "/apply": mode = "apply"
        Case "/wait": mode = "wait"
        Case "/password": wantPassword = True
        Case Else
            If LCase(Left(arg, 6)) = "/test:" Then testName = Mid(arg, 7)
    End Select
Next

resultPath = "X:\uefica.txt"
logPath = ""
If testName <> "" Then
    StubInit testName
    resultPath = "uefica.txt"
    logPath = "uefica-calls.log"
End If
resultText = ""

Dim exitCode
' Any error the functions below do not handle ends here with exit code 1.
On Error Resume Next
Select Case mode
    Case "check": exitCode = CheckMachine()
    Case "apply": exitCode = ApplyChange()
    Case "wait"
        WScript.Sleep 10000
        exitCode = 0
    Case Else
        WScript.Echo "Usage: cscript uefica.vbs /check | /apply [/password] | /wait"
        exitCode = 1
End Select
If Err.Number <> 0 Then
    WScript.Echo ""
    WScript.Echo "Error: " & Err.Description & " (0x" & Hex(Err.Number) & "). Nothing changed."
    exitCode = 1
End If
On Error GoTo 0
WScript.Quit exitCode

' ---- output -----------------------------------------------------------------

Sub Say(text)
    WScript.Echo text
    resultText = resultText & text & vbCrLf
End Sub

Sub SaveResult()
    Dim f
    On Error Resume Next
    Set f = fso.CreateTextFile(resultPath, True, False)
    f.Write resultText
    f.Close
End Sub

Sub LogCall(text)
    Dim f
    If logPath = "" Then Exit Sub
    Set f = fso.OpenTextFile(logPath, 8, True)
    f.WriteLine text
    f.Close
End Sub

' ---- machine, registry and WMI access (replaced by stubs with /test) ---------

Function SystemInfo()
    Dim cs, bios
    If testName <> "" Then
        SystemInfo = StubSystem()
        Exit Function
    End If
    On Error Resume Next
    Set cs = FirstInstance("root\cimv2", "Win32_ComputerSystem")
    Set bios = FirstInstance("root\cimv2", "Win32_BIOS")
    SystemInfo = Array(Trim(cs.Manufacturer & ""), Trim(cs.Model & ""), Trim(bios.SerialNumber & ""))
    If Err.Number <> 0 Then SystemInfo = Array("", "", "")
End Function

' -1: no Secure Boot key (BIOS/CSM mode), 0: off, 1: on.
Function SecureBootState()
    Dim value
    If testName <> "" Then
        SecureBootState = StubSecureBoot()
        Exit Function
    End If
    On Error Resume Next
    value = CreateObject("WScript.Shell").RegRead(SECURE_BOOT_KEY)
    If Err.Number <> 0 Then
        SecureBootState = -1
    ElseIf CLng(value) = 1 Then
        SecureBootState = 1
    Else
        SecureBootState = 0
    End If
End Function

' All instances of a class as an array; raises an error when the class or the
' namespace is missing.
Function WmiInstances(ns, cls)
    Dim set1, item, list, n
    If testName <> "" Then
        WmiInstances = StubInstances(ns, cls)
        Exit Function
    End If
    Set set1 = GetObject("winmgmts:{impersonationLevel=impersonate,authenticationLevel=pktPrivacy}!\\.\" & ns).InstancesOf(cls)
    ReDim list(-1)
    n = 0
    For Each item In set1
        ReDim Preserve list(n)
        Set list(n) = item
        n = n + 1
    Next
    WmiInstances = list
End Function

Function FirstInstance(ns, cls)
    Dim rows
    rows = WmiInstances(ns, cls)
    If UBound(rows) < 0 Then Err.Raise 5, "uefica", "no instance of " & cls
    Set FirstInstance = rows(0)
End Function

' Calls a method of the first instance of a class with named input
' parameters and returns one output parameter.
Function WmiCall(ns, cls, method, names, values, outName)
    Dim inst, inParams, outParams, i
    If testName <> "" Then
        LogCall method & " " & JoinValues(values)
        WmiCall = StubCall(cls, method, values)
        Exit Function
    End If
    Set inst = FirstInstance(ns, cls)
    If UBound(names) >= 0 Then
        Set inParams = inst.Methods_(method).InParameters.SpawnInstance_()
        For i = 0 To UBound(names)
            inParams.Properties_(names(i)).Value = values(i)
        Next
        Set outParams = inst.ExecMethod_(method, inParams)
    Else
        Set outParams = inst.ExecMethod_(method)
    End If
    WmiCall = outParams.Properties_(outName).Value
End Function

Function JoinValues(values)
    Dim i, s, v
    s = ""
    For i = 0 To UBound(values)
        If i > 0 Then s = s & ";"
        If IsArray(values(i)) Then
            For Each v In values(i)
                s = s & v & " "
            Next
            s = RTrim(s)
        Else
            s = s & values(i)
        End If
    Next
    JoinValues = s
End Function

Function ReadPassword()
    If testName <> "" Then
        ReadPassword = StubReadSecret()
    Else
        ReadPassword = WScript.StdIn.ReadLine()
    End If
End Function

' ---- helpers -------------------------------------------------------------------

Function TryInstances(ns, cls, ByRef rows)
    On Error Resume Next
    rows = WmiInstances(ns, cls)
    TryInstances = (Err.Number = 0)
    Err.Clear
End Function

Function TryCall(ns, cls, method, names, values, outName, ByRef result)
    On Error Resume Next
    result = WmiCall(ns, cls, method, names, values, outName)
    If Err.Number <> 0 Then
        result = "WMI error 0x" & Hex(Err.Number) & " " & Err.Description
        TryCall = False
    Else
        TryCall = True
    End If
    Err.Clear
End Function

' A property as a scalar: array values (Dell's CurrentValue is string[] in
' the DMTF model) give their first element, Null gives "".
Function Prop(row, name)
    Dim v
    On Error Resume Next
    Prop = ""
    v = row.Properties_(name).Value
    If Err.Number <> 0 Then Exit Function
    If IsArray(v) Then
        If UBound(v) >= 0 Then v = v(LBound(v)) Else v = ""
    End If
    If IsNull(v) Or IsEmpty(v) Then v = ""
    Prop = v
End Function

Function NumProp(row, name)
    Dim v
    v = Prop(row, name)
    If VarType(v) = vbBoolean Then
        If v Then NumProp = 1 Else NumProp = 0
    ElseIf IsNumeric(v) Then
        NumProp = CLng(v)
    Else
        NumProp = 0
    End If
End Function

Function Vendor(manufacturer)
    Dim m
    m = UCase(manufacturer)
    If InStr(m, "LENOVO") = 1 Then
        Vendor = "Lenovo"
    ElseIf InStr(m, "DELL") = 1 Then
        Vendor = "Dell"
    ElseIf InStr(m, "HP") = 1 Or InStr(m, "HEWLETT") = 1 Then
        Vendor = "HP"
    Else
        Vendor = ""
    End If
End Function

' Unicode code of a character. AscW exists on Windows; Wine's VBScript (used
' to run testdata/uefica-tests.cmd on Linux) lacks it, so fall back to Asc.
Function CodeOf(ch)
    On Error Resume Next
    CodeOf = AscW(ch)
    If Err.Number <> 0 Then
        Err.Clear
        CodeOf = Asc(ch)
    End If
    If CodeOf < 0 Then CodeOf = CodeOf + 65536
End Function

' UTF-8 bytes of a string (characters of the Basic Multilingual Plane).
Function Utf8Bytes(s)
    Dim i, c, list, n
    ReDim list(-1)
    n = 0
    For i = 1 To Len(s)
        c = CodeOf(Mid(s, i, 1))
        If c < &H80 Then
            ReDim Preserve list(n): list(n) = c: n = n + 1
        ElseIf c < &H800 Then
            ReDim Preserve list(n + 1)
            list(n) = &HC0 Or (c \ &H40)
            list(n + 1) = &H80 Or (c And &H3F)
            n = n + 2
        Else
            ReDim Preserve list(n + 2)
            list(n) = &HE0 Or (c \ &H1000)
            list(n + 1) = &H80 Or ((c \ &H40) And &H3F)
            list(n + 2) = &H80 Or (c And &H3F)
            n = n + 3
        End If
    Next
    Utf8Bytes = list
End Function

' ---- option state: returns Array(status, name, value, target) ----------------
' status: Enabled, Disabled, Missing, Failed

Function LenovoOption()
    Dim rows, row, line, main, rest, name, value, options, p, opt, target
    If Not TryInstances(LENOVO_NS, "Lenovo_BiosSetting", rows) Then
        LenovoOption = Array("Missing", "", "Lenovo BIOS WMI interface is not available", "")
        Exit Function
    End If
    For Each row In rows
        line = Prop(row, "CurrentSetting")
        If line <> "" Then
            p = InStr(line, ";")
            If p > 0 Then
                main = Left(line, p - 1): rest = Mid(line, p + 1)
            Else
                main = line: rest = ""
            End If
            p = InStr(main, ",")
            If p > 0 Then
                name = Left(main, p - 1): value = Mid(main, p + 1)
            Else
                name = main: value = ""
            End If
            If name = "Allow3rdPartyUEFICA" Or name = "AllowMicrosoft3rdPartyUEFICA" Then
                If value = "Enable" Or value = "Enabled" Then
                    LenovoOption = Array("Enabled", name, value, "")
                    Exit Function
                End If
                target = ""
                p = InStr(rest, "[Optional:")
                If p > 0 Then
                    options = Mid(rest, p + 10)
                    If InStr(options, "]") > 0 Then options = Left(options, InStr(options, "]") - 1)
                    options = Split(options, ",")
                    For Each opt In options
                        If target = "" And (opt = "Enable" Or opt = "Enabled") Then target = opt
                    Next
                End If
                If target = "" Then
                    If value = "Disabled" Then target = "Enabled" Else target = "Enable"
                End If
                LenovoOption = Array("Disabled", name, value, target)
                Exit Function
            End If
        End If
    Next
    LenovoOption = Array("Missing", "", "no Allow3rdPartyUEFICA option on this model", "")
End Function

Function DellOption()
    Dim rows, row, found, n
    If Not TryInstances(DELL_NS, "EnumerationAttribute", rows) Then
        DellOption = Array("Missing", "", "Dell BIOS WMI interface is not available (model before 2018?)", "")
        Exit Function
    End If
    Set found = Nothing
    For Each row In rows
        If found Is Nothing And Prop(row, "AttributeName") = "MSUefiCA" Then Set found = row
    Next
    If found Is Nothing Then
        For Each row In rows
            n = Prop(row, "AttributeName")
            If found Is Nothing And InStr(1, n, "UefiCA", 1) > 0 And InStr(1, n, "Legacy", 1) = 0 Then Set found = row
        Next
    End If
    If found Is Nothing Then
        DellOption = Array("Missing", "", "no Microsoft UEFI CA option on this model", "")
    ElseIf Prop(found, "CurrentValue") = "Enabled" Then
        DellOption = Array("Enabled", Prop(found, "AttributeName"), "Enabled", "")
    Else
        DellOption = Array("Disabled", Prop(found, "AttributeName"), Prop(found, "CurrentValue"), "Enabled")
    End If
End Function

Function HPOption()
    Dim rows, row, found, n
    If Not TryInstances(HP_NS, "HP_BIOSEnumeration", rows) Then
        HPOption = Array("Missing", "", "HP BIOS WMI interface is not available", "")
        Exit Function
    End If
    Set found = Nothing
    For Each row In rows
        If found Is Nothing And Prop(row, "Name") = "Enable MS UEFI CA key" Then Set found = row
    Next
    If found Is Nothing Then
        For Each row In rows
            n = Prop(row, "Name")
            If found Is Nothing And InStr(1, n, "MS UEFI CA", 1) > 0 And InStr(1, n, "Ready", 1) = 0 Then Set found = row
        Next
    End If
    If found Is Nothing Then
        HPOption = Array("Missing", "", "no ""Enable MS UEFI CA key"" option on this model", "")
    ElseIf Prop(found, "CurrentValue") = "Yes" Then
        HPOption = Array("Enabled", Prop(found, "Name"), "Yes", "")
    Else
        HPOption = Array("Disabled", Prop(found, "Name"), Prop(found, "CurrentValue"), "Yes")
    End If
End Function

Function OptionState(vendorName)
    Select Case vendorName
        Case "Lenovo": OptionState = LenovoOption()
        Case "Dell": OptionState = DellOption()
        Case "HP": OptionState = HPOption()
    End Select
End Function

' ---- BIOS password: "yes", "no" or "unknown" ----------------------------------

Function PasswordSet(vendorName)
    Dim rows, row
    PasswordSet = "unknown"
    Select Case vendorName
        Case "Lenovo"
            If TryInstances(LENOVO_NS, "Lenovo_BiosPasswordSettings", rows) Then
                For Each row In rows
                    If (NumProp(row, "PasswordState") And 2) <> 0 Then PasswordSet = "yes" Else PasswordSet = "no"
                Next
            End If
        Case "Dell"
            If TryInstances(DELL_SECURITY_NS, "PasswordObject", rows) Then
                For Each row In rows
                    If Prop(row, "NameId") = "Admin" Then
                        If NumProp(row, "IsPasswordSet") = 1 Then PasswordSet = "yes" Else PasswordSet = "no"
                    End If
                Next
            End If
        Case "HP"
            If TryInstances(HP_NS, "HP_BIOSPassword", rows) Then
                For Each row In rows
                    If Prop(row, "Name") = "Setup Password" Then
                        If NumProp(row, "IsSet") = 1 Then PasswordSet = "yes" Else PasswordSet = "no"
                    End If
                Next
            End If
    End Select
End Function

' ---- changes: return Array(status, detail); status Changed/Denied/Missing/Failed

Function SetLenovo(opt, secret)
    Dim r, rows, hasOpcode
    hasOpcode = False
    If secret <> "" Then
        If TryInstances(LENOVO_NS, "Lenovo_WmiOpcodeInterface", rows) Then hasOpcode = (UBound(rows) >= 0)
    End If
    If secret = "" Then
        If TryCall(LENOVO_NS, "Lenovo_SetBiosSetting", "SetBiosSetting", Array("parameter"), Array(opt(1) & "," & opt(3)), "return", r) Then
            If r = "Success" Then TryCall LENOVO_NS, "Lenovo_SaveBiosSettings", "SaveBiosSettings", Array(), Array(), "return", r
        End If
    ElseIf hasOpcode Then
        ' Newer models: the password through the opcode interface, between set and save.
        If TryCall(LENOVO_NS, "Lenovo_SetBiosSetting", "SetBiosSetting", Array("parameter"), Array(opt(1) & "," & opt(3)), "return", r) Then
            If r = "Success" Then TryCall LENOVO_NS, "Lenovo_WmiOpcodeInterface", "WmiOpcodeInterface", Array("parameter"), Array("WmiOpcodePasswordAdmin:" & secret), "return", r
            If r = "Success" Then TryCall LENOVO_NS, "Lenovo_SaveBiosSettings", "SaveBiosSettings", Array(), Array(), "return", r
        End If
    Else
        ' Older models: the password in both calls.
        If TryCall(LENOVO_NS, "Lenovo_SetBiosSetting", "SetBiosSetting", Array("parameter"), Array(opt(1) & "," & opt(3) & "," & secret & ",ascii,us"), "return", r) Then
            If r = "Success" Then TryCall LENOVO_NS, "Lenovo_SaveBiosSettings", "SaveBiosSettings", Array("parameter"), Array(secret & ",ascii,us"), "return", r
        End If
    End If
    Select Case r
        Case "Success": SetLenovo = Array("Changed", opt(1) & "=" & opt(3))
        Case "Access Denied": SetLenovo = Array("Denied", "Lenovo: Access Denied (supervisor password needed or wrong)")
        Case Else: SetLenovo = Array("Failed", "Lenovo: " & r)
    End Select
End Function

Function SetDell(opt, secret)
    Dim r, names, values, data
    names = Array("AttributeName", "AttributeValue", "SecType", "SecHndCount")
    values = Array(opt(1), "Enabled", 0, 0)
    If secret <> "" Then
        data = Utf8Bytes(secret)
        names = Array("AttributeName", "AttributeValue", "SecType", "SecHndCount", "SecHandle")
        values = Array(opt(1), "Enabled", 1, UBound(data) + 1, data)
    End If
    If Not TryCall(DELL_NS, "BIOSAttributeInterface", "SetAttribute", names, values, "Status", r) Then
        SetDell = Array("Failed", "Dell: " & r)
        Exit Function
    End If
    If Not IsNumeric(r) Then r = -1
    Select Case CLng(r)
        Case 0: SetDell = Array("Changed", opt(1) & "=Enabled")
        Case 3: SetDell = Array("Denied", "Dell: Access Denied (admin password needed or wrong)")
        Case 4: SetDell = Array("Missing", "Dell: Not Supported")
        Case Else: SetDell = Array("Failed", "Dell: SetAttribute returned " & r)
    End Select
End Function

Function SetHP(opt, secret)
    Dim r, pw
    pw = ""
    If secret <> "" Then pw = "<utf-16/>" & secret
    If Not TryCall(HP_NS, "HP_BIOSSettingInterface", "SetBIOSSetting", Array("Name", "Value", "Password"), Array(opt(1), "Yes", pw), "Return", r) Then
        SetHP = Array("Failed", "HP: " & r)
        Exit Function
    End If
    If Not IsNumeric(r) Then r = -1
    Select Case CLng(r)
        Case 0: SetHP = Array("Changed", opt(1) & "=Yes")
        Case 6: SetHP = Array("Denied", "HP: Access Denied (setup password needed or wrong)")
        Case 1: SetHP = Array("Missing", "HP: Not Supported")
        Case Else: SetHP = Array("Failed", "HP: SetBIOSSetting returned " & r)
    End Select
End Function

' ---- modes ---------------------------------------------------------------------

' Common start of both modes: prints the machine and returns the vendor, or
' an exit code as a number when there is nothing to change.
Function Prepare(ByRef opt, ByRef vendorName, show)
    Dim info, sb
    info = SystemInfo()
    sb = SecureBootState()
    vendorName = Vendor(info(0))
    If show Then
        Say "Manufacturer:  " & info(0)
        Say "Model:         " & info(1)
        Say "Serial number: " & info(2)
        Select Case sb
            Case 1: Say "Secure Boot:   on"
            Case 0: Say "Secure Boot:   off"
            Case Else: Say "Secure Boot:   not in use"
        End Select
    End If
    If sb = -1 Then
        Say ""
        Say "Secure Boot is not in use: the network boot will work as is."
        Prepare = 10
        Exit Function
    End If
    If vendorName = "" Then
        Say ""
        Say "This manufacturer is not supported (only Lenovo, Dell and HP). Nothing changed."
        Prepare = 2
        Exit Function
    End If
    opt = OptionState(vendorName)
    Select Case opt(0)
        Case "Missing"
            Say ""
            Say "The Microsoft UEFI CA option was not found: " & opt(2) & ". Nothing changed."
            Prepare = 2
        Case "Enabled"
            If show Then Say "UEFI CA key:   enabled (" & opt(1) & "=" & opt(2) & ")"
            Say ""
            Say "The Microsoft UEFI CA key is already enabled. Nothing to do."
            Prepare = 10
        Case Else
            If show Then Say "UEFI CA key:   disabled (" & opt(1) & "=" & opt(2) & ")"
            Prepare = ""
    End Select
End Function

Function CheckMachine()
    Dim opt, vendorName, code, pw
    code = Prepare(opt, vendorName, True)
    If code <> "" Then
        CheckMachine = code
        Exit Function
    End If
    pw = PasswordSet(vendorName)
    Say "BIOS password: " & pw
    If pw = "yes" Then CheckMachine = 41 Else CheckMachine = 40
End Function

Function ApplyChange()
    Dim opt, vendorName, code, secret, result
    ' Read the password first: an early exit below must not leave it to be
    ' typed into the next prompt of uefica.cmd.
    secret = ""
    If wantPassword Then secret = ReadPassword()
    code = Prepare(opt, vendorName, False)
    If code <> "" Then
        secret = ""
        SaveResult
        ApplyChange = code
        Exit Function
    End If
    Select Case vendorName
        Case "Lenovo": result = SetLenovo(opt, secret)
        Case "Dell": result = SetDell(opt, secret)
        Case "HP": result = SetHP(opt, secret)
    End Select
    secret = ""
    Say ""
    Select Case result(0)
        Case "Changed"
            Say "Done: " & result(1) & ". The key takes effect after the restart."
            ApplyChange = 20
        Case "Denied"
            Say "The BIOS did not accept the change: " & result(1) & "."
            ApplyChange = 3
        Case "Missing"
            Say "The option cannot be changed on this model: " & result(1) & ". Nothing changed."
            ApplyChange = 2
        Case Else
            Say "Error: " & result(1) & ". Nothing changed."
            ApplyChange = 1
    End Select
    SaveResult
End Function
