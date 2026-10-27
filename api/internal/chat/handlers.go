package chat

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"campusclaw/api/internal/auth"
	"campusclaw/api/internal/httpx"
	"campusclaw/api/internal/qa"
)

// Handler 暴露会话内的流式提问接口。
type Handler struct{ Service *Service }

func NewHandler(s *Service) *Handler { return &Handler{Service: s} }

// SendRequest 是提问请求体，供 OpenAPI 文档引用。
type SendRequest struct {
	Content string `json:"content"`
	// AgentID 仅为兼容而接收，实际不参与选择：智能体由会话锁定。
	AgentID int64 `json:"agent_id"`
}

// SendMessage 处理 POST /api/conversations/{id}/messages，以 SSE 返回。
//
// @Summary      在会话中继续提问（SSE）
// @Description  以 text/event-stream 逐段返回。事件依次为 meta（状态、命中技能、引用列表）、若干 delta（本次新增文本）、done（助手消息编号）；流开始后失败再推一条 error，HTTP 状态保持 200。content 去空白后 2–500 字符。
// @Tags         conversations
// @Accept       json
// @Produce      text/event-stream
// @Security     bearerAuth
// @Param        id   path int                 true "会话编号"
// @Param        body body chat.SendRequest    true "提问内容"
// @Success      200 {string} string "SSE 事件流"
// @Failure      400 {object} httpx.ErrorBody
// @Failure      401 {object} httpx.ErrorBody
// @Failure      404 {object} httpx.ErrorBody
// @Failure      503 {object} httpx.ErrorBody
// @Failure      500 {object} httpx.ErrorBody
// @Router       /api/conversations/{id}/messages [post]
// @ID           CNV-004
func (h *Handler) SendMessage(w http.ResponseWriter, r *http.Request) {
	user, ok := auth.UserFromContext(r.Context())
	if !ok {
		httpx.Error(w, http.StatusUnauthorized, httpx.CodeUnauthorized)
		return
	}
	convID, ok := pathID(r)
	if !ok {
		httpx.NotFound(w)
		return
	}

	var req SendRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}

	// 前七步：任一失败都在开始流之前返回真正的状态码。
	p, err := h.Service.Prepare(r.Context(), user.ID, user.ClassID, convID, req.Content)
	if err != nil {
		switch {
		case errors.Is(err, ErrContentTooShort), errors.Is(err, ErrContentTooLong):
			httpx.JSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, ErrConversationNotFound), errors.Is(err, ErrAgentNotFound):
			log.Printf("chat: reason=%v conv=%d user=%s", err, convID, user.Username)
			httpx.NotFound(w)
		case errors.Is(err, ErrDependency):
			log.Printf("chat: reason=dependency conv=%d err=%v", convID, err)
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "service_unavailable"})
		default:
			log.Printf("chat: prepare failed conv=%d: %v", convID, err)
			httpx.Error(w, http.StatusInternalServerError, "internal_error")
		}
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		log.Printf("chat: ResponseWriter 不支持 Flush")
		httpx.Error(w, http.StatusInternalServerError, "internal_error")
		return
	}
	sse := &sseWriter{w: w, f: flusher}

	var skillInfo any
	if p.HasSkill {
		skillInfo = map[string]any{"id": p.Skill.ID, "name": p.Skill.Name}
	}

	// meta 里 tool_calls 只在本次确有调用时出现——与引用列表同一口径，有则带上、没有则省去。
	metaPayload := func(runs []toolRun) map[string]any {
		payload := map[string]any{
			"status":    p.Status(),
			"skill":     skillInfo,
			"citations": p.Hits,
		}
		if len(runs) > 0 {
			calls := make([]map[string]any, 0, len(runs))
			for _, run := range runs {
				calls = append(calls, map[string]any{
					"ref": run.Ref, "status": run.Status, "text": run.Text,
				})
			}
			payload["tool_calls"] = calls
		}
		return payload
	}

	// 闸门：没有候选工具时一开始就打开（第 5 课行为不变）；
	// 有候选工具时先跑循环，等这一轮确定为最终回答，再发 meta 并冲刷缓冲。
	gateOpen := len(p.ToolSpecs) == 0
	var buffered []string

	emit := func(text string) error {
		if !gateOpen {
			buffered = append(buffered, text)
			return nil
		}
		return sse.send("delta", map[string]string{"text": text})
	}

	openGate := func(runs []toolRun) error {
		if gateOpen {
			return nil
		}
		if err := sse.send("meta", metaPayload(runs)); err != nil {
			return err
		}
		gateOpen = true
		pending := buffered
		buffered = nil
		for _, text := range pending {
			if err := sse.send("delta", map[string]string{"text": text}); err != nil {
				return err
			}
		}
		return nil
	}

	if gateOpen {
		if err := sse.send("meta", metaPayload(nil)); err != nil {
			log.Printf("chat: 推送 meta 失败: %v", err)
			return
		}
	}

	// ⑨⑩ 组消息并逐段生成；有候选工具时走调用循环。
	var (
		full   string
		runs   []toolRun
		genErr error
	)
	if len(p.ToolSpecs) == 0 {
		full, genErr = h.Service.Chat.AnswerPrompt(r.Context(), qa.Prompt{
			System:        p.SystemPrompt(),
			UserText:      p.UserMessage(),
			History:       p.History,
			WithCitations: len(p.Hits) > 0,
		}, emit)
	} else {
		full, runs, genErr = h.Service.answerWithTools(r.Context(), p, emit)
		if err := openGate(runs); err != nil {
			log.Printf("chat: 推送 meta 失败: %v", err)
			return
		}
	}

	skillName := ""
	if p.HasSkill {
		skillName = p.Skill.Name
	}

	if genErr != nil {
		log.Printf("chat: 生成失败 conv=%d: %v", convID, genErr)
		// 已产出的片段照常落库，避免整轮丢失。
		if full != "" {
			if _, err := h.Service.Convs.AppendAssistant(p.Conv.ID, full, p.Hits, skillName); err != nil {
				log.Printf("chat: 保存部分回答失败 conv=%d: %v", convID, err)
			}
		}
		_ = sse.send("error", map[string]string{"error": "生成失败，请稍后重试"})
		return
	}

	// ⑪ 助手消息落库后推 done。
	msgID, err := h.Service.Convs.AppendAssistant(p.Conv.ID, full, p.Hits, skillName)
	if err != nil {
		log.Printf("chat: 保存回答失败 conv=%d: %v", convID, err)
		_ = sse.send("error", map[string]string{"error": "保存回答失败"})
		return
	}
	if err := sse.send("done", map[string]any{"message_id": msgID}); err != nil {
		log.Printf("chat: 推送 done 失败: %v", err)
	}
	log.Printf("chat ok: conv=%d user=%s skill=%q hits=%d len=%d", convID, user.Username, skillName, len(p.Hits), len([]rune(full)))
}

// sseWriter 负责 SSE 的帧格式、响应头与逐帧 flush。
//
// 响应头在第一个事件产生时才写出，因此在此之前仍可返回 404/503。
type sseWriter struct {
	w       http.ResponseWriter
	f       http.Flusher
	started bool
}

func (s *sseWriter) send(event string, payload any) error {
	if !s.started {
		h := s.w.Header()
		h.Set("Content-Type", "text/event-stream")
		h.Set("Cache-Control", "no-cache")
		h.Set("Connection", "keep-alive")
		h.Set("X-Accel-Buffering", "no")
		s.w.WriteHeader(http.StatusOK)
		s.started = true
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, raw); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
