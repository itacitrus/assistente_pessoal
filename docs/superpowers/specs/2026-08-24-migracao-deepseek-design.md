# Migração de LLM: Anthropic → DeepSeek

**Data:** 2026-08-24
**Status:** design aprovado, aguardando revisão do spec
**Motivação:** custo. O cartão da Anthropic foi cancelado; o bot está de pé mas com todas as chamadas de LLM falhando.

---

## 1. Situação verificada

Tudo abaixo foi confirmado contra o sistema real, não inferido.

### Produção (via SSM, `i-0ce25dde7d35adf13`, 2026-08-24)

| Fato | Evidência |
|---|---|
| Containers de pé há 3 semanas | `assistente-bot Up 3 weeks`, `zello-web Up 3 weeks`, `assistente-transcription Up 3 weeks (healthy)` |
| Todas as chamadas de LLM falham | 1.818 `invalid_request_error` em 72h: *"Your credit balance is too low to access the Anthropic API"* |
| Retry storm no scheduler | `psych_catchup` reprocessa snapshot dos users 16/17/18 várias vezes por segundo, sem backoff |
| Companion nunca rodou em DeepSeek | `LLM_PROVIDER_COMPANION` **ausente** do `.env` de prod → default `anthropic` ([config.go:80](../../../bot/config.go)) |
| DeepSeek nunca foi configurado em prod | `DEEPSEEK_API_KEY` ausente do `.env` de prod |

**Consequência:** o bug do modelo morto (§1.2) é **latente**, nunca disparou em produção. A memória do projeto (`idoso roda TODO turno no DeepSeek`) descreve a intenção da fase 4, não o estado de prod.

### API DeepSeek (testada com a chave do projeto, 2026-08-23/24)

| Fato | Evidência |
|---|---|
| Modelos ativos | `deepseek-v4-flash`, `deepseek-v4-pro`, `deepseek-v4-flash-vision-exp` (`GET /models`) |
| `deepseek-chat` / `deepseek-reasoner` | Descontinuados em 24/07/2026 (doc oficial); ausentes do `/models` e da página de preços. **Ainda resolvem como alias não-documentado** — `deepseek-chat` → v4-flash sem raciocínio, `deepseek-reasoner` → v4-flash com raciocínio |
| Tool calling | ✅ 5/5 em `deepseek-v4-flash`, incluindo aritmética de data (`amanhã` → `2026-08-24`) |
| Loop agêntico completo | ✅ tool → tool_result → resposta final, PT-BR natural |
| Cache de contexto | ✅ automático; `prompt_cache_hit_tokens` = 256 já no 2º turno |
| Visão | ✅ `deepseek-v4-flash-vision-exp` leu "Losartana Potássica 50 mg" de foto de caixa |
| Visão no modelo de chat | ❌ `HTTP 400: "This model does not support image"` |
| `reasoning_content` no round-trip | Doc afirma exigir reenvio sob pena de 400. **Não reproduziu:** omitir funcionou nos dois modos. Tratar como risco, não como blocker |
| Raciocínio consome `max_tokens` | ⚠️ com `max_tokens: 12`, `content` voltou **vazio** — o orçamento foi todo para o raciocínio |
| Markdown | ⚠️ emite `**negrito**`, que o WhatsApp renderiza como asterisco literal |

### Preços (página oficial, verificada)

| Modelo | Input (hit) | Input (miss) | Output |
|---|---|---|---|
| `deepseek-v4-flash` | $0,007 / $0,014 | $0,22 / $0,44 | $0,66 / $1,32 |
| `deepseek-v4-pro` | $0,022 / $0,044 | $0,66 / $1,32 | $1,98 / $3,96 |

Off-peak = metade do peak. Peak = 01:00–04:00 e 06:00–10:00 UTC, seg–sex (**22:00–01:00 e 03:00–07:00 BRT**). O horário comercial brasileiro é integralmente off-peak; os lembretes matinais de medicação caem no peak.

**Destino é o Flash, não o Pro:** o Pro custa 3,1x mais e pontua **pior** em benchmark de agente.

### Saldo — bloqueio operacional

| Provider | Saldo |
|---|---|
| Anthropic | zerado (é a causa do incidente) |
| Gemini | `429 — prepayment credits are depleted` |
| **DeepSeek** | **US$ 0,97** |

Nenhum provider está financiado. A migração remove o custo alto, mas **o bot só volta ao ar com crédito no DeepSeek.**

---

## 2. Decisões travadas

