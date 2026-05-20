# Fase 5 — Relatório longitudinal e alertas pro responsável

**Data:** 2026-05-09 (revisado 2026-05-10)
**Depende de:** Fase 1 (`family_links`, `GetGuardians`), Fase 3 (`medication_intake_log`, `escalations`), Fase 4 (`proactive_attempts`, `escalations.proactive_attempt_id`, `social_context` em `user_memories` com convenção `risco:*`, `Notifier` interface, trigger de snapshot writer pós-conversa significativa).
**Status:** Planejamento — pronto para implementação.

---

## 1. Objetivo e não-objetivos

**Objetivo.** Permitir que um responsável (`family_links.guardian_id`) consulte sob demanda o estado **longitudinal** de um dependente (`family_links.dependent_id`) e receba alertas reativos quando o idoso parar de responder a tentativas de conversa do Lurch. O relatório agora é construído a partir de **snapshots diários de estado psicológico** persistidos em `psych_state_daily`, escritos continuamente por um sub-agente Haiku (snapshot writer) e sintetizados sob demanda por um sub-agente Sonnet (synthesis report).

A consulta produz um relatório acolhedor (não clínico, não diagnóstico) com **tendência ao longo do tempo** — comparação semana-vs-semana, evolução de humor/energia/sociabilidade/autocuidado. O resultado é exposto via três superfícies:

1. **WhatsApp** — tool `status_dependente` chamada pelo agente Sonnet operacional.
2. **Web (status)** — endpoint REST `GET /api/v1/family/dependents/{id}/status` consumido pela página `/web/app/dashboard/family/[id]/page.tsx`.
3. **Web (timeline)** — endpoint REST `GET /api/v1/family/dependents/{id}/timeline?days=90` consumido pela página `/web/app/dashboard/family/[id]/evolucao/page.tsx`, que renderiza gráfico de linha das 4 dimensões.

Adicionalmente, dois jobs cron novos:
- `checkInactivityEscalation` — detecta dependentes que não respondem a tentativas proativas há mais de N horas e dispara alerta para responsáveis com `notify_on_inactivity=true`.
- `runDailyPsychSnapshotCatchup` — varre idosos que tiveram mensagens no dia mas para os quais o trigger pós-conversa (Fase 4) não rodou, e materializa o snapshot do dia. Garantia de que cada dia com atividade tem **uma** linha em `psych_state_daily`.

**Não-objetivos.**
- Citação literal de mensagens do idoso (privacidade dura — só inferências agregadas).
- Diagnóstico clínico, recomendação medicamentosa, intervenção profissional.
- Voz/ligação (Fase 6+).
- Modo confidencial dinâmico (idoso revoga relatório a quente) — a política deixa o gancho via `family_links.dependent_consent_status`.
- Análise sub-diária de estado psicológico — granularidade é dia, não turno.
- Predição/forecasting de tendência futura — só descreve o passado observado.

---

## 2. Arquitetura longitudinal

### 2.1 Visão geral

```
                         [conversa Idoso ↔ Companion (DeepSeek, Fase 4)]
                                        │
                                        │ trigger pós-conversa significativa
                                        │ (definido na Fase 4 §10)
                                        ▼
                             ┌──────────────────────┐
                             │  synthesis.WriteSnap │  Haiku 4.5
                             │  (snapshot writer)   │  ~$0.001/call
                             └──────────────────────┘
                                        │
                                        ▼
                            UPSERT em psych_state_daily
                            (PK: user_id + snapshot_date)
                                        │
                                        │ (acumula 1 linha/dia/idoso)
                                        ▼
        Responsável pergunta "como ela está?"
                                        │
                                        ▼
                             ┌──────────────────────┐
                             │  synthesis.Synthesize│  Sonnet 4.6/4.7
                             │  (report longitudin.) │  ~$0.005-0.015/call
                             └──────────────────────┘
                                        │
                                        ▼
                          ReportOutput → tool/REST/web
```

### 2.2 Por que dois sub-agentes diferentes

| Aspecto                  | Snapshot writer (Haiku)                                    | Synthesis report (Sonnet)                                          |
| ------------------------ | ---------------------------------------------------------- | ------------------------------------------------------------------ |
| **Quando roda**          | Pós-conversa significativa (Fase 4) + catch-up diário      | On-demand (responsável pergunta) — chat ou web                     |
| **Volume**               | ~1-3x/dia/idoso                                            | ~2x/semana/idoso                                                   |
| **Input**                | Mensagens recentes + medicação do dia + memos `risco:*`    | N dias de snapshots + stats agregados (sem mensagens nem memos!)   |
| **Output**               | 1 linha em `psych_state_daily` (4 scores + sinais + eventos) | Relatório acolhedor com tendência, comparação, recomendações       |
| **Modelo**               | Haiku 4.5 (rápido, observacional, baixo custo)             | Sonnet 4.6/4.7 (nuance ética, output sensível, baixa latência ok)  |
| **Output sensível?**     | Não vai pro humano direto — só pra DB.                    | Vai pro humano direto — exige cuidado de linguagem.                |
| **Tem tools?**           | Não. Função pura. Uma chamada LLM.                         | Não. Função pura. Uma chamada LLM.                                 |
| **Vê transcrições?**     | Sim — só desde último snapshot, janela curta.              | **Não.** Lê só `psych_state_daily` (já abstrato).                  |

### 2.3 Por que Sonnet pra síntese final (D8 do overview)

A versão anterior deste plano usava Haiku pra síntese. O redesign promove pra Sonnet 4.6/4.7 porque:

1. **Output sensível.** Texto vai direto pro filho/filha sobre o estado da mãe. Cada palavra carrega peso emocional. Sonnet calibra tom melhor — nem alarmista, nem minimizador.
2. **Volume baixo.** ~2 chamadas/idoso/semana × 100 idosos piloto = ~800 chamadas/mês. Custo ~$0.005-0.015/call → US$10-15/mês. Trivial.
3. **Janela cresceu.** Lê 14 dias de snapshots + stats; o raciocínio de tendência ("últimos 7 dias humor 3.2 vs 4.0 nas semanas anteriores → trajetória descendente") é mais robusto em Sonnet.
4. **Haiku continua útil pro lugar certo** — snapshot writer, alta frequência, baixa carga linguística por chamada.

### 2.4 Fluxo de privacidade — barreiras explícitas

```
                                          ┌──────────────────────────┐
                                          │     IDOSO escreve        │
                                          │     no WhatsApp          │
                                          └──────────────────────────┘
                                                       │
                       ┌───────────────────────────────┴──────────────────────┐
                       │                                                      │
                       ▼                                                      ▼
            ┌─────────────────────┐                            ┌──────────────────────────┐
            │ Companion (DeepSeek)│                            │ snapshot writer (Haiku)  │
            │ chat real, persona  │                            │ inferências abstratas    │
            └─────────────────────┘                            └──────────────────────────┘
                       │                                                      │
                       ▼                                                      ▼
            user_memories.social_context                          psych_state_daily
            ├── pessoa:dona_marta  (PRIVADO do idoso)             scores 1-5
            ├── evento:consulta_15  (PRIVADO)                     sinais (sem fofoca)
            ├── rotina:cha_noite    (PRIVADO)                     eventos (saúde/segurança)
            └── risco:queda_recente (CRUZA pra relatório)         confidence

                       │                                                      │
                       │ filtro: só `risco:*`                                  │ leitura DIRETA
                       └──────────────────────────────┬───────────────────────┘
                                                      ▼
                                         ┌──────────────────────────┐
                                         │ synthesis report (Sonnet)│
                                         │ relatório longitudinal   │
                                         └──────────────────────────┘
                                                      │
                                                      ▼
                                            RESPONSÁVEL recebe
```

Pontos cruciais:
- **Synthesis NUNCA lê transcrições.** Só `psych_state_daily` + stats + memos `risco:*`.
- **Snapshot writer rejeita persistir literal.** Mesma `quoteRegex` de antes, agora aplicada no input do writer.
- **Fofoca social não atravessa fronteira.** Memos sem `risco:` ficam no companion, nunca alimentam relatório.

---

## 3. Schema novo — `psych_state_daily`

### 3.1 DDL

Esta fase **declara** a tabela `psych_state_daily`. Não há outras tabelas novas. Não há colunas aditivas em tabelas existentes (a coluna `escalations.proactive_attempt_id` foi entregue pela Fase 4).

```sql
CREATE TABLE IF NOT EXISTS psych_state_daily (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    snapshot_date DATE NOT NULL,                    -- YYYY-MM-DD em fuso local do user
    humor_score INTEGER,                            -- 1-5; NULL se confidence muito baixa
    humor_nuance TEXT NOT NULL DEFAULT '',
    energia_score INTEGER,                          -- 1-5; NULL ok
    sociabilidade_score INTEGER,                    -- 1-5; recolhido(1) → falante(5)
    autocuidado_score INTEGER,                      -- 1-5; medicação, sono, alimentação
    sinais_observados TEXT NOT NULL DEFAULT '[]',   -- JSON array string; só componente saúde/risco
    eventos_dia TEXT NOT NULL DEFAULT '[]',         -- JSON array string; só saúde/segurança
    n_conversations INTEGER NOT NULL DEFAULT 0,
    n_messages INTEGER NOT NULL DEFAULT 0,
    duration_minutes INTEGER NOT NULL DEFAULT 0,
    confidence INTEGER NOT NULL DEFAULT 1,          -- 1-5; baixa = pouca conversa
    inferred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, snapshot_date)
);
CREATE INDEX IF NOT EXISTS idx_psych_state_user_date
    ON psych_state_daily(user_id, snapshot_date DESC);
```

Adicionar à lista `additive` em `db.go`:

```go
// Fase 5 — Snapshot longitudinal de estado psicológico.
`CREATE TABLE IF NOT EXISTS psych_state_daily (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    snapshot_date DATE NOT NULL,
    humor_score INTEGER,
    humor_nuance TEXT NOT NULL DEFAULT '',
    energia_score INTEGER,
    sociabilidade_score INTEGER,
    autocuidado_score INTEGER,
    sinais_observados TEXT NOT NULL DEFAULT '[]',
    eventos_dia TEXT NOT NULL DEFAULT '[]',
    n_conversations INTEGER NOT NULL DEFAULT 0,
    n_messages INTEGER NOT NULL DEFAULT 0,
    duration_minutes INTEGER NOT NULL DEFAULT 0,
    confidence INTEGER NOT NULL DEFAULT 1,
    inferred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, snapshot_date)
)`,
`CREATE INDEX IF NOT EXISTS idx_psych_state_user_date
    ON psych_state_daily(user_id, snapshot_date DESC)`,
```

### 3.2 Semântica das colunas

| Coluna                | Range/tipo            | Significado                                                                                  |
| --------------------- | --------------------- | -------------------------------------------------------------------------------------------- |
| `humor_score`         | 1-5 ou NULL           | 1=muito desanimado, 3=neutro, 5=animado. NULL quando confidence < 2 — não inventa.           |
| `humor_nuance`        | text livre, ≤ 100 ch  | Ex: "saudosa do filho", "irritada com o tempo". Sem citação literal.                         |
| `energia_score`       | 1-5 ou NULL           | 1=apática, 5=cheia de pique. Inferido por verbos, ritmo de resposta, planos mencionados.    |
| `sociabilidade_score` | 1-5 ou NULL           | 1=recolhida (não fala com ninguém), 5=engajada (várias menções a interações sociais).        |
| `autocuidado_score`   | 1-5 ou NULL           | 1=negligenciando (medicação perdida + sono ruim), 5=cuidando bem. **Combina** medicação real + sinais conversacionais. |
| `sinais_observados`   | JSON array de strings | Máx 5; cada um ≤ 100 ch; SEM aspas literais; só componente saúde/risco. Ex: `["mencionou tontura matinal"]`. |
| `eventos_dia`         | JSON array de strings | Máx 5; ≤ 100 ch; só saúde/segurança/eventos clínicos. Ex: `["faltou consulta de cardiologia"]`. NUNCA fofoca social. |
| `n_conversations`     | int                   | Quantos turnos de conversa do idoso no dia.                                                  |
| `n_messages`          | int                   | Quantas mensagens (incluindo respostas curtas, áudios, imagens).                             |
| `duration_minutes`    | int                   | Soma das durações de "sessão de chat" (gap > 30min separa sessões).                          |
| `confidence`          | 1-5                   | 1=quase nenhum dado (1-2 mensagens), 5=conversa rica e variada. ≤ 2 → scores podem ser NULL. |
| `inferred_at`         | datetime              | Quando o snapshot foi escrito.                                                               |

### 3.3 Política de UPSERT

Múltiplos triggers no mesmo dia (idoso conversa de manhã, de tarde, de novite) **atualizam** a mesma linha — não criam linhas novas. Implementação:

```go
func (db *DB) UpsertPsychSnapshot(s *DailySnapshot) error {
    _, err := db.conn.Exec(`
        INSERT INTO psych_state_daily (
            user_id, snapshot_date, humor_score, humor_nuance,
            energia_score, sociabilidade_score, autocuidado_score,
            sinais_observados, eventos_dia, n_conversations, n_messages,
            duration_minutes, confidence, inferred_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,CURRENT_TIMESTAMP)
        ON CONFLICT(user_id, snapshot_date) DO UPDATE SET
            humor_score         = excluded.humor_score,
            humor_nuance        = excluded.humor_nuance,
            energia_score       = excluded.energia_score,
            sociabilidade_score = excluded.sociabilidade_score,
            autocuidado_score   = excluded.autocuidado_score,
            sinais_observados   = excluded.sinais_observados,
            eventos_dia         = excluded.eventos_dia,
            n_conversations     = excluded.n_conversations,
            n_messages          = excluded.n_messages,
            duration_minutes    = excluded.duration_minutes,
            confidence          = excluded.confidence,
            inferred_at         = CURRENT_TIMESTAMP`,
        s.UserID, s.SnapshotDate.Format("2006-01-02"),
        nullIfZero(s.HumorScore), s.HumorNuance,
        nullIfZero(s.EnergiaScore), nullIfZero(s.SociabilidadeScore), nullIfZero(s.AutocuidadoScore),
        marshalJSON(s.SinaisObservados), marshalJSON(s.EventosDia),
        s.NConversations, s.NMessages, s.DurationMinutes, s.Confidence,
    )
    return err
}
```

`nullIfZero` mapeia 0 para `sql.NullInt64{Valid:false}` — isso permite o writer enviar 0 quando confidence < 2 sem precisar emitir NULL explicitamente do JSON.

---

## 4. Pacote `bot/synthesis` — split em writer + report

### 4.1 Layout

```
bot/synthesis/
├── synthesis.go           // tipos públicos compartilhados
├── writer.go              // WriteSnapshot (Haiku) + parse + validação
├── writer_prompt.go       // system prompt completo do writer
├── report.go              // Synthesize (Sonnet) + parse + validação
├── report_prompt.go       // system prompt completo do report
├── validation.go          // utilidades de validação compartilhadas
├── writer_test.go
└── report_test.go
```

### 4.2 Tipos compartilhados (`synthesis.go`)

