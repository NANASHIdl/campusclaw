// Package openapi 把 swaggo 生成的 Swagger 2.0 描述投影为 OpenAPI 3.0 描述。
//
// 为什么需要这一步：本项目的接口文档唯一来源是处理函数上方的 API 注解，
// 由 swag 解析成 Swagger 2.0；而规约要求对外提供 OpenAPI 3.0 描述，
// 并把认证方案声明为 `bearerAuth`（type http / scheme bearer / bearerFormat JWT）。
// 因此这里只做「格式投影」，不引入第二份手工维护的文档：
// 注解改了什么，OpenAPI 描述就跟着改什么。
package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Version 是本项目对外声明的 OpenAPI 版本。
const Version = "3.0.3"

// Convert 把 swag 的 Swagger 2.0 JSON 转换为 OpenAPI 3.0 JSON。
func Convert(swaggerJSON []byte) ([]byte, error) {
	var src map[string]any
	if err := json.Unmarshal(swaggerJSON, &src); err != nil {
		return nil, fmt.Errorf("解析 swagger 描述失败: %w", err)
	}
	if _, ok := src["paths"]; !ok {
		return nil, errors.New("swagger 描述缺少 paths")
	}

	out := map[string]any{
		"openapi": Version,
		"info":    src["info"],
		"servers": []any{map[string]any{"url": serverURL(src)}},
		"paths":   convertPaths(src["paths"]),
	}

	components := map[string]any{}
	if defs, ok := src["definitions"].(map[string]any); ok && len(defs) > 0 {
		components["schemas"] = rewriteRefs(defs)
	}
	if sec, ok := src["securityDefinitions"].(map[string]any); ok && len(sec) > 0 {
		components["securitySchemes"] = convertSecuritySchemes(sec)
	}
	if len(components) > 0 {
		out["components"] = components
	}

	// 顶层 security 原样保留（本项目未使用全局安全声明）。
	if v, ok := src["security"]; ok {
		out["security"] = v
	}

	return json.MarshalIndent(out, "", "  ")
}

func serverURL(src map[string]any) string {
	base, _ := src["basePath"].(string)
	if base == "" {
		base = "/"
	}
	return base
}

// --------------------------------------------------------------------------- //
// paths 与 operation
// --------------------------------------------------------------------------- //

func convertPaths(raw any) map[string]any {
	src, _ := raw.(map[string]any)
	out := make(map[string]any, len(src))
	for path, item := range src {
		methods, ok := item.(map[string]any)
		if !ok {
			continue
		}
		converted := make(map[string]any, len(methods))
		for method, opRaw := range methods {
			op, ok := opRaw.(map[string]any)
			if !ok {
				continue
			}
			converted[method] = convertOperation(op)
		}
		out[path] = converted
	}
	return out
}

func convertOperation(op map[string]any) map[string]any {
	out := map[string]any{}

	for _, key := range []string{"summary", "description", "operationId", "tags", "deprecated", "security"} {
		if v, ok := op[key]; ok {
			out[key] = v
		}
	}

	produces := firstString(op["produces"], "application/json")
	consumes := firstString(op["consumes"], "application/json")

	// 参数：body 提升为 requestBody，其余把类型塞进 schema。
	params := make([]any, 0)
	if list, ok := op["parameters"].([]any); ok {
		for _, pRaw := range list {
			p, ok := pRaw.(map[string]any)
			if !ok {
				continue
			}
			switch p["in"] {
			case "body":
				rb := map[string]any{
					"content": map[string]any{
						consumes: map[string]any{"schema": rewriteRefs(p["schema"])},
					},
				}
				if d, ok := p["description"]; ok {
					rb["description"] = d
				}
				if r, ok := p["required"]; ok {
					rb["required"] = r
				}
				out["requestBody"] = rb
			case "formData":
				// 本项目未使用；保守地跳过，避免产出半对的 multipart 定义。
				continue
			default:
				params = append(params, convertParameter(p))
			}
		}
	}
	if len(params) > 0 {
		out["parameters"] = params
	}

	if resp, ok := op["responses"].(map[string]any); ok {
		out["responses"] = convertResponses(resp, produces)
	}

	// 明确丢弃 Swagger 2.0 专有字段：OpenAPI 3 由 requestBody 与 content 表达。
	return out
}