| # | Decisão | Escolha |
|---|---|---|
| D1 | Provider-alvo | **DeepSeek em todos os papéis**, risco de LGPD assumido explicitamente pelo dono |
| D2 | Anthropic | **Desligada por config, viva no código.** Compila e é testada; sem chave, custo zero |
| D3 | Sequência | **Big-bang num PR**, com todo o roteamento em env var (reverter = mudar variável) |
| D4 | Roteamento | **Por usuário**, não por tipo de agente |
| D5 | Saldo | Recarga + **tratamento explícito de saldo insuficiente no código** |
| D6 | `extrair_receita_imagem` | Mantida, corrigida dentro desta migração |
| D7 | Aceite | Paridade nos fluxos críticos · suíte Go verde · qualidade PT-BR validada com conversa real |

### Sobre D1 e LGPD — registro explícito

A política de privacidade da DeepSeek diz, textualmente:

> *"The Services are **not designed or intended to process sensitive Personal Data** (e.g., ... **health** ...). We do not ask for, and **you should not provide** sensitive Personal Data to the Services."*

E declara uso dos inputs *"to train and improve our technology"*, com armazenamento *"in People's Republic of China"*. Não há decisão de adequação da ANPD (Art. 33 LGPD).

O Zello processa medicamento, posologia, adesão e estado cognitivo de idoso — dado sensível pelo Art. 5º II. **O dono foi informado e assumiu o risco conscientemente.** Este parágrafo existe para que a decisão seja rastreável, não para relitigá-la.

**Mitigação arquitetural:** o roteamento por sensibilidade fica parametrizado com os dois ramos apontando para DeepSeek hoje. Virar o caminho clínico para outro provider passa a ser edição de variável, não redesenho.

---

## 3. Arquitetura

### 3.1 Roteamento por sensibilidade

```
início do turno
      │
      ▼
usuário tem superfície de saúde ativa?
 (perfil idoso · pessoa vinculada com medicação · tools de medicação habilitadas)
      │
      ├── SIM  → LLM_PROVIDER_CLINICO      (deepseek hoje)
      └── NÃO  → LLM_PROVIDER_OPERACIONAL  (deepseek hoje)
```

O critério é **"este turno poderia tocar dado de saúde?"**, não *"tocou?"* — não há como saber qual tool o modelo escolherá antes de ele escolher. Conservador de propósito.

Roteia por usuário e não por agente porque o agente operacional **também** expõe tools de medicação ([agent.go:1060](../../../bot/agent.go)): um filho adulto cadastrando o remédio da mãe carrega posologia pelo caminho operacional.

### 3.2 Matriz de papéis

| Papel | Hoje | Depois | Env var |
|---|---|---|---|
| Operacional (sem saúde) | Sonnet 4.6 | DeepSeek v4-flash | `LLM_PROVIDER_OPERACIONAL` |
| Operacional (com saúde) | Sonnet 4.6 | DeepSeek v4-flash | `LLM_PROVIDER_CLINICO` |
| Companion (idoso) | Anthropic (default) | DeepSeek v4-flash | `LLM_PROVIDER_CLINICO` |
| Proativo | Sonnet 4.6 | segue o usuário | (herda) |
| Sales agent | Haiku 4.5 | DeepSeek v4-flash | `LLM_PROVIDER_SALES` |
| Analysis (snapshot) | Haiku 4.5 | DeepSeek v4-flash | `LLM_PROVIDER_ANALYSIS` |
| Report (síntese) | Sonnet 4.6 | DeepSeek v4-flash | `LLM_PROVIDER_REPORT` |
| Vision | Haiku 4.5 | `deepseek-v4-flash-vision-exp` | `LLM_PROVIDER_VISION` |

Toda env var aceita `deepseek` \| `gemini` \| `anthropic`. Ausente = `deepseek`.

### 3.3 Provider unificado

DeepSeek e Gemini falam o mesmo protocolo (compatível com OpenAI) — ambos validados com o mesmo código nos testes. Portanto **um tradutor, dois perfis**:

```
bot/llm/
  openai_compat.go   NOVO — nasce de deepseek_chat.go; tradutor Message ↔ ChatCompletion
  profiles.go        NOVO — perfis: base URL, modelo default, quirks por provider
  analysis_oai.go    NOVO — AnalysisProvider (json_object + retry no parse)
  report_oai.go      NOVO — ReportProvider
  vision_oai.go      NOVO — VisionProvider
  retry.go           NOVO — IsRetryable canônico + backoff exponencial
  anthropic_*.go     INTOCADOS — rede de segurança atrás de env var
```

O `deepseek_chat.go` atual já contém a parte difícil: tradução de `tool_use`/`tool_result` e a ordenação exigida (mensagens `tool` antes da `user`). Ele é **generalizado**, não duplicado.

### 3.4 Convergência do agente

