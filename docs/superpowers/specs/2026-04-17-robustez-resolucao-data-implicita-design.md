# Robustez da resolução de data implícita em `criar_evento`

**Data:** 2026-04-17
**Escopo:** defesa-em-profundidade contra divergência entre narrativa e ação na criação de eventos via linguagem natural, com foco na regra sagrada de data implícita.

---

## 1. Contexto e motivação

### 1.1 O incidente

Em 16/04/2026 às 07:02, o usuário enviou `"Reunião as 9h com OTC"` pelo WhatsApp.

- **Intenção** (regra sagrada do usuário): 16/04 às 09:00 — hoje, porque 9h > 07:02.
- **Confirmação mostrada pelo bot:** `"Criado! Reunião com OTC amanhã às 9h. ✅"` (sugerindo 17/04).
- **Evento efetivamente criado no Google Calendar:** 18/04 às 09:00.
- **Descoberta:** apenas no dia seguinte, quando o agenda diária não listou a reunião.

Três falhas sobrepostas:

1. **Inferência sem regra explícita.** O prompt injeta `Data/hora atual: 2026-04-16 07:02` ([agent.go:418-419](../../bot/agent.go#L418-L419)) mas não contém regra determinística pra resolver "qual dia?" quando o usuário dá apenas a hora.
2. **Zero validação no tool handler.** [tools.go:201-204](../../bot/tools.go#L201-L204) parseia a data que Claude mandou e cria direto no Calendar. Nenhum sanity check.
3. **Narrativa divergente da ação.** A mensagem "amanhã às 9h" veio do Claude em freehand *após* a tool call, não do `FormatEventCreated` determinístico. Usuário acreditou que estava marcado pra 17/04; estava pra 18/04.

### 1.2 Regra sagrada do usuário

Quando o usuário menciona apenas uma hora, sem nenhum marcador temporal:

- `hora > agora` → **HOJE**
- `hora ≤ agora` → **AMANHÃ**

Princípios derivados na conversa de design:

- **Preferir confirmar a errar silenciosamente.** Casos ambíguos fazem o bot perguntar, não assumir.
- **A fonte da verdade da data é o código Go.** Google Calendar e mensagem ao usuário leem da mesma variável. Impossível divergir.
- **PM-default pra horas bare < 07:00.** Por frequência estatística, "reunião às 2h" sem qualificador tende a significar 14h. Qualificador "da madrugada/manhã" força AM.

### 1.3 Objetivo desta mudança

Fechar as três falhas acima numa única iteração, sem mudar o contrato geral de tools nem o fluxo de turnos de conversação. Escopo restrito a `criar_evento`.

---

## 2. Arquitetura: fonte única da verdade

```
Usuário: "Reunião às 9h"
        │
        ▼
Claude extrai via prompt:
  { time: "09:00", date_source: "inferred", date: "" }
        │
        ▼
┌──────────────────────────────────────────────┐
│ ResolveEventDate(time, date_source, date,   │
│                  now, loc)                   │
│ ──────────────────────────────────────────── │
│ Regra sagrada aplicada AQUI (determinística) │
│ Retorna: ResolveOutput{ Start, Adjusted,    │
│                         AdjustNote }         │
└──────────────────────────────────────────────┘
        │
        ▼
  startTime = Start (variável única)
        │
        ├───▶ Google Calendar.CreateEvent(startTime)
        │
        └───▶ FormatEventCreated(event{Start: startTime})
                   │
                   ▼
             display = "{AdjustNote?}Evento criado: *{title}*
                        {HOJE|AMANHÃ|} ({weekday}, DD/MM) as HH:MM"
                   │
                   ▼
             Tool retorna: "OK_CRIADO|display=<display>"
                   │
                   ▼
             Claude cita <display> verbatim (regra forte no prompt)
```

Invariante-chave: **`startTime` é calculado uma vez em Go e alimenta tanto a chamada ao Calendar quanto o `display`.** Não existe caminho onde a mensagem e o evento criado possam divergir.

---

## 3. Contrato do tool `criar_evento`

### 3.1 Novo input

| Campo | Tipo | Obrigatório | Descrição |
|---|---|---|---|
| `date_source` | `"explicit"` \| `"inferred"` | sim | `explicit` quando o usuário mencionou qualquer marcador temporal (data, dia da semana, "amanhã/hoje", "daqui N dias"). `inferred` quando o usuário mencionou apenas hora. |
| `date` | string `YYYY-MM-DD` | obrigatório se `date_source=explicit`; opcional/ignorado se `inferred` | A data escolhida pelo Claude. Em `inferred`, o Go ignora este campo e recalcula. |
| `time` | string `HH:MM` | sempre | Inalterado. Claude aplica PM-default e qualificadores antes de passar. |

Campos existentes (`title`, `duration_minutes`, `attendees`, `timezone`, `location`, `with_meet`, `force_conflict`, `is_birthday`, `recurrence`) mantêm o comportamento atual.

### 3.2 Output canônico

| Formato | Quando | Ação esperada do Claude |
|---|---|---|
| `OK_CRIADO\|display=<texto>` | sucesso (com ou sem auto-ajuste) | citar `<texto>` verbatim na resposta ao usuário |
| `ERRO\|<mensagem>` | falha técnica (credenciais, API, etc.) | reportar ao usuário, igual hoje |

**Branches removidos/evitados:**
- `AMBIGUO_AMPM` — não necessário: Claude resolve ambiguidade AM/PM via PM-default instruído no prompt.
- `PASSADO` — não necessário: auto-ajuste silencioso para amanhã com aviso no `display`.

---

## 4. Resolver determinístico (Go)

### 4.1 Novo arquivo: `bot/date_resolver.go`

```go
package main

import (
    "fmt"
    "time"
)

type DateSource string

const (
    DateSourceExplicit DateSource = "explicit"
    DateSourceInferred DateSource = "inferred"
)

type ResolveInput struct {
    Source       DateSource
    ExplicitDate string         // "YYYY-MM-DD", obrigatório se Source=Explicit
    Time         string         // "HH:MM"
    Now          time.Time      // injetado (testabilidade)
    Loc          *time.Location
}

type ResolveOutput struct {
    Start      time.Time
    Adjusted   bool
    AdjustNote string // vazio se Adjusted=false
}

func ResolveEventDate(in ResolveInput) (ResolveOutput, error)
```

### 4.2 Algoritmo

```
1. Parse Time → (hh, mm). Se inválido, retorna erro.

2. Se Source == Inferred:
     today  := data(Now) no Loc
     atTime := today + (hh, mm) no Loc
     se atTime > Now:
         return { Start: atTime }                           // regra sagrada: hora > agora → hoje
     else:
         return { Start: atTime + 24h }                     // regra sagrada: hora ≤ agora → amanhã

3. Se Source == Explicit:
     Parse ExplicitDate → d. Se inválido, retorna erro.
     candidate := d + (hh, mm) no Loc
     se d == today(Now, Loc) E candidate < Now:
         return {
             Start: candidate + 24h,
             Adjusted: true,
             AdjustNote: "Esse horário já passou hoje. Marquei pra amanhã nesse horário. ",
         }
     se d < today(Now, Loc):
         return erro("data explícita no passado: " + ExplicitDate)
     return { Start: candidate }
```

### 4.3 Invariantes (codificar como testes)

1. `Inferred` + `time > now` → sempre hoje.
2. `Inferred` + `time ≤ now` → sempre amanhã.
3. `Explicit` + data futura → data explícita sem ajuste.
4. `Explicit` + data=hoje + time ≥ now → hoje sem ajuste.
5. `Explicit` + data=hoje + time < now → amanhã, `Adjusted=true`.
6. `Explicit` + data passada (não-hoje) → retorna erro.
7. Travessia de meia-noite: resolver usa aritmética de `time` que respeita DST e transições de fuso.
8. `Loc` injetado define "hoje/amanhã" localmente. Usuário em Paris com fuso Europe/Paris recebe "hoje" pelo calendário de Paris.
9. `Now` injetado nunca é `time.Now()` direto dentro da função — permite table tests determinísticos.

### 4.4 Casos de teste (`bot/date_resolver_test.go`)

| # | Source | Now | Time | ExplicitDate | Loc | Esperado |
|---|---|---|---|---|---|---|
| 1 | inferred | 2026-04-16 07:02 BRT | 09:00 | — | BRT | 2026-04-16 09:00 (caso do bug) |
| 2 | inferred | 2026-04-16 07:02 BRT | 05:00 | — | BRT | 2026-04-17 05:00 (caminho "5h da manhã") |
| 3 | inferred | 2026-04-16 07:02 BRT | 17:00 | — | BRT | 2026-04-16 17:00 (caminho PM-default "5h" bare) |
| 4 | inferred | 2026-04-16 07:02 BRT | 07:02 | — | BRT | 2026-04-17 07:02 (time == now → amanhã) |
| 5 | inferred | 2026-04-16 23:45 BRT | 23:30 | — | BRT | 2026-04-17 23:30 |
| 6 | explicit | 2026-04-16 07:02 BRT | 05:00 | 2026-04-16 | BRT | 2026-04-17 05:00 + `Adjusted=true` |
| 7 | explicit | 2026-04-16 07:02 BRT | 09:00 | 2026-04-16 | BRT | 2026-04-16 09:00 |
| 8 | explicit | 2026-04-16 07:02 BRT | 14:00 | 2026-04-20 | BRT | 2026-04-20 14:00 |
| 9 | explicit | 2026-04-16 07:02 BRT | 09:00 | 2026-04-10 | BRT | erro "data explícita no passado" |
| 10 | inferred | 2026-04-16 07:02 Europe/Paris | 09:00 | — | Europe/Paris | 2026-04-16 09:00 Paris |
| 11 | explicit | 2026-04-16 23:45 BRT (= 2026-04-17 04:45 Paris) | 08:00 | 2026-04-17 | Europe/Paris | 2026-04-17 08:00 Paris |
| 12 | inferred | 2026-04-16 07:02 BRT | 25:00 | — | BRT | erro (hora inválida) |
| 13 | explicit | 2026-04-16 07:02 BRT | 09:00 | "bad-date" | BRT | erro (data inválida) |

---

## 5. Integração com `handleCriarEvento`

Arquivo: [bot/tools.go](../../bot/tools.go), função `handleCriarEvento` (linhas 149-295).

### 5.1 Mudança no struct `criarEventoParams`

Adicionar campo `DateSource string` (tag JSON `date_source`). Campo `Date` permanece (agora opcional quando `date_source=inferred`).

### 5.2 Mudança no fluxo (lugar das linhas 185-204 atuais)

```go
// Resolve a timezone do evento (lógica atual preservada).
var parsedDateHint time.Time
if p.Date != "" {
    parsedDateHint, _ = time.ParseInLocation("2006-01-02", p.Date, BRT())
}
loc := agent.db.GetEventTimezone(user.ID, parsedDateHint)
tz := p.Timezone
if tz != "" {
    if l, err := time.LoadLocation(tz); err == nil {
        loc = l
    }
} else {
    tz = loc.String()
}
if p.Time == "" {
    return "Preciso do horario do evento. Pergunte ao usuario.", nil
}

// Resolve data determinística.
res, err := ResolveEventDate(ResolveInput{
    Source:       DateSource(p.DateSource),
    ExplicitDate: p.Date,
    Time:         p.Time,
    Now:          time.Now().In(loc),
    Loc:          loc,
})
if err != nil {
    return "", fmt.Errorf("resolve event date: %w", err)
}
startTime := res.Start
```

Restante do fluxo (conflito, travel notes, CreateEvent) usa `startTime` inalterado.

### 5.3 Mudança no output

Após `FormatEventCreated(*created)`, prefixar com `res.AdjustNote` se presente. Prefixar a string final com `"OK_CRIADO|display="`:

```go
display := FormatEventCreated(*created)
if res.AdjustNote != "" {
    display = res.AdjustNote + display
}
// ... meet link, all-day notes, conflict warn (se aplicável)
return "OK_CRIADO|display=" + display, nil
```

### 5.4 Edge case: timezone lookup com data não determinada

Antes da resolução, `loc` era derivado de `p.Date`. Quando `date_source=inferred`, `p.Date` pode estar vazio. Estratégia:

- Se `p.Date` vazio, usar `time.Now().In(BRT())` como hint inicial pra `GetEventTimezone`. Após resolver `res.Start`, se a data resolvida cair em um período de viagem com fuso diferente, **re-resolver** `loc` via `GetEventTimezone(user.ID, res.Start)` e repetir `ResolveEventDate` com o novo `Loc`. Raro (só acontece se a viagem começar exatamente amanhã e o usuário falar sem data), mas cobrir pra não ter surpresas.

---

## 6. Narrativa determinística: `FormatEventCreated`

Arquivo: [bot/formatter.go:72-80](../../bot/formatter.go#L72-L80).

### 6.1 Mudança

Adicionar rótulo relativo `HOJE`/`AMANHÃ` antes do weekday/data, derivado de `ev.Start` vs `time.Now()` no mesmo fuso:

```go
func FormatEventCreated(ev CalendarEvent) string {
    weekday := weekdaysPT[ev.Start.Weekday()]
    rel := relativeDayLabel(ev.Start, time.Now().In(ev.Start.Location()))
    // rel é "HOJE", "AMANHÃ", ou "" (para datas mais distantes)
    prefix := ""
    if rel != "" {
        prefix = rel + " — "
    }
    return fmt.Sprintf("Evento criado: *%s*\n%s%s, %s as %s",
        ev.Title, prefix, weekday, ev.Start.Format("02/01"), ev.Start.Format("15:04"))
}

// relativeDayLabel retorna "HOJE", "AMANHÃ", ou "" (datas ≥ 2 dias no futuro ou passado).
func relativeDayLabel(eventStart, now time.Time) string
```

### 6.2 Exemplos de saída

- Caso do bug: `"Evento criado: *Reunião com OTC*\nHOJE — Quinta, 16/04 as 09:00"`
- Regra sagrada amanhã: `"Evento criado: *Call*\nAMANHÃ — Sexta, 17/04 as 05:00"`
- Explícito daqui 4 dias: `"Evento criado: *Reunião*\nSegunda, 20/04 as 14:00"` (sem rótulo relativo)
- Auto-ajuste: `"Esse horário já passou hoje. Marquei pra amanhã nesse horário. Evento criado: *Reunião*\nAMANHÃ — Sexta, 17/04 as 05:00"`

### 6.3 Por que o rótulo absoluto

`HOJE (Quinta, 16/04)` é redundante por design — se Claude omitir ou reformular "HOJE", a data absoluta entre os elementos continua presente. Blindagem tripla: rótulo + weekday + data.

---

## 7. Trava narrativa no prompt

Arquivo: [bot/agent.go:340-411](../../bot/agent.go#L340-L411), função `buildSystemPromptStable`.

### 7.1 Nova seção, posicionada após "REGRAS CRITICAS PARA CRIAR EVENTOS"

```
REGRA SAGRADA DE DATA IMPLICITA:
Quando o usuario mencionar APENAS uma hora, sem data, dia da semana,
"amanha/hoje", ou qualquer outro marcador temporal, passe date_source="inferred"
e NAO preencha date. O sistema resolve usando a regra deterministica:
  - hora > agora → hoje
  - hora <= agora → amanha

REGRA DE HORA BARE < 7H (PM-DEFAULT):
Horas bare (sem qualificador) menores que 07:00 → interprete como PM
(some 12). "2h" sem qualificador = 14h. "5h" sem qualificador = 17h.
EXCECOES: qualificador explicito "da madrugada", "da manha" → mantenha AM.

REGRA DE DIA DA SEMANA QUE BATE COM HOJE:
Se o usuario mencionar dia da semana que e hoje (ex: "quinta as 9h" sendo
hoje quinta), PERGUNTE antes de chamar a tool qual semana. Nunca assuma.

REGRA DE CITACAO DO RESULTADO:
Quando criar_evento retornar "OK_CRIADO|display=<texto>", sua resposta
ao usuario DEVE incluir <texto> verbatim. Pode adicionar frase antes/depois
mas NUNCA reformular a data relativa (HOJE/AMANHA) nem alterar data/hora
dentro de <texto>.

Exemplos (agora = 2026-04-16 07:02, quinta):
- "Reuniao as 9h"         → date_source=inferred, time=09:00    (sistema: hoje 09:00)
- "Call as 5h"            → date_source=inferred, time=17:00    (PM-default: hoje 17:00)
- "5h da manha"           → date_source=inferred, time=05:00    (qualificador AM: amanha 05:00)
- "Reuniao as 7h"         → date_source=inferred, time=07:00    (>= 7h sem PM-default: amanha 07:00)
- "Reuniao amanha as 9h"  → date_source=explicit, date=2026-04-17, time=09:00
- "Reuniao dia 20 as 14h" → date_source=explicit, date=2026-04-20, time=14:00
- "Quinta as 9h"          → PERGUNTE qual quinta (hoje e quinta); NAO chame a tool.
```

### 7.2 Atualização no tool definition

Em [agent.go:431-633](../../bot/agent.go#L431-L633), `criar_evento` tool: adicionar `date_source` como enum obrigatório no schema JSON. Atualizar `description` do campo `date` pra `"Data (YYYY-MM-DD). Obrigatório se date_source=explicit. Ignorado se date_source=inferred."`

---

## 8. Auditoria expandida

Arquivo: [bot/audit.go](../../bot/audit.go).

Expandir o log em [tools.go:283](../../bot/tools.go#L283) pra capturar:

```
action=criar_evento
title=<p.Title>
user_msg_snippet=<primeiros 120 chars da última mensagem do usuário no history>
date_source=<inferred|explicit>
claude_date=<p.Date>                      // o que Claude mandou, mesmo ignorado
claude_time=<p.Time>
resolved_start=<res.Start.Format(RFC3339)>
adjusted=<res.Adjusted>
```

**Propósito:** post-mortem de regressões. Query-alvo: "em que % dos `date_source=inferred` o `claude_date` diverge de `resolved_start`?" Se alto (ex: > 10%), sinal de que o prompt não está sendo seguido.

**Escopo da mudança:** ajustar assinatura de `audit.Log` ou criar `audit.LogCriarEvento(...)` com os campos estruturados. Preferir variante específica pra não quebrar outros call sites.

**Nota de implementação:** `user_msg_snippet` requer acesso ao histórico de mensagens do usuário dentro de `handleCriarEvento`. Hoje a assinatura recebe `agent *Agent` e `user *User` — o snippet da última mensagem pode ser puxado via `agent.db.GetRecentMessages(user.ID, 1)` ou equivalente (ver se já existe helper em `db.go`).

---

## 9. Observabilidade adicional: watchdog narrativo (opcional, v1)

Arquivo: [bot/watchdog.go](../../bot/watchdog.go).

Após o orquestrador obter a resposta final do Claude pós-`criar_evento`, checagem leve:

- Extrair a substring `display` do tool output (regex `OK_CRIADO\|display=(.+)$`).
- Extrair o rótulo relativo do display (`HOJE` ou `AMANHÃ`). Se display não tem rótulo (evento ≥ 2 dias no futuro), o watchdog não dispara — nada a verificar.
- Se rótulo existe e a resposta final do Claude **não contém** o rótulo esperado: log warning `[NARRATIVE_DRIFT] expected=HOJE got=<resposta>`.
- **Sem** auto-reescrita na v1. Só log. Se virar frequente, v2 força append do `display` ao final.

---

## 10. Estratégia de testes

### 10.1 Unit — resolver puro

Arquivo novo: `bot/date_resolver_test.go`.
Table test com os 13 casos da seção 4.4. Cobertura-alvo: 100% das branches do resolver.

### 10.2 Unit — formatter

Estender [bot/formatter_test.go](../../bot/formatter_test.go) pra cobrir `relativeDayLabel` em:
- Evento hoje → "HOJE"
- Evento amanhã → "AMANHÃ"
- Evento em 2+ dias → ""
- Evento ontem (edge teórico) → ""
- Travessia de meia-noite (evento às 00:30 sendo agora 23:59) → verificar se "HOJE" ou "AMANHÃ" conforme data absoluta

### 10.3 Integration — fluxo completo

Em [bot/integration_test.go](../../bot/integration_test.go), adicionar teste de regressão do incidente:

```go
func TestRegressaoBugReuniaoOTC(t *testing.T) {
    // Simula mensagem "Reunião as 9h com OTC" às 07:02 de 16/04.
    // Claude deve chamar criar_evento com date_source=inferred, time=09:00.
    // Resolver deve produzir 2026-04-16 09:00.
    // Calendar deve receber 2026-04-16 09:00.
    // Display deve conter "HOJE — Quinta, 16/04 as 09:00".
    // Resposta final do Claude deve conter o display verbatim.
}
```

### 10.4 Prompt-adherence (opcional, gated)

Teste fora do CI padrão que chama Claude real com 20 variações da frase ("marca reunião às 9h", "me lembra às 9h", "call 9h", etc.) e asserta `date_source=inferred` em todas. Útil pra detectar regressão quando mudar o prompt.

---

## 11. Escopo explicitamente fora

- `editar_evento` / `cancelar_evento` — não recebem `date_source`. Edições operam sobre evento-alvo já resolvido via `buscar_agenda`.
- `registrar_viagem` — usa datas explícitas por natureza.
- Confirmação pré-criação ("Vou marcar X às Y, ok?") — descartada por fricção; aviso pós-criação com rótulo `HOJE/AMANHÃ` substitui.
- Redesenho code-owned do contrato (Abordagem 3 do brainstorm) — futuro v2 se Abordagem 2 mostrar limitações em produção.

---

## 12. Riscos e mitigações

| Risco | Probabilidade | Impacto | Mitigação |
|---|---|---|---|
| Claude não segue a regra de PM-default | média | médio (interpreta "2h" como 02h) | Audit log captura `user_msg_snippet` + `claude_time`; revisão amostral detecta. Exemplos no prompt são específicos. |
| Claude passa `date_source=explicit` incorretamente quando deveria ser `inferred` | baixa | médio (Claude manda data adivinhada, resolver confia nela) | Log `claude_date` mesmo em `inferred`; comparar divergências. Se alto, escalar para Abordagem 3 (code-owned). |
| Auto-ajuste `explicit hoje + passado` surpreende usuário | baixa | baixo | `AdjustNote` é explícita e vem na primeira linha da mensagem. |
| Travessia de fuso em viagens cruza dia de forma inesperada | baixa | médio | `Loc` sempre derivado de `GetEventTimezone` antes do resolve; testes #10, #11 cobrem. |
| Prompt mudanças quebram outros fluxos | média | médio | Executar suite integration_test antes de merge; teste de regressão específico do bug. |

---

## 13. Plano de implementação (alto nível)

1. Criar `bot/date_resolver.go` com `ResolveEventDate` + tipos. Criar `bot/date_resolver_test.go` com os 13 casos. Rodar isolado até verde.
2. Estender `FormatEventCreated` com rótulo relativo + helper `relativeDayLabel`. Testes em `bot/formatter_test.go`.
3. Atualizar `criarEventoParams` + schema JSON da tool em `agent.go`. Integrar resolver em `handleCriarEvento`. Ajustar output pra `OK_CRIADO|display=...`.
4. Atualizar system prompt em `buildSystemPromptStable` com as 4 novas regras + exemplos.
5. Expandir audit log pra capturar campos estruturados.
6. Adicionar teste de regressão em `integration_test.go` reproduzindo o incidente.
7. (Opcional) Adicionar watchdog narrativo em `watchdog.go`.

Detalhamento por commit e ordem de execução fica pro plano gerado por `superpowers:writing-plans`.