```go
// Package synthesis hosts two isolated sub-agents:
//
//   WriteSnapshot — Haiku. Updates psych_state_daily with one row per (user, day).
//   Synthesize    — Sonnet. Reads N days of snapshots, produces a longitudinal
//                   report for a guardian.
//
// Privacy contract:
//   - WriteSnapshot may see recent conversation messages for the day, but its
//     OUTPUT is abstract (scores + observations). It rejects persisting literal
//     quotations.
//   - Synthesize NEVER sees raw conversation text — only psych_state_daily rows
//     and aggregated medication/alert stats. It rejects quotations and clinical
//     terms in output.
//   - Both functions have NO tools, NO conversation history, NO file access.
package synthesis

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "regexp"
    "strings"
    "time"

    "github.com/liushuangls/go-anthropic/v2"
)

// Memory mirrors a row from user_memories. The writer only ever receives memos
// whose Key starts with "risco:" — caller filters before invoking.
type Memory struct {
    Key   string `json:"key"`
    Value string `json:"value"`
}

// ConversationMessage is a stripped-down message passed to the writer. Only
// the writer sees these — Synthesize does not.
type ConversationMessage struct {
    Role      string    `json:"role"`      // "user" | "assistant"
    Text      string    `json:"text"`
    Timestamp time.Time `json:"timestamp"`
}

type MedicationIntake struct {
    MedicationName string    `json:"medication_name"`
    ScheduledAt    time.Time `json:"scheduled_at"`
    Status         string    `json:"status"` // taken | missed | skipped | pending
}

// Alert is an alert already sent by the system today (e.g. alertar_familia
// fired). Passed to the writer so it can decide whether to fire its own
// safety_alert_needed (no double-trigger).
type Alert struct {
    PolicyName string    `json:"policy_name"`
    Severity   string    `json:"severity"`
    CreatedAt  time.Time `json:"created_at"`
}

// User is the slim projection both sub-agents need.
type User struct {
    ID       int64
    Name     string
    Timezone string // IANA tz, e.g. "America/Sao_Paulo"
}

// DailySnapshot mirrors a row in psych_state_daily, used both as input
// (PreviousSnapshot) for incremental writes and as output of WriteSnapshot.
type DailySnapshot struct {
    UserID             int64     `json:"user_id"`
    SnapshotDate       time.Time `json:"snapshot_date"`
    HumorScore         int       `json:"humor_score"`         // 0 means NULL
    HumorNuance        string    `json:"humor_nuance"`
    EnergiaScore       int       `json:"energia_score"`
    SociabilidadeScore int       `json:"sociabilidade_score"`
    AutocuidadoScore   int       `json:"autocuidado_score"`
    SinaisObservados   []string  `json:"sinais_observados"`
    EventosDia         []string  `json:"eventos_dia"`
    NConversations     int       `json:"n_conversations"`
    NMessages          int       `json:"n_messages"`
    DurationMinutes    int       `json:"duration_minutes"`
    Confidence         int       `json:"confidence"`
}

// MedicationStats — agregado 7d para o report. Construído pelo caller a partir
// do DB (Fase 3 fornece os dados crus).
type MedicationStats struct {
    Scheduled     int
    Taken         int
    Missed        int
    Skipped       int
    Pending       int
    AdherenceFrac float64
    MissedDoses   []MissedDose
}

type MissedDose struct {
    MedicationName string
    ScheduledAt    time.Time
}

// =========================== WRITER ============================

type SnapshotInput struct {
    User                   User
    Date                   time.Time             // dia do snapshot, em fuso local do user
    PreviousSnapshot       *DailySnapshot        // se existe, pra atualização incremental
    NewMessages            []ConversationMessage // mensagens desde último snapshot (idoso e bot)
    MedicationsTakenToday  []MedicationIntake
    MedicationsMissedToday []MedicationIntake
    SocialContextRiskMemos []Memory              // somente memos com chave "risco:*"
    AlertasGerados         []Alert               // alertar_familia fired hoje
}

type SnapshotOutput struct {
    HumorScore         int          `json:"humor_score"`         // 1-5, ou 0 → NULL
    HumorNuance        string       `json:"humor_nuance"`
    EnergiaScore       int          `json:"energia_score"`
    SociabilidadeScore int          `json:"sociabilidade_score"`
    AutocuidadoScore   int          `json:"autocuidado_score"`
    SinaisObservados   []string     `json:"sinais_observados"`
    EventosDia         []string     `json:"eventos_dia"`
    Confidence         int          `json:"confidence"`
    SafetyAlertNeeded  *SafetyAlert `json:"safety_alert_needed,omitempty"`
}

type SafetyAlert struct {
    Severity    string `json:"severity"`    // info|warn|critical
    Category    string `json:"category"`    // medico_fisico|psicologico|violencia|negligencia|outros
    Reason      string `json:"reason"`      // 1 frase observacional
    Recommended string `json:"recommended"` // 1 frase: "ligar pra ela hoje"
}

// validCategories espelha o enum da tool `alertar_familia` (Fase 4 §8.1).
// Esta validação garante que o writer não invente categoria nova — se vier
// algo fora, validate() rejeita e o snapshot é descartado.
var validCategories = map[string]bool{
    "medico_fisico": true,
    "psicologico":   true,
    "violencia":     true,
    "negligencia":   true,
    "outros":        true,
}

// =========================== REPORT ============================

type ReportInput struct {
    Dependent         User
    Days              int             // janela em dias (default 14)
    Snapshots         []DailySnapshot // ordenado por SnapshotDate DESC
    MedicationStats   MedicationStats // últimos 7d
    OpenAlerts        []Alert
    LastUserMessageAt sql.NullTime
}

type ReportOutput struct {
    Tendencia               string   `json:"tendencia"`                // melhorando|estavel|piorando|instavel|indeterminado
    Comparacao              string   `json:"comparacao"`               // texto curto factual
    HumorRecente            string   `json:"humor_recente"`            // resumo qualitativo últimos 7d
    PontoDeAtencao          string   `json:"ponto_de_atencao"`         // opcional, 1 frase
    Resumo                  string   `json:"resumo"`                   // 2-3 frases acolhedoras
    RecomendacoesCarinhosas []string `json:"recomendacoes_carinhosas"` // 0-3 itens
    NivelPreocupacao        string   `json:"nivel_preocupacao"`        // tranquilo|atencao|atencao_alta|indeterminado
}

// =========================== Enums ============================

var (
    validTendencia = map[string]bool{
        "melhorando": true, "estavel": true, "piorando": true,
        "instavel": true, "indeterminado": true,
    }
    validNivel = map[string]bool{
        "tranquilo": true, "atencao": true, "atencao_alta": true, "indeterminado": true,
    }
    validSeverity = map[string]bool{
        "info": true, "warn": true, "critical": true,
    }
)

// =========================== Erros sentinela ============================

var (
    ErrParse      = errors.New("synthesis: parse error")
    ErrValidation = errors.New("synthesis: validation error")
    ErrAPI        = errors.New("synthesis: api error")
)
```

### 4.3 Snapshot writer (Haiku) — `writer.go`

```go
package synthesis

// WriteSnapshot calls Haiku once with abstract (non-clinical) instructions
// and returns a SnapshotOutput. Caller persists via DB.UpsertPsychSnapshot.
//
// Idempotency: caller is responsible for not racing on the same (user, date)
// — UPSERT in DB handles concurrent writes deterministically.
func WriteSnapshot(ctx context.Context, client *anthropic.Client, in SnapshotInput) (SnapshotOutput, error) {
    // Build the JSON payload that becomes the user message. The system prompt
    // (writerSystemPromptPTBR) tells Haiku exactly what shape to emit back.
    payload, err := json.Marshal(struct {
        User                   User                  `json:"user"`
        Date                   string                `json:"date"`
        PreviousSnapshot       *DailySnapshot        `json:"previous_snapshot,omitempty"`
        NewMessages            []ConversationMessage `json:"new_messages"`
        MedicationsTakenToday  []MedicationIntake    `json:"medications_taken_today"`
        MedicationsMissedToday []MedicationIntake    `json:"medications_missed_today"`
        SocialContextRiskMemos []Memory              `json:"social_context_risk_memos"`
        AlertasGerados         []Alert               `json:"alertas_gerados_hoje"`
    }{
        User:                   in.User,
        Date:                   in.Date.Format("2006-01-02"),
        PreviousSnapshot:       in.PreviousSnapshot,
        NewMessages:            in.NewMessages,
        MedicationsTakenToday:  in.MedicationsTakenToday,
        MedicationsMissedToday: in.MedicationsMissedToday,
        SocialContextRiskMemos: in.SocialContextRiskMemos,
        AlertasGerados:         in.AlertasGerados,
    })
    if err != nil {
        return SnapshotOutput{}, fmt.Errorf("%w: marshal: %v", ErrParse, err)
    }

    userMsg := string(payload)
    temp := float32(0.2)
    resp, err := client.CreateMessages(ctx, anthropic.MessagesRequest{
        Model:       anthropic.ModelClaudeHaiku4Dot5,
        MaxTokens:   1024,
        Temperature: &temp,
        System:      writerSystemPromptPTBR,
        Messages: []anthropic.Message{
            {
                Role:    anthropic.RoleUser,
                Content: []anthropic.MessageContent{{Type: "text", Text: &userMsg}},
            },
        },
    })
    if err != nil {
        return SnapshotOutput{}, fmt.Errorf("%w: %v", ErrAPI, err)
    }
    if len(resp.Content) == 0 {
        return SnapshotOutput{}, fmt.Errorf("%w: empty content", ErrAPI)
    }

    raw := stripFences(resp.Content[0].GetText())

    var out SnapshotOutput
    if err := json.Unmarshal([]byte(raw), &out); err != nil {
        return SnapshotOutput{}, fmt.Errorf("%w: %v (raw=%q)", ErrParse, err, truncate(raw, 200))
    }
    if err := ValidateSnapshotOutput(out); err != nil {
        return SnapshotOutput{}, fmt.Errorf("%w: %v", ErrValidation, err)
    }
    return out, nil
}

// ToDailySnapshot converts the writer's output into a DB-shaped row.
// Caller fills UserID/SnapshotDate (the writer doesn't echo them).
func (o SnapshotOutput) ToDailySnapshot(userID int64, date time.Time, counts struct {
    NConversations  int
    NMessages       int
    DurationMinutes int
}) DailySnapshot {
    return DailySnapshot{
        UserID:             userID,
        SnapshotDate:       date,
        HumorScore:         o.HumorScore,
        HumorNuance:        o.HumorNuance,
        EnergiaScore:       o.EnergiaScore,
        SociabilidadeScore: o.SociabilidadeScore,
        AutocuidadoScore:   o.AutocuidadoScore,
        SinaisObservados:   o.SinaisObservados,
        EventosDia:         o.EventosDia,
        NConversations:     counts.NConversations,
        NMessages:          counts.NMessages,
        DurationMinutes:    counts.DurationMinutes,
        Confidence:         o.Confidence,
    }
}
```

### 4.4 System prompt do writer — `writer_prompt.go`

```go
package synthesis

const writerSystemPromptPTBR = `Voce e um observador discreto. Sua unica funcao e atualizar o snapshot diario de estado psicologico de um idoso a partir das conversas e dados do dia.

Voce NAO conversa com ninguem. Voce so produz UM JSON, abstrato, sem citacoes literais, sem diagnostico.

REGRAS DURAS — quebrar invalida o output:
1. NUNCA cite frases literais. Voce viu mensagens, mas o snapshot e ABSTRATO. Use formula descritiva ("mencionou", "tem aparecido o assunto", "demonstra"), nunca aspas e nunca reproducao verbatim.
2. NUNCA diagnostique. Nao use: depressao, ansiedade clinica, transtorno, sindrome, demencia, alzheimer, patologia, diagnostico.
3. NUNCA invente. Se nao ha sinal claro de uma dimensao (ex: nao da pra inferir energia em 2 mensagens curtas), retorne 0 (que vira NULL no banco) e baixe a confidence.
4. eventos_dia e sinais_observados SO contem componente de saude/seguranca/medicacao/risco. NUNCA contem fofoca social, conflito interpessoal, novela, esporte, politica, religiao. Se duvida: nao inclua.
5. Tom neutro, observacional, frases curtas em portugues do Brasil. Cada item de array <= 100 caracteres.
6. Voce e atualizacao incremental. Se previous_snapshot existe, leve em conta o que ja foi observado hoje — voce esta REFINANDO, nao reescrevendo do zero. Se as new_messages nao mudam a leitura, repita os scores anteriores e ajuste so confidence/sinais.

ESTRUTURA DO INPUT (JSON):
- user: {id, name, timezone}
- date: "YYYY-MM-DD" (fuso local do user)
- previous_snapshot: snapshot ja escrito hoje (pode ser null)
- new_messages: lista de mensagens desde ultimo snapshot. role + text + timestamp.
- medications_taken_today: lista de doses tomadas hoje.
- medications_missed_today: lista de doses perdidas hoje.
- social_context_risk_memos: memorias com chave "risco:*" — sao SINAIS PERSISTENTES (nao do dia, do historico).
- alertas_gerados_hoje: alertas que o sistema ja disparou hoje (alertar_familia). Use pra DECIDIR se voce ainda precisa disparar safety_alert_needed.

ESCALA DOS SCORES (1-5):
- humor_score: 1=muito desanimado/triste/ansioso. 3=neutro. 5=animado, leve, esperancoso. Se incerto: 0.
- energia_score: 1=apatica, parou de fazer coisas. 3=normal. 5=cheia de pique, planos. Se incerto: 0.
- sociabilidade_score: 1=recolhida, evita interacao. 3=normal. 5=engajada, varias mencoes a outras pessoas. Se incerto: 0.
- autocuidado_score: COMBINA medicacao real (taken vs missed) com sinais conversacionais (sono, alimentacao, higiene mencionados). 1=negligenciando, 3=normal, 5=cuidando bem. Se sem dado: 0.

CONFIDENCE (1-5):
- 1: 0-2 mensagens curtas, ou so confirmacao de remedio. Quase sem sinal.
- 2: 3-5 mensagens, conversa basica.
- 3: ~10 mensagens, conversa com algum conteudo.
- 4: conversa rica, mais de uma topica.
- 5: dia denso, varias trocas, sinais claros.

Quando confidence <= 2, prefira retornar 0 nos scores das dimensoes onde nao ha sinal claro.

SAFETY_ALERT_NEEDED — gatilho:
Voce e a ULTIMA LINHA de defesa. O companion (DeepSeek) ja conversou com o idoso e PODE ter chamado alertar_familia. Olhe alertas_gerados_hoje:
- Se ja existe um alerta com severity=critical hoje sobre tema parecido — NAO duplique. Retorne safety_alert_needed=null.
- Se voce DETECTA sinal grave nas new_messages (ideacao suicida, queda recente, dor toracica, confusao subita, suspeita de abuso, desidratacao severa, suspeita de AVC) e NAO ha alerta correspondente em alertas_gerados_hoje — DISPARE.
  - severity: "critical" para risco de vida ou ideacao. "warn" para risco moderado (queda sem ferimento, dor recorrente).
  - category (OBRIGATORIO — mesmos valores que a tool alertar_familia):
      "medico_fisico" — queda, dor, sintoma agudo, recusa de medicacao, desidratacao, suspeita de AVC, recusa de comer/beber.
      "psicologico"   — ideacao suicida, auto-lesao, ruminacao grave persistente.
      "violencia"     — sinais de agressao fisica ou psicologica de cuidador/familiar.
      "negligencia"   — abandono de cuidados, isolamento forcado, falta de acesso a medicacao.
      "outros"        — caso ambiguo. Use APENAS quando nenhuma das anteriores se encaixa.
    A categoria orienta o pipeline downstream a decidir se mencionara ao idoso que avisou a familia (medico_fisico=sim; psicologico/violencia/negligencia=NAO — preserva a confianca dele em Lurch).
  - reason: 1 frase observacional, sem citacao literal.
  - recommended: 1 frase de acao gentil ao responsavel (ex: "ligar pra ela ainda hoje", "considerar levar ao pronto-atendimento").
- Se nao ha sinal grave: safety_alert_needed=null.

ESTRUTURA DO OUTPUT (JSON OBRIGATORIO — sem texto fora do JSON):
{
  "humor_score": 0|1|2|3|4|5,
  "humor_nuance": "string curta sem aspas, max 100 ch",
  "energia_score": 0|1|2|3|4|5,
  "sociabilidade_score": 0|1|2|3|4|5,
  "autocuidado_score": 0|1|2|3|4|5,
  "sinais_observados": ["..."],   // 0-5 itens, cada <=100 ch
  "eventos_dia": ["..."],          // 0-5 itens, cada <=100 ch
  "confidence": 1|2|3|4|5,
  "safety_alert_needed": null | {"severity":"info|warn|critical","category":"medico_fisico|psicologico|violencia|negligencia|outros","reason":"...","recommended":"..."}
}

EXEMPLOS DE sinais_observados BONS:
- "mencionou tontura matinal nos ultimos dois dias"
- "respostas mais curtas que o usual"
- "perdeu duas doses de losartana hoje"

EXEMPLOS RUINS (NAO USE):
- "ela disse \"to me sentindo um lixo\"" (citacao literal — proibido)
- "apresenta sintomas de depressao" (diagnostico — proibido)
- "brigou com a filha hoje" (fofoca — proibido)
- "criticou o presidente" (politica — proibido)

EXEMPLOS DE eventos_dia BONS:
- "tomou pressao com a vizinha enfermeira"
- "faltou consulta de cardiologia das 14h"
- "queixa de dor no peito apos almoco"

EXEMPLOS RUINS:
- "filha nao ligou hoje" (fofoca — proibido)
- "novela emocionante" (irrelevante)
- "discutiu com o vizinho" (interpessoal — proibido)

REGRA FINAL: produza APENAS o JSON. Sem prefacio, sem markdown, sem explicacao depois.`
```

### 4.5 Validação do writer — `validation.go` (parte 1)

