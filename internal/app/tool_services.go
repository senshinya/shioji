package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"shioji/internal/domain"
	"shioji/internal/protocol"
	"shioji/internal/transport"
	"strconv"
	"strings"
)

func (c *Controller) prepareToolServiceReply(ctx context.Context, ssh transport.SSH, device, method string, in protocol.ToolServiceInput, body json.RawMessage) (json.RawMessage, error) {
	prepare := func(status *domain.ToolServiceAuthorization) error {
		if status == nil {
			return nil
		}
		if status.ID == "" || status.HarnessID != in.HarnessID {
			return errors.New("工具授权回执与请求不一致")
		}
		if strings.HasSuffix(method, "auth.start") || strings.HasSuffix(method, "auth.status") {
			if status.ID != in.ID || status.ServiceID != in.ServiceID {
				return errors.New("工具授权回执与请求不一致")
			}
		}
		key := "tool/" + status.ID
		if !status.Phase.Active() {
			return errors.Join(c.signInForwards.retire(key, device), c.signInForwards.retire(key+"/provider", device))
		}
		if ssh.Target == "" || status.Phase != domain.SignInWaiting {
			return nil
		}
		if parsed, err := url.Parse(status.Challenge.URL); err == nil && parsed.Hostname() != "" {
			port := 443
			if parsed.Scheme == "http" {
				port = 80
			}
			if parsed.Port() != "" {
				port, _ = strconv.Atoi(parsed.Port())
			}
			endpoint := domain.LoopbackEndpoint{Host: parsed.Hostname(), Port: port}
			if endpoint.Valid() && (status.Challenge.Callback == nil || endpoint != *status.Challenge.Callback) {
				if err := c.signInForwards.ensure(ctx, key+"/provider", device, ssh, endpoint); err != nil {
					_ = c.signInForwards.retire(key+"/provider", device)
					status.Challenge.URL = ""
					status.Message = "无法连接设备上的授权服务，连接恢复后自动重试"
					return nil
				}
			}
		}
		if status.Challenge.Callback == nil {
			return nil
		}
		if !status.Challenge.Callback.Valid() {
			status.Challenge.URL = ""
			status.Message = "工具服务未提供可转发的授权回调"
			return nil
		}
		if err := c.signInForwards.ensure(ctx, key, device, ssh, *status.Challenge.Callback); err != nil {
			_ = c.signInForwards.retire(key, device)
			status.Challenge.URL = ""
			status.Message = "无法建立授权回调，连接恢复后自动重试"
		}
		return nil
	}
	if method == "runtime.tools.list" || method == "runtime.tools.refresh" {
		var result domain.ToolServiceCatalog
		if json.Unmarshal(body, &result) != nil {
			return nil, errors.New("工具服务结果无效")
		}
		if err := prepare(result.Authorization); err != nil {
			return nil, err
		}
		return json.Marshal(result)
	}
	var result domain.ToolServiceAuthorization
	if json.Unmarshal(body, &result) != nil {
		return nil, errors.New("工具授权结果无效")
	}
	if err := prepare(&result); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}
