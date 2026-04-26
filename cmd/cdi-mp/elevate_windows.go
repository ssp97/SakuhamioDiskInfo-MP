//go:build windows

package main

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const shellExecuteSuccessThreshold = 32

var shell32 = syscall.NewLazyDLL("shell32.dll")
var procShellExecuteW = shell32.NewProc("ShellExecuteW")

func relaunchElevated(cause error) bool {
	if os.Getenv("CDI_MP_ELEVATED") == "1" {
		return false
	}

	exe, err := os.Executable()
	if err != nil {
		return false
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(windowsCommandLine(os.Args[1:]))
	workDirText, _ := os.Getwd()
	workDir, _ := syscall.UTF16PtrFromString(workDirText)

	fmt.Fprintf(os.Stderr, "%v\n正在请求管理员权限重新启动...\n", cause)
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafePointer(verb)),
		uintptr(unsafePointer(file)),
		uintptr(unsafePointer(params)),
		uintptr(unsafePointer(workDir)),
		1,
	)
	return ret > shellExecuteSuccessThreshold
}

func windowsCommandLine(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = windowsQuoteArg(arg)
	}
	return strings.Join(quoted, " ")
}

func windowsQuoteArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if !strings.ContainsAny(arg, " \t\n\v\"") {
		return arg
	}
	var b strings.Builder
	b.WriteByte('"')
	backslashes := 0
	for _, r := range arg {
		if r == '\\' {
			backslashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat(`\`, backslashes*2+1))
			b.WriteRune(r)
			backslashes = 0
			continue
		}
		if backslashes > 0 {
			b.WriteString(strings.Repeat(`\`, backslashes))
			backslashes = 0
		}
		b.WriteRune(r)
	}
	if backslashes > 0 {
		b.WriteString(strings.Repeat(`\`, backslashes*2))
	}
	b.WriteByte('"')
	return b.String()
}

func unsafePointer[T any](ptr *T) unsafe.Pointer {
	return unsafe.Pointer(ptr)
}
