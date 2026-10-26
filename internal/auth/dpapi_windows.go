//go:build windows

package auth

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	cryptProtectUIForbidden = 0x1
	dpapiEntropy            = "CDRLauncher/accounts/v1"
)

var (
	crypt32                = syscall.NewLazyDLL("crypt32.dll")
	procCryptProtectData   = crypt32.NewProc("CryptProtectData")
	procCryptUnprotectData = crypt32.NewProc("CryptUnprotectData")
	procLocalFree          = syscall.NewLazyDLL("kernel32.dll").NewProc("LocalFree")
)

type dataBlob struct {
	cbData uint32
	pbData *byte
}

func newDataBlob(data []byte) dataBlob {
	if len(data) == 0 {
		return dataBlob{}
	}
	return dataBlob{cbData: uint32(len(data)), pbData: &data[0]}
}

func (b dataBlob) bytes() []byte {
	if b.pbData == nil || b.cbData == 0 {
		return nil
	}
	out := make([]byte, b.cbData)
	copy(out, unsafe.Slice(b.pbData, int(b.cbData)))
	return out
}

func protect(plain []byte) ([]byte, error) {
	in := newDataBlob(plain)
	entropy := newDataBlob([]byte(dpapiEntropy))
	var out dataBlob
	ok, _, err := procCryptProtectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		uintptr(unsafe.Pointer(&entropy)),
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ok == 0 {
		return nil, fmt.Errorf("DPAPI 加密账号失败: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}

func unprotect(sealed []byte) ([]byte, error) {
	in := newDataBlob(sealed)
	entropy := newDataBlob([]byte(dpapiEntropy))
	var out dataBlob
	ok, _, err := procCryptUnprotectData.Call(
		uintptr(unsafe.Pointer(&in)),
		0,
		uintptr(unsafe.Pointer(&entropy)),
		0,
		0,
		cryptProtectUIForbidden,
		uintptr(unsafe.Pointer(&out)),
	)
	if ok == 0 {
		return nil, fmt.Errorf("DPAPI 解密账号失败（可能由其他 Windows 用户创建）: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.pbData)))
	return out.bytes(), nil
}