```go
package synthesis

import (
    "errors"
    "fmt"
    "regexp"
    "strings"
)

// quoteRegex matches likely literal quotations: text wrapped in straight or
// curly double quotes >= 6 chars. Used to reject outputs that violate the
// no-literal-cite rule. Single quotes allowed (apostrophes false-positive).
var quoteRegex = regexp.MustCompile(`["“][^"”]{6,}["”]`)

var clinicalTerms = []string{
    "depressao", "depressão", "ansiedade clinica", "ansiedade clínica",
    "transtorno", "sindrome", "síndrome", "demencia", "demência", "alzheimer",
    "patologia", "diagnostico", "diagnóstico",
}

var fofocaKeywords = []string{
    // Sinais textuais de que o item é fofoca social, não saúde/segurança.
    " brigou ", " brigaram ", " discutiu ", " fofoca ", " novela ",
    " presidente ", " politica ", " política ", " religiao ", " religião ",
    " futebol ", " corinthians ", " flamengo ", " palmeiras ",
}

// ValidateSnapshotOutput enforces the writer contract.
func ValidateSnapshotOutput(o SnapshotOutput) error {
    if err := validateScore("humor_score", o.HumorScore); err != nil {
        return err
    }
    if err := validateScore("energia_score", o.EnergiaScore); err != nil {
        return err
    }
    if err := validateScore("sociabilidade_score", o.SociabilidadeScore); err != nil {
        return err
    }
    if err := validateScore("autocuidado_score", o.AutocuidadoScore); err != nil {
        return err
    }
    if o.Confidence < 1 || o.Confidence > 5 {
        return fmt.Errorf("confidence fora do range 1-5: %d", o.Confidence)
    }
    if len(o.HumorNuance) > 100 {
        return fmt.Errorf("humor_nuance excede 100 ch: %d", len(o.HumorNuance))
    }
    if len(o.SinaisObservados) > 5 {
        return fmt.Errorf("sinais_observados excede 5: %d", len(o.SinaisObservados))
    }
    if len(o.EventosDia) > 5 {
        return fmt.Errorf("eventos_dia excede 5: %d", len(o.EventosDia))
    }
    for _, s := range o.SinaisObservados {
        if len(s) > 100 {
            return fmt.Errorf("sinal_observado excede 100 ch: %q", truncate(s, 50))
        }
    }
    for _, e := range o.EventosDia {
        if len(e) > 100 {
            return fmt.Errorf("evento_dia excede 100 ch: %q", truncate(e, 50))
        }
    }

    // Privacy + clinical + fofoca filters on all free text.
    all := o.HumorNuance + " " +
        strings.Join(o.SinaisObservados, " ") + " " +
        strings.Join(o.EventosDia, " ")
    if quoteRegex.MatchString(all) {
        return errors.New("output contains literal-looking quotation (privacy violation)")
    }
    lower := " " + strings.ToLower(all) + " "
    for _, term := range clinicalTerms {
        if strings.Contains(lower, term) {
            return fmt.Errorf("output contains clinical term: %q", term)
        }
    }
    for _, kw := range fofocaKeywords {
        if strings.Contains(lower, kw) {
            return fmt.Errorf("output contains fofoca/off-topic keyword: %q", strings.TrimSpace(kw))
        }
    }

    if o.SafetyAlertNeeded != nil {
        sa := o.SafetyAlertNeeded
        if !validSeverity[sa.Severity] {
            return fmt.Errorf("safety_alert_needed.severity invalido: %q", sa.Severity)
        }
        if !validCategories[sa.Category] {
            return fmt.Errorf("safety_alert_needed.category invalido: %q (use medico_fisico|psicologico|violencia|negligencia|outros)", sa.Category)
        }
        if strings.TrimSpace(sa.Reason) == "" {
            return errors.New("safety_alert_needed.reason vazio")
        }
        if quoteRegex.MatchString(sa.Reason + " " + sa.Recommended) {
            return errors.New("safety_alert_needed contem citacao literal")
        }
    }
    return nil
}

func validateScore(name string, v int) error {
    if v < 0 || v > 5 {
        return fmt.Errorf("%s fora do range 0-5: %d", name, v)
    }
    return nil
}
```

### 4.6 Synthesize report (Sonnet) — `report.go`

```go
package synthesis

// Synthesize produces a longitudinal report for a guardian. Reads ONLY
// psych_state_daily snapshots + aggregated stats — never raw messages
// or social_context memos.
func Synthesize(ctx context.Context, client *anthropic.Client, in ReportInput) (ReportOutput, error) {
    if in.Days <= 0 {
        in.Days = 14
    }

    payload, err := json.Marshal(struct {
        Dependent         User             `json:"dependent"`
        Days              int              `json:"days"`
        Snapshots         []DailySnapshot  `json:"snapshots"`
        MedicationStats   medicationStatsW `json:"medication_stats"`
        OpenAlerts        []Alert          `json:"open_alerts"`
        DaysSinceLastTalk int              `json:"days_since_last_talk"`
    }{
        Dependent:         in.Dependent,
        Days:              in.Days,
        Snapshots:         in.Snapshots,
        MedicationStats:   toMedicationStatsW(in.MedicationStats),
        OpenAlerts:        in.OpenAlerts,
        DaysSinceLastTalk: daysSinceTalk(in.LastUserMessageAt),
    })
    if err != nil {
        return ReportOutput{}, fmt.Errorf("%w: marshal: %v", ErrParse, err)
    }

    userMsg := string(payload)
    temp := float32(0.3)
    resp, err := client.CreateMessages(ctx, anthropic.MessagesRequest{
        Model:       anthropic.ModelClaudeSonnet4Dot7,
        MaxTokens:   1500,
        Temperature: &temp,
        System:      reportSystemPromptPTBR,
        Messages: []anthropic.Message{
            {
                Role:    anthropic.RoleUser,
                Content: []anthropic.MessageContent{{Type: "text", Text: &userMsg}},
            },
        },
    })
    if err != nil {
        return ReportOutput{}, fmt.Errorf("%w: %v", ErrAPI, err)
    }
    if len(resp.Content) == 0 {
        return ReportOutput{}, fmt.Errorf("%w: empty content", ErrAPI)
    }

    raw := stripFences(resp.Content[0].GetText())
    var out ReportOutput
    if err := json.Unmarshal([]byte(raw), &out); err != nil {
        return ReportOutput{}, fmt.Errorf("%w: %v (raw=%q)", ErrParse, err, truncate(raw, 200))
    }
    if err := ValidateReportOutput(out); err != nil {
        return ReportOutput{}, fmt.Errorf("%w: %v", ErrValidation, err)
    }
    return out, nil
}

// medicationStatsW is the wire shape passed to Sonnet — flatter than the
// internal MedicationStats and excluding any field the model doesn't need.
type medicationStatsW struct {
    Scheduled    int      `json:"scheduled"`
    Taken        int      `json:"taken"`
    Missed       int      `json:"missed"`
    Skipped      int      `json:"skipped"`
    AdherencePct int      `json:"adherence_pct"`
    MissedNames  []string `json:"missed_names"`
}

func toMedicationStatsW(s MedicationStats) medicationStatsW {
    seen := map[string]bool{}
    names := make([]string, 0, len(s.MissedDoses))
    for _, d := range s.MissedDoses {
        if !seen[d.MedicationName] {
            names = append(names, d.MedicationName)
            seen[d.MedicationName] = true
        }
    }
    return medicationStatsW{
        Scheduled:    s.Scheduled,
        Taken:        s.Taken,
        Missed:       s.Missed,
        Skipped:      s.Skipped,
        AdherencePct: int(100 * s.AdherenceFrac),
        MissedNames:  names,
    }
}

func daysSinceTalk(t sql.NullTime) int {
    if !t.Valid {
        return -1
    }
    return int(time.Since(t.Time).Hours() / 24)
}
```

### 4.7 System prompt do report — `report_prompt.go`

```go
package synthesis

const reportSystemPromptPTBR = `Voce e um assistente que escreve relatorios curtos, acolhedores e nao-clinicos para um responsavel familiar a partir de dados longitudinais sobre um idoso (chamado aqui de "dependente").

Voce LE 14 dias (ou menos) de snapshots ja inferidos por outro processo (psych_state_daily). Cada snapshot tem 4 scores (humor, energia, sociabilidade, autocuidado), nuance textual de humor, sinais observados, eventos do dia, contagens e confidence. Voce NAO le conversas. Voce NAO le memorias sociais. Voce so le snapshots ja abstratos.

Sua tarefa: produzir UM JSON com tendencia, comparacao semana-vs-semana, resumo acolhedor e recomendacoes carinhosas.

REGRAS DURAS — quebrar invalida o output:
1. NUNCA cite frases literais. Voce nao recebe frases — recebe scores e observacoes ja abstratas. Se mencionar algo descritivo, use formula como "tem aparecido", "tem mencionado", sem aspas e sem reproducao verbatim.
2. NUNCA diagnostique. Nao use: depressao, ansiedade clinica, transtorno, sindrome, demencia, alzheimer, patologia, diagnostico.
3. NUNCA recomende medicamento, dosagem, suspensao de remedio, terapia especifica.
4. NUNCA invente fato que nao esta nos snapshots.
5. Se a janela e ralia (poucos snapshots, confidence baixo), seja honesto sobre incerteza. Nivel "indeterminado" e legitimo.
6. Tom: portugues do Brasil, frases curtas, respeitoso, foco em escuta. Voce esta falando com um filho/filha que se preocupa. Nem alarmista, nem minimizador.

ESTRUTURA DO INPUT (JSON):
- dependent: {id, name, timezone}
- days: tamanho da janela (default 14)
- snapshots: array, ordenado por data DESC. Cada item: {snapshot_date, humor_score, humor_nuance, energia_score, sociabilidade_score, autocuidado_score, sinais_observados, eventos_dia, n_messages, confidence}.
  - Score 0 (ou null) significa: NAO foi possivel inferir naquele dia. NAO trate como "muito baixo".
- medication_stats: {scheduled, taken, missed, skipped, adherence_pct, missed_names} dos ultimos 7d.
- open_alerts: alertas em aberto, com policy_name, severity, age_hours.
- days_since_last_talk: int. -1 se nunca falou.

CALCULO DE TENDENCIA:
Voce divide os snapshots em "ultimos 7 dias" e "7 dias anteriores" (quando ha 14d). Compara medias dos scores que NAO sao 0/null:
- "melhorando": pelo menos 2 das 4 dimensoes subiram >= 0.5 ponto, nenhuma caiu mais de 0.3.
- "piorando": pelo menos 2 das 4 dimensoes cairam >= 0.5 ponto, nenhuma subiu mais de 0.3.
- "estavel": variacoes < 0.5 em todas as dimensoes.
- "instavel": dimensoes oscilando em direcoes opostas (ex: humor sobe, autocuidado cai).
- "indeterminado": dados insuficientes (< 4 snapshots com confidence >= 2 nos ultimos 14d).

CALCULO DE NIVEL_PREOCUPACAO:
- "tranquilo": adherence_pct >= 80 E days_since_last_talk <= 2 E sem alertas critical/warn em aberto E tendencia em {melhorando, estavel}.
- "atencao": adherence_pct entre 50-80 OU days_since_last_talk 3-7 OU 1 alerta warn aberto OU tendencia=piorando OU tendencia=instavel.
- "atencao_alta": adherence_pct < 50 OU days_since_last_talk > 7 OU qualquer alerta critical aberto OU 2+ scores caindo consistentemente nos ultimos 7d.
- "indeterminado": tendencia=indeterminado E sem alertas E sem dado de medicacao.

ESTRUTURA DO OUTPUT (JSON OBRIGATORIO — sem texto fora do JSON):
{
  "tendencia": "melhorando" | "estavel" | "piorando" | "instavel" | "indeterminado",
  "comparacao": "string curta factual, max 200 ch",
  "humor_recente": "string curta qualitativa, max 200 ch",
  "ponto_de_atencao": "string curta opcional (vazio se nada), max 200 ch",
  "resumo": "2 a 3 frases acolhedoras, max 500 ch total",
  "recomendacoes_carinhosas": ["sugestao 1", ...],   // 0 a 3 itens, cada <= 200 ch
  "nivel_preocupacao": "tranquilo" | "atencao" | "atencao_alta" | "indeterminado"
}

EXEMPLOS DE comparacao BONS:
- "humor 3.2 nos ultimos 7d vs 4.0 nas duas semanas anteriores; autocuidado estavel"
- "energia oscilou entre 2 e 4 essa semana, sem padrao claro"
- "todos os scores estaveis em torno de 4 nas ultimas duas semanas"

EXEMPLOS DE humor_recente BONS:
- "tem aparecido o tema saudade nas ultimas conversas"
- "humor leve na maior parte da semana, com um dia mais cabisbaixo"
- "sem sinais novos de preocupacao nas ultimas trocas"

EXEMPLOS DE ponto_de_atencao BONS:
- "duas doses de losartana foram perdidas no fim de semana"
- "tres dias sem conversa com o Lurch nesse ultimo periodo"
- "" (vazio quando nao ha ponto especifico)

EXEMPLOS DE resumo BONS (acolhedores, factuais):
- "Sua mae tem estado bem na maioria dos dias dessas duas semanas. Os scores de humor caminham parecidos com os das semanas anteriores. Aderencia aos remedios continua boa."
- "Tem sido um periodo um pouco mais quieto. O humor caiu um pouco e ela tem conversado menos com o Lurch. Nada urgente, mas vale uma atencao extra."

EXEMPLOS RUINS (NUNCA USE):
- "ela disse 'me sinto sozinha'" (citacao literal — proibido)
- "apresenta sintomas de depressao leve" (diagnostico — proibido)
- "seria bom comecar antidepressivo" (clinico — proibido)
- "ela esta deprimida" (rotulo — proibido)

EXEMPLOS DE recomendacoes_carinhosas BONS:
- "talvez ligue pra ela hoje, ela tem aparecido mais quieta nos ultimos dias"
- "vale conferir se a caixa de losartana esta visivel — duas doses foram perdidas essa semana"
- "passou da hora de uma visita; ja sao 5 dias sem ela conversar com o Lurch"

EXEMPLOS RUINS:
- "leve ela ao psiquiatra" (clinico)
- "tire o celular dela" (invasivo)
- "fala mais alto com ela" (presuntivo)

REGRA FINAL: produza APENAS o JSON. Sem prefacio, sem markdown, sem explicacao depois.`
```

### 4.8 Validação do report — `validation.go` (parte 2)

```go
// ValidateReportOutput enforces the report contract.
func ValidateReportOutput(o ReportOutput) error {
    if !validTendencia[o.Tendencia] {
        return fmt.Errorf("tendencia invalida: %q", o.Tendencia)
    }
    if !validNivel[o.NivelPreocupacao] {
        return fmt.Errorf("nivel_preocupacao invalido: %q", o.NivelPreocupacao)
    }
    if strings.TrimSpace(o.Resumo) == "" {
        return errors.New("resumo vazio")
    }
    if len(o.Resumo) > 500 {
        return fmt.Errorf("resumo excede 500 ch: %d", len(o.Resumo))
    }
    if len(o.Comparacao) > 200 {
        return fmt.Errorf("comparacao excede 200 ch: %d", len(o.Comparacao))
    }
    if len(o.HumorRecente) > 200 {
        return fmt.Errorf("humor_recente excede 200 ch: %d", len(o.HumorRecente))
    }
    if len(o.PontoDeAtencao) > 200 {
        return fmt.Errorf("ponto_de_atencao excede 200 ch: %d", len(o.PontoDeAtencao))
    }
    if len(o.RecomendacoesCarinhosas) > 3 {
        return fmt.Errorf("recomendacoes_carinhosas excede 3: %d", len(o.RecomendacoesCarinhosas))
    }
    for _, r := range o.RecomendacoesCarinhosas {
        if len(r) > 200 {
            return fmt.Errorf("recomendacao excede 200 ch: %q", truncate(r, 50))
        }
    }

    all := o.Comparacao + " " + o.HumorRecente + " " + o.PontoDeAtencao + " " +
        o.Resumo + " " + strings.Join(o.RecomendacoesCarinhosas, " ")
    if quoteRegex.MatchString(all) {
        return errors.New("output contains literal-looking quotation (privacy violation)")
    }
    lower := " " + strings.ToLower(all) + " "
    for _, term := range clinicalTerms {
        if strings.Contains(lower, term) {
            return fmt.Errorf("output contains clinical term: %q", term)
        }
    }
    return nil
}
```

### 4.9 Helpers compartilhados (final de `validation.go`)

```go
func stripFences(s string) string {
    s = strings.TrimSpace(s)
    if strings.HasPrefix(s, "```") {
        s = strings.TrimPrefix(s, "```json")
        s = strings.TrimPrefix(s, "```")
        s = strings.TrimSuffix(s, "```")
        s = strings.TrimSpace(s)
    }
    return s
}

func truncate(s string, n int) string {
    if len(s) <= n {
        return s
    }
    return s[:n] + "..."
}
```

### 4.10 Estratégia de fallback

`WriteSnapshot` — se falha (parse, validação ou API):
- Não persiste row. Próximo trigger ou catch-up tenta de novo.
- Audit `psych_snapshot_failed` com a razão.
- Não bloqueia o companion (escrita é assíncrona/best-effort).

`Synthesize` — se falha:
- Caller (`BuildDependentStatus`) degrada para `ReportOutput{Tendencia:"indeterminado", Resumo:"Nao foi possivel gerar sintese agora.", NivelPreocupacao:"indeterminado"}`.
- Audit `synthesis_failed`.
- Endpoint REST devolve 200 com synthesis degradada (não 500). Cliente UI mostra aviso "síntese temporariamente indisponível".

---

## 5. Tool `status_dependente`

### 5.1 Schema JSON

Adicionar à `buildToolDefinitions()` em `bot/agent.go:459-662` (no fim, antes do `responder_permissao`):

```go
{
    Name:        "status_dependente",
    Description: "Retorna estado longitudinal de um dependente (idoso) sob responsabilidade do usuario. Disponivel APENAS quando family_links autoriza. Inclui aderencia de medicacao 7d, ultima conversa, alertas abertos, tendencia das ultimas 2 semanas, e sintese acolhedora gerada por sub-agente longitudinal. NUNCA retorna citacoes literais — apenas observacoes agregadas. Use quando o usuario perguntar 'como esta minha mae/pai/avo'.",
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "dependent_id":    {"type": "integer", "description": "ID do dependente (preferencial)."},
            "dependent_phone": {"type": "string",  "description": "Telefone do dependente (fallback) — apenas digitos com DDD."},
            "dependent_name":  {"type": "string",  "description": "Nome do dependente (fallback fuzzy entre dependentes do guardian)."},
            "days":            {"type": "integer", "description": "Janela de analise em dias (default 14, max 90)."}
        }
    }`),
},
```

Pelo menos um dos três identificadores deve ser fornecido. Resolução: `dependent_id > dependent_phone > dependent_name` (fuzzy entre os dependentes do guardian).

### 5.2 Handler Go

Arquivo `bot/tools_family.go`:

