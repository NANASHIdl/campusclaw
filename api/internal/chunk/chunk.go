// Package chunk 把知识库正文切分为可检索、可追溯的切片。
//
// 三种策略作用于同一份正文：
//   - auto      最大 800 字、重叠 80 字，优先在空行/换行/句号处断开（默认）
//   - custom    最大长度 100–2000 字、重叠 0%–50%，可选文本预处理
//   - hierarchy 按 Markdown #/##/### 分章，标题保留在该章切片内
//
// 切分不改写原文：knowledge_entries.body_text 保持上传时的原样。
// 偏移量以「字符」（rune）为单位，相对于实际参与切分的文本计算。
package chunk

import (
	"fmt"
	"regexp"
	"strings"
)

// 策略标识。
const (
	StrategyAuto      = "auto"
	StrategyCustom    = "custom"
	StrategyHierarchy = "hierarchy"
)

// 默认窗口参数：与课件「约 800 字、重叠约 10%」一致。
const (
	DefaultMaxChars = 800
	DefaultOverlap  = 80

	CustomMinChars = 100
	CustomMaxChars = 2000
)

// Options 是一次切分请求的参数。
type Options struct {
	Strategy           string
	MaxChars           int
	OverlapPercent     int
	RemoveURLs         bool
	CollapseWhitespace bool
	// Separators 为自定义断点，按优先级从高到低。为空时使用默认断点。
	Separators []string
}

// Chunk 是一条切片，含文本、序号与其在待切分文本中的字符位置。
type Chunk struct {
	Index int
	Text  string
	Start int
	End   int
}

var (
	urlRe   = regexp.MustCompile(`(?:https?://|www\.)\S+`)
	emailRe = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)
	wsRe    = regexp.MustCompile(`\s+`)
)

// defaultSeparators 是默认断点，按优先级从高到低。
var defaultSeparators = []string{"\n\n", "\n", "。"}

// Normalize 校验并补全参数。
//
// auto 与 hierarchy 策略忽略请求中另行填写的长度、重叠与预处理参数；
// custom 策略对长度与重叠比例做范围校验。
func Normalize(opt Options) (Options, error) {
	switch opt.Strategy {
	case "", StrategyAuto:
		opt.Strategy = StrategyAuto
		opt.MaxChars = DefaultMaxChars
		opt.OverlapPercent = 10
		opt.RemoveURLs = false
		opt.CollapseWhitespace = false
		opt.Separators = nil
	case StrategyCustom:
		if opt.MaxChars == 0 {
			opt.MaxChars = DefaultMaxChars
		}
		if opt.MaxChars < CustomMinChars || opt.MaxChars > CustomMaxChars {
			return opt, fmt.Errorf("最大长度须在 %d 至 %d 之间", CustomMinChars, CustomMaxChars)
		}
		if opt.OverlapPercent < 0 || opt.OverlapPercent > 50 {
			return opt, fmt.Errorf("重叠比例须在 0%% 至 50%% 之间")
		}
	case StrategyHierarchy:
		opt.MaxChars = DefaultMaxChars
		opt.OverlapPercent = 10
		opt.RemoveURLs = false
		opt.CollapseWhitespace = false
		opt.Separators = nil
	default:
		return opt, fmt.Errorf("未知切分策略 %q", opt.Strategy)
	}
	return opt, nil
}

// Split 按策略把正文切为切片。
func Split(body string, opt Options) ([]Chunk, error) {
	opt, err := Normalize(opt)
	if err != nil {
		return nil, err
	}

	working := body
	if opt.Strategy == StrategyCustom {
		working = preprocess(body, opt)
	}
	if strings.TrimSpace(working) == "" {
		return nil, nil
	}

	if opt.Strategy == StrategyHierarchy {
		return splitHierarchy(working, opt), nil
	}
	return splitWindow(working, opt), nil
}

