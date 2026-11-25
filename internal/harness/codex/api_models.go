package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"shioji/internal/domain"
	"shioji/internal/harness/modelapi"
)

// APIModels validates a draft connection without saving credentials or settings.
func (a Adapter) APIModels(ctx context.Context, in domain.APIConfiguration) ([]domain.Model, error) {
	in.Model = ""
	if a.responses {
		return modelapi.Models(ctx, domain.AgentResponses, in)
	}
	in, err := a.apiConfiguration(in)
	if err != nil {
		return nil, err
	}
	return discoverAPIModels(ctx, in)
}

func discoverAPIModels(ctx context.Context, in domain.APIConfiguration) ([]domain.Model, error) {
	headers := http.Header{}
	if in.Credential != "" {
		headers.Set("Authorization", "Bearer "+in.Credential)
	}
	return fetchAPIModels(ctx, strings.TrimRight(in.Endpoint, "/")+"/models", headers, nil)
}

func fetchAPIModels(ctx context.Context, endpoint string, headers http.Header, known []domain.Model) ([]domain.Model, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, errors.New("模型列表地址无效")
	}
	request.Header = headers.Clone()
	request.Header.Set("Accept", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("无法获取可用模型，请检查 API 地址和网络")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		if response.StatusCode == 401 || response.StatusCode == 403 {
			return nil, errors.New("模型列表授权失败，请检查 API 凭据")
		}
		return nil, fmt.Errorf("获取可用模型失败（HTTP %d）", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(data) > 1<<20 {
		return nil, errors.New("模型列表响应过大或读取失败")
	}
	var result struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
		Models []struct {
			ID         string `json:"slug"`
			Name       string `json:"display_name"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if json.Unmarshal(data, &result) != nil {
		return nil, errors.New("服务未返回有效的模型列表")
	}
	metadata := map[string]domain.Model{}
	for _, m := range known {
		metadata[m.ID] = m
	}
	models := []domain.Model{}
	seen := map[string]bool{}
	add := func(id, name string) {
		if id == "" || strings.ContainsAny(id, "\r\n\x00") || len(id) > 256 || seen[id] {
			return
		}
		seen[id] = true
		model, ok := metadata[id]
		if !ok {
			model = domain.Model{ID: id, Name: id}
		}
		if name != "" {
			model.Name = name
		}
		model.Default = false
		models = append(models, model)
	}
	for _, m := range result.Data {
		add(m.ID, m.Name)
	}
	for _, m := range result.Models {
		if m.Visibility != "hide" {
			add(m.ID, m.Name)
		}
	}
	if len(models) == 0 {
		return nil, errors.New("服务没有返回可用模型")
	}
	models[0].Default = true
	return models, nil
}

func (r *Runtime) availableAPIModels(ctx context.Context, known []domain.Model) ([]domain.Model, error) {
	config, err := r.systemConfiguration(ctx)
	if err != nil {
		return nil, errors.New("无法读取当前模型服务配置")
	}
	provider := config.provider(r.processEnvironment())
	endpoint := provider.Endpoint
	if endpoint == "" {
		return nil, errors.New("当前服务未提供可读取的模型列表地址")
	}
	headers := http.Header{}
	for key, value := range provider.Headers {
		headers.Set(key, value)
	}
	for key, name := range provider.EnvHeaders {
		headers.Set(key, environmentValue(r.processEnvironment(), name))
	}
	token := provider.Token
	if provider.EnvKey != "" {
		token = environmentValue(r.processEnvironment(), provider.EnvKey)
	}
	var account accountStatus
	if err = r.call(ctx, "account/read", map[string]any{"refreshToken": false}, &account); err != nil {
		return nil, errors.New("无法读取模型服务授权状态")
	}
	var public struct {
		Type string `json:"type"`
	}
	json.Unmarshal(account.Account, &public)
	if (provider.RequiresAuth || config.Config.Provider == "" || config.Config.Provider == "openai") && token == "" {
		// Native account authorization and its refresh remain entirely in Codex.
		if public.Type == "chatgpt" {
			return known, nil
		}
		if public.Type != "apiKey" {
			token = environmentValue(r.processEnvironment(), "OPENAI_API_KEY")
		}
		if token == "" {
			token, err = config.nativeAPIKey(ctx)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(provider.Auth) > 0 && string(provider.Auth) != "null" || len(provider.Gateway) > 0 && string(provider.Gateway) != "null" {
		// These native authentication mechanisms already enable Codex's remote
		// model discovery; do not reproduce OAuth or helper execution in Shioji.
		return known, nil
	}
	if token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	catalog := provider.CatalogURL
	if catalog == "" {
		catalog = strings.TrimRight(endpoint, "/") + "/models"
	}
	u, err := url.Parse(catalog)
	if err != nil {
		return nil, errors.New("模型列表地址无效")
	}
	query := u.Query()
	for key, value := range provider.Query {
		query.Set(key, value)
	}
	u.RawQuery = query.Encode()
	models, err := fetchAPIModels(ctx, u.String(), headers, known)
	if config.Config.Provider == responsesProvider && config.Config.Model != "" {
		if err != nil {
			models = nil
		}
		found := false
		for _, model := range models {
			found = found || model.ID == config.Config.Model
		}
		if !found {
			model := domain.Model{ID: config.Config.Model, Name: config.Config.Model}
			for _, candidate := range known {
				if candidate.ID == model.ID {
					model = candidate
					break
				}
			}
			models = append(models, model)
		}
		err = nil
	}
	if err != nil {
		return nil, err
	}
	for i := range models {
		models[i].Default = models[i].ID == config.Config.Model
	}
	return models, nil
}

// Read only the native API-key field. OAuth tokens are never consumed by this
// path. Store selection and direct keyring namespace follow Codex auth storage.
func (c nativeConfiguration) nativeAPIKey(ctx context.Context) (string, error) {
	home := ""
	for _, layer := range c.Layers {
		if layer.Name.Type == "user" {
			home = filepath.Dir(layer.Name.File)
			break
		}
	}
	if home == "" {
		return "", errors.New("无法读取系统 API 凭据")
	}
	var data []byte
	store := c.Config.CredentialsStore
	if store == "keyring" || store == "auto" {
		canonical, err := filepath.EvalSymlinks(home)
		if err != nil {
			canonical = home
		}
		digest := sha256.Sum256([]byte(canonical))
		key := fmt.Sprintf("cli|%x", digest[:8])
		command := exec.CommandContext(ctx, "/usr/bin/security", "find-generic-password", "-s", "Codex Auth", "-a", key, "-w")
		output, err := command.Output()
		if err == nil {
			data = output
		} else {
			var exit *exec.ExitError
			if store == "keyring" || !errors.As(err, &exit) || exit.ExitCode() != 44 {
				return "", errors.New("无法读取系统钥匙串中的 API 凭据")
			}
		}
	}
	if data == nil && store != "ephemeral" {
		data, _ = os.ReadFile(filepath.Join(home, "auth.json"))
	}
	var auth struct {
		Key string `json:"OPENAI_API_KEY"`
	}
	if json.Unmarshal(data, &auth) != nil || auth.Key == "" {
		return "", errors.New("系统 API 凭据不可读取，请重新检测授权")
	}
	return auth.Key, nil
}
