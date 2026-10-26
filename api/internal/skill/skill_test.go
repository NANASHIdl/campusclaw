package skill

import (
	"errors"
	"strings"
	"testing"
)

const validSource = `名称：数学解题引导
适用条件：方程,去括号、分数；面积|行程
资料：数学
工具：knowledge_search
启用：是

学生正在自己做数学作业，先看懂他卡在哪一步。`

func TestParseValid(t *testing.T) {
	p, err := Parse(validSource)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "数学解题引导" {
		t.Errorf("name=%q", p.Name)
	}
	if p.Subject != "数学" {
		t.Errorf("subject=%q", p.Subject)
	}
	if !p.Enabled {
		t.Error("enabled 应为 true")
	}
	if len(p.Tools) != 1 || p.Tools[0] != ToolKnowledgeSearch {
		t.Errorf("tools=%v", p.Tools)
	}
	if !strings.Contains(p.Description, "先看懂") {
		t.Errorf("description=%q", p.Description)
	}
	if p.Source != validSource {
		t.Error("source 必须原样保留")
	}
}

func TestParseIgnoresCommentAndBlankInMeta(t *testing.T) {
	// 元数据区里 `#` 开头的行被忽略；第一个空行是元数据与正文的分隔。
	src := "# 这是注释\n名称：甲\n适用条件：a\n启用：否\n\n正文"
	p, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Name != "甲" || p.Enabled {
		t.Errorf("解析结果异常: %+v", p)
	}
	if p.Description != "正文" {
		t.Errorf("description=%q", p.Description)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want error
	}{
		{"空文件", "   ", ErrEmptyFile},
		{"没有空行", "名称：甲\n适用条件：a\n启用：是", ErrNoBody},
		{"空行下无正文", "名称：甲\n适用条件：a\n启用：是\n\n   ", ErrNoBody},
		{"缺名称", "适用条件：a\n启用：是\n\n正文", nil},
		{"缺适用条件", "名称：甲\n启用：是\n\n正文", nil},
		{"缺启用", "名称：甲\n适用条件：a\n\n正文", nil},
		{"未定义字段", "名称：甲\n适用条件：a\n启用：是\n作者：x\n\n正文", ErrUnknownField},
		{"重复字段", "名称：甲\n名称：乙\n适用条件：a\n启用：是\n\n正文", ErrDuplicate},
		{"非法工具", "名称：甲\n适用条件：a\n启用：是\n工具：web_search\n\n正文", ErrBadTool},
		{"非法启用", "名称：甲\n适用条件：a\n启用：maybe\n\n正文", ErrBadEnabled},
		{"超长文件", "名称：甲\n适用条件：a\n启用：是\n\n" + strings.Repeat("字", 8001), ErrTooLongFile},
	}
	for _, tc := range cases {
		_, err := Parse(tc.src)
		if tc.want == nil {
			if err == nil {
				t.Errorf("%s: 期望报错，实际通过", tc.name)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: 期望 %v，实际 %v", tc.name, tc.want, err)
		}
	}
}

func TestParseLengthLimits(t *testing.T) {
	long := strings.Repeat("名", 33)
	src := "名称：" + long + "\n适用条件：a\n启用：是\n\n正文"
	if _, err := Parse(src); err == nil {
		t.Error("名称超过 32 字应被拒绝")
	}
}

func TestRoutePicksHighestScore(t *testing.T) {
	cands := []Skill{
		{ID: 1, Name: "甲", Trigger: "方程,面积", Enabled: true},
		{ID: 2, Name: "乙", Trigger: "作文,阅读", Enabled: true},
	}
	got, ok := Route("这道方程的面积怎么求", cands)
	if !ok || got.ID != 1 {
		t.Errorf("应选中 id=1，实际 %+v ok=%v", got, ok)
	}
}

func TestRouteTieBreaksBySmallerID(t *testing.T) {
	cands := []Skill{
		{ID: 3, Name: "甲", Trigger: "方程"},
		{ID: 5, Name: "乙", Trigger: "方程"},
	}
	got, ok := Route("方程", cands)
	if !ok || got.ID != 3 {
		t.Errorf("同分应取 id 较小者，实际 %+v", got)
	}
}

func TestRouteZeroScore(t *testing.T) {
	cands := []Skill{{ID: 1, Name: "甲", Trigger: "方程"}}
	if _, ok := Route("今天天气如何", cands); ok {
		t.Error("零分不应选技能")
	}
}

func TestRouteCaseInsensitive(t *testing.T) {
	cands := []Skill{{ID: 1, Name: "甲", Trigger: "Equation"}}
	if _, ok := Route("solve this equation please", cands); !ok {
		t.Error("大小写不敏感应命中")
	}
}