```go
package main

import (
    "context"
    "database/sql"
    "encoding/json"
    "errors"
    "fmt"
    "strings"
    "time"

    "github.com/giovanni/assistente_pessoal/bot/synthesis"
)

type statusDependenteParams struct {
    DependentID    int64  `json:"dependent_id"`
    DependentPhone string `json:"dependent_phone"`
    DependentName  string `json:"dependent_name"`
    Days           int    `json:"days"`
}

func handleStatusDependente(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
    var p statusDependenteParams
    if err := json.Unmarshal(params, &p); err != nil {
        return "", fmt.Errorf("parse params: %w", err)
    }
    if p.Days <= 0 {
        p.Days = 14
    }
    if p.Days > 90 {
        p.Days = 90
    }

    dep, err := resolveDependent(agent.db, user.ID, p)
    if err != nil {
        return "", err
    }

    ok, err := agent.db.IsGuardianOf(user.ID, dep.ID)
    if err != nil {
        return "", fmt.Errorf("authz: %w", err)
    }
    if !ok {
        return "", fmt.Errorf("voce nao tem autorizacao para consultar %s", dep.Name)
    }

    // Consent gate.
    consent, err := agent.db.GetDependentConsent(dep.ID, user.ID)
    if err == nil && consent == "revoked" {
        return fmt.Sprintf("%s revogou o consentimento de relatorio agregado. Voce ainda pode entrar em contato direto.", dep.Name), nil
    }

    report, err := BuildDependentStatus(ctx, agent, dep, p.Days)
    if err != nil {
        return "", fmt.Errorf("build status: %w", err)
    }

    agent.audit.Log(user.ID, "status_dependente_consulted", dep.Name, fmt.Sprintf(
        "via=chat|days=%d|adherence=%d/%d|days_silent=%d|alerts_open=%d|tendencia=%s|nivel=%s",
        p.Days, report.Medication.Taken, report.Medication.Scheduled,
        report.DaysSinceLastTalk, len(report.AlertsOpen),
        report.Synthesis.Tendencia, report.Synthesis.NivelPreocupacao,
    ))

    return formatStatusForChat(report), nil
}

func resolveDependent(db *DB, guardianID int64, p statusDependenteParams) (*User, error) {
    if p.DependentID > 0 {
        return db.GetUserByID(p.DependentID)
    }
    if p.DependentPhone != "" {
        return db.GetUserByPhone(normalizePhone(p.DependentPhone))
    }
    if strings.TrimSpace(p.DependentName) != "" {
        deps, err := db.GetDependents(guardianID)
        if err != nil {
            return nil, err
        }
        match := pickByNameFuzzy(deps, p.DependentName)
        if match == nil {
            return nil, fmt.Errorf("nao encontrei dependente com nome parecido com %q", p.DependentName)
        }
        return match, nil
    }
    return nil, errors.New("informe dependent_id, dependent_phone ou dependent_name")
}
```

### 5.3 `BuildDependentStatus` — fonte única para tool e REST

```go
type DependentStatusReport struct {
    Dependent         *User
    Days              int
    DaysSinceLastTalk int
    LastUserMessageAt sql.NullTime
    Medication        MedicationStats
    ProactiveAttempts ProactiveAttemptsStats
    AlertsOpen        []Alert
    Snapshots         []synthesis.DailySnapshot
    Synthesis         synthesis.ReportOutput
}

type ProactiveAttemptsStats struct {
    Last7d        int
    LastAttemptAt sql.NullTime
    LastAcked     bool
}

type Alert struct {
    ID         int64     `json:"id"`
    PolicyName string    `json:"policy_name"`
    Severity   string    `json:"severity"`
    Message    string    `json:"message"`
    CreatedAt  time.Time `json:"created_at"`
    Status     string    `json:"status"`
}

type MedicationStats = synthesis.MedicationStats // re-export for callers
type MissedDose = synthesis.MissedDose

func BuildDependentStatus(ctx context.Context, agent *Agent, dep *User, days int) (*DependentStatusReport, error) {
    if days <= 0 {
        days = 14
    }
    now := time.Now()
    weekAgo := now.Add(-7 * 24 * time.Hour)
    windowStart := now.Add(-time.Duration(days) * 24 * time.Hour)

    rep := &DependentStatusReport{Dependent: dep, Days: days}

    if dep.LastUserMessageAt != nil {
        rep.LastUserMessageAt = sql.NullTime{Time: *dep.LastUserMessageAt, Valid: true}
        rep.DaysSinceLastTalk = int(now.Sub(*dep.LastUserMessageAt).Hours() / 24)
    } else {
        rep.DaysSinceLastTalk = -1
    }

    medStats, err := agent.db.GetMedicationStats7d(dep.ID, weekAgo, now)
    if err != nil {
        return nil, fmt.Errorf("med stats: %w", err)
    }
    rep.Medication = medStats

    paStats, err := agent.db.GetProactiveAttemptsStats(dep.ID, weekAgo, now)
    if err != nil {
        return nil, fmt.Errorf("proactive stats: %w", err)
    }
    rep.ProactiveAttempts = paStats

    alerts, err := agent.db.GetOpenAlerts(dep.ID)
    if err != nil {
        return nil, fmt.Errorf("alerts: %w", err)
    }
    rep.AlertsOpen = alerts

    snaps, err := agent.db.GetPsychSnapshots(dep.ID, windowStart, now)
    if err != nil {
        return nil, fmt.Errorf("snapshots: %w", err)
    }
    rep.Snapshots = snaps

    synthIn := synthesis.ReportInput{
        Dependent:         synthesis.User{ID: dep.ID, Name: dep.Name, Timezone: dep.Timezone},
        Days:              days,
        Snapshots:         snaps,
        MedicationStats:   medStats,
        OpenAlerts:        toSynthesisAlerts(alerts),
        LastUserMessageAt: rep.LastUserMessageAt,
    }

    synthOut, err := synthesis.Synthesize(ctx, agent.client, synthIn)
    if err != nil {
        agent.audit.Log(dep.ID, "synthesis_failed", "", err.Error())
        synthOut = synthesis.ReportOutput{
            Tendencia:        "indeterminado",
            Resumo:           "Nao foi possivel gerar sintese agora.",
            NivelPreocupacao: "indeterminado",
        }
    } else {
        agent.audit.Log(dep.ID, "synthesis_executed", "", fmt.Sprintf(
            "tendencia=%s|nivel=%s|n_snapshots=%d",
            synthOut.Tendencia, synthOut.NivelPreocupacao, len(snaps)))
    }
    rep.Synthesis = synthOut

    return rep, nil
}
```

### 5.4 Format pra chat

```go
func formatStatusForChat(r *DependentStatusReport) string {
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("Status de %s (ultimos %d dias):\n\n", r.Dependent.Name, r.Days))

    // Tendência (a estrela do show, agora que é longitudinal).
    sb.WriteString(fmt.Sprintf("Tendencia: %s.\n", r.Synthesis.Tendencia))
    if r.Synthesis.Comparacao != "" {
        sb.WriteString(r.Synthesis.Comparacao + "\n")
    }

    // Medicação.
    if r.Medication.Scheduled == 0 {
        sb.WriteString("Sem medicacoes cadastradas.\n")
    } else {
        pct := int(100 * r.Medication.AdherenceFrac)
        sb.WriteString(fmt.Sprintf("Aderencia 7d: %d/%d doses (%d%%).\n",
            r.Medication.Taken, r.Medication.Scheduled, pct))
    }

    // Última conversa.
    switch {
    case !r.LastUserMessageAt.Valid:
        sb.WriteString("Ainda nao houve conversa.\n")
    case r.DaysSinceLastTalk == 0:
        sb.WriteString("Falou com o Lurch hoje.\n")
    case r.DaysSinceLastTalk == 1:
        sb.WriteString("Ultima conversa: ontem.\n")
    default:
        sb.WriteString(fmt.Sprintf("Ultima conversa ha %d dias.\n", r.DaysSinceLastTalk))
    }

    if len(r.AlertsOpen) > 0 {
        sb.WriteString(fmt.Sprintf("Alertas em aberto: %d.\n", len(r.AlertsOpen)))
    }

    sb.WriteString("\n" + r.Synthesis.Resumo)
    if r.Synthesis.PontoDeAtencao != "" {
        sb.WriteString("\n\nPonto de atencao: " + r.Synthesis.PontoDeAtencao)
    }
    if len(r.Synthesis.RecomendacoesCarinhosas) > 0 {
        sb.WriteString("\n\nSugestao: " + r.Synthesis.RecomendacoesCarinhosas[0])
    }
    return sb.String()
}
```

### 5.5 Registro

Em `bot/tools.go:42-58`:

```go
var toolHandlers = map[string]ToolHandler{
    // ... (existentes)
    "status_dependente": handleStatusDependente,
    "alertar_familia":   handleAlertarFamilia, // Fase 4
}
```

---

## 6. DB helpers — `bot/db.go`

Adicionar nesta fase (declarações novas, sem migrations exceto `psych_state_daily` da §3):

```go
// GetPsychSnapshots returns snapshots for user in [from, to], ordered DESC.
func (db *DB) GetPsychSnapshots(userID int64, from, to time.Time) ([]synthesis.DailySnapshot, error) {
    rows, err := db.conn.Query(`
        SELECT user_id, snapshot_date, humor_score, humor_nuance,
               energia_score, sociabilidade_score, autocuidado_score,
               sinais_observados, eventos_dia,
               n_conversations, n_messages, duration_minutes, confidence
        FROM psych_state_daily
        WHERE user_id = ? AND snapshot_date BETWEEN ? AND ?
        ORDER BY snapshot_date DESC`,
        userID, from.Format("2006-01-02"), to.Format("2006-01-02"))
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []synthesis.DailySnapshot
    for rows.Next() {
        var s synthesis.DailySnapshot
        var dateStr string
        var hum, ene, soc, aut sql.NullInt64
        var sinais, eventos string
        if err := rows.Scan(
            &s.UserID, &dateStr, &hum, &s.HumorNuance,
            &ene, &soc, &aut,
            &sinais, &eventos,
            &s.NConversations, &s.NMessages, &s.DurationMinutes, &s.Confidence,
        ); err != nil {
            return nil, err
        }
        d, _ := time.Parse("2006-01-02", dateStr)
        s.SnapshotDate = d
        if hum.Valid {
            s.HumorScore = int(hum.Int64)
        }
        if ene.Valid {
            s.EnergiaScore = int(ene.Int64)
        }
        if soc.Valid {
            s.SociabilidadeScore = int(soc.Int64)
        }
        if aut.Valid {
            s.AutocuidadoScore = int(aut.Int64)
        }
        _ = json.Unmarshal([]byte(sinais), &s.SinaisObservados)
        _ = json.Unmarshal([]byte(eventos), &s.EventosDia)
        out = append(out, s)
    }
    return out, rows.Err()
}

// GetPsychSnapshotForDate returns the existing snapshot for (user, date) or
// nil if none. Used by the writer for incremental updates.
func (db *DB) GetPsychSnapshotForDate(userID int64, date time.Time) (*synthesis.DailySnapshot, error) {
    snaps, err := db.GetPsychSnapshots(userID, date, date)
    if err != nil {
        return nil, err
    }
    if len(snaps) == 0 {
        return nil, nil
    }
    return &snaps[0], nil
}

// GetTimelineSnapshots is a thin projection used by the timeline endpoint —
// only the columns the chart needs. Cheaper than fetching the full row.
type TimelinePoint struct {
    Date           string `json:"date"`
    Humor          *int   `json:"humor"`
    Energia        *int   `json:"energia"`
    Sociabilidade  *int   `json:"sociabilidade"`
    Autocuidado    *int   `json:"autocuidado"`
    Confidence     int    `json:"confidence"`
}

func (db *DB) GetTimelinePoints(userID int64, days int) ([]TimelinePoint, error) {
    if days <= 0 || days > 365 {
        days = 90
    }
    rows, err := db.conn.Query(`
        SELECT snapshot_date, humor_score, energia_score, sociabilidade_score,
               autocuidado_score, confidence
        FROM psych_state_daily
        WHERE user_id = ? AND snapshot_date >= date('now', ?)
        ORDER BY snapshot_date ASC`,
        userID, fmt.Sprintf("-%d days", days))
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []TimelinePoint
    for rows.Next() {
        var p TimelinePoint
        var hum, ene, soc, aut sql.NullInt64
        if err := rows.Scan(&p.Date, &hum, &ene, &soc, &aut, &p.Confidence); err != nil {
            return nil, err
        }
        if hum.Valid {
            v := int(hum.Int64)
            p.Humor = &v
        }
        if ene.Valid {
            v := int(ene.Int64)
            p.Energia = &v
        }
        if soc.Valid {
            v := int(soc.Int64)
            p.Sociabilidade = &v
        }
        if aut.Valid {
            v := int(aut.Int64)
            p.Autocuidado = &v
        }
        out = append(out, p)
    }
    return out, rows.Err()
}

// GetMedicationStats7d aggregates medication_intake_log.
func (db *DB) GetMedicationStats7d(userID int64, from, to time.Time) (synthesis.MedicationStats, error) {
    rows, err := db.conn.Query(`
        SELECT status, COUNT(*) FROM medication_intake_log
        WHERE user_id = ? AND scheduled_at BETWEEN ? AND ?
        GROUP BY status`, userID, from.UTC(), to.UTC())
    if err != nil {
        return synthesis.MedicationStats{}, err
    }
    defer rows.Close()
    var s synthesis.MedicationStats
    for rows.Next() {
        var status string
        var count int
        if err := rows.Scan(&status, &count); err != nil {
            return s, err
        }
        s.Scheduled += count
        switch status {
        case "taken":
            s.Taken = count
        case "missed":
            s.Missed = count
        case "skipped":
            s.Skipped = count
        case "pending":
            s.Pending = count
        }
    }
    if s.Scheduled > 0 {
        s.AdherenceFrac = float64(s.Taken) / float64(s.Scheduled)
    }
    missRows, err := db.conn.Query(`
        SELECT m.name, l.scheduled_at FROM medication_intake_log l
        JOIN medications m ON m.id = l.medication_id
        WHERE l.user_id = ? AND l.status = 'missed'
          AND l.scheduled_at BETWEEN ? AND ?
        ORDER BY l.scheduled_at DESC LIMIT 5`,
        userID, from.UTC(), to.UTC())
    if err != nil {
        return s, err
    }
    defer missRows.Close()
    for missRows.Next() {
        var md synthesis.MissedDose
        if err := missRows.Scan(&md.MedicationName, &md.ScheduledAt); err != nil {
            return s, err
        }
        s.MissedDoses = append(s.MissedDoses, md)
    }
    return s, missRows.Err()
}

// GetMessagesSinceForUser returns conversation messages of an elder since a
// timestamp, used by the writer to decide what's new. Caller filters out
// messages older than the previous snapshot.
func (db *DB) GetMessagesSinceForUser(userID int64, since time.Time) ([]synthesis.ConversationMessage, error) {
    rows, err := db.conn.Query(`
        SELECT role, content, created_at FROM conversation_history
        WHERE user_id = ? AND created_at > ?
        ORDER BY created_at ASC`, userID, since.UTC())
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []synthesis.ConversationMessage
    for rows.Next() {
        var m synthesis.ConversationMessage
        if err := rows.Scan(&m.Role, &m.Text, &m.Timestamp); err != nil {
            return nil, err
        }
        out = append(out, m)
    }
    return out, rows.Err()
}

// GetMedicationIntakeOnDay returns medication_intake_log rows for user on a
// given calendar day (in user's local timezone).
func (db *DB) GetMedicationIntakeOnDay(userID int64, day time.Time, tz *time.Location) ([]synthesis.MedicationIntake, error) {
    if tz == nil {
        tz = time.UTC
    }
    dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tz)
    dayEnd := dayStart.Add(24 * time.Hour)
    rows, err := db.conn.Query(`
        SELECT m.name, l.scheduled_at, l.status FROM medication_intake_log l
        JOIN medications m ON m.id = l.medication_id
        WHERE l.user_id = ? AND l.scheduled_at >= ? AND l.scheduled_at < ?`,
        userID, dayStart.UTC(), dayEnd.UTC())
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []synthesis.MedicationIntake
    for rows.Next() {
        var i synthesis.MedicationIntake
        if err := rows.Scan(&i.MedicationName, &i.ScheduledAt, &i.Status); err != nil {
            return nil, err
        }
        out = append(out, i)
    }
    return out, rows.Err()
}

// GetSocialContextRiskMemos returns memos whose key starts with "risco:".
// Privacy boundary defined in Phase 4 §5.3.
func (db *DB) GetSocialContextRiskMemos(userID int64, limit int) ([]synthesis.Memory, error) {
    rows, err := db.conn.Query(`
        SELECT key, value FROM user_memories
        WHERE user_id = ? AND category = 'social_context' AND key LIKE 'risco:%'
        ORDER BY updated_at DESC LIMIT ?`, userID, limit)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []synthesis.Memory
    for rows.Next() {
        var m synthesis.Memory
        if err := rows.Scan(&m.Key, &m.Value); err != nil {
            return nil, err
        }
        out = append(out, m)
    }
    return out, rows.Err()
}

// GetAlertsOnDay returns escalations created on a given day (any status).
// Used by the writer to decide whether to fire safety_alert_needed.
func (db *DB) GetAlertsOnDay(userID int64, day time.Time, tz *time.Location) ([]synthesis.Alert, error) {
    if tz == nil {
        tz = time.UTC
    }
    dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tz)
    dayEnd := dayStart.Add(24 * time.Hour)
    rows, err := db.conn.Query(`
        SELECT policy_name, severity, created_at FROM escalations
        WHERE dependent_id = ? AND created_at >= ? AND created_at < ?`,
        userID, dayStart.UTC(), dayEnd.UTC())
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []synthesis.Alert
    for rows.Next() {
        var a synthesis.Alert
        if err := rows.Scan(&a.PolicyName, &a.Severity, &a.CreatedAt); err != nil {
            return nil, err
        }
        out = append(out, a)
    }
    return out, rows.Err()
}

// GetUsersWithMessagesOnDay returns elder users that exchanged at least one
// message on the given day (in their local TZ). Used by the catch-up job.
func (db *DB) GetUsersWithMessagesOnDay(day time.Time) ([]User, error) {
    // We compute in UTC bounds wide enough to cover all TZs (-12 to +14),
    // then filter precisely in Go.
    pad := 14 * time.Hour
    rows, err := db.conn.Query(`
        SELECT DISTINCT u.id, u.name, u.phone_number, u.type, u.timezone
        FROM users u
        JOIN conversation_history h ON h.user_id = u.id
        WHERE u.type = 'idoso'
          AND h.created_at BETWEEN ? AND ?`,
        day.UTC().Add(-pad), day.UTC().Add(24*time.Hour+pad))
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []User
    for rows.Next() {
        var u User
        if err := rows.Scan(&u.ID, &u.Name, &u.PhoneNumber, &u.Type, &u.Timezone); err != nil {
            return nil, err
        }
        out = append(out, u)
    }
    return out, rows.Err()
}

// GetOpenAlerts (Fase 5).
func (db *DB) GetOpenAlerts(userID int64) ([]Alert, error) {
    rows, err := db.conn.Query(`
        SELECT id, policy_name, severity, message, created_at, status
        FROM escalations
        WHERE dependent_id = ? AND status IN ('pending','sent')
        ORDER BY created_at DESC`, userID)
    if err != nil {
        return nil, err
    }
    defer rows.Close()
    var out []Alert
    for rows.Next() {
        var a Alert
        if err := rows.Scan(&a.ID, &a.PolicyName, &a.Severity, &a.Message, &a.CreatedAt, &a.Status); err != nil {
            return nil, err
        }
        out = append(out, a)
    }
    return out, rows.Err()
}

// GetProactiveAttemptsStats — janela 7d.
func (db *DB) GetProactiveAttemptsStats(userID int64, from, to time.Time) (ProactiveAttemptsStats, error) {
    var s ProactiveAttemptsStats
    err := db.conn.QueryRow(`
        SELECT COUNT(*) FROM proactive_attempts
        WHERE user_id = ? AND attempted_at BETWEEN ? AND ?`,
        userID, from.UTC(), to.UTC()).Scan(&s.Last7d)
    if err != nil {
        return s, err
    }
    var lastAt sql.NullTime
    var lastStatus sql.NullString
    err = db.conn.QueryRow(`
        SELECT attempted_at, status FROM proactive_attempts
        WHERE user_id = ? ORDER BY attempted_at DESC LIMIT 1`, userID,
    ).Scan(&lastAt, &lastStatus)
    if err != nil && !errors.Is(err, sql.ErrNoRows) {
        return s, err
    }
    s.LastAttemptAt = lastAt
    if lastStatus.Valid {
        s.LastAcked = lastStatus.String == "replied"
    }
    return s, nil
}

// GetGuardiansForInactivity is a wrapper over Phase 1's GetGuardians — filters
// to those with NotifyOnInactivity=true. Kept as a named function for caller
// clarity; it's NOT a new Phase 1 helper.
func (db *DB) GetGuardiansForInactivity(dependentID int64) ([]FamilyLink, error) {
    all, err := db.GetGuardians(dependentID)
    if err != nil {
        return nil, err
    }
    out := make([]FamilyLink, 0, len(all))
    for _, fl := range all {
        if fl.NotifyOnInactivity {
            out = append(out, fl)
        }
    }
    return out, nil
}

// HasOpenEscalation idempotency check.
func (db *DB) HasOpenEscalation(dependentID int64, policy string, attemptID int64) (bool, error) {
    var n int
    err := db.conn.QueryRow(`
        SELECT COUNT(*) FROM escalations
        WHERE dependent_id = ? AND policy_name = ? AND proactive_attempt_id = ?
          AND status IN ('pending','sent','acked')`,
        dependentID, policy, attemptID).Scan(&n)
    return n > 0, err
}

// CreateEscalation / UpdateEscalationStatus / GetLatestProactiveAttempt /
// ListUsersByType / GetDependentConsent: como na versão anterior do plano —
// definições idênticas mantidas, omitidas aqui por brevidade.
```

