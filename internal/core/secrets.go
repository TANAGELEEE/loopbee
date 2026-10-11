package core

import (
	"errors"
	"os"
	"strings"

	"github.com/zalando/go-keyring"
)

// Environment references permit headless hosts without an unlocked system keychain.
func resolveSecret(value string) (string, error) {
	if strings.HasPrefix(value, "env:") {
		v, ok := os.LookupEnv(strings.TrimPrefix(value, "env:"))
		if !ok || v == "" {
			return "", errors.New("notification environment variable is not set")
		}
		return v, nil
	}
	if strings.HasPrefix(value, "keyring:") {
		v, err := keyring.Get(Brand, strings.TrimPrefix(value, "keyring:"))
		if err != nil {
			return "", errors.New("notification credential unavailable in system keychain")
		}
		return v, nil
	}
	return value, nil
}
func storeSecret(publisher, field, value string) (string, error) {
	if value == "" || strings.HasPrefix(value, "env:") || strings.HasPrefix(value, "keyring:") {
		return value, nil
	}
	ref := publisher + "/" + field
	if err := keyring.Set(Brand, ref, value); err != nil {
		return "", errors.New("system keychain unavailable; use env:VARIABLE_NAME for headless credentials")
	}
	return "keyring:" + ref, nil
}
