//go:build windows

package core

import "golang.org/x/sys/windows/registry"

// savedUserEnv reads the user's, then the system's saved environment variable, so a variable set after Loopbee
// started (setx, System Properties) is found without restarting the app.
func savedUserEnv(name string) (string, bool) {
	locations := []struct {
		root registry.Key
		path string
	}{
		{registry.CURRENT_USER, `Environment`},
		{registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Control\Session Manager\Environment`},
	}
	for _, loc := range locations {
		k, err := registry.OpenKey(loc.root, loc.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, typ, err := k.GetStringValue(name)
		k.Close()
		if err != nil || v == "" {
			continue
		}
		if typ == registry.EXPAND_SZ {
			if expanded, err := registry.ExpandString(v); err == nil {
				v = expanded
			}
		}
		return v, true
	}
	return "", false
}
