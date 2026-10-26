' Exits 0 when two text files are equal, 1 otherwise (Wine's fc always says equal).
Dim fso, a, b
Set fso = CreateObject("Scripting.FileSystemObject")
a = fso.OpenTextFile(WScript.Arguments(0), 1).ReadAll()
b = fso.OpenTextFile(WScript.Arguments(1), 1).ReadAll()
If a = b Then WScript.Quit 0
WScript.Quit 1
