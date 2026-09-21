//go:build windows

package winruntime

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ProtectMachine(plaintext, entropy []byte) ([]byte, error) {
	if len(plaintext) == 0 || len(entropy) < 16 {
		return nil, errors.New("plaintext and at least 16 bytes of entropy are required")
	}
	in, entropyBlob := blob(plaintext), blob(entropy)
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, &entropyBlob, 0, nil, windows.CRYPTPROTECT_LOCAL_MACHINE|windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return copyAndFreeBlob(out), nil
}

func UnprotectMachine(ciphertext, entropy []byte) ([]byte, error) {
	if len(ciphertext) == 0 || len(entropy) < 16 {
		return nil, errors.New("ciphertext and at least 16 bytes of entropy are required")
	}
	in, entropyBlob := blob(ciphertext), blob(entropy)
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, &entropyBlob, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	return copyAndFreeBlob(out), nil
}

func copyAndFreeBlob(value windows.DataBlob) []byte {
	// #nosec G103 -- Windows DPAPI returns LocalAlloc memory through DATA_BLOB; copying and LocalFree are the documented ownership boundary.
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(value.Data)))
	// #nosec G103 -- Size and pointer are returned together by CryptProtectData/CryptUnprotectData after a successful call.
	return append([]byte(nil), unsafe.Slice(value.Data, value.Size)...)
}

func blob(value []byte) windows.DataBlob {
	return windows.DataBlob{Size: uint32(len(value)), Data: &value[0]}
}
