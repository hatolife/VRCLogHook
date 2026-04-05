//go:build windows

package main

import (
	"errors"
	"os"
	"syscall"
	"unsafe"
)

const (
	genericRead  = 0x80000000
	genericWrite = 0x40000000
	shareRead    = 0x00000001
	shareWrite   = 0x00000002
	openExisting = 3
)

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procCreateFileW = kernel32.NewProc("CreateFileW")
)

func dialIPC(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	r1, _, e1 := procCreateFileW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(genericRead|genericWrite),
		uintptr(shareRead|shareWrite),
		0,
		uintptr(openExisting),
		0,
		0,
	)
	if r1 == uintptr(syscall.InvalidHandle) {
		if e1 != syscall.Errno(0) {
			return nil, e1
		}
		return nil, errors.New("failed to open named pipe")
	}
	f := os.NewFile(r1, path)
	if f == nil {
		return nil, errors.New("failed to convert named pipe handle")
	}
	return f, nil
}
