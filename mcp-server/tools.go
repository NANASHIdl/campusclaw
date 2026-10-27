package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// tool 是一个可被发现的工具：名称、说明、输入结构，外加两个函数。
//
// 三项声明会原样进入提示词与宿主的校验器，所以说明里必须写清适用与不适用。
type tool struct {
	Name        string
	Description string
	Schema      map[string]any
	validate    func(args map[string]any) string
	run         func(args map[string]any) (string, error)
}

// tools 是注册表；键即工具名，必须满足 ^[a-z][a-z0-9_]*$。
var tools = map[string]*tool{
	"formula_hint": {
		Name: "formula_hint",
		Description: "按主题给出一条数学公式提示与下一步该检查什么。" +
			"适用于学生在解题中途卡住、需要回忆公式或公式变形时。" +
			"只给提示与公式，不给最终答案、不代做题；" +
			"不适用于需要直接算出具体数值结果的场景，也不适用于与数学无关的问题。",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"topic": map[string]any{
					"type":        "string",
					"description": "公式主题，如 求根公式 / 勾股定理 / 扇形面积",
				},
				"grade": map[string]any{
					"type":        "string",
					"enum":        []string{"小学", "初中", "高中"},
					"description": "学段，用于调整提示深度，缺省按初中",
				},
			},
			"required":             []string{"topic"},
			"additionalProperties": false,
		},
		validate: validateFormulaHint,
		run:      runFormulaHint,
	},
	"unit_convert": {
		Name: "unit_convert",
		Description: "在同一物理量的单位之间做一步换算（长度、质量、时间、温度）。" +
			"适用于需要把数值换成另一种单位以便计算或比较时。" +
			"只做一步换算，需要多步时请连续调用；" +
			"不适用于不同物理量之间的换算（如米换千克），也不适用于货币与汇率。",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"value": map[string]any{"type": "number", "description": "待换算的数值"},
				"from":  map[string]any{"type": "string", "description": "原单位，如 米 / 千米 / 克 / 分钟 / 摄氏度"},
				"to":    map[string]any{"type": "string", "description": "目标单位"},
			},
			"required":             []string{"value", "from", "to"},
			"additionalProperties": false,
		},
		validate: validateUnitConvert,
		run:      runUnitConvert,
	},
}

// toolDeclarations 按名称排序输出，保证 tools/list 结果稳定可比。
func toolDeclarations() []map[string]any {
	names := make([]string, 0, len(tools))
	for name := range tools {
		names = append(names, name)
	}
	sort.Strings(names)

	out := make([]map[string]any, 0, len(names))
	for _, name := range names {
		t := tools[name]
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.Schema,
		})
	}
	return out
}

// --- 参数校验 ---------------------------------------------------------------

func validateFormulaHint(args map[string]any) string {
	topic, ok := args["topic"].(string)
	if !ok || strings.TrimSpace(topic) == "" {
		return "topic 必填，且必须是字符串"
	}
	if g, ok := args["grade"]; ok && g != nil {
		s, ok := g.(string)
		if !ok {
			return "grade 必须是字符串"
		}
		if s != "小学" && s != "初中" && s != "高中" {
			return "grade 只能是 小学 / 初中 / 高中"
		}
	}
	return ""
}

func validateUnitConvert(args map[string]any) string {
	v, ok := args["value"]
	if !ok {
		return "value 必填"
	}
	if _, ok := v.(float64); !ok {
		return "value 必须是数字"
	}
	for _, key := range []string{"from", "to"} {
		s, ok := args[key].(string)
		if !ok || strings.TrimSpace(s) == "" {
			return key + " 必填，且必须是字符串"
		}
	}
	return ""
}

// --- formula_hint -----------------------------------------------------------

type formula struct {
	keywords []string
	text     string
}

// 只写公式与下一步检查点：不给最终答案，也不代做题。
var formulas = []formula{
	{
		keywords: []string{"求根公式", "一元二次", "判别式", "二次方程"},
		text: "公式：x = (-b ± √(b²-4ac)) / (2a)，判别式 Δ = b² - 4ac。\n" +
			"下一步：先确认方程已化成 ax²+bx+c=0 的标准形式，再逐项核对 a、b、c 的符号（尤其 b 为负时）。",
	},
	{
		keywords: []string{"勾股", "直角三角形"},
		text: "公式：a² + b² = c²，其中 c 是斜边。\n" +
			"下一步：先确认哪一条边是斜边（对着直角的那个），再把已知两边代入，注意单位要统一。",
	},
	{
		keywords: []string{"扇形面积", "扇形"},
		text: "公式：扇形面积 S = (n/360)·πr²，弧长 l = (n/360)·2πr，n 为圆心角（度）。\n" +
			"下一步：先确认题目给的是圆心角还是弧长，两者用的公式不同。",
	},
	{
		keywords: []string{"圆的面积", "圆面积", "周长"},
		text: "公式：圆面积 S = πr²，周长 C = 2πr。\n" +
			"下一步：确认题目给的是半径还是直径；给直径要先除以 2。",
	},
	{
		keywords: []string{"三角形面积", "三角面积"},
		text: "公式：三角形面积 S = ½ × 底 × 高。\n" +
			"下一步：确认所用的「高」是与这条底垂直的那一条，不要拿斜边当高。",
	},
	{
		keywords: []string{"完全平方", "配方"},
		text: "公式：(a ± b)² = a² ± 2ab + b²。\n" +
			"下一步：看中间项是不是两边乘积的 2 倍；配方法则先看二次项系数是否已化为一。",
	},
	{
		keywords: []string{"速度", "路程", "行程"},
		text: "公式：路程 = 速度 × 时间。\n" +
			"下一步：先把速度与时间的单位统一（如千米/小时 配 小时），再代入。",
	},
	{
		keywords: []string{"密度"},
		text: "公式：密度 = 质量 ÷ 体积。\n" +
			"下一步：确认质量用克还是千克、体积用立方厘米还是立方米，单位要与答案要求一致。",
	},
}

