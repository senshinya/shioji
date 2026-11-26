package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	"shioji/internal/domain"
)

const ProfileEffortsFile = "model-efforts.json"

func ReadProfileEfforts(home string) (map[string][]string, error) {
	if home == "" {
		return nil, nil
	}
	path := filepath.Join(home, ProfileEffortsFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 64<<10 {
		return nil, errors.New("无法读取此 Profile 的推理档位配置")
	}
	data, err := os.ReadFile(path)
	var overrides map[string][]string
	if err != nil || json.Unmarshal(data, &overrides) != nil {
		return nil, errors.New("无法读取此 Profile 的推理档位配置")
	}
	return overrides, nil
}

// ApplyProfileEfforts leaves the discovery snapshot intact so removing an
// override immediately restores the reported options, even for live runtimes.
func ApplyProfileEfforts(home string, ready domain.Readiness) domain.Readiness {
	overrides, err := ReadProfileEfforts(home)
	if err != nil {
		ready.State, ready.Error = domain.RuntimeUnavailable, err.Error()
		return ready
	}
	if len(overrides) == 0 {
		return ready
	}
	ready.Models = slices.Clone(ready.Models)
	for i, model := range ready.Models {
		if levels, exists := overrides[model.ID]; exists {
			ready.Models[i] = model.WithEfforts(levels)
		}
	}
	ready.Harnesses = slices.Clone(ready.Harnesses)
	for i := range ready.Harnesses {
		if ready.Harnesses[i].ID == ready.HarnessID {
			ready.Harnesses[i].Models = ready.Models
		}
	}
	return ready
}