Helpers de adapter (em `bot/tools_family.go`):

```go
func toSynthesisAlerts(alerts []Alert) []synthesis.Alert {
    out := make([]synthesis.Alert, 0, len(alerts))
    for _, a := range alerts {
        out = append(out, synthesis.Alert{
            PolicyName: a.PolicyName,
            Severity:   a.Severity,
            CreatedAt:  a.CreatedAt,
        })
    }
    return out
}
```

---

## 7. Endpoint REST `GET /api/v1/family/dependents/{id}/status`

### 7.1 Contrato

- **Auth:** middleware `RequireAuth` (Fase 2). Resolve `ctx.user_id`.
- **Authz:** `db.IsGuardianOf(ctx.user_id, id)` → 403 se falso.
- **Query params:** `days` (opcional, default 14, max 90).
- **Response 200** (shape):

```json
{
  "dependent": { "id": 42, "name": "Antonia", "phone_number": "5561988887777", "last_user_message_at": "2026-05-08T14:22:00Z" },
  "days": 14,
  "days_since_last_talk": 1,
  "medication": {
    "scheduled": 14, "taken": 12, "missed": 2, "skipped": 0, "pending": 0,
    "adherence_pct": 86,
    "missed_doses": [{"medication_name": "losartana", "scheduled_at": "2026-05-04T08:00:00-03:00"}]
  },
  "proactive_attempts": { "last_7d": 1, "last_attempt_at": "2026-05-07T19:00:00-03:00", "last_acked": true },
  "alerts_open": [{"id": 33, "policy_name": "medication_miss", "severity": "warn", "message": "...", "created_at": "...", "status": "sent"}],
  "snapshots_count": 12,
  "synthesis": {
    "tendencia": "estavel",
    "comparacao": "humor 4.0 ultimos 7d vs 4.1 anteriores; autocuidado estavel",
    "humor_recente": "tem aparecido o tema saudade nas ultimas conversas",
    "ponto_de_atencao": "duas doses de losartana perdidas no fim de semana",
    "resumo": "Sua mae tem estado bem na maioria dos dias. ...",
    "recomendacoes_carinhosas": ["talvez ligue pra ela hoje, ela tem aparecido mais quieta"],
    "nivel_preocupacao": "tranquilo"
  }
}
```

### 7.2 Handler Go

```go
// bot/api_family.go
func (s *APIServer) handleDependentStatus(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    ctxUser, ok := userFromContext(r.Context())
    if !ok {
        writeJSONError(w, http.StatusUnauthorized, "unauthenticated")
        return
    }

    depID, err := parseDependentIDFromPath(r.URL.Path, "status")
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid_id")
        return
    }
    days := 14
    if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
        days = d
        if days > 90 {
            days = 90
        }
    }

    dep, err := s.db.GetUserByID(depID)
    if errors.Is(err, ErrUserNotFound) {
        writeJSONError(w, http.StatusNotFound, "dependent_not_found")
        return
    }
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "internal")
        return
    }

    ok, err = s.db.IsGuardianOf(ctxUser.ID, dep.ID)
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "internal")
        return
    }
    if !ok {
        writeJSONError(w, http.StatusForbidden, "not_a_guardian")
        return
    }

    consent, _ := s.db.GetDependentConsent(dep.ID, ctxUser.ID)
    if consent == "revoked" {
        writeJSONError(w, http.StatusForbidden, "consent_revoked")
        return
    }

    // 60s cache per (guardian, dependent, days).
    cacheKey := fmt.Sprintf("%d:%d:%d", ctxUser.ID, dep.ID, days)
    if cached, hit := s.statusCache.get(cacheKey); hit {
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("X-Cache", "HIT")
        w.Write(cached)
        return
    }

    report, err := BuildDependentStatus(r.Context(), s.agent, dep, days)
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "internal")
        return
    }

    s.audit.Log(ctxUser.ID, "status_dependente_consulted", dep.Name,
        fmt.Sprintf("via=web|days=%d|tendencia=%s|nivel=%s",
            days, report.Synthesis.Tendencia, report.Synthesis.NivelPreocupacao))

    body, _ := json.Marshal(toAPIResponse(report))
    s.statusCache.put(cacheKey, body)
    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("Cache-Control", "no-store")
    w.Header().Set("X-Cache", "MISS")
    w.Write(body)
}

func toAPIResponse(r *DependentStatusReport) any {
    var lastTalk any
    if r.LastUserMessageAt.Valid {
        lastTalk = r.LastUserMessageAt.Time
    } else {
        lastTalk = nil
    }
    return map[string]any{
        "dependent": map[string]any{
            "id":                   r.Dependent.ID,
            "name":                 r.Dependent.Name,
            "phone_number":         r.Dependent.PhoneNumber,
            "last_user_message_at": lastTalk,
        },
        "days":                 r.Days,
        "days_since_last_talk": r.DaysSinceLastTalk,
        "medication": map[string]any{
            "scheduled":     r.Medication.Scheduled,
            "taken":         r.Medication.Taken,
            "missed":        r.Medication.Missed,
            "skipped":       r.Medication.Skipped,
            "pending":       r.Medication.Pending,
            "adherence_pct": int(100 * r.Medication.AdherenceFrac),
            "missed_doses":  r.Medication.MissedDoses,
        },
        "proactive_attempts": map[string]any{
            "last_7d":         r.ProactiveAttempts.Last7d,
            "last_attempt_at": nullableTime(r.ProactiveAttempts.LastAttemptAt),
            "last_acked":      r.ProactiveAttempts.LastAcked,
        },
        "alerts_open":     r.AlertsOpen,
        "snapshots_count": len(r.Snapshots),
        "synthesis": map[string]any{
            "tendencia":                r.Synthesis.Tendencia,
            "comparacao":               r.Synthesis.Comparacao,
            "humor_recente":            r.Synthesis.HumorRecente,
            "ponto_de_atencao":         r.Synthesis.PontoDeAtencao,
            "resumo":                   r.Synthesis.Resumo,
            "recomendacoes_carinhosas": r.Synthesis.RecomendacoesCarinhosas,
            "nivel_preocupacao":        r.Synthesis.NivelPreocupacao,
        },
    }
}
```

---

## 8. Endpoint REST `GET /api/v1/family/dependents/{id}/timeline`

### 8.1 Contrato

- **Auth:** middleware `RequireAuth`.
- **Authz:** `db.IsGuardianOf(ctx.user_id, id)` → 403 se falso.
- **Query param:** `days` (opcional, default 90, max 365).
- **Response 200:**

```json
{
  "dependent": { "id": 42, "name": "Antonia" },
  "days": 90,
  "snapshots": [
    { "date": "2026-02-09", "humor": 4, "energia": 3, "sociabilidade": 4, "autocuidado": 5, "confidence": 4 },
    { "date": "2026-02-10", "humor": null, "energia": null, "sociabilidade": 3, "autocuidado": 5, "confidence": 1 }
  ]
}
```

- **Response 403:** `{"error":"not_a_guardian"}` ou `{"error":"consent_revoked"}`.
- **Response 404:** `{"error":"dependent_not_found"}`.
- **Response 200 com `snapshots: []`:** quando dependente existe mas não há snapshots.

### 8.2 Handler

```go
func (s *APIServer) handleDependentTimeline(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodGet {
        http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }
    ctxUser, ok := userFromContext(r.Context())
    if !ok {
        writeJSONError(w, http.StatusUnauthorized, "unauthenticated")
        return
    }

    depID, err := parseDependentIDFromPath(r.URL.Path, "timeline")
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid_id")
        return
    }
    days := 90
    if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
        days = d
        if days > 365 {
            days = 365
        }
    }

    dep, err := s.db.GetUserByID(depID)
    if errors.Is(err, ErrUserNotFound) {
        writeJSONError(w, http.StatusNotFound, "dependent_not_found")
        return
    }
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "internal")
        return
    }

    ok, err = s.db.IsGuardianOf(ctxUser.ID, dep.ID)
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "internal")
        return
    }
    if !ok {
        writeJSONError(w, http.StatusForbidden, "not_a_guardian")
        return
    }
    consent, _ := s.db.GetDependentConsent(dep.ID, ctxUser.ID)
    if consent == "revoked" {
        writeJSONError(w, http.StatusForbidden, "consent_revoked")
        return
    }

    pts, err := s.db.GetTimelinePoints(dep.ID, days)
    if err != nil {
        writeJSONError(w, http.StatusInternalServerError, "internal")
        return
    }
    if pts == nil {
        pts = []TimelinePoint{}
    }

    s.audit.Log(ctxUser.ID, "timeline_consulted", dep.Name, fmt.Sprintf("days=%d|points=%d", days, len(pts)))

    body, _ := json.Marshal(map[string]any{
        "dependent": map[string]any{"id": dep.ID, "name": dep.Name},
        "days":      days,
        "snapshots": pts,
    })
    w.Header().Set("Content-Type", "application/json")
    w.Header().Set("Cache-Control", "no-store")
    w.Write(body)
}

// parseDependentIDFromPath extracts {id} from /api/v1/family/dependents/{id}/<suffix>.
func parseDependentIDFromPath(path, suffix string) (int64, error) {
    parts := strings.Split(strings.Trim(path, "/"), "/")
    // ["api","v1","family","dependents","{id}","<suffix>"]
    if len(parts) != 6 || parts[5] != suffix {
        return 0, fmt.Errorf("bad path")
    }
    id, err := strconv.ParseInt(parts[4], 10, 64)
    if err != nil || id <= 0 {
        return 0, fmt.Errorf("bad id")
    }
    return id, nil
}
```

### 8.3 Registro de rotas

Em `bot/api.go`:

```go
mux.HandleFunc("/api/v1/family/dependents/", s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
    switch {
    case strings.HasSuffix(r.URL.Path, "/status"):
        s.handleDependentStatus(w, r)
    case strings.HasSuffix(r.URL.Path, "/timeline"):
        s.handleDependentTimeline(w, r)
    default:
        writeJSONError(w, http.StatusNotFound, "not_found")
    }
}))
```

---

## 9. UI web — dashboard com timeline

### 9.1 Página de status (já existia, atualizar)

`/web/app/dashboard/family/[id]/page.tsx` — atualizar pra:
- Usar campo `tendencia` no `<StatusHeader>`.
- Usar `comparacao`, `humor_recente`, `ponto_de_atencao` no `<SynthesisCard>`.
- Adicionar link "Ver evolução →" pra `/dashboard/family/[id]/evolucao`.

```tsx
// web/components/family/StatusHeader.tsx
import { Badge } from "@/components/ui/badge"

type Tendencia = "melhorando" | "estavel" | "piorando" | "instavel" | "indeterminado"
type Nivel = "tranquilo" | "atencao" | "atencao_alta" | "indeterminado"

const tendenciaLabel: Record<Tendencia, string> = {
  melhorando: "melhorando",
  estavel: "estavel",
  piorando: "piorando",
  instavel: "oscilando",
  indeterminado: "sem dado suficiente",
}

const tendenciaTone: Record<Tendencia, "good" | "warn" | "bad" | "neutral"> = {
  melhorando: "good",
  estavel: "neutral",
  piorando: "bad",
  instavel: "warn",
  indeterminado: "neutral",
}

const nivelTone: Record<Nivel, "good" | "warn" | "bad" | "neutral"> = {
  tranquilo: "good",
  atencao: "warn",
  atencao_alta: "bad",
  indeterminado: "neutral",
}

type Props = {
  name: string
  daysSinceLastTalk: number
  lastTalkAt: string | null
  tendencia: Tendencia
  nivelPreocupacao: Nivel
}

export function StatusHeader(props: Props) {
  const ago =
    props.lastTalkAt == null
      ? "ainda nao houve conversa"
      : props.daysSinceLastTalk === 0
      ? "falou com o Lurch hoje"
      : props.daysSinceLastTalk === 1
      ? "ultima conversa: ontem"
      : `ultima conversa ha ${props.daysSinceLastTalk} dias`

  return (
    <header className="flex flex-col gap-2 rounded-xl border bg-card p-4">
      <h1 className="text-2xl font-semibold">{props.name}</h1>
      <p className="text-sm text-muted-foreground">{ago}</p>
      <div className="flex flex-wrap gap-2">
        <Badge variant={nivelTone[props.nivelPreocupacao]}>
          nivel: {props.nivelPreocupacao.replace("_", " ")}
        </Badge>
        <Badge variant={tendenciaTone[props.tendencia]}>
          tendencia: {tendenciaLabel[props.tendencia]}
        </Badge>
      </div>
    </header>
  )
}
```