// preprocess 仅作用于待切分文本，不改变入库原文。
func preprocess(text string, opt Options) string {
	if opt.RemoveURLs {
		text = urlRe.ReplaceAllString(text, "")
		text = emailRe.ReplaceAllString(text, "")
	}
	if opt.CollapseWhitespace {
		text = wsRe.ReplaceAllString(text, " ")
	}
	return text
}

// splitWindow 按窗口切分，尽量在断点处断开。
func splitWindow(text string, opt Options) []Chunk {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return nil
	}

	maxChars := opt.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultMaxChars
	}
	overlap := maxChars * opt.OverlapPercent / 100
	breaks := opt.Separators
	if len(breaks) == 0 {
		breaks = defaultSeparators
	}
	bps := make([][]rune, 0, len(breaks))
	for _, b := range breaks {
		if b != "" {
			bps = append(bps, []rune(b))
		}
	}

	var out []Chunk
	start := 0
	for start < n {
		end := start + maxChars
		if end >= n {
			end = n
		} else {
			// 只在窗口后半段寻找断点，避免产生过短的切片。
			cut := findBreak(runes, start+maxChars/2, end, bps)
			if cut > start {
				end = cut
			}
		}

		out = append(out, Chunk{Index: len(out), Text: string(runes[start:end]), Start: start, End: end})
		if end >= n {
			break
		}
		next := end - overlap
		if next <= start {
			// 重叠不小于步长时强制前进，避免死循环。
			next = end
		}
		start = next
	}
	return out
}

// findBreak 在 [lo, hi) 内从后向前寻找断点，返回断点之后的字符下标。
func findBreak(runes []rune, lo, hi int, bps [][]rune) int {
	if lo < 0 {
		lo = 0
	}
	for i := hi - 1; i >= lo; i-- {
		for _, bp := range bps {
			if i+len(bp) <= hi && runesEqual(runes[i:i+len(bp)], bp) {
				return i + len(bp)
			}
		}
	}
	return hi
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitHierarchy 按 Markdown 标题层级分章；某章过长时再按窗口切分。
func splitHierarchy(text string, opt Options) []Chunk {
	runes := []rune(text)
	n := len(runes)
	if n == 0 {
		return nil
	}

	bounds := headingStarts(runes)
	if len(bounds) == 0 {
		// 没有标题时退化为自动窗口，保证任何正文都能被切分。
		return splitWindow(text, opt)
	}

	type span struct{ start, end int }
	sections := make([]span, 0, len(bounds)+1)
	if strings.TrimSpace(string(runes[:bounds[0]])) != "" {
		sections = append(sections, span{0, bounds[0]})
	}
	for k, b := range bounds {
		end := n
		if k+1 < len(bounds) {
			end = bounds[k+1]
		}
		sections = append(sections, span{b, end})
	}

	var out []Chunk
	for _, s := range sections {
		seg := runes[s.start:s.end]
		if strings.TrimSpace(string(seg)) == "" {
			continue
		}
		if len(seg) <= opt.MaxChars {
			out = append(out, Chunk{Index: len(out), Text: string(seg), Start: s.start, End: s.end})
			continue
		}
		for _, sub := range splitWindow(string(seg), opt) {
			out = append(out, Chunk{
				Index: len(out),
				Text:  sub.Text,
				Start: s.start + sub.Start,
				End:   s.start + sub.End,
			})
		}
	}
	return out
}

// headingStarts 返回各行首为 #/##/### 的行起始字符下标。
func headingStarts(runes []rune) []int {
	var starts []int
	for i := 0; i < len(runes); i++ {
		if i != 0 && runes[i-1] != '\n' {
			continue
		}
		j := i
		hashes := 0
		for j < len(runes) && runes[j] == '#' && hashes < 4 {
			hashes++
			j++
		}
		if hashes >= 1 && hashes <= 3 && j < len(runes) && runes[j] == ' ' {
			starts = append(starts, i)
		}
	}
	return starts
}
