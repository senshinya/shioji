package codex

import (
	"context"

	"shioji/internal/domain"
	"shioji/internal/harness"
	"shioji/internal/harness/managedagent"
	"shioji/internal/harness/modelapi"
)

const responsesProvider = "shioji-responses"

func NewResponsesAdapter(executable, state string) Adapter {
	return Adapter{Executable: executable, responses: true, managedState: state}
}

func (a Adapter) responsesExecutable(ctx context.Context, install bool, updates ...func(int64, int64)) (string, error) {
	env, _ := a.environment(ctx)
	if image, err := installedExecutable(a.Executable, env); err == nil {
		if binary, _, err := compatibleExecutable(ctx, image.path, env); err == nil {
			return binary, nil
		}
	}
	if path := managedagent.Codex.Find(a.managedState); path != "" {
		return path, nil
	}
	if install {
		return managedagent.Codex.Ensure(ctx, a.managedState, updates...)
	}
	return "", &harness.SetupError{Issue: domain.RuntimeMissing, Message: "需要安装兼容的 Codex 运行环境"}
}

func (a Adapter) verifyResponsesSetup(ctx context.Context, in domain.APIConfiguration) error {
	if err := modelapi.Verify(ctx, domain.AgentResponses, in); err != nil {
		return apiSetupError(err.Error())
	}
	return nil
}
