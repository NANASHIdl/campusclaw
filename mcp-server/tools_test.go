package main

import (
	"regexp"
	"strings"
	"testing"
)

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func TestDeclarationsHaveThreeFields(t *testing.T) {
	decls := toolDeclarations()
	if len(decls) < 2 {
		t.Fatalf("至少要暴露两个工具，实际 %d 个", len(decls))
	}
	for _, d := range decls {
		name, _ := d["name"].(string)
		desc, _ := d["description"].(string)
		schema, ok := d["inputSchema"].(map[string]any)
		if name == "" || desc == "" || !ok {
			t.Errorf("工具 %q 的三项声明不齐：desc=%q inputSchema 存在=%v", name, desc, ok)
			continue
		}
		if !toolNamePattern.MatchString(name) {
			t.Errorf("工具名 %q 不符合 ^[a-z][a-z0-9_]*$", name)
		}
		if !strings.Contains(desc, "不适用") {
			t.Errorf("工具 %q 的说明缺少「不适用」边界", name)
		}
		if schema["type"] != "object" {
			t.Errorf("工具 %q 的 inputSchema 不是 object", name)
		}
		props, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]string)
		if len(props) == 0 || len(required) == 0 {
			t.Errorf("工具 %q 的 inputSchema 缺 properties 或 required", name)
		}
	}
}

func TestFormulaHintReturnsHintNotAnswer(t *testing.T) {
	text, err := runFormulaHint(map[string]any{"topic": "求根公式"})
	if err != nil {
		t.Fatalf("求根公式应能给出提示，实际 %v", err)
	}
	if !strings.Contains(text, "x = (-b ± √(b²-4ac))") {
		t.Errorf("提示里应含求根公式，实际 %q", text)
	}
	if strings.Contains(text, "x1 =") || strings.Contains(text, "x2 =") {
		t.Errorf("不应给出具体方程的解，实际 %q", text)
	}
}

func TestFormulaHintUnknownTopic(t *testing.T) {
	if _, err := runFormulaHint(map[string]any{"topic": "怎么做红烧肉"}); err == nil {
		t.Fatal("非数学主题应报错")
	} else if !strings.Contains(err.Error(), "不在能力范围内") {
		t.Errorf("错误文案应说明超出能力范围，实际 %v", err)
	}
}

func TestUnitConvertOneStep(t *testing.T) {
	text, err := runUnitConvert(map[string]any{"value": 3.0, "from": "千米", "to": "米"})
	if err != nil {
		t.Fatalf("换算应成功，实际 %v", err)
	}
	if !strings.Contains(text, "3000") {
		t.Errorf("3 千米应等于 3000 米，实际 %q", text)
	}
	if strings.Count(text, "=") != 1 {
		t.Errorf("只应做一步换算，实际 %q", text)
	}
}

func TestUnitConvertRejectsDifferentDimension(t *testing.T) {
	_, err := runUnitConvert(map[string]any{"value": 1.0, "from": "米", "to": "千克"})
	if err == nil {
		t.Fatal("不同物理量应报错")
	}
	if !strings.Contains(err.Error(), "不是同一物理量") {
		t.Errorf("错误文案应说明不是同一物理量，实际 %v", err)
	}
}

func TestUnitConvertTemperature(t *testing.T) {
	text, err := runUnitConvert(map[string]any{"value": 100.0, "from": "摄氏度", "to": "华氏度"})
	if err != nil {
		t.Fatalf("温度换算应成功，实际 %v", err)
	}
	if !strings.Contains(text, "212") {
		t.Errorf("100 摄氏度应等于 212 华氏度，实际 %q", text)
	}
}

func TestUnitConvertUnknownUnit(t *testing.T) {
	if _, err := runUnitConvert(map[string]any{"value": 1.0, "from": "光年", "to": "米"}); err == nil {
		t.Fatal("未知单位应报错")
	}
}

func TestValidateArgs(t *testing.T) {
	cases := []struct {
		name string
		fn   func(map[string]any) string
		args map[string]any
		bad  bool
	}{
		{"formula 缺 topic", validateFormulaHint, map[string]any{}, true},
		{"formula grade 非法", validateFormulaHint, map[string]any{"topic": "勾股定理", "grade": "大学"}, true},
		{"formula 合法", validateFormulaHint, map[string]any{"topic": "勾股定理"}, false},
		{"convert 缺 value", validateUnitConvert, map[string]any{"from": "米", "to": "厘米"}, true},
		{"convert value 非数字", validateUnitConvert, map[string]any{"value": "3", "from": "米", "to": "厘米"}, true},
		{"convert 缺 to", validateUnitConvert, map[string]any{"value": 3.0, "from": "米"}, true},
		{"convert 合法", validateUnitConvert, map[string]any{"value": 3.0, "from": "米", "to": "厘米"}, false},
	}
	for _, c := range cases {
		msg := c.fn(c.args)
		if c.bad && msg == "" {
			t.Errorf("%s：应判为不合法", c.name)
		}
		if !c.bad && msg != "" {
			t.Errorf("%s：应判为合法，实际 %q", c.name, msg)
		}
	}
}

func TestToolTextHasNoInstructionPhrases(t *testing.T) {
	bad := []string{"忽略之前", "ignore previous", "system:", "请调用", "必须执行"}
	for name := range tools {
		if strings.Contains(tools[name].Description, bad[0]) {
			t.Errorf("工具 %q 的说明含指令式句式", name)
		}
	}
	texts := []string{}
	if v, err := runFormulaHint(map[string]any{"topic": "勾股定理"}); err == nil {
		texts = append(texts, v)
	}
	if v, err := runUnitConvert(map[string]any{"value": 1.0, "from": "米", "to": "厘米"}); err == nil {
		texts = append(texts, v)
	}
	for _, text := range texts {
		for _, phrase := range bad {
			if strings.Contains(text, phrase) {
				t.Errorf("返回文本含指令式句式 %q：%q", phrase, text)
			}
		}
	}
}