O trabalho já foi feito uma vez. `runLoopLLM` ([agent_companion.go:182](../../../bot/agent_companion.go)) é funcionalmente equivalente a `runLoop` ([agent.go:290](../../../bot/agent.go)): mesmas 8 iterações, **mesmo registry de 33 tool handlers**, mesma extração de resultados.

| Arquivo | Mudança |
|---|---|
| `agent.go` | `Run()` passa a usar `runLoopLLM`. Morrem: `runLoop`, `markLastMessageForCache`, `anthropicRewriteFn`. `buildToolDefinitions()` passa a devolver `[]llm.ToolDef` |
| `agent_proactive.go` | idem |
| `turn_context.go` | `llm.SystemPart` no lugar de `anthropic.MessageSystemPart` |
| `sales_agent.go` | porta para a abstração (21 refs) |
| `llm_router.go` | **NOVO** — roteador por sensibilidade |
| `db_llm_usage.go` | **NOVO** — contabilidade de custo |
| `config.go` | provider por papel; `ANTHROPIC_API_KEY` deixa de ser obrigatória |
| `main.go` | registry de providers |
| `output_guard.go` | +CJK, +sanitizador de markdown, modo `enforce` |

**Não se toca:** `tools.go` (33 handlers, agnóstico), `escalation.go` (100% determinístico), `transcription/` (Python, zero Anthropic), `web/` (zero chamada LLM).

---

## 4. Defesas obrigatórias

Cada uma cobre uma diferença de protocolo confirmada em teste ou na doc. Nenhuma é opcional.

### 4.1 `is_error` no tool_result
A Anthropic tem flag de erro; OpenAI/DeepSeek não. O tradutor atual **descarta** `ContentBlock.IsError` ([deepseek_chat.go:178](../../../bot/llm/deepseek_chat.go)). O modelo deixa de distinguir "a ferramenta explodiu" de "a ferramenta retornou este texto" — colisão frontal com a regra de nunca narrar sucesso sem confirmação.
**Correção:** prefixar o conteúdo com `[ERRO] ` na tradução.

### 4.2 Circuit breaker de tool loop
Dedupe de `(nome_da_tool, args normalizados)` com corte após N repetições + teto duro de rounds. Loop de call idêntica é o modo de falha agêntico mais documentado do DeepSeek.
**Adicional:** parser de recuperação para tool call vazando como texto puro em `content` com `finish_reason: "stop"`.

### 4.3 Guard de CJK
Vazamento de chinês é limitação **declarada pelo fornecedor** (paper do R1: *"optimized for Chinese and English, which may result in language mixing"*). Público é idoso.
**Correção:** invariante nova no `output_guard.go` — faixa `\p{Han}` na saída bloqueia e regenera.

### 4.4 Sanitizador de markdown
Gap **novo, criado pela migração**. O prompt proíbe `**` e `##` ([agent.go:651](../../../bot/agent.go)), mas não existe sanitizador — a Anthropic simplesmente obedecia. DeepSeek emitiu `**14:00**` nos testes; WhatsApp renderiza asterisco literal.
**Correção:** post-processor determinístico no transporte (`**x**` → `*x*`, remove `#`).

### 4.5 Saldo/auth vira alerta, não silêncio
Foi o modo exato do incidente atual. `401`/`402`/saldo insuficiente detectado → fallback por request + aviso no WhatsApp de admin (`ADMIN_PHONES`).

### 4.6 Backoff no scheduler
`psych_catchup` gerou 1.818 erros em 72h reprocessando em loop apertado. Falha de snapshot precisa de backoff exponencial + circuit breaker por usuário/dia. **Independente da migração** — sem isso, troca-se de provider e leva-se a tempestade junto.

### 4.7 Fallback por request
Hoje [main.go:184](../../../bot/main.go) escolhe o provider uma vez no boot. Se o DeepSeek cair às 3h, o companion fica morto até alguém reiniciar o container. Para lembrete de medicação, isso é dose potencialmente perdida.
**Correção:** falha/timeout/erro retryable → repete no provider de fallback dentro do mesmo turno.

### 4.8 Raciocínio não pode comer a resposta
Com `max_tokens: 12` e raciocínio ligado, `content` voltou **vazio** — mensagem em branco chegaria ao WhatsApp do idoso.
**Correção:** `thinking` explicitamente desligado no perfil DeepSeek (`go-openai` não expõe o campo → marshalling próprio), e piso de `max_tokens` validado.

