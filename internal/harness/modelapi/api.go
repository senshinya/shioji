// Package modelapi implements API setup probes without starting a harness.
package modelapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"shioji/internal/domain"
)

func Normalize(in domain.APIConfiguration) (domain.APIConfiguration, error) {
	in.Endpoint = strings.TrimRight(strings.TrimSpace(in.Endpoint), "/")
	in.Model = strings.TrimSpace(in.Model)
	in.Credential = strings.TrimSpace(in.Credential)
	u, err := url.Parse(in.Endpoint)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || len(in.Endpoint) > 4096 {
		return in, errors.New("请输入不含凭据的 API 地址")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || ip != nil && ip.IsLoopback())) {
		return in, errors.New("API 地址须使用 HTTPS，回环服务可使用 HTTP")
	}
	if len(in.Model) > 256 || strings.ContainsAny(in.Model, "\x00\r\n") || len(in.Credential) > 16<<10 || strings.ContainsAny(in.Credential, "\x00\r\n") {
		return in, errors.New("请检查模型 ID 和 API Key")
	}
	return in, nil
}

func Model(in domain.APIConfiguration) domain.Model {
	return domain.Model{ID: in.Model, Name: in.Model, Default: true}
}

func Models(ctx context.Context, id string, in domain.APIConfiguration) ([]domain.Model, error) {
	in, err := Normalize(in)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	address := endpoint(id, in.Endpoint, "models")
	var models []domain.Model
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 20; page++ {
		u := address
		if cursor != "" {
			u += "?after_id=" + url.QueryEscape(cursor)
		}
		var listing struct {
			Data []struct {
				ID          string
				Name        string
				DisplayName string `json:"display_name"`
			}
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err = request(ctx, id, in.Credential, http.MethodGet, u, nil, &listing); err != nil {
			return nil, err
		}
		for _, row := range listing.Data {
			if row.ID == "" || len(row.ID) > 256 || strings.ContainsAny(row.ID, "\x00\r\n") || seen[row.ID] {
				continue
			}
			seen[row.ID] = true
			name := row.DisplayName
			if name == "" {
				name = row.Name
			}
			if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") {
				name = row.ID
			}
			models = append(models, domain.Model{ID: row.ID, Name: name})
		}
		if id != domain.AgentAnthropic || !listing.HasMore {
			if len(models) == 0 {
				return nil, errors.New("服务没有返回可用模型，可直接输入模型 ID")
			}
			return models, nil
		}
		if listing.LastID == "" || listing.LastID == cursor {
			break
		}
		cursor = listing.LastID
	}
	return nil, errors.New("模型列表未能完整读取，可直接输入模型 ID")
}

func Verify(ctx context.Context, id string, in domain.APIConfiguration) error {
	payload := map[string]any{"model": in.Model}
	var route string
	var reply struct {
		Type   string
		Object string
		Status string
	}
	switch id {
	case domain.AgentResponses:
		route = "responses"
		payload["input"] = "Reply OK."
		payload["max_output_tokens"] = 32
		payload["store"] = false
	case domain.AgentAnthropic:
		route = "messages"
		payload["messages"] = []map[string]string{{"role": "user", "content": "Reply OK."}}
		payload["max_tokens"] = 16
	default:
		return errors.New("API 类型不可用")
	}
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if err := request(ctx, id, in.Credential, http.MethodPost, endpoint(id, in.Endpoint, route), payload, &reply); err != nil {
		return err
	}
	if id == domain.AgentResponses && reply.Object == "response" && (reply.Status == "completed" || reply.Status == "incomplete") ||
		id == domain.AgentAnthropic && reply.Type == "message" {
		return nil
	}
	return errors.New("服务未返回所选 API 的有效响应")
}

func endpoint(id, base, route string) string {
	if id == domain.AgentAnthropic && !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	return base + "/" + route
}

func request(ctx context.Context, id, key, method, address string, body any, out any) error {
	var payload io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, payload)
	if err != nil {
		return errors.New("API 地址无效")
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if id == domain.AgentAnthropic {
		req.Header.Set("anthropic-version", "2023-06-01")
		if key != "" {
			req.Header.Set("x-api-key", key)
		}
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("无法访问模型服务，请检查 API 地址和网络")
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return errors.New("API 服务拒绝了凭据，请检查 API Key")
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		if method == http.MethodGet {
			return errors.New("服务未提供模型列表，可直接输入模型 ID")
		}
		fallthrough
	default:
		return fmt.Errorf("模型服务请求失败（HTTP %d）", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil || len(raw) > 2<<20 || json.Unmarshal(raw, out) != nil {
		return errors.New("模型服务响应无效")
	}
	return nil
}