```tsx
// web/components/family/SynthesisCard.tsx
import type { DependentStatus } from "@/lib/api/family"
import Link from "next/link"

export function SynthesisCard({
  synthesis,
  dependentId,
}: {
  synthesis: DependentStatus["synthesis"]
  dependentId: number
}) {
  return (
    <section className="rounded-xl border bg-card p-4">
      <h2 className="mb-2 text-lg font-medium">Como ela esta</h2>
      {synthesis.comparacao && (
        <p className="mb-2 text-sm text-muted-foreground">{synthesis.comparacao}</p>
      )}
      <p className="mb-3 text-base">{synthesis.resumo}</p>
      {synthesis.humor_recente && (
        <p className="mb-3 text-sm">
          <span className="font-medium">Humor recente: </span>
          {synthesis.humor_recente}
        </p>
      )}
      {synthesis.ponto_de_atencao && (
        <p className="mb-3 text-sm">
          <span className="font-medium">Ponto de atencao: </span>
          {synthesis.ponto_de_atencao}
        </p>
      )}
      {synthesis.recomendacoes_carinhosas.length > 0 && (
        <div className="mb-3">
          <p className="mb-1 text-sm font-medium">Sugestoes:</p>
          <ul className="ml-4 list-disc text-sm">
            {synthesis.recomendacoes_carinhosas.map((r, i) => (
              <li key={i}>{r}</li>
            ))}
          </ul>
        </div>
      )}
      <Link
        href={`/dashboard/family/${dependentId}/evolucao`}
        className="text-sm underline underline-offset-4"
      >
        Ver evolucao &rarr;
      </Link>
    </section>
  )
}
```

### 9.2 Página nova `evolucao`

`/web/app/dashboard/family/[id]/evolucao/page.tsx`:

```tsx
import { notFound } from "next/navigation"
import { getDependentTimeline, type Timeline } from "@/lib/api/family"
import { PsychTimeline } from "@/components/family/PsychTimeline"
import Link from "next/link"

export const dynamic = "force-dynamic"

type Props = { params: { id: string }; searchParams: { days?: string } }

export default async function EvolucaoPage({ params, searchParams }: Props) {
  const id = Number(params.id)
  if (!Number.isFinite(id) || id <= 0) notFound()

  const days = clampDays(Number(searchParams.days ?? 90))
  const timeline = await getDependentTimeline(id, days)
  if (!timeline) notFound()

  return (
    <main className="mx-auto flex max-w-5xl flex-col gap-6 p-4">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-semibold">Evolucao de {timeline.dependent.name}</h1>
        <Link
          href={`/dashboard/family/${id}`}
          className="text-sm underline underline-offset-4"
        >
          &larr; Voltar ao status
        </Link>
      </div>

      <div className="flex flex-wrap gap-2 text-sm">
        {[30, 60, 90, 180].map((d) => (
          <Link
            key={d}
            href={`/dashboard/family/${id}/evolucao?days=${d}`}
            className={`rounded-full border px-3 py-1 ${
              d === days ? "bg-foreground text-background" : "bg-card"
            }`}
          >
            {d}d
          </Link>
        ))}
      </div>

      <PsychTimeline snapshots={timeline.snapshots} />

      <p className="text-xs text-muted-foreground">
        Pontos transparentes indicam dias com pouca conversa (confidence baixa)
        — a inferencia nao e robusta nesses dias. Linha tracejada nao significa
        ausencia de dado: significa que aquela dimensao nao pode ser inferida.
      </p>
    </main>
  )
}

function clampDays(d: number): number {
  if (!Number.isFinite(d) || d <= 0) return 90
  if (d > 365) return 365
  return d
}
```

### 9.3 Componente do gráfico

```tsx
// web/components/family/PsychTimeline.tsx
"use client"

import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  ReferenceLine,
  Dot,
} from "recharts"

type Snapshot = {
  date: string // YYYY-MM-DD
  humor: number | null
  energia: number | null
  sociabilidade: number | null
  autocuidado: number | null
  confidence: number
}

type Props = { snapshots: Snapshot[] }

const dimensions = [
  { key: "humor", label: "Humor", stroke: "#6366f1" },
  { key: "energia", label: "Energia", stroke: "#f59e0b" },
  { key: "sociabilidade", label: "Sociabilidade", stroke: "#10b981" },
  { key: "autocuidado", label: "Autocuidado", stroke: "#ef4444" },
] as const

export function PsychTimeline({ snapshots }: Props) {
  if (snapshots.length === 0) {
    return (
      <div className="rounded-xl border bg-card p-8 text-center text-sm text-muted-foreground">
        Ainda nao ha snapshots suficientes. Volte em alguns dias.
      </div>
    )
  }
  return (
    <div className="grid gap-6 sm:grid-cols-2">
      {dimensions.map((dim) => (
        <div key={dim.key} className="rounded-xl border bg-card p-3">
          <h3 className="mb-2 text-sm font-medium" style={{ color: dim.stroke }}>
            {dim.label}
          </h3>
          <ResponsiveContainer width="100%" height={180}>
            <LineChart data={snapshots} margin={{ top: 10, right: 10, left: -20, bottom: 0 }}>
              <CartesianGrid strokeDasharray="3 3" stroke="rgba(0,0,0,0.06)" />
              <XAxis
                dataKey="date"
                tick={{ fontSize: 10 }}
                tickFormatter={(v) => v.slice(5)}
              />
              <YAxis
                domain={[1, 5]}
                ticks={[1, 2, 3, 4, 5]}
                tick={{ fontSize: 10 }}
              />
              <Tooltip
                labelFormatter={(v) => `Dia: ${v}`}
                formatter={(v: number, _name, p) =>
                  v == null ? "sem dado" : `${v} (conf ${p.payload.confidence})`
                }
              />
              <ReferenceLine y={3} stroke="rgba(0,0,0,0.2)" strokeDasharray="2 2" />
              <Line
                type="monotone"
                dataKey={dim.key}
                stroke={dim.stroke}
                strokeWidth={2}
                connectNulls={false}
                dot={(props) => {
                  const { cx, cy, payload } = props
                  if (cx == null || cy == null || payload[dim.key] == null) {
                    return <></>
                  }
                  const lowConfidence = payload.confidence < 3
                  return (
                    <Dot
                      cx={cx}
                      cy={cy}
                      r={3}
                      fill={dim.stroke}
                      fillOpacity={lowConfidence ? 0.3 : 1}
                    />
                  )
                }}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      ))}
    </div>
  )
}
```

### 9.4 Cliente API

```ts
// web/lib/api/family.ts (acrescentar Timeline)
export type Timeline = {
  dependent: { id: number; name: string }
  days: number
  snapshots: {
    date: string
    humor: number | null
    energia: number | null
    sociabilidade: number | null
    autocuidado: number | null
    confidence: number
  }[]
}

export async function getDependentTimeline(id: number, days = 90): Promise<Timeline | null> {
  const res = await fetch(
    `${process.env.NEXT_PUBLIC_API_URL}/api/v1/family/dependents/${id}/timeline?days=${days}`,
    { credentials: "include", cache: "no-store" },
  )
  if (res.status === 403 || res.status === 404) return null
  if (!res.ok) throw new Error(`api error: ${res.status}`)
  return res.json()
}

export type DependentStatus = {
  dependent: { id: number; name: string; phone_number: string; last_user_message_at: string | null }
  days: number
  days_since_last_talk: number
  medication: {
    scheduled: number; taken: number; missed: number; skipped: number; pending: number
    adherence_pct: number
    missed_doses: { medication_name: string; scheduled_at: string }[]
  }
  proactive_attempts: { last_7d: number; last_attempt_at: string | null; last_acked: boolean }
  alerts_open: { id: number; policy_name: string; severity: "info"|"warn"|"critical"; message: string; created_at: string; status: string }[]
  snapshots_count: number
  synthesis: {
    tendencia: "melhorando"|"estavel"|"piorando"|"instavel"|"indeterminado"
    comparacao: string
    humor_recente: string
    ponto_de_atencao: string
    resumo: string
    recomendacoes_carinhosas: string[]
    nivel_preocupacao: "tranquilo"|"atencao"|"atencao_alta"|"indeterminado"
  }
}
```

### 9.5 Setup do recharts

```bash
cd web && pnpm add recharts
```

`recharts` é tree-shakeable, ~30KB gz para os componentes que usamos. SSR-safe quando o componente que o importa é `"use client"`.

---

## 10. Scheduler — `checkInactivityEscalation` + `runDailyPsychSnapshotCatchup`

### 10.1 Lógica de `checkInactivityEscalation`

(Inalterada vs versão anterior — a Fase 4 entrega `escalations.proactive_attempt_id` e o índice `idx_escalations_inactivity_lookup`. Esta fase **apenas usa**.)

Roda a cada minuto, agindo a cada 30 minutos via `shouldRun`. Para cada idoso:

1. Pega `proactive_attempts` mais recente.
2. Se `attempt.status == "sent"` e há mais de `inactivity_escalate_hours` (default 4h) e idoso não respondeu (`last_user_message_at < attempt.attempted_at`):
3. Para cada guardian com `notify_on_inactivity=true`:
   - Verifica idempotência via `HasOpenEscalation(elder.ID, "inactivity", attempt.ID)`.
   - Cria `escalations` row, envia mensagem, marca `status='sent'`.

```go
// bot/scheduler.go
s.cron.AddFunc("* * * * *", s.checkInactivityEscalation)
s.cron.AddFunc("@every 1h", s.runDailyPsychSnapshotCatchup)

func (s *Scheduler) checkInactivityEscalation() {
    if !s.shouldRun("inactivity_escalation", 30*time.Minute) {
        return
    }
    elders, err := s.db.ListUsersByType("idoso")
    if err != nil {
        log.Printf("Scheduler: list elders: %v", err)
        return
    }
    for _, elder := range elders {
        s.checkInactivityForElder(&elder)
    }
}

func (s *Scheduler) checkInactivityForElder(elder *User) {
    threshold, err := time.ParseDuration(elder.InactivityEscalateAfter)
    if err != nil || threshold == 0 {
        threshold = 4 * time.Hour
    }
    attempt, err := s.db.GetLatestProactiveAttempt(elder.ID)
    if err != nil || attempt == nil || attempt.Status == "replied" {
        return
    }
    if elder.LastUserMessageAt != nil && elder.LastUserMessageAt.After(attempt.AttemptedAt) {
        return
    }
    if time.Since(attempt.AttemptedAt) < threshold {
        return
    }

    guardians, err := s.db.GetGuardiansForInactivity(elder.ID)
    if err != nil || len(guardians) == 0 {
        return
    }
    for _, g := range guardians {
        exists, err := s.db.HasOpenEscalation(elder.ID, "inactivity", attempt.ID)
        if err != nil || exists {
            continue
        }
        msg := fmt.Sprintf(
            "Oi %s, sua %s %s nao responde ao Lurch ha %s. Tentei puxar conversa sem sucesso. Quer ligar pra ela?",
            firstName(g.Other.Name),
            relationshipPT(g.Relationship),
            elder.Name,
            humanizeDuration(time.Since(attempt.AttemptedAt)),
        )
        escID, err := s.db.CreateEscalation(&Escalation{
            DependentID:        elder.ID,
            GuardianID:         g.Other.ID,
            PolicyName:         "inactivity",
            Severity:           "warn",
            Message:            msg,
            Status:             "pending",
            ProactiveAttemptID: &attempt.ID,
        })
        if err != nil {
            continue
        }
        if err := s.sendMsg(g.Other.PhoneNumber, msg); err != nil {
            continue
        }
        s.db.UpdateEscalationStatus(escID, "sent")
        s.audit.Log(elder.ID, "inactivity_escalation_triggered", g.Other.Name,
            fmt.Sprintf("attempt_id=%d|escalation_id=%d", attempt.ID, escID))
    }
}
```

### 10.2 `runDailyPsychSnapshotCatchup`

O trigger primário do snapshot writer roda na Fase 4 (pós-conversa significativa). Esta fase adiciona um job de **catch-up** que garante que cada dia com atividade tem um snapshot. Roda a cada hora.

Lógica:
1. Calcula o "dia anterior" no fuso de cada idoso (UTC+2/3 ou whatever).
2. Para cada idoso com mensagens nesse dia mas SEM linha em `psych_state_daily`: chama `WriteSnapshot`.
3. Idempotência: UPSERT na tabela. Se outro processo escreveu antes, este sobrescreve com a versão mais recente — ok.

```go
func (s *Scheduler) runDailyPsychSnapshotCatchup() {
    if !s.shouldRun("psych_snapshot_catchup", 60*time.Minute) {
        return
    }

    // Catch-up roda no "dia anterior" do user (TZ local). Conservadoramente,
    // pega ontem-em-UTC e itera; cada user resolve TZ ao chamar.
    yesterday := time.Now().Add(-24 * time.Hour)

    elders, err := s.db.GetUsersWithMessagesOnDay(yesterday)
    if err != nil {
        log.Printf("Scheduler: catchup list: %v", err)
        return
    }

    for _, elder := range elders {
        s.catchupSnapshotForElder(&elder, yesterday)
    }
}

func (s *Scheduler) catchupSnapshotForElder(elder *User, day time.Time) {
    tz, err := time.LoadLocation(elder.Timezone)
    if err != nil || tz == nil {
        tz = time.UTC
    }
    localDay := day.In(tz)
    dayDate := time.Date(localDay.Year(), localDay.Month(), localDay.Day(), 0, 0, 0, 0, tz)

    // Skip if snapshot already exists.
    existing, err := s.db.GetPsychSnapshotForDate(elder.ID, dayDate)
    if err != nil {
        log.Printf("Scheduler: GetPsychSnapshotForDate: %v", err)
        return
    }
    if existing != nil && existing.Confidence >= 2 {
        return // already written with reasonable confidence
    }

    msgs, err := s.db.GetMessagesSinceForUser(elder.ID, dayDate)
    if err != nil || len(msgs) == 0 {
        return
    }
    // Filter to messages within the day in local TZ.
    nextDay := dayDate.Add(24 * time.Hour)
    var dayMsgs []synthesis.ConversationMessage
    for _, m := range msgs {
        if m.Timestamp.In(tz).Before(nextDay) {
            dayMsgs = append(dayMsgs, m)
        }
    }
    if len(dayMsgs) == 0 {
        return
    }

    intake, _ := s.db.GetMedicationIntakeOnDay(elder.ID, dayDate, tz)
    var taken, missed []synthesis.MedicationIntake
    for _, i := range intake {
        switch i.Status {
        case "taken":
            taken = append(taken, i)
        case "missed":
            missed = append(missed, i)
        }
    }
    riskMemos, _ := s.db.GetSocialContextRiskMemos(elder.ID, 10)
    alerts, _ := s.db.GetAlertsOnDay(elder.ID, dayDate, tz)

    in := synthesis.SnapshotInput{
        User: synthesis.User{
            ID: elder.ID, Name: elder.Name, Timezone: elder.Timezone,
        },
        Date:                   dayDate,
        PreviousSnapshot:       existing,
        NewMessages:            dayMsgs,
        MedicationsTakenToday:  taken,
        MedicationsMissedToday: missed,
        SocialContextRiskMemos: riskMemos,
        AlertasGerados:         alerts,
    }

    out, err := synthesis.WriteSnapshot(s.ctx, s.haikuClient, in)
    if err != nil {
        s.audit.Log(elder.ID, "psych_snapshot_failed", "", err.Error())
        return
    }

    snap := out.ToDailySnapshot(elder.ID, dayDate, struct {
        NConversations  int
        NMessages       int
        DurationMinutes int
    }{
        NConversations:  countSessions(dayMsgs),
        NMessages:       len(dayMsgs),
        DurationMinutes: estimateDurationMinutes(dayMsgs),
    })
    if err := s.db.UpsertPsychSnapshot(&snap); err != nil {
        log.Printf("Scheduler: UpsertPsychSnapshot: %v", err)
        return
    }
    s.audit.Log(elder.ID, "psych_snapshot_written", "",
        fmt.Sprintf("via=catchup|date=%s|confidence=%d", dayDate.Format("2006-01-02"), out.Confidence))

    // Se Haiku detectou sinal grave nao capturado pelo companion, dispara
    // alertar_familia equivalente.
    if out.SafetyAlertNeeded != nil {
        s.handleSafetyAlertFromWriter(elder, dayDate, out.SafetyAlertNeeded)
    }
}

// countSessions splits messages into "sessions" using a 30-min idle gap.
func countSessions(msgs []synthesis.ConversationMessage) int {
    if len(msgs) == 0 {
        return 0
    }
    sessions := 1
    for i := 1; i < len(msgs); i++ {
        if msgs[i].Timestamp.Sub(msgs[i-1].Timestamp) > 30*time.Minute {
            sessions++
        }
    }
    return sessions
}

func estimateDurationMinutes(msgs []synthesis.ConversationMessage) int {
    total := 0.0
    var sessionStart time.Time
    var prev time.Time
    for i, m := range msgs {
        if i == 0 {
            sessionStart = m.Timestamp
            prev = m.Timestamp
            continue
        }
        if m.Timestamp.Sub(prev) > 30*time.Minute {
            total += prev.Sub(sessionStart).Minutes()
            sessionStart = m.Timestamp
        }
        prev = m.Timestamp
    }
    total += prev.Sub(sessionStart).Minutes()
    return int(total)
}

func (s *Scheduler) handleSafetyAlertFromWriter(elder *User, day time.Time, sa *synthesis.SafetyAlert) {
    // Reusa o mesmo path que `alertar_familia` da Fase 4 — evita lógica duplicada.
    // A função handleAlertarFamiliaInternal cria escalations + envia notifier
    // + RESPEITA disclosurePolicy[Category] na hora de enviar mensagens. Como
    // este caminho é assíncrono (não está no loop de chat com o idoso), o
    // disclose_to_elder não tem efeito imediato sobre conversa — mas continua
    // sendo gravado em escalations.details pra auditoria e pra próxima
    // mensagem do companion (que LÊ escalations recentes via context).
    handleAlertarFamiliaInternal(s.db, s.audit, s.sendMsg,
        elder.ID, sa.Severity, sa.Category, sa.Reason, sa.Recommended)
    s.audit.Log(elder.ID, "safety_alert_from_writer", "",
        fmt.Sprintf("date=%s|severity=%s|category=%s",
            day.Format("2006-01-02"), sa.Severity, sa.Category))
}
```

