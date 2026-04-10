# Refatoração: Agent com Tool Use

**Data:** 2026-04-10
**Status:** Aprovado
**Escopo:** Substituir intent extraction por Claude Agent com tool use nativo

## Objetivo

Transformar o bot de "parser de intenções" em um agente inteligente que:
- Decide sozinho quando consultar a agenda, criar eventos, etc.
- Suporta multi-step reasoning (consulta → decide → age)
- Responde de forma conversacional e natural
- Tem arquitetura plugável para adicionar skills futuras

## Arquitetura

### Agent Loop

```
User msg + conversation history → Claude API (system prompt + tools)
    │
    ├── text response → envia ao usuário, salva no histórico
    │
    └── tool_use → executa tool handler → envia tool_result → Claude continua
                                                │
                                                ├── text response → envia
                                                └── tool_use → (repete, max 5 iterações)
```

### Tools (V1 — Agenda)

| Tool | Descrição | Quando Claude usa |
|---|---|---|
| `buscar_agenda` | Lista eventos num período | "como está minha agenda amanhã?" |
| `criar_evento` | Cria evento na agenda | "marca reunião às 15h" |
| `editar_evento` | Edita evento existente | "muda a reunião pra 16h" |
| `cancelar_evento` | Remove evento | "cancela a reunião" |
| `buscar_historico` | Busca mensagens antigas | "o que eu pedi ontem?" |
| `criar_evento_outro_usuario` | Cria na agenda de outro user | "marca na agenda do Andre" |

### System Prompt

```
Voce e o assistente pessoal de {nome} via WhatsApp. Seja conciso e amigavel.

Voce tem ferramentas para gerenciar a agenda. Use-as livremente:
- Consulte a agenda ANTES de responder perguntas sobre compromissos
- Ao criar evento com informacoes claras, crie DIRETO e avise (nao peca confirmacao)
- So peca confirmacao quando houver ambiguidade, conflito de horario, ou acao destrutiva (cancelar/editar)
- Para agendar na agenda de outro usuario, verifique permissao primeiro
- Responda em portugues, informal mas profissional

Data/hora atual: {now}
```

### Regras de Criação de Eventos

| Situação | Comportamento |
|---|---|
| Info clara ("reunião amanhã 15h") | Cria direto, avisa |
| Info ambígua ("marca algo semana que vem") | Pergunta detalhes |
| Conflito de horário | Avisa e pergunta se mantém |
| Cancelar/editar | Confirma antes |
| Agenda de outro user sem permissão | Pede autorização conversacional |

## Mudanças nos Arquivos

### Novos
- `bot/agent.go` — RunAgent loop, tool definitions (schemas), system prompt builder
- `bot/tools.go` — Tool handler functions (cada tool é uma função que recebe params e retorna resultado)

### Refatorados
- `bot/claude.go` — Remove `ExtractIntent()` e `BuildIntentPrompt()`. Adiciona `RunAgentLoop()` que gerencia o loop de tool use com a API da Anthropic.
- `bot/orchestrator.go` — Simplifica drasticamente: `Process()` chama `RunAgentLoop()` e retorna a resposta textual. Remove todo o switch de intents e handlers individuais. Mantém referência aos serviços (cal, db, etc.) que são passados aos tools.

### Mantidos (sem mudança)
- `bot/handler.go` — Continua mandando texto para `orchestrator.Process()`
- `bot/db.go` — Mantém todas as tabelas e métodos
- `bot/calendar.go` — Mantém client Google Calendar
- `bot/formatter.go` — Tools usam os formatters existentes
- `bot/scheduler.go` — Mantém lembretes e resumos
- `bot/permissions.go` — Mantém delegação
- `bot/audit.go` — Mantém log de ações
- `bot/crypto.go`, `bot/config.go`, `bot/transcription.go` — Sem mudança

### Removidos/Deprecados
- `bot/confirmation.go` — Fluxo de confirmação pendente não é mais necessário para criação de eventos. Mantém apenas para o fluxo de permissão cross-user (pending_permission_requests). Pode ser simplificado ou inlined no tools.go.

## Tool Definitions (Schema)

Cada tool é definida com o formato da Anthropic API:

```json
{
  "name": "buscar_agenda",
  "description": "Busca eventos na agenda do usuario em um periodo",
  "input_schema": {
    "type": "object",
    "properties": {
      "start_date": {"type": "string", "description": "Data inicio (YYYY-MM-DD)"},
      "end_date": {"type": "string", "description": "Data fim (YYYY-MM-DD)"}
    },
    "required": ["start_date", "end_date"]
  }
}
```

```json
{
  "name": "criar_evento",
  "description": "Cria um novo evento na agenda do usuario",
  "input_schema": {
    "type": "object",
    "properties": {
      "title": {"type": "string", "description": "Titulo do evento"},
      "date": {"type": "string", "description": "Data (YYYY-MM-DD)"},
      "time": {"type": "string", "description": "Hora (HH:MM)"},
      "duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
      "location": {"type": "string", "description": "Local (opcional)"}
    },
    "required": ["title", "date", "time"]
  }
}
```

