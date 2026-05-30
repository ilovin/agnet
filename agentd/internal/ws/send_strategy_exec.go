package ws

import (
	"fmt"
	"time"

	"github.com/phone-talk/agentd/internal/agent"
	"github.com/phone-talk/agentd/internal/provider"
)

type sendContext struct {
	req        RPCRequest
	agentID    string
	message    string
	raw        bool
	imageFiles []string
	imagePaths []string
	ag         *agent.Agent
	caps       provider.Capabilities
}

func (h *handler) handleSendTmux(ctx sendContext) RPCResponse {
	if ctx.caps.RequiresTmuxForegroundValidation {
		tmuxTarget := ctx.ag.TmuxTarget()
		if err := agent.ValidateNonShellPane(tmuxTarget); err != nil {
			ctx.ag.SetStatus(agent.StatusStopped)
			return errResp(ctx.req.ID, -32000, "attached CLI not running in tmux pane: "+err.Error())
		}
	}
	if len(ctx.imageFiles) > 0 && !ctx.caps.SupportsImageAttachment {
		return errResp(ctx.req.ID, -32000, fmt.Sprintf("%s sessions do not support image attachments", ctx.ag.Provider))
	}

	eventData := map[string]any{
		"role":       "user",
		"text":       ctx.message,
		"raw":        false,
		"imageCount": len(ctx.imageFiles),
	}
	if len(ctx.imagePaths) > 0 {
		eventData["images"] = ctx.imagePaths
	}
	seq, err := h.server.manager.RecordConversationEvent(ctx.agentID, eventData)
	if err != nil {
		return errResp(ctx.req.ID, -32000, "record user message: "+err.Error())
	}
	h.server.broadcast(event("conversation.message", h.buildUserMessageBroadcast(ctx.ag, ctx.message, ctx.imagePaths, len(ctx.imageFiles), seq)), nil)

	promptText := ctx.message
	if ctx.ag.Provider != "hermes" && len(ctx.imagePaths) > 0 {
		promptText = formatTmuxMessageWithImages(ctx.message, ctx.imagePaths)
	}
	input := promptText
	if !ctx.raw {
		input = promptText + "\n"
	}
	if ctx.ag.Provider == "hermes" {
		ctx.ag.BeginSend()
	}
	err = ctx.ag.WriteInput(input)
	if ctx.ag.Provider == "hermes" {
		ctx.ag.EndSend()
	}
	if err != nil {
		return errResp(ctx.req.ID, -32000, "write to tmux agent: "+err.Error())
	}
	return okResp(ctx.req.ID, map[string]any{"id": ctx.agentID})
}

func (h *handler) handleSendPTY(ctx sendContext) RPCResponse {
	ptyEventData := map[string]any{
		"role":       "user",
		"text":       ctx.message,
		"raw":        false,
		"imageCount": len(ctx.imageFiles),
	}
	if len(ctx.imagePaths) > 0 {
		ptyEventData["images"] = ctx.imagePaths
	}
	ptySeq, err := h.server.manager.RecordConversationEvent(ctx.agentID, ptyEventData)
	if err != nil {
		return errResp(ctx.req.ID, -32000, "record user message: "+err.Error())
	}
	h.server.broadcast(event("conversation.message", h.buildUserMessageBroadcast(ctx.ag, ctx.message, ctx.imagePaths, len(ctx.imageFiles), ptySeq)), nil)

	input := ctx.message
	if !ctx.raw {
		input = ctx.message + "\n"
	}
	if ctx.ag.PermissionPromptActive() {
		if err := ctx.ag.WriteInput("\t\r\r"); err != nil {
			return errResp(ctx.req.ID, -32000, "resolve permission prompt: "+err.Error())
		}
		ctx.ag.SetPermissionPromptActive(false)
		time.Sleep(120 * time.Millisecond)
	}
	if err := ctx.ag.WriteInput(input); err != nil {
		msg := err.Error()
		if ctx.ag.AttachReadOnly() && ctx.ag.AttachReadOnlyReason() != "" {
			msg = "write to agent: attached session is read-only: " + ctx.ag.AttachReadOnlyReason()
		} else {
			msg = "write to agent: " + msg
		}
		return errResp(ctx.req.ID, -32000, msg)
	}
	for i := 0; i < 8; i++ {
		time.Sleep(120 * time.Millisecond)
		if !ctx.ag.PermissionPromptActive() {
			continue
		}
		if err := ctx.ag.WriteInput("\t\r\r"); err != nil {
			return errResp(ctx.req.ID, -32000, "resolve permission prompt(after send): "+err.Error())
		}
		ctx.ag.SetPermissionPromptActive(false)
		time.Sleep(120 * time.Millisecond)
		if err := ctx.ag.WriteInput(input); err != nil {
			return errResp(ctx.req.ID, -32000, "re-send after permission prompt: "+err.Error())
		}
		break
	}
	return okResp(ctx.req.ID, map[string]any{"ok": true})
}
