package mcp

import (
	"fmt"
	"regexp"
	"strings"
)

// 引用格式：工具名@服务器名。
//
//   - 工具名段：小写字母开头，只含小写字母、数字与下划线（不允许连字符）；
//   - 服务器名段：小写字母或数字开头，可含连字符（服务器名由人命名，需要可读）；
//   - 两段之间恰好一个 @。
var (
	refPattern      = regexp.MustCompile(`^([a-z][a-z0-9_]*)@([a-z0-9][a-z0-9-]*)$`)
	toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

// Ref 是一条工具引用。
type Ref struct {
	Tool   string
	Server string
}

func (r Ref) String() string { return r.Tool + "@" + r.Server }

// ParseRef 解析并校验引用格式；不合法时返回可读原因（带可用写法）。
func ParseRef(raw string) (Ref, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Ref{}, fmt.Errorf("工具项不能为空，可用写法：knowledge_search 或 工具名@服务器名")
	}
	if !strings.Contains(s, "@") {
		return Ref{}, fmt.Errorf("「%s」缺少服务器名，可用写法：工具名@服务器名（如 formula_hint@campus-tools）", s)
	}
	m := refPattern.FindStringSubmatch(s)
	if m == nil {
		return Ref{}, fmt.Errorf("「%s」不符合引用格式：工具名需以小写字母开头、只含小写字母/数字/下划线，"+
			"服务器名可含连字符（如 formula_hint@campus-tools）", s)
	}
	return Ref{Tool: m[1], Server: m[2]}, nil
}

// IsBuiltin 判断是否为本项目内置工具（不需要服务器）。
func IsBuiltin(name string) bool { return name == BuiltinKnowledgeSearch }

// BuiltinKnowledgeSearch 是内置的知识库检索工具，由服务端固定执行。
const BuiltinKnowledgeSearch = "knowledge_search"

// ValidToolName 判断服务器上的工具名是否合法（发现结果也要过一遍，防止远端给怪名字）。
func ValidToolName(name string) bool { return toolNamePattern.MatchString(name) }
