//go:build !darwin || !cgo

package credentials

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Every platform but macOS reaches its credential store through go-keyring:
// wincred on Windows, secret-service on Linux. macOS uses it too when cgo is
// off, which is the only build where dbmap cannot call the Security framework
// in its own process.
func itemGet(service, account string) (string, error) {
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errMissing
	}
	return value, err
}

func itemSet(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func itemDelete(service, account string) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return errMissing
	}
	return err
}
