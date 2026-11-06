package domain

import (
	"errors"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// FormContent validates the same normalized contract in UI and at the harness
// response boundary; only fields explicitly present in the request can leave it.
func FormContent(fields []FormField, answers map[string][]string) (map[string]any, error) {
	content := map[string]any{}
	known := map[string]bool{}
	for _, f := range fields {
		known[f.ID] = true
		values, present := answers[f.ID]
		if !present || f.Type != "array" && len(values) == 0 || f.Type != "array" && len(values) == 1 && values[0] == "" {
			if f.Required {
				return nil, fmt.Errorf("请填写%s", f.Title)
			}
			continue
		}
		fail := func() error { return fmt.Errorf("%s的值不符合要求", f.Title) }
		if f.Type != "array" && len(values) != 1 {
			return nil, fail()
		}
		for _, value := range values {
			if len(value) > 16<<10 {
				return nil, fail()
			}
			if len(f.Options) > 0 {
				found := false
				for _, o := range f.Options {
					found = found || o.Value == value
				}
				if !found {
					return nil, fail()
				}
			}
		}
		if f.Type == "array" {
			if f.MinItems != nil && len(values) < *f.MinItems || f.MaxItems != nil && len(values) > *f.MaxItems {
				return nil, fail()
			}
			seen := map[string]bool{}
			for _, v := range values {
				if seen[v] {
					return nil, fail()
				}
				seen[v] = true
			}
			content[f.ID] = append([]string{}, values...)
			continue
		}
		value := values[0]
		switch f.Type {
		case "boolean":
			if value != "true" && value != "false" {
				return nil, fail()
			}
			content[f.ID] = value == "true"
		case "number", "integer":
			v, err := strconv.ParseFloat(value, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || f.Type == "integer" && math.Trunc(v) != v || f.Minimum != nil && v < *f.Minimum || f.Maximum != nil && v > *f.Maximum {
				return nil, fail()
			}
			content[f.ID] = v
		case "string":
			length := utf8.RuneCountInString(value)
			if f.MinLength != nil && length < *f.MinLength || f.MaxLength != nil && length > *f.MaxLength {
				return nil, fail()
			}
			if f.Required && strings.TrimSpace(value) == "" {
				return nil, fail()
			}
			switch f.Format {
			case "email":
				address, err := mail.ParseAddress(value)
				if err != nil || address.Address != value {
					return nil, fail()
				}
			case "uri":
				u, err := url.Parse(value)
				if err != nil || u.Scheme == "" {
					return nil, fail()
				}
			case "date":
				if _, err := time.Parse("2006-01-02", value); err != nil {
					return nil, fail()
				}
			case "date-time":
				if _, err := time.Parse(time.RFC3339, value); err != nil {
					return nil, fail()
				}
			}
			content[f.ID] = value
		default:
			return nil, errors.New("不支持的表单字段")
		}
	}
	for key := range answers {
		if !known[key] {
			return nil, errors.New("表单包含未请求的字段")
		}
	}
	return content, nil
}
