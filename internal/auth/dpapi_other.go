//go:build !windows

package auth

import "errors"

func protect([]byte) ([]byte, error) {
	return nil, errors.New("账号加密仅支持 Windows")
}

func unprotect([]byte) ([]byte, error) {
	return nil, errors.New("账号解密仅支持 Windows")
}
