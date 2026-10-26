' WMI, registry and keyboard stubs for "cscript uefica-test.vbs /test:<scenario>".
' uefica-tests.cmd appends this file to a copy of ../files/uefica.vbs.

' A WMI instance: properties are read as row.Properties_(name).Value.
Class Row
    Private props
    Private Sub Class_Initialize()
        Set props = CreateObject("Scripting.Dictionary")
    End Sub
    Public Sub SetProp(name, value)
        props(name) = value
    End Sub
    Public Function Properties_(name)
        Dim p
        If Not props.Exists(name) Then Err.Raise 4096, "stub", "Not found " & name
        Set p = New PropValue
        p.Value = props(name)
        Set Properties_ = p
    End Function
End Class

Class PropValue
    Public Value
End Class

Dim stubScenario, stubVendor, stubSecureBoot, stubOption, stubPassword, stubOpcode, stubResult

Function NewRow()
    Set NewRow = New Row
End Function

Sub StubInit(name)
    stubScenario = name
    stubSecureBoot = 1
    stubOpcode = True
    stubPassword = 0
    stubResult = ""
    Select Case name
        Case "nosb": stubVendor = "LENOVO": stubSecureBoot = -1
        Case "qemu": stubVendor = "QEMU"
        Case "lenovo-on": stubVendor = "LENOVO": stubOption = "Allow3rdPartyUEFICA,Enable;[Optional:Disable,Enable]"
        Case "lenovo-nopass": stubVendor = "LENOVO": stubOption = "Allow3rdPartyUEFICA,Disable;[Optional:Disable,Enable]"
        Case "lenovo-opcode": stubVendor = "LENOVO": stubOption = "Allow3rdPartyUEFICA,Disable;[Optional:Disable,Enable]": stubPassword = 2
        Case "lenovo-old": stubVendor = "LENOVO": stubOption = "Allow3rdPartyUEFICA,Disable;[Optional:Disable,Enable]": stubPassword = 2: stubOpcode = False
        Case "lenovo-denied": stubVendor = "LENOVO": stubOption = "Allow3rdPartyUEFICA,Disable;[Optional:Disable,Enable]": stubPassword = 2: stubResult = "Access Denied"
        Case "lenovo-unknown": stubVendor = "LENOVO": stubOption = "AllowMicrosoft3rdPartyUEFICA,Disabled": stubPassword = -1
        Case "dell-pass-unicode": stubVendor = "Dell Inc.": stubOption = "Disabled": stubPassword = 1
        Case "dell-pass": stubVendor = "Dell Inc.": stubOption = "Disabled": stubPassword = 1
        Case "dell-array": stubVendor = "Dell Inc.": stubOption = Array("Disabled"): stubPassword = 0
        Case "lenovo-nobracket": stubVendor = "LENOVO": stubOption = "Allow3rdPartyUEFICA,Disable;[Optional:Disable,Enable"
        Case "crash": stubVendor = "CRASH"
        Case "hp-nopass": stubVendor = "HP": stubOption = "No"
        Case "hp-denied": stubVendor = "HP": stubOption = "No": stubPassword = 1: stubResult = 6
        Case Else
            WScript.Echo "unknown scenario " & name
            WScript.Quit 99
    End Select
End Sub

Function StubSystem()
    If stubVendor = "CRASH" Then Err.Raise 5, "stub", "simulated WMI failure"
    StubSystem = Array(stubVendor, "Test model", "PF1ABC")
End Function

Function StubSecureBoot()
    StubSecureBoot = stubSecureBoot
End Function

Function StubInstances(ns, cls)
    Dim r
    Set r = NewRow()
    Select Case cls
        Case "Lenovo_BiosSetting"
            Dim other
            Set other = NewRow()
            other.SetProp "CurrentSetting", "WakeOnLAN,Enable;[Optional:Disable,Enable]"
            r.SetProp "CurrentSetting", stubOption
            StubInstances = Array(other, r)
        Case "Lenovo_BiosPasswordSettings"
            If stubPassword = -1 Then Err.Raise 4096, "stub", "Invalid class"
            r.SetProp "PasswordState", stubPassword
            StubInstances = Array(r)
        Case "Lenovo_WmiOpcodeInterface"
            If Not stubOpcode Then Err.Raise 4096, "stub", "Invalid class"
            StubInstances = Array(r)
        Case "EnumerationAttribute"
            r.SetProp "AttributeName", "MSUefiCA": r.SetProp "CurrentValue", stubOption
            StubInstances = Array(r)
        Case "PasswordObject"
            Dim sys
            Set sys = NewRow()
            sys.SetProp "NameId", "System": sys.SetProp "IsPasswordSet", 0
            r.SetProp "NameId", "Admin": r.SetProp "IsPasswordSet", stubPassword
            StubInstances = Array(sys, r)
        Case "HP_BIOSEnumeration"
            r.SetProp "Name", "Enable MS UEFI CA key": r.SetProp "CurrentValue", stubOption
            StubInstances = Array(r)
        Case "HP_BIOSPassword"
            r.SetProp "Name", "Setup Password": r.SetProp "IsSet", stubPassword
            StubInstances = Array(r)
        Case Else
            Err.Raise 4096, "stub", "Invalid class " & cls
    End Select
End Function

Function StubCall(cls, method, values)
    If Left(cls, 7) = "Lenovo_" Then
        If stubResult <> "" And method = "WmiOpcodeInterface" Then StubCall = stubResult Else StubCall = "Success"
        If stubResult <> "" And Not stubOpcode And method = "SetBiosSetting" Then StubCall = stubResult
    ElseIf stubResult <> "" Then
        StubCall = stubResult
    Else
        StubCall = 0
    End If
End Function

' The password comes from input.txt (Wine's cscript has no standard input,
' and the console code page differs between machines): "U1" stands for the
' Cyrillic "Parol1".
Function StubReadSecret()
    Dim line
    line = CreateObject("Scripting.FileSystemObject").OpenTextFile("input.txt", 1).ReadLine()
    If line = "U1" Then
        StubReadSecret = ChrW(&H41F) & ChrW(&H430) & ChrW(&H440) & ChrW(&H43E) & ChrW(&H43B) & ChrW(&H44C) & "1"
    Else
        StubReadSecret = line
    End If
End Function