```json
{
  "name": "editar_evento",
  "description": "Edita um evento existente na agenda",
  "input_schema": {
    "type": "object",
    "properties": {
      "search_query": {"type": "string", "description": "Texto para encontrar o evento"},
      "new_title": {"type": "string", "description": "Novo titulo (opcional)"},
      "new_date": {"type": "string", "description": "Nova data YYYY-MM-DD (opcional)"},
      "new_time": {"type": "string", "description": "Nova hora HH:MM (opcional)"},
      "new_location": {"type": "string", "description": "Novo local (opcional)"}
    },
    "required": ["search_query"]
  }
}
```

```json
{
  "name": "cancelar_evento",
  "description": "Remove um evento da agenda",
  "input_schema": {
    "type": "object",
    "properties": {
      "search_query": {"type": "string", "description": "Texto para encontrar o evento a cancelar"}
    },
    "required": ["search_query"]
  }
}
```

```json
{
  "name": "buscar_historico",
  "description": "Busca mensagens antigas da conversa por tema ou data",
  "input_schema": {
    "type": "object",
    "properties": {
      "query": {"type": "string", "description": "Termo de busca"},
      "limit": {"type": "integer", "description": "Numero maximo de resultados (default: 10)"}
    },
    "required": ["query"]
  }
}
```

```json
{
  "name": "criar_evento_outro_usuario",
  "description": "Cria evento na agenda de outro usuario (requer permissao)",
  "input_schema": {
    "type": "object",
    "properties": {
      "target_user": {"type": "string", "description": "Nome do usuario alvo"},
      "title": {"type": "string", "description": "Titulo do evento"},
      "date": {"type": "string", "description": "Data (YYYY-MM-DD)"},
      "time": {"type": "string", "description": "Hora (HH:MM)"},
      "duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
      "location": {"type": "string", "description": "Local (opcional)"}
    },
    "required": ["target_user", "title", "date", "time"]
  }
}
```

## Tool Handler Pattern

```go
// tools.go
type ToolHandler func(ctx context.Context, user *User, params json.RawMessage) (string, error)

var toolHandlers = map[string]ToolHandler{
    "buscar_agenda":              handleBuscarAgenda,
    "criar_evento":               handleCriarEvento,
    "editar_evento":              handleEditarEvento,
    "cancelar_evento":            handleCancelarEvento,
    "buscar_historico":           handleBuscarHistorico,
    "criar_evento_outro_usuario": handleCriarEventoOutroUsuario,
}

func handleBuscarAgenda(ctx context.Context, user *User, params json.RawMessage) (string, error) {
    var p struct {
        StartDate string `json:"start_date"`
        EndDate   string `json:"end_date"`
    }
    json.Unmarshal(params, &p)
    // ... usa CalendarClient para buscar, retorna texto formatado
}
```

## Agent Loop (claude.go)

```go
func (c *ClaudeClient) RunAgentLoop(ctx context.Context, systemPrompt string, 
    history []ConversationMessage, userMsg string, 
    tools []anthropic.ToolDefinition, execTool func(name string, input json.RawMessage) (string, error),
) (string, error) {
    
    messages := buildMessages(history, userMsg)
    
    for i := 0; i < 5; i++ { // max 5 tool iterations
        resp, err := c.client.CreateMessages(ctx, anthropic.MessagesRequest{
            Model:     anthropic.ModelClaudeHaiku4Dot5,
            MaxTokens: 2048,
            System:    systemPrompt,
            Messages:  messages,
            Tools:     tools,
        })
        
        if resp.StopReason == "end_turn" {
            return extractText(resp), nil
        }
        
        if resp.StopReason == "tool_use" {
            // Execute each tool call
            for _, block := range resp.Content {
                if block.Type == "tool_use" {
                    result, err := execTool(block.Name, block.Input)
                    // Append assistant response + tool result to messages
                    // Continue loop
                }
            }
        }
    }
    return "Desculpe, nao consegui processar. Tente de novo.", nil
}
```

## Extensibilidade

Adicionar nova tool no futuro (ex: `buscar_web`):

1. Adicionar schema em `agent.go` no array de tools
2. Implementar handler em `tools.go`
3. Registrar no mapa `toolHandlers`

Nenhuma outra mudança necessária — o loop já suporta qualquer tool.

## Custos

| Cenário | Antes (intent) | Depois (agent) |
|---|---|---|
| "marca reunião 15h" | 1 call Haiku | 1-2 calls Haiku (tool + response) |
| "como está minha semana?" | 1 call Haiku | 2 calls Haiku (tool + format) |
| "e o jantar?" (follow-up) | FALHA | 2-3 calls Haiku (funciona com contexto) |
| Custo médio/msg | ~$0.001 | ~$0.002-0.003 |

Aumento de ~2x no custo Claude, mas com capacidade muito superior.
