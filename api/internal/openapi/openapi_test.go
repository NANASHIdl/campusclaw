package openapi

import (
	"encoding/json"
	"testing"
)

const sampleSwagger = `{
  "swagger": "2.0",
  "info": {"title": "T", "version": "1.0"},
  "basePath": "/",
  "schemes": ["http"],
  "paths": {
    "/api/agents/{id}": {
      "put": {
        "summary": "修改",
        "operationId": "AGT-004",
        "tags": ["agents"],
        "consumes": ["application/json"],
        "produces": ["application/json"],
        "security": [{"bearerAuth": []}],
        "parameters": [
          {"name": "id", "in": "path", "required": true, "type": "integer", "format": "int64"},
          {"name": "body", "in": "body", "required": true, "schema": {"$ref": "#/definitions/internal_agent.WriteRequest"}}
        ],
        "responses": {
          "200": {"description": "OK", "schema": {"$ref": "#/definitions/internal_agent.Agent"}},
          "500": {"description": "Internal Server Error", "schema": {"$ref": "#/definitions/httpx.ErrorBody"}}
        }
      }
    },
    "/api/login": {
      "post": {
        "summary": "登录",
        "operationId": "AUTH-001",
        "produces": ["application/json"],
        "responses": {"200": {"description": "OK"}}
      }
    }
  },
  "definitions": {
    "internal_agent.Agent": {
      "type": "object",
      "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}
    },
    "internal_agent.WriteRequest": {
      "type": "object",
      "properties": {"name": {"type": "string"}, "skill_ids": {"type": "array", "items": {"type": "integer"}}}
    },
    "httpx.ErrorBody": {
      "type": "object",
      "properties": {"error": {"type": "string"}}
    }
  },
  "securityDefinitions": {
    "bearerAuth": {"type": "apiKey", "name": "Authorization", "in": "header", "description": "Bearer 令牌"}
  }
}`

func convertSample(t *testing.T) map[string]any {
	t.Helper()
	raw, err := Convert([]byte(sampleSwagger))
	if err != nil {
		t.Fatalf("Convert 失败: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}
	return out
}

func TestConvertVersionAndBearerScheme(t *testing.T) {
	out := convertSample(t)

	if out["openapi"] != Version {
		t.Errorf("openapi = %v, 期望 %s", out["openapi"], Version)
	}
	if out["swagger"] != nil {
		t.Errorf("不应保留 swagger 字段")
	}

	comps, _ := out["components"].(map[string]any)
	schemes, _ := comps["securitySchemes"].(map[string]any)
	bearer, _ := schemes["bearerAuth"].(map[string]any)
	if bearer["type"] != "http" || bearer["scheme"] != "bearer" || bearer["bearerFormat"] != "JWT" {
		t.Errorf("bearerAuth 应为 http/bearer/JWT，实际 %v", bearer)
	}
	if bearer["in"] != nil || bearer["name"] != nil {
		t.Errorf("apiKey 专有字段应被移除，实际 %v", bearer)
	}
}

func TestConvertBodyParameterToRequestBody(t *testing.T) {
	out := convertSample(t)
	paths, _ := out["paths"].(map[string]any)
	item, _ := paths["/api/agents/{id}"].(map[string]any)
	op, _ := item["put"].(map[string]any)

	if op["consumes"] != nil || op["produces"] != nil {
		t.Errorf("应丢弃 consumes/produces")
	}
	body, ok := op["requestBody"].(map[string]any)
	if !ok {
		t.Fatalf("body 参数未提升为 requestBody")
	}
	if body["required"] != true {
		t.Errorf("requestBody.required 应保留")
	}
	content, _ := body["content"].(map[string]any)
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("requestBody.content 缺少 application/json，实际 %v", content)
	}
	schema, _ := media["schema"].(map[string]any)
	if schema["$ref"] != "#/components/schemas/internal_agent.WriteRequest" {
		t.Errorf("$ref 未改写，实际 %v", schema["$ref"])
	}

	params, _ := op["parameters"].([]any)
	if len(params) != 1 {
		t.Fatalf("path 参数应保留 1 个，实际 %d", len(params))
	}
	p, _ := params[0].(map[string]any)
	if p["type"] != nil {
		t.Errorf("OpenAPI 3 的参数不应把 type 放在顶层，实际 %v", p)
	}
	ps, _ := p["schema"].(map[string]any)
	if ps["type"] != "integer" || ps["format"] != "int64" {
		t.Errorf("参数类型应搬进 schema，实际 %v", ps)
	}
}

func TestConvertResponseSchemaToContent(t *testing.T) {
	out := convertSample(t)
	paths, _ := out["paths"].(map[string]any)
	item, _ := paths["/api/agents/{id}"].(map[string]any)
	op, _ := item["put"].(map[string]any)
	resps, _ := op["responses"].(map[string]any)

	ok200, _ := resps["200"].(map[string]any)
	if ok200["schema"] != nil {
		t.Errorf("响应不应保留顶层 schema")
	}
	content, _ := ok200["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	if schema["$ref"] != "#/components/schemas/internal_agent.Agent" {
		t.Errorf("响应 $ref 未改写，实际 %v", schema["$ref"])
	}

	// 无 schema 的响应只保留描述。
	loginPaths, _ := paths["/api/login"].(map[string]any)
	loginOp, _ := loginPaths["post"].(map[string]any)
	loginResps, _ := loginOp["responses"].(map[string]any)
	l200, _ := loginResps["200"].(map[string]any)
	if l200["content"] != nil {
		t.Errorf("没有 schema 的响应不应凭空生成 content")
	}
	if l200["description"] == nil {
		t.Errorf("响应描述必须保留")
	}
}

func TestConvertKeepsSecurityPerOperation(t *testing.T) {
	out := convertSample(t)
	paths, _ := out["paths"].(map[string]any)
	item, _ := paths["/api/agents/{id}"].(map[string]any)
	op, _ := item["put"].(map[string]any)
	sec, _ := op["security"].([]any)
	if len(sec) != 1 {
		t.Fatalf("需登录操作应保留 security，实际 %v", op["security"])
	}

	loginPaths, _ := paths["/api/login"].(map[string]any)
	loginOp, _ := loginPaths["post"].(map[string]any)
	if loginOp["security"] != nil {
		t.Errorf("白名单操作不应带 security")
	}
}

func TestConvertRejectsInvalidInput(t *testing.T) {
	if _, err := Convert([]byte("不是 JSON")); err == nil {
		t.Error("非法 JSON 应报错")
	}
	if _, err := Convert([]byte(`{"swagger":"2.0"}`)); err == nil {
		t.Error("缺少 paths 应报错")
	}
}