func runFormulaHint(args map[string]any) (string, error) {
	topic := strings.TrimSpace(args["topic"].(string))
	lowered := strings.ToLower(topic)

	for _, f := range formulas {
		for _, kw := range f.keywords {
			if strings.Contains(topic, kw) || strings.Contains(lowered, strings.ToLower(kw)) {
				text := f.text
				if grade, ok := args["grade"].(string); ok && grade == "小学" {
					text += "\n提示：小学阶段通常先用算术方法理解，不必强记公式。"
				}
				return text, nil
			}
		}
	}
	return "", fmt.Errorf("该主题不在能力范围内：%s。可用主题：求根公式、勾股定理、扇形面积、"+
		"圆的面积、三角形面积、完全平方、速度路程、密度", topic)
}

// --- unit_convert -----------------------------------------------------------

// factor 表示 1 个该单位等于多少基准单位（长度用米、质量用千克、时间用秒）。
var unitGroups = []struct {
	name  string
	units map[string]float64
}{
	{"长度", map[string]float64{"毫米": 0.001, "厘米": 0.01, "分米": 0.1, "米": 1, "千米": 1000, "公里": 1000}},
	{"质量", map[string]float64{"毫克": 1e-6, "克": 0.001, "千克": 1, "公斤": 1, "吨": 1000}},
	{"时间", map[string]float64{"秒": 1, "分钟": 60, "小时": 3600, "天": 86400}},
}

// 温度不是比例关系，单独处理。
var temperatureUnits = map[string]bool{"摄氏度": true, "华氏度": true, "开尔文": true}

func findGroup(unit string) (string, map[string]float64, bool) {
	for _, g := range unitGroups {
		if _, ok := g.units[unit]; ok {
			return g.name, g.units, true
		}
	}
	return "", nil, false
}

func runUnitConvert(args map[string]any) (string, error) {
	value := args["value"].(float64)
	from := strings.TrimSpace(args["from"].(string))
	to := strings.TrimSpace(args["to"].(string))

	// 温度：先归一到摄氏度再换出去。
	if temperatureUnits[from] || temperatureUnits[to] {
		if !temperatureUnits[from] || !temperatureUnits[to] {
			return "", fmt.Errorf("%s 与 %s 不是同一物理量，无法换算", from, to)
		}
		celsius := toCelsius(value, from)
		result := fromCelsius(celsius, to)
		return fmt.Sprintf("%s %s = %s %s（温度）",
			formatNumber(value), from, formatNumber(result), to), nil
	}

	fromGroup, fromUnits, okFrom := findGroup(from)
	toGroup, toUnits, okTo := findGroup(to)
	if !okFrom {
		return "", fmt.Errorf("未知单位：%s。可用单位：%s", from, allUnits())
	}
	if !okTo {
		return "", fmt.Errorf("未知单位：%s。可用单位：%s", to, allUnits())
	}
	if fromGroup != toGroup {
		return "", fmt.Errorf("%s 与 %s 不是同一物理量，无法换算", from, to)
	}

	result := value * fromUnits[from] / toUnits[to]
	return fmt.Sprintf("%s %s = %s %s（%s）",
		formatNumber(value), from, formatNumber(result), to, fromGroup), nil
}

func toCelsius(v float64, unit string) float64 {
	switch unit {
	case "华氏度":
		return (v - 32) * 5 / 9
	case "开尔文":
		return v - 273.15
	default:
		return v
	}
}

func fromCelsius(v float64, unit string) float64 {
	switch unit {
	case "华氏度":
		return v*9/5 + 32
	case "开尔文":
		return v + 273.15
	default:
		return v
	}
}

func formatNumber(v float64) string {
	if math.Abs(v-math.Round(v)) < 1e-9 {
		return strconv.FormatFloat(math.Round(v), 'f', -1, 64)
	}
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func allUnits() string {
	var out []string
	for _, g := range unitGroups {
		for u := range g.units {
			out = append(out, u)
		}
	}
	out = append(out, "摄氏度", "华氏度", "开尔文")
	sort.Strings(out)
	return strings.Join(out, "、")
}