### 10.3 Adendo ao `Scheduler` struct

```go
type Scheduler struct {
    cron        *cron.Cron
    db          *DB
    cal         *CalendarClient
    cfg         *Config
    sendMsg     func(phone, text string) error
    audit       *AuditLog
    ctx         context.Context
    haikuClient *anthropic.Client     // pra writer
    sonnetClient *anthropic.Client    // pra synthesis (passado também ao Agent)
    mu          sync.Mutex
    lastRun     map[string]time.Time
}
```

`NewScheduler` recebe `audit *AuditLog`, `haikuClient`, `ctx` e inicializa `lastRun`.

---

## 11. Auditoria

Estender `bot/audit.go:56-70` (`actionLabelsPT`):

```go
var actionLabelsPT = map[string]string{
    // ... existentes
    "status_dependente_consulted":     "Consultou status do dependente",
    "timeline_consulted":              "Consultou timeline do dependente",
    "synthesis_executed":              "Sintese gerada",
    "synthesis_failed":                "Falha na sintese",
    "psych_snapshot_written":          "Snapshot psicologico escrito",
    "psych_snapshot_failed":           "Falha ao escrever snapshot",
    "safety_alert_from_writer":        "Alerta de seguranca disparado pelo writer",
    "inactivity_escalation_triggered": "Escalou inatividade para familia",
    "alertar_familia":                 "Alertou familia (sinal serio)", // Fase 4
}
```

Convenções de `details`:
- `status_dependente_consulted`: `via=chat|web|days=N|adherence=X/Y|days_silent=Z|alerts_open=N|tendencia=...|nivel=...`.
- `timeline_consulted`: `days=N|points=M`.
- `synthesis_executed`: `tendencia=...|nivel=...|n_snapshots=N`.
- `synthesis_failed`: mensagem de erro completa.
- `psych_snapshot_written`: `via=trigger|catchup|date=YYYY-MM-DD|confidence=N`.
- `psych_snapshot_failed`: razão.
- `safety_alert_from_writer`: `date=YYYY-MM-DD|severity=...`.

---

## 12. Privacidade — política explícita

### 12.1 O que é INFERÊNCIA, não conteúdo

`psych_state_daily` armazena **inferência abstrata** do estado psicológico do idoso. Não é conteúdo bruto:

- Scores 1-5 são abstrações.
- `humor_nuance` é descritivo curto, sem citação literal.
- `sinais_observados` e `eventos_dia` filtram fofoca: só componente saúde/segurança.
- Cada item máx 100 ch — força concisão e impede que vire transcrição.

### 12.2 Barreiras múltiplas

| Camada                  | Mecanismo                                                                                       |
| ----------------------- | ----------------------------------------------------------------------------------------------- |
| **Convenção de chave**  | Memos `social_context.*` ficam privadas; só `risco:*` atravessam pra writer (Fase 4 §5.3).      |
| **Filtro no leitor**    | `GetSocialContextRiskMemos` faz `WHERE key LIKE 'risco:%'` no SQL — fofoca nunca chega ao writer. |
| **Validação do writer** | `ValidateSnapshotOutput` rejeita output com aspas literais, termos clínicos OU keywords de fofoca. |
| **Synthesize não vê transcrições** | Por construção: `Synthesize` recebe `ReportInput` que NÃO contém mensagens nem memos. Só snapshots já abstratos. |
| **Validação do report** | `ValidateReportOutput` rejeita output com aspas literais ou termos clínicos.                    |
| **Consent revogado**    | `family_links.dependent_consent_status='revoked'` bloqueia LEITURA do timeline, status, AND ESCRITA futura de snapshots. |

### 12.3 O que o sub-agente writer (Haiku) VÊ

- Mensagens recentes do dia (idoso e bot) — mas APENAS na chamada efêmera; não persistido.
- Medicação tomada/perdida no dia.
- Memos `risco:*` (apenas).
- Alertas já gerados hoje (pra não duplicar).

### 12.4 O que o sub-agente report (Sonnet) VÊ

- Snapshots já abstratos de N dias.
- Medication stats agregados 7d.
- Alertas em aberto (policy_name + severity + age, não message livre).
- Days since last talk.

NUNCA vê: transcrições, áudio, imagens, memos não-`risco`, dados de outros usuários.

### 12.5 O que o responsável VÊ

- Status (`/status`): JSON com synthesis acolhedora + medication stats + alerts.
- Timeline (`/timeline`): scores diários numéricos com confidence flag.
- Histórico de alertas dos últimos 30 dias.

NUNCA vê: transcrições, memos `social_context` (incluindo `risco:*`), conteúdo cru de qualquer tipo.

### 12.6 Consent revogado — comportamento

Quando `family_links.dependent_consent_status='revoked'`:
- Tool `status_dependente` retorna mensagem padrão "X revogou o consentimento de relatorio agregado."
- Endpoint `/status` retorna `403 consent_revoked`.
- Endpoint `/timeline` retorna `403 consent_revoked`.
- Job `runDailyPsychSnapshotCatchup` **pula** o idoso (verifica consent antes de chamar writer).
- Trigger pós-conversa da Fase 4 também pula (mesma verificação).
- Snapshots já escritos **permanecem** no banco até pedido explícito de delete.

### 12.7 Direito de delete

Idoso pode pedir delete do histórico de snapshots:
- Tool nova (escopo Fase 6+): `apagar_meu_historico_psicologico`.
- Por enquanto, processo manual: idoso pede via WhatsApp, admin executa `DELETE FROM psych_state_daily WHERE user_id=?`.
- Audit log preserva o pedido + execução, com hash do que foi deletado (linha count, não conteúdo).

### 12.8 Aviso periódico ao idoso

Onboarding (Fase 2) inclui texto:
- "Seu responsável `<nome>` pode consultar quando quiser um resumo agregado de como você está. Ele NUNCA vê o que você escreve pra mim. Ele vê uma estimativa de humor (1 a 5), aderência aos remédios, e uma síntese acolhedora gerada por IA."
- Aviso mensal no chat: o Lurch envia mensagem informando se o responsável fez consultas no último mês (uma vez, agregado, sem horários nem detalhes), oferecendo a opção de pedir revogação ou delete.

---

## 13. Casos de teste

### 13.1 Tabela de cobertura

| #   | Caso                                                                              | Tipo            | Expectativa                                                                |
| --- | --------------------------------------------------------------------------------- | --------------- | -------------------------------------------------------------------------- |
| T1  | guardian autorizado consulta status                                               | unit (handler)  | 200 + payload completo com tendencia                                       |
| T2  | usuario nao-guardian tenta consultar status                                       | unit            | 403 (REST) / mensagem de autz (chat)                                       |
| T3  | dependente nao existe                                                             | unit            | 404 (REST) / mensagem clara (chat)                                         |
| T4  | consent revoked                                                                   | unit            | 403 consent_revoked / mensagem padrao                                      |
| T5  | timeline endpoint: nao-guardian recebe 403                                        | unit            | 403                                                                        |
| T6  | timeline endpoint: dependente sem snapshots                                       | unit            | 200 com `snapshots: []`                                                    |
| T7  | timeline endpoint: consent revoked                                                | unit            | 403 consent_revoked                                                        |
| T8  | writer detecta sinal nao capturado pelo companion                                 | unit            | `safety_alert_needed != nil` → escalation criada                          |
| T9  | writer com 1 mensagem so → confidence=1, scores podem ser 0                       | unit            | output valido com scores zerados                                           |
| T10 | writer rejeita output com aspas literais                                          | unit            | `ValidateSnapshotOutput` rejeita                                           |
| T11 | writer rejeita output com termo clinico ("depressao")                             | unit            | rejeita                                                                    |
| T12 | writer rejeita output com fofoca ("brigou com filha")                             | unit            | rejeita                                                                    |
| T13 | UPSERT em (user_id, snapshot_date) — multiplos triggers no mesmo dia → 1 row     | integration     | 1 row, valores do ultimo trigger                                           |
| T14 | report calcula tendencia: 14 dias subindo → "melhorando"                          | unit (mock LLM) | output `tendencia=melhorando`                                              |
| T15 | report com janela vazia (0 snapshots)                                             | unit            | `tendencia=indeterminado`, `nivel=tranquilo`, resumo honesto               |
| T16 | report rejeita output com aspas                                                   | unit            | `ValidateReportOutput` rejeita                                             |
| T17 | report rejeita output com termo clinico                                           | unit            | rejeita                                                                    |
| T18 | inactivity escalation nao duplica em restart                                      | integration     | 1 row em `escalations` apos 3 ticks                                        |
| T19 | inactivity escalation nao dispara se idoso respondeu                              | integration     | 0 rows                                                                     |
| T20 | inactivity escalation respeita `notify_on_inactivity=false`                       | integration     | guardian sem flag nao recebe                                               |
| T21 | catch-up snapshot pula idoso com consent=revoked                                  | integration     | 0 rows escritas                                                            |
| T22 | catch-up snapshot nao roda quando snapshot ja existe com confidence>=2            | integration     | 0 chamadas Haiku                                                           |
| T23 | rate limit do REST status (cache 60s)                                             | unit            | 2a chamada em <60s vem do cache                                            |
| T24 | cache invalidado quando consent muda                                              | unit            | apos UpdateConsent, cache hit nao retorna stale                            |
| T25 | audit log de `status_dependente_consulted` preenchido                             | unit            | 1 row em `action_log`                                                      |

### 13.2 Stubs de teste — writer

```go
// bot/synthesis/writer_test.go
func TestValidateSnapshotOutput_RejectsLiteralQuote(t *testing.T) {
    out := SnapshotOutput{
        HumorScore: 3, EnergiaScore: 3, SociabilidadeScore: 3, AutocuidadoScore: 3,
        Confidence: 3,
        SinaisObservados: []string{`ela disse "me sinto sozinha" outro dia`},
    }
    if err := ValidateSnapshotOutput(out); err == nil ||
        !strings.Contains(err.Error(), "literal-looking quotation") {
        t.Fatalf("expected privacy error, got: %v", err)
    }
}

func TestValidateSnapshotOutput_RejectsClinicalTerm(t *testing.T) {
    out := SnapshotOutput{
        HumorScore: 2, Confidence: 3,
        SinaisObservados: []string{"apresenta sintomas de depressao leve"},
    }
    if err := ValidateSnapshotOutput(out); err == nil ||
        !strings.Contains(err.Error(), "clinical term") {
        t.Fatalf("expected clinical-term error, got: %v", err)
    }
}

func TestValidateSnapshotOutput_RejectsFofoca(t *testing.T) {
    out := SnapshotOutput{
        HumorScore: 2, Confidence: 3,
        EventosDia: []string{"brigou com a filha sobre dinheiro"},
    }
    if err := ValidateSnapshotOutput(out); err == nil ||
        !strings.Contains(err.Error(), "fofoca") {
        t.Fatalf("expected fofoca error, got: %v", err)
    }
}

func TestValidateSnapshotOutput_AcceptsObservational(t *testing.T) {
    out := SnapshotOutput{
        HumorScore: 3, EnergiaScore: 3, SociabilidadeScore: 4, AutocuidadoScore: 5,
        HumorNuance: "saudosa do filho",
        SinaisObservados: []string{"mencionou tontura matinal"},
        EventosDia:       []string{"tomou pressao com a vizinha enfermeira"},
        Confidence:       3,
    }
    if err := ValidateSnapshotOutput(out); err != nil {
        t.Fatalf("expected accept, got: %v", err)
    }
}

func TestValidateSnapshotOutput_AcceptsZeroScoresLowConfidence(t *testing.T) {
    out := SnapshotOutput{
        HumorScore: 0, EnergiaScore: 0, SociabilidadeScore: 0, AutocuidadoScore: 0,
        Confidence: 1,
    }
    if err := ValidateSnapshotOutput(out); err != nil {
        t.Fatalf("expected accept (low confidence + zero scores ok), got: %v", err)
    }
}

func TestWriteSnapshot_SafetyAlertWhenCompanionMissed(t *testing.T) {
    // Mock client returns SafetyAlertNeeded when alertas_gerados is empty
    // but message clearly mentions chest pain.
    client := mockHaikuClient(t, SnapshotOutput{
        HumorScore: 2, Confidence: 3,
        EventosDia: []string{"queixa de dor no peito apos almoco"},
        SafetyAlertNeeded: &SafetyAlert{
            Severity:    "warn",
            Category:    "medico_fisico",
            Reason:      "queixa de dor toracica recorrente",
            Recommended: "considerar avaliacao medica hoje",
        },
    })
    in := SnapshotInput{
        User: User{ID: 1, Name: "Antonia"},
        Date: time.Now(),
        NewMessages: []ConversationMessage{
            {Role: "user", Text: "to com uma dor no peito chata desde o almoco", Timestamp: time.Now()},
        },
        AlertasGerados: nil, // companion não alertou
    }
    out, err := WriteSnapshot(context.Background(), client, in)
    if err != nil {
        t.Fatalf("unexpected: %v", err)
    }
    if out.SafetyAlertNeeded == nil {
        t.Fatal("expected safety alert when companion missed signal")
    }
}
```

### 13.3 Stubs de teste — report

```go
// bot/synthesis/report_test.go
func TestValidateReportOutput_RejectsQuote(t *testing.T) {
    out := ReportOutput{
        Tendencia: "estavel", NivelPreocupacao: "tranquilo",
        Resumo: `Ela disse "me sinto sozinha" essa semana.`,
    }
    if err := ValidateReportOutput(out); err == nil {
        t.Fatal("expected privacy error")
    }
}

func TestValidateReportOutput_AcceptsLongitudinalDescription(t *testing.T) {
    out := ReportOutput{
        Tendencia:        "piorando",
        Comparacao:       "humor 2.5 ultimos 7d vs 3.5 anteriores",
        HumorRecente:     "tem aparecido o tema saudade nas ultimas conversas",
        Resumo:           "Tem sido um periodo um pouco mais quieto. Vale uma atencao extra.",
        NivelPreocupacao: "atencao",
    }
    if err := ValidateReportOutput(out); err != nil {
        t.Fatalf("expected accept, got: %v", err)
    }
}

func TestSynthesize_EmptyWindow(t *testing.T) {
    // 0 snapshots → mock LLM should return indeterminado.
    client := mockSonnetClient(t, ReportOutput{
        Tendencia:        "indeterminado",
        NivelPreocupacao: "indeterminado",
        Resumo:           "Sem dados suficientes nesse periodo.",
    })
    in := ReportInput{
        Dependent: User{ID: 1, Name: "Antonia"},
        Days:      14,
        Snapshots: nil,
    }
    out, err := Synthesize(context.Background(), client, in)
    if err != nil {
        t.Fatalf("unexpected: %v", err)
    }
    if out.Tendencia != "indeterminado" {
        t.Errorf("expected indeterminado, got: %s", out.Tendencia)
    }
}

func TestSynthesize_TrendImproving(t *testing.T) {
    // Snapshots subindo → mock LLM retorna melhorando.
    snaps := genTrendSnapshots(t, 14, "up")
    client := mockSonnetClient(t, ReportOutput{
        Tendencia:        "melhorando",
        NivelPreocupacao: "tranquilo",
        Comparacao:       "humor 4.2 ultimos 7d vs 3.1 anteriores",
        Resumo:           "Ela tem estado mais animada essa semana.",
    })
    in := ReportInput{Dependent: User{ID: 1, Name: "Antonia"}, Days: 14, Snapshots: snaps}
    out, err := Synthesize(context.Background(), client, in)
    if err != nil {
        t.Fatalf("unexpected: %v", err)
    }
    if out.Tendencia != "melhorando" {
        t.Errorf("expected melhorando, got: %s", out.Tendencia)
    }
}
```

### 13.4 Stubs de teste — UPSERT e timeline

