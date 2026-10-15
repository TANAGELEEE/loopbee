package core

import (
	"errors"
	"fmt"
	"strings"

	"github.com/zalando/go-keyring"
)

// Environment references permit headless hosts without an unlocked system keychain.
func resolveSecret(value string) (string, error) {
	if name, ok := strings.CutPrefix(value, "env:"); ok {
		v, ok := userEnv(name)
		if !ok {
			return "", fmt.Errorf("%s is not set in loopbee's environment or your login shell; export it in your shell profile (Windows: set it as a user environment variable), or store the value in loopbee instead", name)
		}
		return v, nil
	}
	if ref, ok := strings.CutPrefix(value, "keyring:"); ok {
		v, err := keyring.Get(keyringService, ref)
		if err != nil {
			return "", errors.New("the stored value is missing from the system keychain; enter it again in loopbee")
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
	if err := keyring.Set(keyringService, ref, value); err != nil {
		return "", errors.New("system keychain unavailable; use env:VARIABLE_NAME for headless credentials")
	}
	return "keyring:" + ref, nil
}
