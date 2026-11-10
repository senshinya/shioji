package domain

// These are Shioji operations. Adapters report actual support; absent support
// never authorizes a substitute operation or a guessed configuration.
type HarnessCapabilities struct {
	ToolServiceInspection bool `json:"toolServiceInspection,omitempty"`
	ToolServices          bool `json:"toolServices,omitempty"`
	Skills                bool `json:"skills,omitempty"`
	SkillManagement       bool `json:"skillManagement,omitempty"`
	Steer                 bool `json:"steer"`
	Interrupt             bool `json:"interrupt"`
	Recovery              bool `json:"recovery"`
	Continue              bool `json:"continue"`
	Fork                  bool `json:"fork"`
	Restart               bool `json:"restart"`
}
type HarnessDescriptor struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Icon string `json:"icon,omitempty"`
}
type HarnessInfo struct {
	Account           *RuntimeAccount      `json:"account,omitempty"`
	Configured        bool                 `json:"configured"`
	Installation      *RuntimeInstallation `json:"installation,omitempty"`
	ID                string               `json:"id"`
	Name              string               `json:"name"`
	Icon              string               `json:"icon,omitempty"`
	State             ReadinessState       `json:"state"`
	Error             string               `json:"error,omitempty"`
	Models            []Model              `json:"models,omitempty"`
	Permissions       []string             `json:"permissions,omitempty"`
	PermissionChoices []OptionChoice       `json:"permissionChoices,omitempty"`
	Plan              bool                 `json:"plan"`
	Capabilities      HarnessCapabilities  `json:"capabilities"`
}
type OptionChoice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// ForHarness keeps each catalog separate. The top-level projection remains the
// default catalog for older clients; explicit unknown IDs never use it.
func (r Readiness) ForHarness(id string) (Readiness, bool) {
	if id == "" {
		id = r.HarnessID
	}
	if len(r.Harnesses) == 0 {
		return r, id == "" || id == r.HarnessID
	}
	for _, h := range r.Harnesses {
		if h.ID == id {
			return Readiness{Account: h.Account, Sessions: r.Sessions, HarnessID: h.ID, Harnesses: r.Harnesses, State: h.State, Error: h.Error, Models: h.Models, Permissions: h.Permissions, PermissionChoices: h.PermissionChoices, Plan: h.Plan, Capabilities: h.Capabilities}, true
		}
	}
	return Readiness{}, false
}
func (r Readiness) Descriptor() HarnessDescriptor {
	for _, h := range r.Harnesses {
		if h.ID == r.HarnessID {
			return HarnessDescriptor{ID: h.ID, Name: h.Name, Icon: h.Icon}
		}
	}
	return HarnessDescriptor{ID: r.HarnessID, Name: "Agent"}
}

const ModelUnavailableMessage = "当前模型已不可用，请更换模型"
