package main

import (
	"os"
	"path/filepath"
)

// signForSimulator signs the bundle for a simulator, with no identity. Entitlements
// don't go in such a signature, which the simulator then refuses to launch: they're
// in the executable (simulatorEntitlements), as Xcode has them.
func signForSimulator(bundle string) error {
	if err := signFrameworks(bundle, "-"); err != nil {
		return err
	}
	return runQuiet("codesign", "--force", "--sign", "-", bundle)
}

// simulatorEntitlements links the app's Keychain identity and plugin
// entitlements into the simulator executable.
func (ps mobilePlugins) simulatorEntitlements(work, bundleID string) ([]string, error) {
	ent := ps.entitlements()
	if _, ok := ent["application-identifier"]; !ok {
		ent["application-identifier"] = "LOCALSIM00." + bundleID
	}
	path := filepath.Join(work, "simulator.entitlements")
	if err := os.WriteFile(path, plistBytes(ent), 0o644); err != nil {
		return nil, err
	}
	return []string{"-Xlinker", "-sectcreate", "-Xlinker", "__TEXT", "-Xlinker", "__entitlements", "-Xlinker", path}, nil
}

// signFrameworks signs the frameworks of the bundle, which iosModules
// built from the Swift of native modules.
func signFrameworks(bundle, identity string) error {
	dirs, err := filepath.Glob(filepath.Join(bundle, "Frameworks", "*.framework"))
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if err := runQuiet("codesign", "--force", "--sign", identity, dir); err != nil {
			return err
		}
	}
	return nil
}
