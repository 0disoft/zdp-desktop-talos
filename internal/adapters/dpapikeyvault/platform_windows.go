//go:build windows

package dpapikeyvault

import (
	"errors"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const replaceFileWriteThrough = 0x00000001

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

func platformAvailable() bool {
	return true
}

func platformProtect(plaintext, optionalEntropy []byte) ([]byte, error) {
	input := dataBlob(plaintext)
	entropy := dataBlob(optionalEntropy)
	var output windows.DataBlob
	if err := windows.CryptProtectData(input, nil, entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer freeDataBlob(&output, false)
	runtime.KeepAlive(plaintext)
	runtime.KeepAlive(optionalEntropy)
	return copyDataBlob(&output), nil
}

func platformUnprotect(protected, optionalEntropy []byte) ([]byte, error) {
	input := dataBlob(protected)
	entropy := dataBlob(optionalEntropy)
	var output windows.DataBlob
	if err := windows.CryptUnprotectData(input, nil, entropy, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &output); err != nil {
		return nil, err
	}
	defer freeDataBlob(&output, true)
	runtime.KeepAlive(protected)
	runtime.KeepAlive(optionalEntropy)
	return copyDataBlob(&output), nil
}

func platformCommit(from, to string, replace bool) error {
	fromPointer, err := windows.UTF16PtrFromString(from)
	if err != nil {
		return err
	}
	toPointer, err := windows.UTF16PtrFromString(to)
	if err != nil {
		return err
	}
	if !replace {
		err = windows.MoveFileEx(fromPointer, toPointer, windows.MOVEFILE_WRITE_THROUGH)
		if err != nil {
			if _, statErr := os.Stat(to); statErr == nil {
				return os.ErrExist
			}
		}
		return err
	}

	result, _, callErr := replaceFileW.Call(
		uintptr(unsafe.Pointer(toPointer)),
		uintptr(unsafe.Pointer(fromPointer)),
		0,
		replaceFileWriteThrough,
		0,
		0,
	)
	runtime.KeepAlive(fromPointer)
	runtime.KeepAlive(toPointer)
	if result == 0 {
		if errors.Is(callErr, windows.ERROR_FILE_NOT_FOUND) || errors.Is(callErr, windows.ERROR_PATH_NOT_FOUND) {
			return os.ErrNotExist
		}
		return callErr
	}
	return nil
}

func dataBlob(data []byte) *windows.DataBlob {
	if len(data) == 0 {
		return &windows.DataBlob{}
	}
	return &windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
}

func copyDataBlob(blob *windows.DataBlob) []byte {
	if blob == nil || blob.Size == 0 || blob.Data == nil {
		return nil
	}
	return append([]byte(nil), unsafe.Slice(blob.Data, int(blob.Size))...)
}

func freeDataBlob(blob *windows.DataBlob, clear bool) {
	if blob == nil || blob.Data == nil {
		return
	}
	if clear && blob.Size > 0 {
		plaintext := unsafe.Slice(blob.Data, int(blob.Size))
		for index := range plaintext {
			plaintext[index] = 0
		}
		runtime.KeepAlive(plaintext)
	}
	_, _ = windows.LocalFree(windows.Handle(uintptr(unsafe.Pointer(blob.Data))))
	blob.Data = nil
	blob.Size = 0
}
