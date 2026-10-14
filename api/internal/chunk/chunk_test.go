package chunk

import (
	"strings"
	"testing"
)

func TestAutoWindowSizeAndOverlap(t *testing.T) {
	body := strings.Repeat("这是一段用于测试自动窗口切分的中文正文。", 200)

	got, err := Split(body, Options{Strategy: StrategyAuto})
	if err != nil {
		t.Fatalf("Split 返回错误: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("长正文应切出多片，实际 %d 片", len(got))
	}

	total := len([]rune(body))
	for i, c := range got {
		size := len([]rune(c.Text))
		if size > DefaultMaxChars {
			t.Errorf("第 %d 片长度 %d 超过上限 %d", i, size, DefaultMaxChars)
		}
		if c.Index != i {
			t.Errorf("第 %d 片序号应为 %d，实际 %d", i, i, c.Index)
		}
		if c.End <= c.Start {
			t.Errorf("第 %d 片偏移非法: [%d,%d)", i, c.Start, c.End)
		}
		if c.End > total {
			t.Errorf("第 %d 片结束偏移 %d 超出正文长度 %d", i, c.End, total)
		}
	}

	// 相邻切片必须重叠。
	for i := 1; i < len(got); i++ {
		if got[i].Start >= got[i-1].End {
			t.Errorf("第 %d 片与上一片没有重叠: prev.End=%d cur.Start=%d", i, got[i-1].End, got[i].Start)
		}
	}
}

func TestAutoIgnoresCustomParameters(t *testing.T) {
	body := strings.Repeat("自动策略应忽略请求中另行填写的长度与预处理参数。", 120)

	// auto 策略下即便传入 200 字上限也不生效。
	got, err := Split(body, Options{Strategy: StrategyAuto, MaxChars: 200, OverlapPercent: 50, CollapseWhitespace: true})
	if err != nil {
		t.Fatalf("Split 返回错误: %v", err)
	}
	for i, c := range got {
		if size := len([]rune(c.Text)); size > DefaultMaxChars {
			t.Errorf("第 %d 片长度 %d，说明 auto 未忽略自定义上限", i, size)
		}
	}
}

func TestCustomValidation(t *testing.T) {
	cases := []struct {
		name string
		opt  Options
	}{
		{"长度过小", Options{Strategy: StrategyCustom, MaxChars: 50}},
		{"长度过大", Options{Strategy: StrategyCustom, MaxChars: 5000}},
		{"重叠为负", Options{Strategy: StrategyCustom, MaxChars: 400, OverlapPercent: -1}},
		{"重叠过大", Options{Strategy: StrategyCustom, MaxChars: 400, OverlapPercent: 60}},
	}
	for _, tc := range cases {
		if _, err := Normalize(tc.opt); err == nil {
			t.Errorf("%s: 期望校验失败，实际通过", tc.name)
		}
	}

	if _, err := Normalize(Options{Strategy: StrategyCustom, MaxChars: 400, OverlapPercent: 25}); err != nil {
		t.Errorf("合法参数被拒绝: %v", err)
	}
}

func TestCustomMaxLengthRespected(t *testing.T) {
	body := strings.Repeat("自定义切分需要严格遵守最大长度限制。", 100)

	got, err := Split(body, Options{Strategy: StrategyCustom, MaxChars: 300, OverlapPercent: 0})
	if err != nil {
		t.Fatalf("Split 返回错误: %v", err)
	}
	for i, c := range got {
		if size := len([]rune(c.Text)); size > 300 {
			t.Errorf("第 %d 片长度 %d 超过自定义上限 300", i, size)
		}
	}
}

func TestCustomPreprocessKeepsOriginalBody(t *testing.T) {
	body := "第一句。请访问 https://example.com/a 查看，或发邮件到 a@b.com。\n\n第二句。"
	got, err := Split(body, Options{Strategy: StrategyCustom, MaxChars: 200, OverlapPercent: 0, RemoveURLs: true})
	if err != nil {
		t.Fatalf("Split 返回错误: %v", err)
	}
	joined := ""
	for _, c := range got {
		joined += c.Text
	}
	if strings.Contains(joined, "https://example.com") || strings.Contains(joined, "a@b.com") {
		t.Errorf("预处理未移除 URL/邮箱: %q", joined)
	}
	// 切分不改写原文：body 变量本身不受影响。
	if !strings.Contains(body, "https://example.com") {
		t.Errorf("原文不应被改写")
	}
}

func TestHierarchyKeepsHeadings(t *testing.T) {
	body := "# 第一章\n内容甲。\n## 第一节\n内容乙。\n# 第二章\n内容丙。"

	got, err := Split(body, Options{Strategy: StrategyHierarchy})
	if err != nil {
		t.Fatalf("Split 返回错误: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("期望 3 个章节切片，实际 %d: %+v", len(got), got)
	}
	for i, want := range []string{"# 第一章", "## 第一节", "# 第二章"} {
		if !strings.HasPrefix(strings.TrimSpace(got[i].Text), want) {
			t.Errorf("第 %d 片应以标题 %q 开头，实际 %q", i, want, got[i].Text)
		}
	}
}

func TestHierarchyFallsBackWithoutHeadings(t *testing.T) {
	body := strings.Repeat("没有标题的正文也应当能被切分。", 100)
	got, err := Split(body, Options{Strategy: StrategyHierarchy})
	if err != nil {
		t.Fatalf("Split 返回错误: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("无标题正文应退化为窗口切分，实际 0 片")
	}
}

func TestHierarchyHonoursLengthParameters(t *testing.T) {
	// 单个超长章节应再按最大长度与重叠比例切开。
	long := strings.Repeat("这一章内容很长，需要按窗口继续切分。", 60)
	body := "# 唯一章节\n" + long

	got, err := Split(body, Options{Strategy: StrategyHierarchy, MaxChars: 300, OverlapPercent: 20})
	if err != nil {
		t.Fatalf("Split 返回错误: %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("超长章节应被切成多片，实际 %d 片", len(got))
	}
	for i, c := range got {
		if size := len([]rune(c.Text)); size > 300 {
			t.Errorf("第 %d 片长度 %d 超过指定上限 300", i, size)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i].Start >= got[i-1].End {
			t.Errorf("第 %d 片与上一片没有重叠（指定重叠 20%%）", i)
		}
	}
}

func TestHierarchyValidatesParameters(t *testing.T) {
	if _, err := Normalize(Options{Strategy: StrategyHierarchy, MaxChars: 50}); err == nil {
		t.Error("hierarchy 的最大长度越界应被拒绝")
	}
	if _, err := Normalize(Options{Strategy: StrategyHierarchy, MaxChars: 800, OverlapPercent: 80}); err == nil {
		t.Error("hierarchy 的重叠比例越界应被拒绝")
	}
	if _, err := Normalize(Options{Strategy: StrategyHierarchy}); err != nil {
		t.Errorf("hierarchy 默认参数应合法: %v", err)
	}
}

func TestUnknownStrategyRejected(t *testing.T) {
	if _, err := Split("正文", Options{Strategy: "semantic"}); err == nil {
		t.Error("未知策略应被拒绝")
	}
}