### 4.9 Visão nunca decide o medicamento
`extrair_receita_imagem` ([tools_medication.go:853](../../../bot/tools_medication.go)) hoje recebe `items[]` já preenchidos pelo modelo, sem a imagem ter chegado nele — o modelo pode inventar a receita inteira.
**Correção:** a visão extrai **texto bruto**; quem identifica o medicamento é o `drug_catalog` (CMED + matching fonético PT-BR, já implementado). Leitura ruim vira "não identifiquei, me manda de novo", nunca dose errada persistida. Mantém a regra de nunca narrar sem persistir.

---

## 5. Observabilidade

Não existe hoje nenhuma contabilidade de LLM — só `log.Printf` efêmeros. Você cancelou o cartão por custo e **não tem como provar a economia nem detectar regressão**.

**Tabela `llm_usage`:** `user_id, engine, provider, model, input_tokens, output_tokens, cache_hit_tokens, cache_miss_tokens, latency_ms, ts`.

`go-openai` **não desserializa** `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` → `Usage.CacheReadTokens` ficaria sempre 0. Exige struct de response própria. É o dado que valida a economia inteira.

---

## 6. Testes e aceite

| Critério (D7) | Verificação |
|---|---|
| Suíte Go verde | `go test ./...` — 731 testes hoje, todos passando. Os ~13 que quebram por tipo `anthropic.*` são **migrados, não deletados** |
| Paridade nos fluxos críticos | Harness ponta a ponta: criar/listar/alterar evento · cadastrar remédio · marcar dose · escalação · transcrição de áudio |
| Qualidade PT-BR | Replay de diálogos reais do histórico, com detector automático de: CJK, markdown vazado, menu numerado e **narrar-sem-persistir** |

### Harness de narrar-sem-persistir

Já validado nesta sessão: 5 casos clínicos, contando tool calls e casando a resposta contra regex de afirmação de persistência. Resultados medidos:

| Modelo | Tool chamada | Narrou sem persistir |
|---|---|---|
| `deepseek-v4-flash` | **5/5** | **0/5** |
| `gemini-2.5-flash` | 5/5 | 0/5 |
| `gemini-2.5-flash-lite` | **0/5** | **3/5** |

O Flash-Lite foi reprovado empiricamente apesar de ser o mais barato e de ter sido o recomendado pela pesquisa — motivo pelo qual este harness entra na suíte permanente, e não apenas neste PR.

---

## 7. Sequência de execução

Um PR, conforme D3. Ordem interna:

1. `llm/` — provider unificado, perfis, retry, providers de analysis/report/vision
2. `llm_router.go` + `db_llm_usage.go`
3. `config.go` / `main.go` — env vars por papel, `ANTHROPIC_API_KEY` condicional
4. `agent.go` / `agent_proactive.go` / `turn_context.go` / `sales_agent.go` — convergência
5. `output_guard.go` — CJK, markdown, `enforce`
6. Defesas 4.1–4.9
7. Testes: migrar os ~13 quebrados, adicionar harness de paridade e de PT-BR

### Pré-deploy — bloqueadores

- [ ] **Crédito no DeepSeek** (US$ 0,97 hoje; sem isso o bot não volta)
- [ ] `DEEPSEEK_API_KEY` no `.env` de prod (ausente hoje)
- [ ] `swapon --show` não-vazio antes de buildar (t3.small, 2GB — build sem swap derruba prod)
- [ ] `df -h /` e `docker builder prune -af` se o cache estiver grande
- [ ] `git fetch origin` **antes** do `reset --hard` (senão deploy silencioso de código velho)

### Rollback

Todo o roteamento é env var. Reverter é editar `.env` e reiniciar — nenhum rollback de código. Com crédito na Anthropic, `LLM_PROVIDER_*=anthropic` restaura o comportamento anterior integralmente.

---

## 8. Riscos aceitos

| Risco | Decisão |
|---|---|
| LGPD — dado de saúde em jurisdição sem adequação, fornecedor treina com input e desaconselha o uso | **Aceito explicitamente (D1).** Mitigado arquiteturalmente: virar o caminho clínico é uma env var |
| Visão em modelo `-exp`, teto de 384 tokens/imagem, sem OCR | Aceito. Mitigado por 4.9 — visão nunca decide o medicamento |
| Big-bang mistura risco de refactor com risco de modelo | Aceito (D3). Mitigado por rollback via env var e pelos 731 testes |
| Sem SLA; uptime observado ~97,8–99,9% | Mitigado por 4.7 (fallback por request) |
| PT-BR com vazamento de chinês declarado pelo fornecedor | Mitigado por 4.3 (guard CJK) e pelo harness de replay |
| `deepseek-chat`/`deepseek-reasoner` ainda respondem mas estão descontinuados | Não usar. `DEEPSEEK_MODEL` explícito, default `deepseek-v4-flash` |
