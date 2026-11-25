package antigravity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"shioji/internal/domain"
	"shioji/internal/harness"
)

type cachedCatalog struct {
	Version string           `json:"version"`
	Profile string           `json:"profile"`
	Models  domain.Readiness `json:"catalog"`
}

func (r *runtime) discover(ctx context.Context) error {
	dir := r.options.CatalogDirectory
	path := filepath.Join(dir, "model-catalog.json")
	if !r.options.RefreshCatalog {
		if dir != "" {
			if saved := readCatalog(path); saved.Version == r.version && saved.Profile == r.profile && saved.Models.HarnessID == r.ready.HarnessID {
				r.ready = saved.Models
				addYOLOPermission(&r.ready)
				normalizeCachedAccount(&r.ready)
				r.ready.Capabilities = r.capabilities()
				r.ready.Capabilities.Compact = saved.Models.Capabilities.Compact
				r.ready.Capabilities.CompactNeedsCheck = !r.ready.Capabilities.Compact && r.ready.Capabilities.CompactNeedsCheck
			}
		}
		return nil
	}
	cwd, err := os.MkdirTemp("", "shioji-agy-catalog-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(cwd)
	found, err := r.bind(ctx, domain.Session{Context: domain.LaunchContext{CWD: cwd}}, "")
	if err != nil {
		var setup *harness.SetupError
		if dir != "" && errors.As(err, &setup) && setup.Issue == domain.RuntimeSignInRequired {
			if invalidate := os.Remove(path); invalidate != nil && !errors.Is(invalidate, os.ErrNotExist) {
				return errors.Join(err, invalidate)
			}
		}
		return err
	}
	compact := r.waitForCompactCommand(ctx, found.SessionID)
	if r.native.AgentCapabilities.SessionCapabilities.Close != nil {
		if err := r.process.call(ctx, "session/close", sessionID(found.SessionID), nil); err != nil {
			return setupError(err)
		}
	}
	caps := r.capabilities()
	caps.Compact = compact
	caps.CompactNeedsCheck = !compact && caps.CompactNeedsCheck
	r.ready = makeReadiness(r.ready.HarnessID, found, caps)
	if r.ready.State != domain.RuntimeReady {
		return errors.New(r.ready.Error)
	}
	r.ready.Account = &domain.RuntimeAccount{
		Mode:         "account",
		Label:        "Antigravity",
		State:        "authenticated",
		Subscription: subscriptionTier(found),
	}
	if dir == "" {
		return nil
	}
	return saveCatalog(dir, path, cachedCatalog{Version: r.version, Profile: r.profile, Models: r.ready})
}

func normalizeCachedAccount(ready *domain.Readiness) {
	if ready.State != domain.RuntimeReady {
		return
	}
	if ready.Account == nil {
		ready.Account = &domain.RuntimeAccount{Mode: "account", Label: "Antigravity", State: "authenticated"}
		return
	}
	if ready.Account.State == "configured" || len(ready.Account.QuotaWindows) > 0 {
		account := *ready.Account
		if account.State == "configured" {
			account.State = "authenticated"
		}
		account.QuotaWindows = nil
		ready.Account = &account
	}
}

func readCatalog(path string) cachedCatalog {
	var saved cachedCatalog
	f, err := os.Open(path)
	if err != nil {
		return saved
	}
	defer f.Close()
	if err := json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&saved); err != nil {
		return cachedCatalog{}
	}
	return saved
}

func saveCatalog(dir, path string, values cachedCatalog) error {
	f, err := os.CreateTemp(dir, ".model-catalog-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := json.NewEncoder(f).Encode(values); err != nil {
		_ = f.Close()
		return err
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