// convertParameter 把 Swagger 2.0 的非 body 参数转换为 OpenAPI 3 的 parameter。
func convertParameter(p map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"name", "in", "description", "required", "allowEmptyValue"} {
		if v, ok := p[key]; ok {
			out[key] = v
		}
	}

	schema := map[string]any{}
	for _, key := range []string{"type", "format", "items", "enum", "default",
		"minimum", "maximum", "minLength", "maxLength", "pattern", "uniqueItems"} {
		if v, ok := p[key]; ok {
			schema[key] = rewriteRefs(v)
		}
	}
	// OpenAPI 3 不再有 collectionFormat：多值参数改用 style/explode 表达。
	if cf, ok := p["collectionFormat"].(string); ok {
		switch cf {
		case "multi":
			out["style"] = "form"
			out["explode"] = true
		case "csv", "ssv", "pipes", "tsv":
			out["style"] = map[string]string{
				"csv": "form", "ssv": "spaceDelimited", "pipes": "pipeDelimited", "tsv": "form",
			}[cf]
			out["explode"] = false
		}
	}
	if len(schema) > 0 {
		out["schema"] = schema
	}
	return out
}

func convertResponses(src map[string]any, mediaType string) map[string]any {
	out := make(map[string]any, len(src))
	for code, rRaw := range src {
		r, ok := rRaw.(map[string]any)
		if !ok {
			out[code] = rRaw
			continue
		}
		item := map[string]any{"description": r["description"]}
		if schema, ok := r["schema"]; ok && schema != nil {
			item["content"] = map[string]any{
				mediaType: map[string]any{"schema": rewriteRefs(schema)},
			}
		}
		out[code] = item
	}
	return out
}

// --------------------------------------------------------------------------- //
// 安全方案与 $ref
// --------------------------------------------------------------------------- //

// convertSecuritySchemes 把 Swagger 2.0 的 apiKey 声明改写为 OpenAPI 3 的 http/bearer。
//
// swag 没有 `securityDefinitions.bearer` 的写法，本项目用
// `@securityDefinitions.apikey bearerAuth` + `@in header` + `@name Authorization` 声明，
// 语义上就是「Authorization 头里放 Bearer 令牌」，这里把它翻译成标准写法。
func convertSecuritySchemes(src map[string]any) map[string]any {
	out := make(map[string]any, len(src))
	for name, raw := range src {
		s, ok := raw.(map[string]any)
		if !ok {
			out[name] = raw
			continue
		}
		if s["type"] == "apiKey" && s["in"] == "header" && s["name"] == "Authorization" {
			scheme := map[string]any{
				"type":         "http",
				"scheme":       "bearer",
				"bearerFormat": "JWT",
			}
			if d, ok := s["description"]; ok {
				scheme["description"] = d
			}
			out[name] = scheme
			continue
		}
		out[name] = s
	}
	return out
}

// rewriteRefs 把 `#/definitions/X` 改写为 `#/components/schemas/X`，并递归处理嵌套结构。
func rewriteRefs(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if k == "$ref" {
				if s, ok := val.(string); ok {
					out[k] = rewriteRefString(s)
					continue
				}
			}
			out[k] = rewriteRefs(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = rewriteRefs(val)
		}
		return out
	default:
		return v
	}
}

func rewriteRefString(s string) string {
	const prefix = "#/definitions/"
	if len(s) > len(prefix) && s[:len(prefix)] == prefix {
		return "#/components/schemas/" + s[len(prefix):]
	}
	return s
}

func firstString(v any, fallback string) string {
	if list, ok := v.([]any); ok && len(list) > 0 {
		if s, ok := list[0].(string); ok && s != "" {
			return s
		}
	}
	return fallback
}
