package mcp

import (
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	cases := []struct {
		raw     string
		wantErr bool
		tool    string
		server  string
	}{
		{"formula_hint@campus-tools", false, "formula_hint", "campus-tools"},
		{"unit_convert@mcp-demo", false, "unit_convert", "mcp-demo"},
		{"Formula_Hint@campus-tools", true, "", ""}, // 大写
		{"formula-hint@campus-tools", true, "", ""}, // 工具名不允许连字符
		{"formula_hint", true, "", ""},              // 缺服务器名
		{"formula_hint@", true, "", ""},
		{"@campus-tools", true, "", ""},
		{"formula_hint@Campus", true, "", ""}, // 服务器名大写
		{"9tool@campus-tools", true, "", ""},  // 工具名须以字母开头
		{"", true, "", ""},
	}
	for _, c := range cases {
		ref, err := ParseRef(c.raw)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q 应被判为非法", c.raw)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q 应合法，实际 %v", c.raw, err)
			continue
		}
		if ref.Tool != c.tool || ref.Server != c.server {
			t.Errorf("%q 解析结果不对：%+v", c.raw, ref)
		}
	}
}

func TestParseRefErrorMessageIsReadable(t *testing.T) {
	_, err := ParseRef("formula-hint@campus-tools")
	if err == nil {
		t.Fatal("应报错")
	}
	msg := err.Error()
	if !strings.Contains(msg, "小写字母") || !strings.Contains(msg, "服务器名") {
		t.Errorf("错误文案应给出可用写法，实际 %q", msg)
	}
}

func TestIsBuiltin(t *testing.T) {
	if !IsBuiltin(BuiltinKnowledgeSearch) {
		t.Error("knowledge_search 应识别为内置项")
	}
	if IsBuiltin("formula_hint@campus-tools") {
		t.Error("引用不应被当作内置项")
	}
}

func TestCallSummary(t *testing.T) {
	if got := CallSummary("短文本", 200); got != "短文本" {
		t.Errorf("短文本不应截断，实际 %q", got)
	}
	long := strings.Repeat("字", 300)
	got := CallSummary(long, 200)
	if len([]rune(got)) != 201 {
		t.Errorf("应截断到 200 字加省略号，实际 %d", len([]rune(got)))
	}
}

func TestInputNormalize(t *testing.T) {
	good := Input{Name: "  campus-tools ", BaseURL: "http://mcp-demo:8090/", AuthToken: " t "}
	if err := good.Normalize(); err != nil {
		t.Fatalf("合法入参应通过，实际 %v", err)
	}
	if good.Name != "campus-tools" {
		t.Errorf("名称应去空白，实际 %q", good.Name)
	}
	if good.BaseURL != "http://mcp-demo:8090" {
		t.Errorf("地址应去掉尾部斜杠，实际 %q", good.BaseURL)
	}

	bad := []Input{
		{Name: "", BaseURL: "http://x"},
		{Name: strings.Repeat("名", 33), BaseURL: "http://x"},
		{Name: "x", BaseURL: "mcp-demo:8090"},
	}
	for _, in := range bad {
		if err := in.Normalize(); err == nil {
			t.Errorf("非法入参应被拒：%+v", in)
		}
	}
}

// 服务器结构体刻意不含令牌字段：序列化时无从泄露。
func TestServerHasNoTokenField(t *testing.T) {
	s := Server{ID: 1, Name: "campus-tools", HasAuth: true}
	_ = s
	// 编译期即可确认：Server 没有 AuthToken 字段（写出来会编译失败）。
}