```go
// bot/db_psych_test.go
func TestUpsertPsychSnapshot_SameDayUpdates(t *testing.T) {
    db := setupTestDB(t)
    elder := createElder(t, db, "Antonia")
    today := time.Now().Truncate(24 * time.Hour)

    s1 := synthesis.DailySnapshot{
        UserID: elder.ID, SnapshotDate: today,
        HumorScore: 3, Confidence: 2, NMessages: 3,
    }
    if err := db.UpsertPsychSnapshot(&s1); err != nil {
        t.Fatal(err)
    }
    s2 := synthesis.DailySnapshot{
        UserID: elder.ID, SnapshotDate: today,
        HumorScore: 4, Confidence: 4, NMessages: 12,
    }
    if err := db.UpsertPsychSnapshot(&s2); err != nil {
        t.Fatal(err)
    }
    snaps, _ := db.GetPsychSnapshots(elder.ID, today, today)
    if len(snaps) != 1 {
        t.Fatalf("expected 1 row, got %d", len(snaps))
    }
    if snaps[0].HumorScore != 4 || snaps[0].NMessages != 12 {
        t.Errorf("expected upserted values, got: %+v", snaps[0])
    }
}

func TestTimelineEndpoint_EmptyForNewElder(t *testing.T) {
    srv := setupTestServer(t)
    guardian := createUser(t, srv.db, "Caio")
    elder := createElder(t, srv.db, "Antonia")
    srv.db.LinkFamily(guardian.ID, elder.ID, "filho_de", true, true, true)

    req := authedReq(t, guardian, "GET",
        fmt.Sprintf("/api/v1/family/dependents/%d/timeline?days=30", elder.ID))
    rec := httptest.NewRecorder()
    srv.handleDependentTimeline(rec, req)

    if rec.Code != 200 {
        t.Fatalf("expected 200, got: %d", rec.Code)
    }
    var body struct {
        Snapshots []TimelinePoint `json:"snapshots"`
    }
    json.NewDecoder(rec.Body).Decode(&body)
    if len(body.Snapshots) != 0 {
        t.Fatalf("expected empty, got: %d", len(body.Snapshots))
    }
}

func TestTimelineEndpoint_NotAGuardian(t *testing.T) {
    srv := setupTestServer(t)
    bob := createUser(t, srv.db, "Bob")
    elder := createElder(t, srv.db, "Antonia") // Bob não é guardian dela

    req := authedReq(t, bob, "GET", fmt.Sprintf("/api/v1/family/dependents/%d/timeline", elder.ID))
    rec := httptest.NewRecorder()
    srv.handleDependentTimeline(rec, req)

    if rec.Code != 403 {
        t.Fatalf("expected 403, got: %d", rec.Code)
    }
}

func TestCatchupSkipsRevokedConsent(t *testing.T) {
    db := setupTestDB(t)
    elder := createElder(t, db, "Antonia")
    guardian := createUser(t, db, "Caio")
    db.LinkFamily(guardian.ID, elder.ID, "filho_de", true, true, true)
    db.UpdateDependentConsent(elder.ID, guardian.ID, "revoked")

    insertMessage(t, db, elder.ID, "ola", time.Now().Add(-3*time.Hour))

    haikuCalled := 0
    s := newTestSchedulerWithHaiku(db, &haikuCalled)
    s.runDailyPsychSnapshotCatchup()

    if haikuCalled != 0 {
        t.Fatalf("expected 0 haiku calls (consent revoked), got %d", haikuCalled)
    }
}
```

### 13.5 Stubs de teste — escalation (mantidos, mantém compatibilidade)

```go
// bot/scheduler_inactivity_test.go
func TestInactivityEscalation_NoDuplicateOnRestart(t *testing.T) {
    db := setupTestDB(t)
    elder := createElder(t, db, "Antonia")
    guardian := createUser(t, db, "Caio")
    db.LinkFamily(guardian.ID, elder.ID, "filho_de", false, true, false)
    attemptID := db.RecordProactiveAttempt(elder.ID, time.Now().Add(-5*time.Hour), "sent")

    sent := 0
    s := newTestScheduler(db, func(phone, text string) error { sent++; return nil })
    s.checkInactivityForElder(&elder)
    s.checkInactivityForElder(&elder) // segundo tick
    s.checkInactivityForElder(&elder) // simula restart
    if sent != 1 {
        t.Fatalf("expected 1 send, got %d", sent)
    }
    rows := countEscalations(t, db, elder.ID, "inactivity", attemptID)
    if rows != 1 {
        t.Fatalf("expected 1 escalation row, got %d", rows)
    }
}
```

---

## 14. Plano de implementação granular (PRs)

| PR        | Título                                                                | Conteúdo                                                                                                                                                                                                                                                                                                                                                                                              | Depende de                              |
| --------- | --------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------- |
| **PR-5.1** | `feat(synthesis): split em writer (Haiku) + report (Sonnet)`          | `bot/synthesis/{synthesis,writer,writer_prompt,report,report_prompt,validation}.go`. Ambos os system prompts completos em pt-BR. Validações com regex de aspas + termos clínicos + keywords de fofoca. Testes T8–T12, T14–T17. Refatora a função única antiga em duas entrypoints. | —                                       |
| **PR-5.2** | `feat(db): tabela psych_state_daily + helpers + DDL`                  | DDL `psych_state_daily` (esta fase declara). Helpers: `UpsertPsychSnapshot`, `GetPsychSnapshots`, `GetPsychSnapshotForDate`, `GetTimelinePoints`, `GetMedicationStats7d`, `GetProactiveAttemptsStats`, `GetOpenAlerts`, `GetMessagesSinceForUser`, `GetMedicationIntakeOnDay`, `GetSocialContextRiskMemos` (LIKE 'risco:%'), `GetAlertsOnDay`, `GetUsersWithMessagesOnDay`, `GetGuardiansForInactivity` (wrapper sobre `GetGuardians` da Fase 1), `HasOpenEscalation`, `CreateEscalation`, `UpdateEscalationStatus`, `GetDependentConsent`, `ListUsersByType`. Testes T13. **NÃO** declara `escalations.proactive_attempt_id` — Fase 4 dona. | Fases 1, 3, 4 mergeadas                 |
| **PR-5.3** | `feat(tool): status_dependente longitudinal (Sonnet)`                 | Tool nova com Sonnet pro report. `bot/tools_family.go`: `handleStatusDependente`, `BuildDependentStatus`, `formatStatusForChat`, `resolveDependent`, `toSynthesisAlerts`. Schema JSON com `days`. Audit `status_dependente_consulted` + `synthesis_executed/failed`. Testes T1–T4, T25.                                                                                                                | PR-5.1, PR-5.2                          |
| **PR-5.4** | `feat(api): endpoints status + timeline`                              | `bot/api_family.go`: `handleDependentStatus`, `handleDependentTimeline`, `parseDependentIDFromPath`, `toAPIResponse`, cache 60s. Registro de rotas em `bot/api.go`. Testes T1–T7, T23, T24.                                                                                                                                                                                                            | PR-5.3, Fase 2 (auth middleware)        |
| **PR-5.5** | `feat(scheduler): inactivity_escalation + psych_snapshot_catchup`     | Em `bot/scheduler.go`: `checkInactivityEscalation`, `runDailyPsychSnapshotCatchup`, `catchupSnapshotForElder`, `handleSafetyAlertFromWriter`, `countSessions`, `estimateDurationMinutes`, `shouldRun`. Struct adendo (`mu`, `lastRun`, `audit`, `haikuClient`, `ctx`). Testes T18–T22.                                                                                                                  | PR-5.1, PR-5.2                          |
| **PR-5.6** | `feat(web): dashboard com timeline e gráfico longitudinal`            | `web/app/dashboard/family/[id]/page.tsx` (atualizar pra usar tendencia/comparacao). `web/app/dashboard/family/[id]/evolucao/page.tsx` (nova). `web/components/family/{StatusHeader,SynthesisCard,PsychTimeline,MetricCard,AlertList}.tsx`. `web/lib/api/family.ts` (Timeline + DependentStatus). Setup recharts. Testes Playwright básicos da rota nova.                                              | PR-5.4 + Fase 2 layout                  |
| **PR-5.7** | `chore(privacy): aviso periodico ao idoso`                            | Cron mensal `notifyDependentAboutGuardianAccess` que envia mensagem agregada se houve ≥1 consulta no último mês. Lê `action_log` filtrado por `status_dependente_consulted` e `timeline_consulted`.                                                                                                                                                                                                  | PR-5.3                                   |
| **PR-5.8** | `docs: politica de privacidade da Fase 5 (longitudinal)`              | README/docs atualizado com §12 deste plano. Onboarding (Fase 2) ganha texto explícito: scores 1-5, tendencia, snapshots diários inferidos, sem citação literal, direito de delete.                                                                                                                                                                                                                    | PR-5.6                                   |

Sequência sugerida: 5.1 + 5.2 paralelos → 5.3 + 5.5 paralelos → 5.4 → 5.6 → 5.7 + 5.8.

---

## 15. Riscos da fase

| Risco                                                                            | Probabilidade           | Impacto    | Mitigação                                                                                                                                                                                                                          |
| -------------------------------------------------------------------------------- | ----------------------- | ---------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Privacidade vazada** — writer cita frase literal do idoso no DB.               | Média                   | Crítico    | (a) Validação `quoteRegex` no writer rejeita output com aspas literais. (b) Prompt do writer tem 4+ exemplos negativos. (c) Auditoria registra falhas de validação para review. (d) Mesma validação no report.                     |
| **Writer alucina sintoma** que não existe nas mensagens.                         | Média                   | Alto       | Prompt diz "se faltar dado, retorne 0 e baixe confidence". Validação de range. Temperatura 0.2.                                                                                                                                    |
| **Snapshot escrito sem confidence** vira ruído no gráfico.                       | Alta                    | Médio      | Frontend renderiza pontos com confidence < 3 em opacidade reduzida. UI explica.                                                                                                                                                    |
| **Tendência calculada errada** com poucos snapshots.                             | Alta no início          | Médio      | Prompt define "indeterminado" quando < 4 snapshots com confidence >= 2.                                                                                                                                                            |
| **Custo Sonnet se chamado em loop pela UI.**                                     | Alta                    | Médio      | Cache server-side de 60s por (guardian, dependent, days). Tool no chat não tem cache, é user-driven. Monitorar via audit.                                                                                                          |
| **Latência Sonnet no chat WhatsApp.**                                            | Média                   | Médio      | Sonnet 4.7 é ~2-4s típico. Aceitável pra "como está minha mãe?". Se subir, considerar streaming (já suportado pelo SDK).                                                                                                           |
| **Custo Haiku writer** se idoso é falante (10 conversas/dia).                    | Baixa                   | Baixo      | Trigger pós-conversa significativa (Fase 4) tem debounce (≥ 1 conversa/30min). Custo médio: ~$0.005/dia/idoso = $0.15/mês/idoso.                                                                                                  |
| **Escalação dispara em massa após restart.**                                     | Baixa (com idempotência) | Alto       | Idempotência por `(dependent_id, policy_name, proactive_attempt_id)`. Teste T18 cobre.                                                                                                                                             |
| **Falsos positivos de inatividade** (idoso só viajou).                           | Média                   | Baixo      | Severity é `warn`, mensagem gentil. Limite default 4h é configurável.                                                                                                                                                              |
| **Consent revogado, snapshots antigos vazam pelo cache.**                        | Baixa                   | Médio      | Cache key inclui consent? Não — invalida-se cache na mudança de consent (UpdateConsent emite evento → `statusCache.invalidate(guardianID, dependentID)`). TTL de 60s mitigatório.                                                  |
| **Memos `risco:*` mal-rotulados** (Claude marca fofoca como risco).              | Média                   | Médio      | Prompt da Fase 4 tem exemplos de uso correto. Auditoria semanal de uma amostra de memos `risco:*`. Em duvida: writer ainda filtra fofoca via keyword no output (defesa em profundidade).                                            |
| **Bug de fuzzy match** resolve nome para dependente errado.                      | Baixa                   | Alto       | `pickByNameFuzzy` busca apenas dentro de `GetDependents(guardianID)`. Em duplicidade, exigir desempate.                                                                                                                            |
| **Timezone errado** no UPSERT — snapshot do dia X cai no slot do dia Y.          | Baixa                   | Médio      | TZ resolvido por `time.LoadLocation(elder.Timezone)`. Default UTC se ausente. Teste T13 cobre. Catchup conservador: re-escreve mesmo se já existe com confidence baixa.                                                            |
| **Writer dispara safety_alert duplicado** com a do companion.                    | Baixa                   | Médio      | Prompt do writer inclui `alertas_gerados_hoje` e regra explícita "não duplique se já existe alerta crítico hoje sobre tema parecido". Em integração, `handleSafetyAlertFromWriter` reusa `handleAlertarFamiliaInternal` que já tem dedup. |

---

## 16. Checklist de pronto

Antes de declarar a fase completa:

- [ ] Todos os PRs 5.1–5.8 mergeados em `main`.
- [ ] `go test -v ./bot/... ./bot/synthesis/...` verde.
- [ ] `pnpm test` (web) verde, incluindo Playwright da rota `/evolucao`.
- [ ] Lint Go (`go vet`, `staticcheck`) zero warnings novos.
- [ ] Lint TS (`pnpm lint`) zero warnings novos no diff.
- [ ] Manual smoke: 1 família piloto com 1 idoso real, 14 dias de uso. Responsável usa tool no WhatsApp E web. Gráfico timeline carrega com dados reais. Sem 5xx. Sem leak de citação literal nos logs.
- [ ] `audit_log` filtrado por `synthesis_failed` mostra <5% de falhas.
- [ ] `audit_log` filtrado por `psych_snapshot_failed` mostra <5% de falhas.
- [ ] Aviso ao idoso (PR-5.7) disparado pelo menos uma vez em ambiente de teste.
- [ ] Política de privacidade escrita (PR-5.8) revisada e linkada do onboarding (Fase 2).
- [ ] Pré-requisito: Fase 4 mergeada (entrega `escalations.proactive_attempt_id`, índice `idx_escalations_inactivity_lookup`, trigger de snapshot pós-conversa). Esta fase NÃO declara essas colunas.
- [ ] Pré-requisito: Fase 4 mergeada (memos `social_context.risco:*` documentados em §5.3).
- [ ] Métricas de custo Haiku (writer) e Sonnet (report) loggadas via `resp.Usage`. Alerta se >$1.00/dia/família.
- [ ] Teste E2E manual de revogação de consentimento: dependente com `consent_status=revoked` retorna 403 em ambos `/status` e `/timeline`, e tool retorna mensagem padrão. Catchup pula o user.
- [ ] Documentação da nova tool em `bot/agent.go` `buildSystemPromptStable` (lista "Ferramentas disponiveis"): incluir `status_dependente` com 1 linha descritiva.
- [ ] CHANGELOG / git log claros: cada PR tem mensagem que justifica o "porquê" (privacidade, robustez, longitudinal), não só o "o que".

---

## 17. Anexos

### A. Helpers de relação português

```go
func relationshipPT(rel string) string {
    switch rel {
    case "filho_de", "filha_de":
        return "mae"
    case "marido_de":
        return "esposa"
    case "esposa_de":
        return "marido"
    case "neto_de", "neta_de":
        return "avo"
    case "sobrinho_de", "sobrinha_de":
        return "tia"
    default:
        return "familiar"
    }
}

func firstName(full string) string {
    parts := strings.Fields(full)
    if len(parts) == 0 {
        return full
    }
    return parts[0]
}

func humanizeDuration(d time.Duration) string {
    h := int(d.Hours())
    if h < 1 {
        return "menos de uma hora"
    }
    if h == 1 {
        return "1 hora"
    }
    if h < 24 {
        return fmt.Sprintf("%d horas", h)
    }
    days := h / 24
    if days == 1 {
        return "1 dia"
    }
    return fmt.Sprintf("%d dias", days)
}
```

### B. Schema das tabelas tocadas (referência consolidada)

```sql
-- Fase 1
ALTER TABLE users ADD COLUMN type TEXT NOT NULL DEFAULT 'comum';
ALTER TABLE users ADD COLUMN last_user_message_at DATETIME;
ALTER TABLE users ADD COLUMN inactivity_escalate_after TEXT NOT NULL DEFAULT '4h';

CREATE TABLE family_links (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    guardian_id INTEGER NOT NULL REFERENCES users(id),
    dependent_id INTEGER NOT NULL REFERENCES users(id),
    relationship TEXT NOT NULL,
    notify_on_medication_miss INTEGER NOT NULL DEFAULT 1,
    notify_on_inactivity      INTEGER NOT NULL DEFAULT 1,
    notify_on_severe_signal   INTEGER NOT NULL DEFAULT 1,
    dependent_consent_status  TEXT NOT NULL DEFAULT 'granted',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(guardian_id, dependent_id)
);

-- Fase 3
CREATE TABLE medication_intake_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    medication_id INTEGER NOT NULL REFERENCES medications(id),
    scheduled_at DATETIME NOT NULL,
    status TEXT NOT NULL,
    confirmed_at DATETIME,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE escalations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    dependent_id INTEGER NOT NULL REFERENCES users(id),
    guardian_id  INTEGER NOT NULL REFERENCES users(id),
    policy_name  TEXT NOT NULL,
    severity     TEXT NOT NULL,
    message      TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    proactive_attempt_id INTEGER,                  -- ADITIVO Fase 4
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_escalations_inactivity_lookup    -- ADITIVO Fase 4
    ON escalations(dependent_id, policy_name, proactive_attempt_id, status);

-- Fase 4
CREATE TABLE proactive_attempts (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id       INTEGER NOT NULL REFERENCES users(id),
    attempted_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    message_sent  TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'sent',
    replied_at    DATETIME
);

-- Fase 5 (NOVO — DECLARADO POR ESTA FASE)
CREATE TABLE psych_state_daily (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    snapshot_date DATE NOT NULL,
    humor_score INTEGER,
    humor_nuance TEXT NOT NULL DEFAULT '',
    energia_score INTEGER,
    sociabilidade_score INTEGER,
    autocuidado_score INTEGER,
    sinais_observados TEXT NOT NULL DEFAULT '[]',
    eventos_dia TEXT NOT NULL DEFAULT '[]',
    n_conversations INTEGER NOT NULL DEFAULT 0,
    n_messages INTEGER NOT NULL DEFAULT 0,
    duration_minutes INTEGER NOT NULL DEFAULT 0,
    confidence INTEGER NOT NULL DEFAULT 1,
    inferred_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(user_id, snapshot_date)
);
CREATE INDEX idx_psych_state_user_date
    ON psych_state_daily(user_id, snapshot_date DESC);
```

Esta fase **declara apenas** `psych_state_daily` (+ índice). Tudo o mais é entregue pelas Fases 1, 3 e 4.

---

**Fim do plano.**
