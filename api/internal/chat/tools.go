package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"regexp"
	"strings"

	"campusclaw/api/internal/mcp"
	"campusclaw/api/internal/qa"
	"campusclaw/api/internal/skill"
)

// 工具结果的长度上限：过长的返回只会挤占上下文。
const MaxToolResultRunes = 1200

// 指令式句式表。命中即整条丢弃——只降低概率，真正拦动作的是授权与配额。
var instructionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`忽略(之前|以上|前面)的?(指令|要求|提示)`),
	regexp.MustCompile(`ignore\s+(all\s+)?(previous|above)`),
	regexp.MustCompile(`(?m)^\s*(system|assistant)\s*:`),
	regexp.MustCompile(`(请|务必|必须)(先)?(调用|执行|运行)`),
	regexp.MustCompile(`(请|务必|必须)(先)?(读取|打开|访问)`),
	regexp.MustCompile(`把.{0,20}(发送|上传|提交)到`),
}

// toolCandidates 构造本次提问的候选工具集。
//
// 候选集只来自命中技能的引用：技能没命中、或只引用内置检索时，返回空集——
// 模型看不到任何工具，行为与第 5 课完全一致。
func (s *Service) toolCandidates(classID int64, sk skill.Skill,
	hasSkill bool) ([]qa.ToolSpec, map[string]mcp.Ref) {

	if !hasSkill || s.MCP == nil {
		return nil, nil
	}
	specs := make([]qa.ToolSpec, 0, len(sk.Tools))
	refs := map[string]mcp.Ref{}

	for _, item := range sk.Tools {
		if mcp.IsBuiltin(item) {
			continue
		}
		ref, err := mcp.ParseRef(item)
		if err != nil {
			continue
		}
		if _, dup := refs[ref.Tool]; dup {
			log.Printf("chat: 候选集中工具名重复，忽略 %s", ref)
			continue
		}

		spec := qa.ToolSpec{Name: ref.Tool}
		if server, err := s.MCP.FindServerByName(classID, ref.Server); err == nil {
			if tool, found, _ := s.MCP.FindTool(server.ID, ref.Tool); found {
				spec.Description = tool.Description
				spec.Schema = tool.InputSchema
			}
		}
		if spec.Description == "" {
			// 服务器未连接或工具不在清单：仍然声明出来，
			// 让这次调用走到「被拒」并被记录，而不是从候选集里静默消失。
			spec.Description = fmt.Sprintf("工具 %s（服务器 %s 当前不可用）", ref.Tool, ref.Server)
			spec.Schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		refs[ref.Tool] = ref
		specs = append(specs, spec)
	}
	return specs, refs
}

func (s *Service) maxRounds() int {
	if s.MaxRounds > 0 {
		return s.MaxRounds
	}
	return DefaultMaxRounds
}

func (s *Service) maxCalls() int {
	if s.MaxCalls > 0 {
		return s.MaxCalls
	}
	return DefaultMaxCalls
}

// toolRun 是一次工具调用的结果，用于 SSE 的 meta 与调用记录。
type toolRun struct {
	Ref    string
	Status string
	Text   string
}

// sanitizeToolResult 清洗工具返回：命中句式整条丢弃，过长截断，并加上来源标注。
func sanitizeToolResult(ref, text string) (string, bool) {
	lowered := strings.ToLower(text)
	for _, re := range instructionPatterns {
		if re.MatchString(lowered) {
			return "（工具返回的内容包含疑似指令，已整条丢弃）", true
		}
	}
	runes := []rune(text)
	if len(runes) > MaxToolResultRunes {
		text = string(runes[:MaxToolResultRunes]) + "…"
	}
	return "[工具 " + ref + " 返回的数据]\n" + text, false
}

// validateToolArgs 按工具自己的 inputSchema 校验模型填的参数。
//
// 同一份结构被用两次：模型据此填写，宿主据此校验——这正是「声明必须来自发现结果」的原因。
func validateToolArgs(schema json.RawMessage, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		raw = "{}"
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return fmt.Errorf("参数不是 JSON 对象")
	}

	var spec struct {
		Properties map[string]struct {
			Type string `json:"type"`
			Enum []any  `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if len(schema) > 0 {
		_ = json.Unmarshal(schema, &spec)
	}

	for _, key := range spec.Required {
		if _, ok := args[key]; !ok {
			return fmt.Errorf("缺少必填参数 %s", key)
		}
	}

	for key, value := range args {
		prop, known := spec.Properties[key]
		if !known {
			continue
		}
		switch prop.Type {
		case "string":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("参数 %s 应为字符串", key)
			}
		case "number":
			if _, ok := value.(float64); !ok {
				return fmt.Errorf("参数 %s 应为数字", key)
			}
		case "integer":
			f, ok := value.(float64)
			if !ok || f != math.Trunc(f) {
				return fmt.Errorf("参数 %s 应为整数", key)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("参数 %s 应为布尔值", key)
			}
		}
		if len(prop.Enum) > 0 {
			matched := false
			for _, e := range prop.Enum {
				if fmt.Sprint(e) == fmt.Sprint(value) {
					matched = true
					break
				}
			}
			if !matched {
				return fmt.Errorf("参数 %s 只能是 %v", key, prop.Enum)
			}
		}
	}
	return nil
}

// answerWithTools 跑调用循环：决策归模型，执行与收尾归宿主。
//
// 返回最终正文、本次所有调用（供 meta 与记录用）与错误。
// 文本通过 emit 回调送出；是否立刻转发由调用方（闸门）决定。
func (s *Service) answerWithTools(ctx context.Context, p *Prepared,
	emit func(string) error) (string, []toolRun, error) {

	// 对话实现不支持函数调用时，回落到第 5 课的路径，工具链路整体关闭。
	tp, supportsTools := s.Chat.(qa.ToolProvider)
	if !supportsTools {
		full, err := s.Chat.AnswerPrompt(ctx, qa.Prompt{
			System:        p.SystemPrompt(),
			UserText:      p.UserMessage(),
			History:       p.History,
			WithCitations: len(p.Hits) > 0,
		}, emit)
		return full, nil, err
	}

	var (
		runs      []toolRun
		exchanges []qa.ToolExchange
		calls     int
	)

	for round := 1; ; round++ {
		res, err := tp.AnswerWithTools(ctx, qa.Prompt{
			System:        p.SystemPrompt(),
			UserText:      p.UserMessage(),
			History:       p.History,
			WithCitations: len(p.Hits) > 0,
		}, p.ToolSpecs, exchanges, emit)
		if err != nil {
			return res.Content, runs, err
		}
		if len(res.ToolCalls) == 0 {
			return res.Content, runs, nil
		}

		// 上限：轮次管「一条链能走多深」，累计次数管「一次提问总共能做多少动作」。
		overRounds := round >= s.maxRounds()
		overCalls := calls+len(res.ToolCalls) > s.maxCalls()
		if overRounds || overCalls {
			log.Printf("chat: 工具调用达到上限（round=%d calls=%d/%d），转为基于已有信息作答",
				round, calls, s.maxCalls())
			exchanges = append(exchanges, qa.ToolExchange{
				Calls:   res.ToolCalls,
				Results: notExecuted(res.ToolCalls),
			})
			// 再发一次**不带 tools** 的请求：模型只能用已有信息作答。
			final, ferr := tp.AnswerWithTools(ctx, qa.Prompt{
				System:        p.SystemPrompt(),
				UserText:      p.UserMessage(),
				History:       p.History,
				WithCitations: len(p.Hits) > 0,
			}, nil, exchanges, emit)
			if ferr != nil {
				return final.Content, runs, ferr
			}
			return final.Content, runs, nil
		}

		exchange := qa.ToolExchange{Calls: res.ToolCalls}
		for _, call := range res.ToolCalls {
			content, run := s.runTool(ctx, p, round, call)
			runs = append(runs, run)
			exchange.Results = append(exchange.Results, qa.ToolResult{
				CallID: call.ID, Name: call.Name, Content: content,
			})
			calls++
		}
		exchanges = append(exchanges, exchange)
	}
}

// notExecuted 给「因上限未执行」的调用补上工具消息，保证消息序列完整。
func notExecuted(calls []qa.ToolCall) []qa.ToolResult {
	out := make([]qa.ToolResult, 0, len(calls))
	for _, c := range calls {
		out = append(out, qa.ToolResult{
			CallID:  c.ID,
			Name:    c.Name,
			Content: "（已达本次提问的调用上限，本次未执行；请基于已有信息作答）",
		})
	}
	return out
}

// runTool 执行一次调用：解析引用 → 判断可用 → 校验参数 → 带令牌执行 → 清洗。
//
// 返回要回传给模型的工具消息正文，以及供 meta 与记录使用的 run。
func (s *Service) runTool(ctx context.Context, p *Prepared, round int,
	call qa.ToolCall) (string, toolRun) {

	ref, ok := p.ToolRefs[call.Name]
	if !ok {
		// 模型提出了候选集之外的调用：不执行，也无可归属的服务器。
		return "（该工具不在本次候选集内，未执行）",
			toolRun{Ref: call.Name, Status: mcp.StatusDenied, Text: "不在候选集内"}
	}
	refStr := ref.String()

	server, err := s.MCP.FindServerByName(p.Conv.ClassID, ref.Server)
	if err != nil {
		// 服务器被改名或删除：无法归属，不写记录。
		log.Printf("chat: 工具引用 %s 无法归属（服务器不存在），不写记录", refStr)
		return "（该工具所在的服务器已不存在，未执行）",
			toolRun{Ref: refStr, Status: mcp.StatusDenied, Text: "服务器已不存在"}
	}
	if !server.Connected {
		s.recordCall(p, server, ref, round, mcp.StatusDenied, "服务器未连接")
		return "（该工具所在的服务器当前未连接，未执行）",
			toolRun{Ref: refStr, Status: mcp.StatusDenied, Text: "服务器未连接"}
	}
	tool, found, err := s.MCP.FindTool(server.ID, ref.Tool)
	if err != nil || !found {
		s.recordCall(p, server, ref, round, mcp.StatusDenied, "工具不在发现清单内")
		return "（该工具不在服务器的发现清单内，未执行）",
			toolRun{Ref: refStr, Status: mcp.StatusDenied, Text: "工具不在发现清单内"}
	}

	if err := validateToolArgs(tool.InputSchema, call.Arguments); err != nil {
		reason := "参数不合法：" + err.Error()
		s.recordCall(p, server, ref, round, mcp.StatusError, reason)
		return "（" + reason + "，请调整参数后重试）",
			toolRun{Ref: refStr, Status: mcp.StatusError, Text: reason}
	}

	token, err := s.MCP.ServerToken(p.Conv.ClassID, server.ID)
	if err != nil {
		s.recordCall(p, server, ref, round, mcp.StatusError, "读取令牌失败")
		return "（读取服务器令牌失败，未执行）",
			toolRun{Ref: refStr, Status: mcp.StatusError, Text: "读取令牌失败"}
	}

	text, isError, err := s.MCPClient.CallTool(ctx, server.BaseURL, token, ref.Tool,
		json.RawMessage(call.Arguments))
	if err != nil {
		reason := "调用失败：" + err.Error()
		s.recordCall(p, server, ref, round, mcp.StatusError, reason)
		return "（" + reason + "）",
			toolRun{Ref: refStr, Status: mcp.StatusError, Text: reason}
	}
	if isError {
		s.recordCall(p, server, ref, round, mcp.StatusError, mcp.CallSummary(text, 200))
		return "（工具报错：" + text + "）",
			toolRun{Ref: refStr, Status: mcp.StatusError, Text: mcp.CallSummary(text, 200)}
	}

	s.recordCall(p, server, ref, round, mcp.StatusOK, mcp.CallSummary(text, 200))
	content, dropped := sanitizeToolResult(refStr, text)
	summary := mcp.CallSummary(text, 200)
	if dropped {
		summary = "返回内容含疑似指令，已整条丢弃"
	}
	return content, toolRun{Ref: refStr, Status: mcp.StatusOK, Text: summary}
}

// recordCall 写一条调用记录。只增不改；写失败只记日志，不影响回答。
func (s *Service) recordCall(p *Prepared, server mcp.Server, ref mcp.Ref, round int,
	status, detail string) {

	rec := mcp.CallRecord{
		ClassID:        p.Conv.ClassID,
		ServerID:       server.ID,
		ServerName:     server.Name,
		ToolName:       ref.Tool,
		ToolRef:        ref.String(),
		Status:         status,
		UserID:         p.Conv.UserID,
		ConversationID: p.Conv.ID,
		RoundNo:        round,
		Detail:         detail,
	}
	if err := s.MCP.InsertCall(rec); err != nil {
		log.Printf("chat: 写入工具调用记录失败 ref=%s: %v", rec.ToolRef, err)
		return
	}
	log.Printf("mcp call: ref=%s status=%s user=%d conv=%d round=%d",
		rec.ToolRef, status, rec.UserID, rec.ConversationID, round)
}
