package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"shioji/internal/domain"
	"shioji/internal/harness/codex/wire"
	"sort"
	"strconv"
	"strings"
)

func makeRequest(method string, raw json.RawMessage, p wire.Params) (*pending, error) {
	entry := &pending{request: domain.PendingRequest{ID: domain.NewID(), SessionID: "", TurnID: p.TurnID, ItemID: itemKey(p.TurnID, p.ItemID), State: "pending", Revision: 1}, decisions: map[string]json.RawMessage{}}
	r := &entry.request
	switch method {
	case "item/tool/requestUserInput":
		r.Kind, r.Title = "questions", "需要回答"
		for _, q := range p.Questions {
			question := domain.Question{ID: q.ID, Header: q.Header, Text: bounded(q.Question), Custom: q.IsOther, Secret: q.IsSecret}
			for _, o := range q.Options {
				question.Options = append(question.Options, domain.Option{Label: o.Label, Description: o.Description})
			}
			r.Questions = append(r.Questions, question)
		}
		if len(r.Questions) == 0 {
			return nil, errors.New("empty question set")
		}
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		r.Kind, r.Title = "approval", "允许执行此命令？"
		r.Text = p.Command
		var extra struct {
			AvailableDecisions          *[]json.RawMessage
			NetworkApprovalContext      *struct{ Host, Protocol string }
			Kind                        string
			ProposedExecpolicyAmendment json.RawMessage
		}
		json.Unmarshal(raw, &extra)
		if p.CWD != "" {
			r.Text += "\n" + p.CWD
		}
		if p.Reason != "" {
			r.Text += "\n" + p.Reason
		}
		if extra.Kind == "writeStdin" {
			r.Title = "允许向终端发送输入？"
		}
		if extra.NetworkApprovalContext != nil {
			r.Title = "允许网络访问？"
			r.Text = extra.NetworkApprovalContext.Protocol + "://" + extra.NetworkApprovalContext.Host + "\n" + p.Reason
		}
		if method == "item/fileChange/requestApproval" {
			r.Title = "允许修改这些文件？"
		}
		decisions := []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"decline"`)}
		if extra.AvailableDecisions != nil {
			decisions = *extra.AvailableDecisions
		}
		if len(decisions) == 0 {
			r.Notice = "运行时暂未提供可用的审批选项"
		}

		for i, d := range decisions {
			var value string
			json.Unmarshal(d, &value)
			label := map[string]string{"accept": "允许这一次", "acceptForSession": "本会话允许", "decline": "拒绝", "cancel": "取消"}[value]
			id := value
			restriction := false
			if value == "" {
				var object map[string]json.RawMessage
				json.Unmarshal(d, &object)
				if raw, ok := object["acceptWithExecpolicyAmendment"]; ok {
					label = "始终允许此命令"
					var policy struct {
						Prefix []string `json:"execpolicy_amendment"`
					}
					if err := json.Unmarshal(raw, &policy); err != nil {
						return nil, err
					}
					quoted := make([]string, len(policy.Prefix))
					for i, arg := range policy.Prefix {
						quoted[i] = strconv.Quote(arg)
					}
					r.Text += "\n允许命令前缀：" + strings.Join(quoted, " ")
				}
				if raw, ok := object["applyNetworkPolicyAmendment"]; ok {
					label = "拒绝此网络访问"
					restriction = true
					var policy struct {
						Rule struct{ Host, Action string } `json:"network_policy_amendment"`
					}
					if err := json.Unmarshal(raw, &policy); err != nil {
						return nil, err
					}
					action := "拒绝网络访问："
					if policy.Rule.Action == "allow" {
						action = "允许网络访问："
						label = "允许此网络访问"
						restriction = false
					} else if policy.Rule.Action != "deny" {
						return nil, errors.New("unsupported network policy action")
					}
					r.Text += "\n" + action + policy.Rule.Host
				}
				id = "decision:" + strconv.Itoa(i)
			}
			if label != "" {
				r.Choices = append(r.Choices, domain.RequestChoice{ID: id, Label: label, Restriction: restriction})
				entry.decisions[id] = d
			}
		}
		r.Text = bounded(r.Text)
	case "item/permissions/requestApproval":
		r.Kind, r.Title, r.Text = "permissions", "允许额外访问？", bounded(p.Reason)
		var params struct{ Permissions json.RawMessage }
		json.Unmarshal(raw, &params)
		entry.permissions = params.Permissions
		r.Choices = permissionChoices(params.Permissions)
	case "mcpServer/elicitation/request":
		var params struct {
			Mode, Message, Description, Title, ServerName, URL string
			RequestedSchema                                    json.RawMessage
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
		r.Kind, r.Title, r.Text = "form", "来自 "+params.ServerName+" 的请求", bounded(firstNonempty(params.Message, params.Description))
		if params.Title != "" {
			r.Title = boundedTo(params.Title, 1024)
		}
		switch params.Mode {
		case "form", "openai/form", "openaiForm":
			fields, err := formFields(params.RequestedSchema)
			if err != nil {
				r.Kind = "external"
				r.Notice = "此表单包含暂不支持的字段或约束，可取消请求。"
			} else {
				r.Fields = fields
			}
		case "url":
			r.Kind, r.URL = "external", boundedTo(params.URL, 4096)
		default:
			r.Kind = "external"
			r.Notice = "此请求需要额外宿主能力，可取消请求。"
		}
	default:
		return nil, errors.New("unsupported server request")
	}
	return entry, nil
}

func formFields(raw json.RawMessage) ([]domain.FormField, error) {
	var schema struct {
		Type       string
		Properties map[string]json.RawMessage
		Required   []string
	}
	if err := json.Unmarshal(raw, &schema); err != nil || schema.Type != "object" || len(schema.Properties) > 32 {
		return nil, errors.New("unsupported form schema")
	}
	var rootKeys map[string]json.RawMessage
	json.Unmarshal(raw, &rootKeys)
	for key := range rootKeys {
		switch key {
		case "type", "properties", "required", "title", "description", "$schema", "additionalProperties":
		default:
			return nil, errors.New("unsupported form root constraint")
		}
	}
	if value, ok := rootKeys["additionalProperties"]; ok && string(value) != "false" {
		return nil, errors.New("additional form properties are unsupported")
	}
	for _, required := range schema.Required {
		if _, exists := schema.Properties[required]; !exists {
			return nil, errors.New("required form field is missing")
		}
	}
	ids := []string{}
	for key := range schema.Properties {
		ids = append(ids, key)
	}
	sort.Strings(ids)
	fields := []domain.FormField{}
	for _, id := range ids {
		var spec struct {
			Type, Title, Description, Format         string
			Enum, EnumNames                          []string
			OneOf, AnyOf                             []struct{ Const, Title string }
			Default                                  json.RawMessage
			Items                                    json.RawMessage
			MinLength, MaxLength, MinItems, MaxItems *int
			Minimum, Maximum                         *float64
		}
		if err := json.Unmarshal(schema.Properties[id], &spec); err != nil {
			return nil, err
		}
		var keys map[string]json.RawMessage
		json.Unmarshal(schema.Properties[id], &keys)
		for key := range keys {
			switch key {
			case "type", "title", "description", "format", "enum", "enumNames", "oneOf", "anyOf", "default", "items", "minLength", "maxLength", "minimum", "maximum", "minItems", "maxItems":
			default:
				return nil, errors.New("unsupported form constraint")
			}
		}
		f := domain.FormField{ID: id, Title: firstNonempty(spec.Title, id), Description: spec.Description, Type: spec.Type, Format: spec.Format, MinLength: spec.MinLength, MaxLength: spec.MaxLength, Minimum: spec.Minimum, Maximum: spec.Maximum, MinItems: spec.MinItems, MaxItems: spec.MaxItems}
		for _, required := range schema.Required {
			f.Required = f.Required || required == id
		}
		optionSpec := spec
		if f.Type == "array" {
			var itemKeys map[string]json.RawMessage
			json.Unmarshal(spec.Items, &itemKeys)
			for key := range itemKeys {
				switch key {
				case "type", "enum", "enumNames", "oneOf", "anyOf", "title", "description":
				default:
					return nil, errors.New("unsupported array constraint")
				}
			}
			if err := json.Unmarshal(spec.Items, &optionSpec); err != nil {
				return nil, err
			}
		}
		for i, value := range optionSpec.Enum {
			label := value
			if i < len(optionSpec.EnumNames) {
				label = optionSpec.EnumNames[i]
			}
			f.Options = append(f.Options, domain.FormOption{Value: value, Label: label})
		}
		choices := append(optionSpec.OneOf, optionSpec.AnyOf...)
		for _, o := range choices {
			f.Options = append(f.Options, domain.FormOption{Value: o.Const, Label: firstNonempty(o.Title, o.Const)})
		}
		if f.Type == "array" && len(f.Options) == 0 {
			return nil, errors.New("array requires enum choices")
		}
		switch f.Type {
		case "string", "number", "integer", "boolean", "array":
		default:
			return nil, errors.New("unsupported form field")
		}
		switch f.Format {
		case "", "email", "uri", "date", "date-time":
		default:
			return nil, errors.New("unsupported form format")
		}
		if len(spec.Default) > 0 && string(spec.Default) != "null" {
			if f.Type == "array" {
				json.Unmarshal(spec.Default, &f.Default)
			} else {
				var v any
				json.Unmarshal(spec.Default, &v)
				f.Default = []string{fmt.Sprint(v)}
			}
		}
		fields = append(fields, f)
	}
	return fields, nil
}

type requestedPermissions struct {
	Network    *struct{ Enabled bool }
	FileSystem *struct {
		Read, Write      []string
		Entries          []json.RawMessage
		GlobScanMaxDepth *int
	}
}

func permissionChoices(raw json.RawMessage) []domain.RequestChoice {
	var p requestedPermissions
	json.Unmarshal(raw, &p)
	choices := []domain.RequestChoice{}
	if p.Network != nil && p.Network.Enabled {
		choices = append(choices, domain.RequestChoice{ID: "network", Label: "网络访问"})
	}
	if p.FileSystem == nil {
		return choices
	}
	for _, group := range []struct {
		Kind, Label string
		Paths       []string
	}{{"read", "读取 ", p.FileSystem.Read}, {"write", "写入 ", p.FileSystem.Write}} {
		for i, path := range group.Paths {
			choices = append(choices, domain.RequestChoice{ID: fmt.Sprintf("%s:%d", group.Kind, i), Label: group.Label + path})
		}
	}
	for i, raw := range p.FileSystem.Entries {
		var e struct {
			Access string
			Path   struct {
				Type, Path, Pattern string
				Value               json.RawMessage
			}
		}
		json.Unmarshal(raw, &e)
		label := map[string]string{"read": "读取 ", "write": "写入 ", "deny": "不可访问 "}[e.Access]
		if label == "" {
			continue
		}
		choices = append(choices, domain.RequestChoice{ID: fmt.Sprintf("entry:%d", i), Label: label + firstNonempty(e.Path.Path, e.Path.Pattern, string(e.Path.Value)), Restriction: e.Access == "deny"})
	}
	return choices
}
func permissionGrant(raw json.RawMessage, selected []string) (map[string]any, error) {
	allowed := map[string]bool{}
	for _, c := range permissionChoices(raw) {
		if !c.Restriction {
			allowed[c.ID] = true
		}
	}
	set := map[string]bool{}
	for _, id := range selected {
		if !allowed[id] || set[id] {
			return nil, errors.New("permission was not requested or duplicated")
		}
		set[id] = true
	}
	var p requestedPermissions
	json.Unmarshal(raw, &p)
	grant := map[string]any{}
	if set["network"] {
		grant["network"] = map[string]bool{"enabled": true}
	}
	if p.FileSystem != nil {
		fs := map[string]any{}
		for _, group := range []struct {
			Kind  string
			Paths []string
		}{{"read", p.FileSystem.Read}, {"write", p.FileSystem.Write}} {
			paths := []string{}
			for i, path := range group.Paths {
				if set[fmt.Sprintf("%s:%d", group.Kind, i)] {
					paths = append(paths, path)
				}
			}
			if len(paths) > 0 {
				fs[group.Kind] = paths
			}
		}
		entries := []json.RawMessage{}
		hasGrant := false
		for i, e := range p.FileSystem.Entries {
			var access struct{ Access string }
			json.Unmarshal(e, &access)
			if access.Access == "deny" || set[fmt.Sprintf("entry:%d", i)] {
				entries = append(entries, e)
				hasGrant = hasGrant || access.Access != "deny"
			}
		}
		if hasGrant || len(fs) > 0 {
			if len(entries) > 0 {
				fs["entries"] = entries
			}
			if p.FileSystem.GlobScanMaxDepth != nil {
				fs["globScanMaxDepth"] = *p.FileSystem.GlobScanMaxDepth
			}
		}
		if len(fs) > 0 {
			if _, ok := fs["read"]; !ok {
				fs["read"] = nil
			}
			if _, ok := fs["write"]; !ok {
				fs["write"] = nil
			}
			grant["fileSystem"] = fs
		}
	}
	return grant, nil
}

func requestReceipt(r domain.PendingRequest, a domain.Answer) []domain.ContentPart {
	result := []domain.ContentPart{}
	if r.Kind == "questions" {
		for _, q := range r.Questions {
			value := "已提交敏感答案"
			if !q.Secret {
				value = strings.Join(a.Answers[q.ID], "、")
			}
			result = append(result, domain.ContentPart{Kind: "text", Label: q.Text, Text: boundedTo(value, 4096)})
		}
	} else if r.Kind == "form" && a.Decision == "accept" {
		for _, f := range r.Fields {
			if values := a.Answers[f.ID]; len(values) > 0 {
				labels := append([]string{}, values...)
				for i, value := range labels {
					for _, option := range f.Options {
						if option.Value == value {
							labels[i] = option.Label
							break
						}
					}
				}
				result = append(result, domain.ContentPart{Kind: "text", Label: f.Title, Text: boundedTo(strings.Join(labels, "、"), 4096)})
			}
		}
	} else {
		label := map[string]string{"accept": "已允许这一次", "decline": "已拒绝", "cancel": "已取消", "turn": "已允许本轮访问", "session": "已允许本会话访问", "acceptForSession": "本会话已允许"}[a.Decision]
		for _, choice := range r.Choices {
			if choice.ID == a.Decision {
				label = choice.Label
			}
		}
		if label == "" {
			label = "已提交选择"
		}
		result = append(result, domain.ContentPart{Kind: "text", Text: label})
		if r.Kind == "permissions" {
			for _, choice := range r.Choices {
				for _, id := range a.Answers["permissions"] {
					if choice.ID == id {
						result = append(result, domain.ContentPart{Kind: "text", Text: choice.Label})
					}
				}
			}
		}
	}
	return result
}
