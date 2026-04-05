//go:build windows

package ipc

import (
	"encoding/json"
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
	procCreateFileW = kernel32.NewProc("CreateFileW")
)

func Call(path string, req Request) (Response, error) {
	if path == "" {
		path = DefaultPath()
	}
	h, err := openPipe(path)
	if err != nil {
		return Response{}, err
	}
	defer closeHandle(h)

	f := os.NewFile(uintptr(h), path)
	if f == nil {
		return Response{}, errors.New("failed to open named pipe handle")
	}
	defer f.Close()

	if err := json.NewEncoder(f).Encode(req); err != nil {
		return Response{}, err
	}
	var resp Response
	if err := json.NewDecoder(f).Decode(&resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func openPipe(path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
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
			return 0, e1
		}
		return 0, syscall.EINVAL
	}
	return syscall.Handle(r1), nil
}
