package llm

import (
	"encoding/base64"
	"fmt"

	"github.com/liushuangls/go-anthropic/v2"
)

// toAnthropicMessage traduz uma Message canonica em anthropic.Message.
// ContentBlocks viram MessageContent — text, tool_use, tool_result, image.
func toAnthropicMessage(m Message) (anthropic.Message, error) {
	role := anthropic.RoleUser
	if m.Role == RoleAssistant {
		role = anthropic.RoleAssistant
	}
	contents := make([]anthropic.MessageContent, 0, len(m.Content))
	for _, b := range m.Content {
		switch b.Type {
		case "text":
			contents = append(contents, anthropic.NewTextMessageContent(b.Text))
		case "tool_use":
			contents = append(contents, anthropic.NewToolUseMessageContent(b.ToolUseID, b.ToolName, b.ToolInput))
		case "tool_result":
			contents = append(contents, anthropic.NewToolResultMessageContent(b.ToolUseID, b.ToolResult, b.IsError))
		case "image":
			// b.ImageData ja vem em base64 raw (sem prefixo data:).
			// Validacao defensiva: se vier bytes brutos por engano, encoda.
			data := b.ImageData
			if !looksLikeBase64(data) {
				data = base64.StdEncoding.EncodeToString([]byte(data))
			}
			src := anthropic.MessageContentSource{
				Type:      anthropic.MessagesContentSourceTypeBase64,
				MediaType: b.ImageMedia,
				Data:      data,
			}
			contents = append(contents, anthropic.NewImageMessageContent(src))
		default:
			return anthropic.Message{}, fmt.Errorf("unsupported content block type %q", b.Type)
		}
	}
	return anthropic.Message{Role: role, Content: contents}, nil
}

// fromAnthropicContent traduz uma slice de MessageContent (resposta) em
// ContentBlocks canonicos. Ignora tipos que nao sao text/tool_use (ex:
// thinking) — caller nao precisa deles na camada conversacional.
func fromAnthropicContent(in []anthropic.MessageContent) []ContentBlock {
	out := make([]ContentBlock, 0, len(in))
	for _, c := range in {
		switch c.Type {
		case anthropic.MessagesContentTypeText:
			text := ""
			if c.Text != nil {
				text = *c.Text
			}
			out = append(out, ContentBlock{Type: "text", Text: text})
		case anthropic.MessagesContentTypeToolUse:
			if c.MessageContentToolUse == nil {
				continue
			}
			out = append(out, ContentBlock{
				Type:      "tool_use",
				ToolUseID: c.MessageContentToolUse.ID,
				ToolName:  c.MessageContentToolUse.Name,
				ToolInput: c.MessageContentToolUse.Input,
			})
			// Outros tipos (thinking, redacted_thinking, citations*) sao ignorados
			// pra simplicidade — nao precisamos deles na ponte.
		}
	}
	return out
}

// mapAnthropicStop normaliza o stop reason.
func mapAnthropicStop(s anthropic.MessagesStopReason) StopReason {
	switch s {
	case anthropic.MessagesStopReasonEndTurn:
		return StopEndTurn
	case anthropic.MessagesStopReasonToolUse:
		return StopToolUse
	case anthropic.MessagesStopReasonMaxTokens:
		return StopMaxTokens
	default:
		// stop_sequence + qualquer outro nao mapeado vira end_turn — mais seguro
		// pro runLoop tratar como "modelo terminou".
		return StopEndTurn
	}
}

// looksLikeBase64 retorna true se s parecer base64. Usado pra evitar
// dupla-encodagem em casos onde o caller passou bytes brutos por engano.
// Heuristica simples: tudo na faixa A-Z, a-z, 0-9, +/= e tamanho razoavel.
func looksLikeBase64(s string) bool {
	if len(s) < 4 {
		return false
	}
	// Limit check pra evitar varrer string gigante toda — primeiros 256 chars
	// bastam pra heuristica.
	limit := len(s)
	if limit > 256 {
		limit = 256
	}
	for i := 0; i < limit; i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z':
		case c >= 'a' && c <= 'z':
		case c >= '0' && c <= '9':
		case c == '+' || c == '/' || c == '=' || c == '\n' || c == '\r':
		default:
			return false
		}
	}
	return true
}
