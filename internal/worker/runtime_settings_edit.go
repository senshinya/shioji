package worker

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/protocol"
)

func (s *Server) editableRuntimeInstallation(id string, provider harness.InstallationProvider) domain.RuntimeInstallation {
	install := provider.Installation()
	s.runtimeMu.RLock()
	defer s.runtimeMu.RUnlock()
	if s.runtimeSettingErrors[id] != nil {
		install.Override = s.runtimeSettings[id]
		install.Options = s.runtimeOptions[id]
	}
	return install
}

func runtimeSettingsProjection(install domain.RuntimeInstallation) protocol.RuntimeSettings {
	data, _ := json.Marshal(struct {
		Override string
		Support  domain.RuntimeOptionSupport
		Options  domain.RuntimeOptions
	}{install.Override, install.OptionsSupported, install.Options})
	hash := sha256.Sum256(data)
	result := protocol.RuntimeSettings{Override: install.Override, Executable: install.Executable,
		Revision: hex.EncodeToString(hash[:]), Support: install.OptionsSupported, Options: install.Options}
	for _, entry := range install.Options.Environment {
		result.Environment = append(result.Environment, entry.Name)
	}
	result.Options.Environment = nil
	return result
}

func runtimeSettingsConflict() error {
	return &protocol.RemoteError{Code: "runtimeSettingsChanged", Message: "设备上的运行设置已更新，草稿已保留"}
}

func (s *Server) readRuntimeSettings(in protocol.RuntimeSettingsQuery, private bool) (any, error) {
	gate := s.catalogGates[in.HarnessID]
	if gate == nil {
		return nil, harness.Unsupported("configure-runtime")
	}
	if !gate.TryRLock() {
		return nil, errCatalogRefreshing
	}
	defer gate.RUnlock()
	provider, ok := s.adapter(in.HarnessID).(harness.InstallationProvider)
	if !ok {
		return nil, harness.Unsupported("configure-runtime")
	}
	install := s.editableRuntimeInstallation(in.HarnessID, provider)
	snapshot := runtimeSettingsProjection(install)
	if !private {
		return snapshot, nil
	}
	if !snapshot.Support.Environment || in.Revision != snapshot.Revision {
		return nil, runtimeSettingsConflict()
	}
	for _, entry := range install.Options.Environment {
		if entry.Name == in.Name {
			return protocol.RuntimePrivateValue{Value: entry.Value}, nil
		}
	}
	return nil, errors.New("设备上的环境变量已改变，请重新读取")
}

func (s *Server) changeRuntimeSettings(in protocol.RuntimeSettingsChange) (protocol.RuntimeSettings, error) {
	s.dispatchMu.Lock()
	defer s.dispatchMu.Unlock()
	provider, ok := s.adapter(in.HarnessID).(harness.InstallationProvider)
	if !ok {
		return protocol.RuntimeSettings{}, harness.Unsupported("configure-runtime")
	}
	gate := s.catalogGates[in.HarnessID]
	if !gate.TryLock() {
		return protocol.RuntimeSettings{}, errCatalogRefreshing
	}
	defer gate.Unlock()
	install := s.editableRuntimeInstallation(in.HarnessID, provider)
	if !install.Configurable {
		return protocol.RuntimeSettings{}, harness.Unsupported("configure-runtime")
	}
	snapshot := runtimeSettingsProjection(install)
	if in.Revision != snapshot.Revision {
		return protocol.RuntimeSettings{}, runtimeSettingsConflict()
	}
	options, err := editedRuntimeOptions(install, in)
	if err != nil {
		return protocol.RuntimeSettings{}, err
	}
	config := protocol.RuntimeConfiguration{HarnessID: in.HarnessID, Executable: in.Executable}
	if install.OptionsSupported != (domain.RuntimeOptionSupport{}) {
		config.Options = &options
	}
	if err = s.updateAdmission(); err != nil {
		return protocol.RuntimeSettings{}, err
	}
	if err = s.applyRuntimeConfiguration(config, provider); err != nil {
		return protocol.RuntimeSettings{}, err
	}
	configured, ok := s.adapter(in.HarnessID).(harness.InstallationProvider)
	if !ok {
		return protocol.RuntimeSettings{}, harness.Unsupported("configure-runtime")
	}
	return runtimeSettingsProjection(s.editableRuntimeInstallation(in.HarnessID, configured)), nil
}

func editedRuntimeOptions(install domain.RuntimeInstallation, in protocol.RuntimeSettingsChange) (domain.RuntimeOptions, error) {
	options, support := install.Options, install.OptionsSupported
	if support.Arguments {
		options.Arguments = in.Options.Arguments
	}
	if support.Directory {
		options.Directory = in.Options.Directory
	}
	if support.Instructions {
		options.Instructions = in.Options.Instructions
	}
	if support.TitleModel {
		options.TitleModel = in.Options.TitleModel
	}
	if support.Environment {
		options.Environment = make([]domain.EnvironmentVariable, 0, len(in.Environment))
		for _, edit := range in.Environment {
			entry := domain.EnvironmentVariable{Name: strings.TrimSpace(edit.Name)}
			if edit.Value == nil {
				i := slices.IndexFunc(install.Options.Environment, func(old domain.EnvironmentVariable) bool {
					return old.Name == edit.PreviousName
				})
				if i < 0 {
					return options, errors.New("请为新的环境变量填写值")
				}
				entry.Value = install.Options.Environment[i].Value
			} else {
				entry.Value = *edit.Value
			}
			options.Environment = append(options.Environment, entry)
		}
	}
	return options, nil
}
