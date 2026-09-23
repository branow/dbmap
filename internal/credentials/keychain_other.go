//go:build !darwin || !cgo

package credentials

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// Every platform but macOS reaches its credential store through go-keyring:
// wincred on Windows, secret-service on Linux, and macOS too when cgo is off.
//
// The allow decision cannot be honoured here - go-keyring exposes no way to
// refuse a dialog - but these backends do not tie an item to the calling
// binary's code identity, so they never raise the dialog that motivated it.
func itemGet(service, account string, _ ui) (string, error) {
	value, err := keyring.Get(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", errMissing
	}
	return value, err
}

func itemSet(service, account, secret string, _ ui) error {
	return keyring.Set(service, account, secret)
}

func itemDelete(service, account string, _ ui) error {
	err := keyring.Delete(service, account)
	if errors.Is(err, keyring.ErrNotFound) {
		return errMissing
	}
	return err
}
