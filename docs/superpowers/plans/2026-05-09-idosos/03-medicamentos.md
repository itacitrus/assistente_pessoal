# Fase 3 — Medicamentos, Lembretes Ativos e Escalação

**Data:** 2026-05-09
**Autor:** Giovanni (planejado com Claude)
**Status:** Planejamento — aguardando aprovação
**Depende de:** Fase 1 (`family_links`, `notify_on_medication_miss`, `users.type`)
**Habilita:** Fase 4 (companion psicológico precisa do contexto de medicação), Fase 5 (painel do responsável usa `medication_intake_log`).

---

## 1. Objetivo e não-objetivos

### Objetivo

Permitir que um idoso (ou qualquer usuário) cadastre medicamentos com horários, e que o Lurch dispare lembretes via WhatsApp, insista até obter confirmação, e — se silêncio persistir — escale para os familiares cadastrados em `family_links`. O motor de escalação é genérico: se aplica a qualquer `pending_confirmation` crítica, não só remédio.

Adicionalmente, suportar entrada por **foto da receita médica**: o usuário tira uma foto, o Lurch usa Claude Vision para extrair os medicamentos, apresenta item a item em linguagem natural ("Vi que você precisa tomar Losartana 50mg uma vez por dia. Em qual horário você prefere?"), e só persiste após confirmação.

### Não-objetivos

- Voz / Twilio: fica para Fase 6+. A interface `Notifier` é definida agora, mas só `WhatsAppNotifier` é implementada.
- Integração com farmácias / OCR de bula: fora do escopo.
- Ajuste automático de dose / interação medicamentosa: o sistema não aconselha clinicamente; só lembra e registra. Disclaimer obrigatório no system prompt do agente quando lidar com medicamentos.
- UI web do responsável: Fase 5. Aqui só geramos os dados que ela vai consumir.
- Lembretes de remédio em fuso de viagem: implementar de forma a respeitar `user_travel_periods` desde já (RRULE expandida no fuso vigente naquele dia), mas sem heurísticas avançadas (jet-lag, ajuste de dose por horário de destino).

---

## 2. DDL completo

Tudo abaixo entra no método `migrate()` em `bot/db.go`. Aditivo, idempotente. Para `pending_confirmations` precisamos de migração de coluna; tratada na Seção 3.

```sql
-- Cadastro de medicamento
CREATE TABLE IF NOT EXISTS medications (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                TEXT    NOT NULL,
    dose                TEXT    NOT NULL DEFAULT '',
    instructions        TEXT    NOT NULL DEFAULT '',
    active              INTEGER NOT NULL DEFAULT 1,
    created_by_user_id  INTEGER NOT NULL REFERENCES users(id),
    created_at          DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at          DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_medications_user_active
    ON medications(user_id, active);

-- Horários (RRULE iCal). Um medication tem 1..N schedules.
CREATE TABLE IF NOT EXISTS medication_schedules (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    medication_id   INTEGER NOT NULL REFERENCES medications(id) ON DELETE CASCADE,
    rrule           TEXT    NOT NULL,                -- ex: "FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0"
    start_date      TEXT    NOT NULL,                -- YYYY-MM-DD, na tz do user no momento do cadastro
    end_date        TEXT,                            -- nullable: tratamento contínuo
    critical        INTEGER NOT NULL DEFAULT 0,      -- 1 = política critical (5 tentativas, 3 min)
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_med_sched_med
    ON medication_schedules(medication_id);

-- Histórico de tomada. UNIQUE garante idempotência do scheduler.
CREATE TABLE IF NOT EXISTS medication_intake_log (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    medication_id   INTEGER NOT NULL REFERENCES medications(id) ON DELETE CASCADE,
    scheduled_at    DATETIME NOT NULL,                  -- UTC
    status          TEXT NOT NULL CHECK(status IN ('pending','taken','skipped','missed','escalated')),
    confirmed_at    DATETIME,                           -- quando user respondeu
    response_text   TEXT,                               -- texto cru da resposta
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(medication_id, scheduled_at)
);
CREATE INDEX IF NOT EXISTS idx_intake_med_time
    ON medication_intake_log(medication_id, scheduled_at);
CREATE INDEX IF NOT EXISTS idx_intake_status
    ON medication_intake_log(status);

-- Histórico de escalações por pending_confirmation (uma row por tentativa).
CREATE TABLE IF NOT EXISTS escalations (
    id                       INTEGER PRIMARY KEY AUTOINCREMENT,
    pending_confirmation_id  INTEGER NOT NULL REFERENCES pending_confirmations(id) ON DELETE CASCADE,
    policy_name              TEXT    NOT NULL,                            -- ex: 'medication_default'
    attempt_number           INTEGER NOT NULL,                            -- 1..N
    scheduled_for            DATETIME NOT NULL,                           -- UTC, quando o disparo foi agendado
    status                   TEXT    NOT NULL CHECK(status IN ('pending','sent','acknowledged','escalated_to_family','failed')),
    notifier_used            TEXT    NOT NULL DEFAULT 'whatsapp',         -- 'whatsapp' | 'voice' (futuro)
    recipient_user_id        INTEGER NOT NULL REFERENCES users(id),       -- quem recebeu
    sent_at                  DATETIME,
    created_at               DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(pending_confirmation_id, attempt_number, recipient_user_id)
);
CREATE INDEX IF NOT EXISTS idx_escalations_pc
    ON escalations(pending_confirmation_id);
CREATE INDEX IF NOT EXISTS idx_escalations_status_sched
    ON escalations(status, scheduled_for);
```

### Justificativa dos `ON DELETE`

- `medications.user_id ON DELETE CASCADE`: ao excluir usuário, as medicações somem junto (sem dados órfãos).
- `medications.created_by_user_id` **sem CASCADE**: histórico de quem criou pode persistir mesmo se o cadastrador foi removido (caso responsável crie pra idoso).
- `medication_schedules.medication_id ON DELETE CASCADE`: schedules sem medication não fazem sentido.
- `medication_intake_log.medication_id ON DELETE CASCADE`: log fica órfão sem medicação.
- `escalations.pending_confirmation_id ON DELETE CASCADE`: idem.

### `UNIQUE(medication_id, scheduled_at)`

Esta é a chave de idempotência do scheduler. Se o cron rodar duas vezes no mesmo minuto (clock skew, restart), o segundo `INSERT` falha com `UNIQUE constraint failed` e o disparo é absorvido. **Sem isto, restarts no segundo certo geram lembretes duplicados.**

### `UNIQUE(pending_confirmation_id, attempt_number, recipient_user_id)`

Mesmo princípio para escalação: cada tentativa para cada destinatário é registrada uma única vez. Permite escalar pra múltiplos guardians na mesma tentativa (cada guardian é uma row separada).

---

## 3. Migrations idempotentes em Go

`pending_confirmations` precisa ganhar duas colunas. Não dá para usar `CREATE TABLE IF NOT EXISTS` com colunas novas porque a tabela já existe em produção. SQLite não tem `ADD COLUMN IF NOT EXISTS`, então usamos o mesmo padrão já presente em `db.go:160-174` (rodar `ALTER TABLE` e ignorar `duplicate column`).

Adicionar ao slice `additive` em `bot/db.go`:

```go
additive := []string{
    // ... migrations existentes ...

    // Discriminador entre evento de calendário e lembrete de remédio.
    // Default 'event' preserva semântica anterior (todas rows pré-Fase-3 são eventos).
    `ALTER TABLE pending_confirmations ADD COLUMN kind TEXT NOT NULL DEFAULT 'event'`,

    // Política de escalação aplicada à pending. NULL = sem escalação (default
    // pra eventos de calendário — eles auto-confirmam via timeout, não escalam).
    `ALTER TABLE pending_confirmations ADD COLUMN escalation_policy TEXT`,

    // last_attempt_at é o lock de idempotência do scheduler de escalação.
    // Sem isto, dois ticks do cron com diferença de 1s podem escalar duas vezes.
    `ALTER TABLE pending_confirmations ADD COLUMN last_attempt_at DATETIME`,

    // attempt_number é incrementado pelo motor de escalação.
    `ALTER TABLE pending_confirmations ADD COLUMN attempt_number INTEGER NOT NULL DEFAULT 0`,
}
```

### CHECK constraint

SQLite **não suporta** `ALTER TABLE ADD CONSTRAINT`. Para adicionar `CHECK(kind IN ('event','medication'))` numa tabela existente, seria necessário um swap-table (cópia + rename). Decisão: validar `kind` na camada de aplicação (em `db.go:CreatePendingConfirmation` e `db.go:GetPendingConfirmations*`). Mais simples, menos arriscado, e ainda passa por code review.

```go
// validKinds é checado em todos os pontos de inserção e leitura.
var validKinds = map[string]bool{"event": true, "medication": true}

func validatePendingKind(k string) error {
    if !validKinds[k] {
        return fmt.Errorf("invalid pending_confirmations.kind: %q", k)
    }
    return nil
}
```

### Migração de dados existentes

Não precisa. `DEFAULT 'event'` aplica retroativamente a todas as rows que existirem. `escalation_policy` fica NULL (= sem escalação) para todas elas, o que é o comportamento correto: eventos de calendário criados antes da Fase 3 nunca tiveram escalação e devem continuar usando o auto-confirm via timeout (`bot/scheduler.go:102-133`).

### Verificação pós-migração

Adicionar a `db_test.go` um teste que cria uma DB nova, roda `migrate()`, depois insere uma `pending_confirmation` sem campo `kind` explícito, e verifica que veio como `'event'`. Garante que ninguém quebrou o default por acidente.

---

## 4. Tipos Go

Arquivo novo: `bot/medication.go`. Tipos puros, sem lógica de DB (essa fica em `bot/db_medication.go`).

```go
package main

import (
    "context"
    "time"
)

// Medication é o cadastro mestre. Um medication tem 1..N schedules.
type Medication struct {
    ID               int64
    UserID           int64     // dono (quem toma o remédio)
    Name             string    // "Losartana"
    Dose             string    // "50mg" — texto livre porque pode ser "1 colher", "2 gotas"
    Instructions     string    // "tomar com água em jejum" — texto livre
    Active           bool      // soft-delete via active=false
    CreatedByUserID  int64     // pode ser != UserID (responsável cadastrou pro idoso)
    CreatedAt        time.Time
    UpdatedAt        time.Time
}

// MedicationSchedule é um RRULE iCal aplicado ao medication.
// Usuário pode ter múltiplos schedules pro mesmo medication
// (ex: "lunes/quartas/sextas 8h" + "diariamente 20h" — raro, mas suportado).
type MedicationSchedule struct {
    ID            int64
    MedicationID  int64
    RRULE         string    // "FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0"
    StartDate     time.Time // YYYY-MM-DD parseado em BRT (ou tz do user no cadastro)
    EndDate       *time.Time // nil = tratamento contínuo
    Critical      bool      // afeta política de escalação
    CreatedAt     time.Time
}

// MedicationIntakeLog é o histórico real de tomadas.
// UNIQUE(medication_id, scheduled_at) é a chave de idempotência.
type MedicationIntakeLog struct {
    ID            int64
    MedicationID  int64
    ScheduledAt   time.Time // UTC, instante exato da ocorrência prevista
    Status        IntakeStatus
    ConfirmedAt   *time.Time
    ResponseText  string
    CreatedAt     time.Time
}

type IntakeStatus string

const (
    IntakePending   IntakeStatus = "pending"
    IntakeTaken     IntakeStatus = "taken"
    IntakeSkipped   IntakeStatus = "skipped"
    IntakeMissed    IntakeStatus = "missed"
    IntakeEscalated IntakeStatus = "escalated"
)

// Escalation é uma row por tentativa por destinatário.
// Várias tentativas pra mesma pending_confirmation_id (attempt_number incremental).
type Escalation struct {
    ID                     int64
    PendingConfirmationID  int64
    PolicyName             string
    AttemptNumber          int
    ScheduledFor           time.Time // UTC
    Status                 EscalationStatus
    NotifierUsed           string // "whatsapp" | "voice"
    RecipientUserID        int64
    SentAt                 *time.Time
    CreatedAt              time.Time
}

type EscalationStatus string

const (
    EscPending             EscalationStatus = "pending"
    EscSent                EscalationStatus = "sent"
    EscAcknowledged        EscalationStatus = "acknowledged"
    EscEscalatedToFamily   EscalationStatus = "escalated_to_family"
    EscFailed              EscalationStatus = "failed"
)

// EscalationTarget controla o destinatário da escalação final.
type EscalationTarget string

const (
    EscalateToFamily   EscalationTarget = "family"     // notify guardians via family_links
    EscalateToSelfOnly EscalationTarget = "self_only"  // só insiste com o próprio usuário
    EscalateToNone     EscalationTarget = "none"       // sem escalação (não cria escalations rows)
)

// EscalationContext é passado ao formatter da policy pra renderizar mensagens.
type EscalationContext struct {
    User           *User           // quem deveria responder
    Medication     *Medication     // contexto opcional (pode ser nil)
    ScheduledAt    time.Time       // UTC
    AttemptNumber  int             // 1, 2, 3...
    Recipient      *User           // pode ser o próprio user ou guardian
    IsFinalEscalation bool         // true se attempt == MaxAttempts (último disparo, vai pra família)
}

// EscalationPolicy é a abstração genérica.
// Política é DADO: política nova = struct nova no map em escalation.go.
type EscalationPolicy struct {
    Name           string
    MaxAttempts    int
    Interval       time.Duration  // entre tentativas
    EscalateTo     EscalationTarget
    EscalationMsg  func(ctx EscalationContext) string
}

// Notifier abstrai o canal de envio. Hoje só WhatsApp;
// Twilio voz vem na Fase 6 sem mudar nada acima.
type Notifier interface {
    Send(ctx context.Context, recipient *User, message string) error
    Channel() string // "whatsapp", "voice"
}

// WhatsAppNotifier embrulha o callback sendMsg(phone, text) que já existe
// em handler.SendTextToPhone. Mantém compat com o resto do bot e isola
// o scheduler/escalação do detalhe de transporte.
type WhatsAppNotifier struct {
    sendMsg func(phone, text string) error
}

func NewWhatsAppNotifier(sendMsg func(phone, text string) error) *WhatsAppNotifier {
    return &WhatsAppNotifier{sendMsg: sendMsg}
}

func (n *WhatsAppNotifier) Send(ctx context.Context, recipient *User, message string) error {
    if recipient == nil {
        return fmt.Errorf("WhatsAppNotifier.Send: nil recipient")
    }
    return n.sendMsg(recipient.PhoneNumber, message)
}

func (n *WhatsAppNotifier) Channel() string { return "whatsapp" }
```

### IntentData reuso

Para encaixar `kind=medication` no fluxo de `pending_confirmations`, estendemos a struct `IntentData` (em `agent.go`) com um sub-objeto opcional:

```go
type IntentData struct {
    // ... campos existentes (Title, Date, Time, etc) ...

    // Quando este pending é de medicação, o blob abaixo é populado e
    // os campos de evento ficam vazios.
    Medication *MedicationIntent `json:"medication,omitempty"`
}

type MedicationIntent struct {
    // Para "criar cadastro de medicação" pendente de confirmação:
    Name           string `json:"name,omitempty"`
    Dose           string `json:"dose,omitempty"`
    Instructions   string `json:"instructions,omitempty"`
    ScheduleRRULE  string `json:"schedule_rrule,omitempty"`
    StartDate      string `json:"start_date,omitempty"` // YYYY-MM-DD
    EndDate        string `json:"end_date,omitempty"`
    Critical       bool   `json:"critical,omitempty"`

    // Para "lembrete de tomada" pendente de confirmação:
    MedicationID   int64     `json:"medication_id,omitempty"`
    ScheduledAt    time.Time `json:"scheduled_at,omitempty"` // UTC
    Reminder       bool      `json:"reminder,omitempty"`     // true quando é o lembrete da hora
}
```

`Medication != nil && Reminder == true` ⇒ pending é "tomei o remédio?" disparado pelo scheduler.
`Medication != nil && Reminder == false` ⇒ pending é "confirma cadastro deste medicamento?".
`Medication == nil` ⇒ é um evento de calendário (caminho atual).

---

## 5. Tools — schemas JSON completos

Todas as tools registram-se em `bot/tools.go` no map `toolHandlers`, e suas definições JSON entram em `bot/agent.go:buildToolDefinitions()` seguindo o padrão visível em `agent.go:459-662`. Implementações vão para `bot/tools_medication.go` (arquivo novo).

### 5.1 Registry adicionado em `bot/tools.go`

```go
var toolHandlers = map[string]ToolHandler{
    // ... handlers existentes ...
    "cadastrar_medicamento":  handleCadastrarMedicamento,
    "listar_medicamentos":    handleListarMedicamentos,
    "editar_medicamento":     handleEditarMedicamento,
    "cancelar_medicamento":   handleCancelarMedicamento,
    "marcar_remedio_tomado":  handleMarcarRemedioTomado,
    "pular_dose":             handlePularDose,
}
```

### 5.2 Schemas JSON (em `agent.go:buildToolDefinitions`)

```go
{
    Name:        "cadastrar_medicamento",
    Description: "Cadastra um medicamento com horários para o usuario tomar. Cria pending_confirmation; o usuario confirma na proxima mensagem antes da persistencia. Use schedule_rrule no formato iCal: 'FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0' para 'todos os dias as 8h, 14h e 20h'. Para 'segundas e quartas as 9h': 'FREQ=WEEKLY;BYDAY=MO,WE;BYHOUR=9;BYMINUTE=0'.",
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "target_user": {"type": "string", "description": "Nome do usuario para quem o medicamento e cadastrado. Omitir = self. Se preenchido e diferente, valida via family_links."},
            "name": {"type": "string", "description": "Nome do medicamento (ex: Losartana, AAS, Metformina)"},
            "dose": {"type": "string", "description": "Dose (ex: '50mg', '1 comprimido', '10 gotas')"},
            "instructions": {"type": "string", "description": "Instrucoes adicionais (ex: 'em jejum', 'apos almoco', 'com agua')"},
            "schedule_rrule": {"type": "string", "description": "Regra iCal RRULE. Ex: 'FREQ=DAILY;BYHOUR=8;BYMINUTE=0' (1x/dia 8h), 'FREQ=DAILY;BYHOUR=8,20;BYMINUTE=0' (2x/dia)."},
            "start_date": {"type": "string", "description": "Data de inicio (YYYY-MM-DD). Default: hoje."},
            "end_date": {"type": "string", "description": "Data de fim (YYYY-MM-DD, inclusiva). Omitir = continuo."},
            "critical": {"type": "boolean", "description": "Se true, usa politica medication_critical (5 tentativas, 3min, escala mais rapido). Default false."}
        },
        "required": ["name", "schedule_rrule"]
    }`),
},
{
    Name:        "listar_medicamentos",
    Description: "Lista medicamentos ativos do usuario. Se target_user != self, valida via family_links.",
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "target_user": {"type": "string", "description": "Nome do usuario (omitir = self). Permissao via family_links."}
        }
    }`),
},
{
    Name:        "editar_medicamento",
    Description: "Edita campos de um medicamento existente. Para mudar horario, passe novo schedule_rrule (substitui todos os schedules atuais). Cria pending_confirmation pra confirmar antes de persistir.",
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "medication_id": {"type": "integer", "description": "ID do medicamento (obtido via listar_medicamentos). Preferivel."},
            "name_query": {"type": "string", "description": "Nome aproximado para busca fuzzy se nao tiver ID."},
            "new_name": {"type": "string"},
            "new_dose": {"type": "string"},
            "new_instructions": {"type": "string"},
            "new_schedule_rrule": {"type": "string", "description": "Substitui schedule completo. Use com cuidado."},
            "new_end_date": {"type": "string", "description": "Nova data de fim (YYYY-MM-DD) ou string vazia pra remover."},
            "new_critical": {"type": "boolean"}
        }
    }`),
},
{
    Name:        "cancelar_medicamento",
    Description: "Cancela (soft-delete: active=false) um medicamento. Lembretes futuros param. Historico de tomadas e preservado. Cria pending_confirmation pra confirmar.",
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "medication_id": {"type": "integer"},
            "name_query": {"type": "string"},
            "reason": {"type": "string", "description": "Motivo (ex: 'medico tirou', 'nao preciso mais'). Salvo em audit log."}
        }
    }`),
},
{
    Name:        "marcar_remedio_tomado",
    Description: "Registra que o usuario tomou um remedio. Se medication_id for omitido, pega o lembrete pendente atual (pending_confirmations.kind='medication' mais recente). Use SEMPRE que o usuario disser 'tomei', 'ja bebi', 'pronto, foi', em resposta a um lembrete.",
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "medication_id": {"type": "integer", "description": "Opcional. Omitir = pegar pending atual."}
        }
    }`),
},
{
    Name:        "pular_dose",
    Description: "Registra que o usuario decidiu pular a dose atual. Salva razao e marca intake_log status='skipped'. NAO cancela o medicamento (proximas doses continuam).",
    InputSchema: json.RawMessage(`{
        "type": "object",
        "properties": {
            "medication_id": {"type": "integer"},
            "reason": {"type": "string", "description": "Razao do skip (ex: 'estou enjoado', 'esqueci de comprar'). Sempre pedir antes de chamar."}
        },
        "required": ["reason"]
    }`),
},
```

### 5.3 Esqueleto dos handlers (em `bot/tools_medication.go`)

Todos seguem o padrão dos handlers existentes (parse, valida, persiste, audita, retorna texto natural).

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "strings"
    "time"
)

type cadastrarMedicamentoParams struct {
    TargetUser    string `json:"target_user"`
    Name          string `json:"name"`
    Dose          string `json:"dose"`
    Instructions  string `json:"instructions"`
    ScheduleRRULE string `json:"schedule_rrule"`
    StartDate     string `json:"start_date"`
    EndDate       string `json:"end_date"`
    Critical      bool   `json:"critical"`
}

func handleCadastrarMedicamento(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
    var p cadastrarMedicamentoParams
    if err := json.Unmarshal(params, &p); err != nil {
        return "", fmt.Errorf("parse params: %w", err)
    }
    if strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.ScheduleRRULE) == "" {
        return "Preciso do nome do medicamento e dos horarios. Pergunte ao usuario.", nil
    }

    target := user
    if p.TargetUser != "" && !strings.EqualFold(p.TargetUser, user.Name) {
        t, err := agent.db.GetUserByName(p.TargetUser)
        if err != nil {
            return fmt.Sprintf("Nao encontrei o usuario '%s'.", p.TargetUser), nil
        }
        // Valida vinculo familiar (Fase 1: family_links). Sem vinculo, nega.
        canManage, err := agent.db.CanManageMedicationFor(user.ID, t.ID)
        if err != nil {
            return "", fmt.Errorf("check family link: %w", err)
        }
        if !canManage {
            return fmt.Sprintf("Voce nao tem permissao pra cadastrar remedio pra %s.", t.Name), nil
        }
        target = t
    }

    if _, err := ParseRRULE(p.ScheduleRRULE); err != nil {
        return fmt.Sprintf("Nao consegui entender o horario '%s'. Pode descrever em palavras (ex: 'todos os dias as 8h e 20h')?", p.ScheduleRRULE), nil
    }

    startDate := p.StartDate
    if startDate == "" {
        startDate = time.Now().In(BRT()).Format("2006-01-02")
    }

    intent := IntentData{
        Medication: &MedicationIntent{
            Name:          p.Name,
            Dose:          p.Dose,
            Instructions:  p.Instructions,
            ScheduleRRULE: p.ScheduleRRULE,
            StartDate:     startDate,
            EndDate:       p.EndDate,
            Critical:      p.Critical,
        },
    }
    if target.ID != user.ID {
        intent.TargetUser = target.Name
    }
    eventJSON, _ := json.Marshal(intent)

    pc := &PendingConfirmation{
        UserID:    user.ID,
        EventData: string(eventJSON),
        Kind:      "medication",
    }
    if err := agent.db.CreatePendingConfirmation(pc); err != nil {
        return "", err
    }

    desc := DescribeRRULE(p.ScheduleRRULE) // ex: "todos os dias as 8h e 20h"
    msg := fmt.Sprintf("Vou cadastrar %s %s, %s. Confirma?", p.Name, p.Dose, desc)
    if target.ID != user.ID {
        msg = fmt.Sprintf("Vou cadastrar %s %s pra %s, %s. Confirma?", p.Name, p.Dose, target.Name, desc)
    }
    return msg, nil
}

type listarMedicamentosParams struct {
    TargetUser string `json:"target_user"`
}

func handleListarMedicamentos(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
    var p listarMedicamentosParams
    if err := json.Unmarshal(params, &p); err != nil {
        return "", fmt.Errorf("parse params: %w", err)
    }
    target := user
    if p.TargetUser != "" && !strings.EqualFold(p.TargetUser, user.Name) {
        t, err := agent.db.GetUserByName(p.TargetUser)
        if err != nil {
            return fmt.Sprintf("Nao encontrei '%s'.", p.TargetUser), nil
        }
        canManage, _ := agent.db.CanManageMedicationFor(user.ID, t.ID)
        if !canManage {
            return fmt.Sprintf("Voce nao tem acesso aos medicamentos de %s.", t.Name), nil
        }
        target = t
    }
    meds, err := agent.db.ListActiveMedications(target.ID)
    if err != nil {
        return "", err
    }
    if len(meds) == 0 {
        if target.ID == user.ID {
            return "Voce nao tem medicamentos cadastrados.", nil
        }
        return fmt.Sprintf("%s nao tem medicamentos cadastrados.", target.Name), nil
    }
    var sb strings.Builder
    sb.WriteString("Medicamentos:\n")
    for _, m := range meds {
        scheds, _ := agent.db.ListSchedulesForMedication(m.ID)
        for _, s := range scheds {
            sb.WriteString(fmt.Sprintf("- %s %s — %s\n", m.Name, m.Dose, DescribeRRULE(s.RRULE)))
        }
    }
    return sb.String(), nil
}

// editarMedicamento, cancelarMedicamento, marcarRemedioTomado, pularDose
// seguem o mesmo padrao: parse → valida → cria pending_confirmation (quando
// muda estado importante) ou aplica direto (marcar/pular sao baixo risco) →
// audita → retorna texto natural.
```

**Nota sobre `marcar_remedio_tomado` sem confirmação:** dizer "tomei" é ato declarativo do usuário; não vale a pena criar pending_confirmation pra confirmar ("você tomou mesmo? confirma?" seria condescendente com idoso). Aplica direto: atualiza `medication_intake_log` da ocorrência mais recente em estado `pending`/`missed`, fecha o pending_confirmation correspondente, registra audit.

---

## 6. RRULE — decisão de lib + exemplos

### Decisão

**Usar `github.com/teambition/rrule-go`.** Razões:

1. Lib madura (4 anos, 600+ stars), single dependency, zero CGO.
2. Suporta `FREQ`, `BYHOUR`, `BYMINUTE`, `BYDAY`, `INTERVAL`, `COUNT`, `UNTIL` — cobre 100% dos casos de remédio.
3. API funcional: `rrule.Set` aceita `Between(start, end, inclusive)` que devolve as ocorrências na janela. É exatamente o que o scheduler precisa.
4. Compatível com a sintaxe iCal já usada em `criar_evento.recurrence` (consistência interna).

Adicionar a `bot/go.mod`:

```
github.com/teambition/rrule-go v1.8.2
```

### Wrapper local: `bot/rrule.go`

Mesmo com a lib, encapsulamos uso atrás de funções nossas para:

- Centralizar tratamento de timezone (RRULE não tem fuso intrínseco — quem cria a `RRule` decide o `Dtstart.Location`).
- Adicionar `DescribeRRULE(s)` em PT-BR para mensagens ("todos os dias às 8h e 20h").
- Restringir o subset suportado e retornar erros claros se vier algo fora dele.

```go
package main

import (
    "fmt"
    "sort"
    "strings"
    "time"

    "github.com/teambition/rrule-go"
)

// ParseRRULE valida e devolve uma rrule.RRule pronta. Não aceita FREQ menos
// granular que daily — não faz sentido pra remédio (e horários por hora seriam
// caso de medicação intra-hospitalar, fora do MVP).
func ParseRRULE(s string) (*rrule.RRule, error) {
    if s == "" {
        return nil, fmt.Errorf("rrule vazia")
    }
    // teambition/rrule aceita prefixo "RRULE:" ou direto "FREQ=...". Normaliza.
    raw := strings.TrimPrefix(s, "RRULE:")
    opts, err := rrule.StrToROption(raw)
    if err != nil {
        return nil, fmt.Errorf("rrule parse: %w", err)
    }
    if opts.Freq != rrule.DAILY && opts.Freq != rrule.WEEKLY && opts.Freq != rrule.MONTHLY {
        return nil, fmt.Errorf("frequencia nao suportada: use DAILY, WEEKLY ou MONTHLY")
    }
    if len(opts.Byhour) == 0 {
        return nil, fmt.Errorf("rrule sem BYHOUR — preciso saber o horario do remedio")
    }
    return rrule.NewRRule(*opts)
}

// ExpandOccurrences devolve todas as ocorrências de schedule no intervalo
// [start, end), interpretado no fuso loc. Lê start_date do schedule pra
// fixar o Dtstart; respeita end_date se presente.
func ExpandOccurrences(sched *MedicationSchedule, start, end time.Time, loc *time.Location) ([]time.Time, error) {
    rr, err := ParseRRULE(sched.RRULE)
    if err != nil {
        return nil, err
    }
    // Dtstart no fuso correto. Sem isto, BYHOUR=8 vira 8h UTC e a hora
    // local sai 5h (em BRT) — bug clássico de RRULE.
    dtStart := time.Date(
        sched.StartDate.Year(), sched.StartDate.Month(), sched.StartDate.Day(),
        0, 0, 0, 0, loc,
    )
    rr.DTStart(dtStart)
    if sched.EndDate != nil {
        rr.GetROption().Until = sched.EndDate.Add(24 * time.Hour) // inclusiva
    }

    occs := rr.Between(start, end, true)
    sort.Slice(occs, func(i, j int) bool { return occs[i].Before(occs[j]) })
    return occs, nil
}

// DescribeRRULE retorna texto natural em PT-BR. Best-effort — fallback para
// a string crua se o caso não for coberto.
func DescribeRRULE(s string) string {
    raw := strings.TrimPrefix(s, "RRULE:")
    opts, err := rrule.StrToROption(raw)
    if err != nil {
        return s
    }
    var freq string
    switch opts.Freq {
    case rrule.DAILY:
        freq = "todos os dias"
    case rrule.WEEKLY:
        days := []string{}
        for _, d := range opts.Byweekday {
            days = append(days, weekdayPT(d))
        }
        freq = "toda " + strings.Join(days, " e ")
    case rrule.MONTHLY:
        freq = "todo mes"
    default:
        return s
    }
    var hours []string
    for _, h := range opts.Byhour {
        hours = append(hours, fmt.Sprintf("%dh", h))
    }
    if len(hours) == 0 {
        return freq
    }
    return freq + " as " + strings.Join(hours, " e ")
}

func weekdayPT(d rrule.Weekday) string {
    switch d.Day() {
    case 0:
        return "segunda"
    case 1:
        return "terca"
    case 2:
        return "quarta"
    case 3:
        return "quinta"
    case 4:
        return "sexta"
    case 5:
        return "sabado"
    case 6:
        return "domingo"
    }
    return ""
}
```

### Edge cases

**DST** (horário de verão): Brasil aboliu DST em 2019, mas usuários viajando para país com DST (ex: Europa em outubro) podem cair na transição. `rrule-go` lida corretamente quando `DTStart.Location` é populado com a tz IANA correta. Como passamos `loc` derivado de `agent.db.GetEventTimezone(user.ID, occurrenceDate)`, o RRULE expande no fuso vigente naquele dia — inclusive ajustando para DST quando a tz for `Europe/Paris`/`America/New_York`.

**Travel period sobreposto**: usuário cadastra remédio em BRT, viaja para Paris, schedule continua válido. Decisão arquitetural: o RRULE permanece no fuso de cadastro (BRT), e `checkMedicationReminders` compara `now.UTC()` com a expansão da ocorrência. Se o user toma "8h da manhã" e está em Paris (3h de diferença em horário de verão), o lembrete chega às 4h da manhã em Paris — comportamento *correto*: o RRULE foi cadastrado em BRT, é o que o usuário escolheu.

Se quisermos "RRULE no fuso vigente" (idoso decide tomar as 8h locais onde quer que esteja), a Fase 6+ pode adicionar um campo `schedule.timezone_mode` (`'fixed'`/`'local'`). Para o MVP da Fase 3, mantemos `'fixed'` implícito — mais previsível para idoso.

**Janela de 60s do scheduler**: o scheduler roda cron `* * * * *` (a cada minuto). Cada tick expande RRULE para `[now-30s, now+30s]`. Risco: se a ocorrência cai exatamente em `now-31s` por clock skew, perdemos. Mitigação: janela é `[now-60s, now+1s]` (assimétrica, prefere atrasar 1min a perder), e `UNIQUE(medication_id, scheduled_at)` impede duplicar.

**Bare RRULE without DTSTART**: se o usuário cadastrar `FREQ=DAILY;BYHOUR=8` sem o sistema saber o `start_date`, a primeira expansão usa "agora" como `DTStart` e perde a primeira ocorrência caso já tenha passado das 8h hoje. Por isso `start_date` é obrigatório no schema (default = hoje, atribuído na tool).

**INTERVAL > 1**: ex: `FREQ=DAILY;INTERVAL=3;BYHOUR=8` ("a cada 3 dias às 8h"). Suportado pela lib sem mudanças. Útil para remédios semanais (aliás, melhor escrever como `FREQ=WEEKLY;BYDAY=MO`).

---

## 7. Fluxo foto-da-receita

### 7.1 Capacidade existente

`bot/handler.go:187-203` já baixa imagem do WhatsApp e a coloca em `[]ImageAttachment` (struct `{Data []byte, Mime string}`). O orchestrator passa as imagens ao `agent.Process()`. Ou seja, **a stack de imagem no Claude já existe** (foi adicionada antes da Fase 3, provavelmente para "manda foto desse cartão de visita pra eu marcar contato"). Confirmar em `bot/agent.go` se as imagens já entram no `messages.image` do SDK Claude — se não, adicionar é uma chamada ao bloco `image` da API Anthropic v1/messages:

```go
// Em agent.Run, ao montar Messages para o Claude:
for _, img := range req.Images {
    contents = append(contents, anthropic.MessageContent{
        Type: "image",
        Source: &anthropic.ImageSource{
            Type:      "base64",
            MediaType: img.Mime,             // "image/jpeg", "image/png"
            Data:      base64.StdEncoding.EncodeToString(img.Data),
        },
    })
}
```

Validar no commit em `bot/agent.go` que essa branch está presente. Se não, é pré-requisito da Fase 3.

### 7.2 Fluxo passo a passo

1. **Usuário envia foto da receita** (com ou sem caption).
2. **Handler baixa imagem** (linhas 187-203 já fazem). Texto cru ou caption vai junto.
3. **Orchestrator chama `agent.Process(ctx, user, text, images)`**.
4. **Sistema detecta intenção de "extrair receita"**: feito pelo Claude, com base no system prompt enriquecido. Não é um path especial no código Go — Claude lê a imagem e decide chamar a tool `extrair_receita_imagem` (nova).

Decisão importante: extrair-receita **é uma tool**, não um caminho hardcoded. Razões:

- Usuário pode mandar foto que não é receita (foto do gato). Claude decide se chama a tool ou responde livremente.
- Usuário pode mandar foto com caption "guarda essa receita pra quando eu quiser cadastrar". Claude extrai mas não cadastra ainda.
- Tool é testável isoladamente.

5. **Tool `extrair_receita_imagem`** (novo handler, mas note: ela não recebe a imagem como param — a imagem já está no contexto do Claude. O handler só dispara o processamento e retorna estruturado.)

Schema:

```json
{
    "name": "extrair_receita_imagem",
    "description": "Use SOMENTE quando o usuario enviou uma imagem que parece ser receita medica (lista de remedios manuscrita ou impressa). Extrai cada item da receita. APOS extrair, voce DEVE apresentar item-a-item ao usuario (sem menu numerado, em linguagem natural) e perguntar horarios de cada um.",
    "input_schema": {
        "type": "object",
        "properties": {
            "items": {
                "type": "array",
                "items": {
                    "type": "object",
                    "properties": {
                        "name": {"type": "string"},
                        "dose": {"type": "string"},
                        "frequency_text": {"type": "string", "description": "Frequencia em texto livre, exatamente como escrito na receita (ex: '1x ao dia', '8/8h', 'em jejum')"},
                        "duration_text": {"type": "string", "description": "Duracao do tratamento se mencionada (ex: '7 dias', 'continuo', 'ate acabar')"}
                    },
                    "required": ["name"]
                }
            }
        },
        "required": ["items"]
    }
}
```

6. **Handler salva no audit log a extração crua** (mais hash da imagem, não o conteúdo bruto — questão de privacidade/PII em dados médicos). Retorna o array para o agente.

```go
func handleExtrairReceitaImagem(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
    var p struct {
        Items []struct {
            Name          string `json:"name"`
            Dose          string `json:"dose"`
            FrequencyText string `json:"frequency_text"`
            DurationText  string `json:"duration_text"`
        } `json:"items"`
    }
    if err := json.Unmarshal(params, &p); err != nil {
        return "", err
    }
    if len(p.Items) == 0 {
        return "Nao consegui identificar medicamentos na imagem.", nil
    }
    // Audit: registra extração crua para revisão posterior. Sem hash da imagem
    // — a Fase 3 fica em audit-log textual. Hash entra na Fase 4 (privacidade).
    raw, _ := json.Marshal(p.Items)
    agent.audit.Log(user.ID, "prescription_image_processed", "", string(raw))

    // O agente AGORA conduz a confirmacao item-a-item via texto natural.
    // Retornamos um sumário pra ele, e ele vai construir as perguntas.
    var sb strings.Builder
    sb.WriteString(fmt.Sprintf("Extraidos %d medicamentos. Apresentar item-a-item ao usuario em linguagem natural, perguntando o horario de cada um, e chamar cadastrar_medicamento ao final de cada confirmacao:\n", len(p.Items)))
    for i, it := range p.Items {
        sb.WriteString(fmt.Sprintf("%d. %s %s — frequencia: %s; duracao: %s\n", i+1, it.Name, it.Dose, it.FrequencyText, it.DurationText))
    }
    return sb.String(), nil
}
```

7. **Agente conduz confirmação interativa**: o resultado da tool volta como `tool_result` no próximo turno do Claude, e o system prompt instrui:

```
Quando extrair_receita_imagem retornar items, voce deve:
1. Apresentar UM item por vez em linguagem natural ("Vi que voce precisa tomar Losartana 50mg uma vez por dia. Qual horario voce prefere tomar?")
2. Aguardar resposta do usuario com horario.
3. Converter o horario em RRULE (ex: "todo dia 8h" → "FREQ=DAILY;BYHOUR=8;BYMINUTE=0").
4. Chamar cadastrar_medicamento com os campos preenchidos.
5. Apos confirmacao do cadastro, passar pro proximo item.
6. Nunca persistir sem confirmacao do usuario.
7. NUNCA usar menu numerado — sempre conversa fluida.
```

8. **Cada item vira `cadastrar_medicamento`** (sub-fluxo já definido). Cada um cria sua própria `pending_confirmation`. O user confirma cada um separadamente (sequencial — fluxo natural de conversa).

9. **Audit final**: ao fim da sessão, o audit log tem `prescription_image_processed` (1x) + `medication_created` (N vezes, uma por item confirmado).

### 7.3 Privacidade / PII

A imagem em si **não é persistida**. Apenas o JSON de extração textual entra em `action_log`. Caminho mais conservador para dados médicos. Se a auditoria precisar revisar a foto original, o caminho é regenerar via WhatsApp (a média fica armazenada lá por 14 dias).

---

## 8. Scheduler — código novo

Tudo abaixo entra em `bot/scheduler.go`. Wireup em `Start()`:

```go
func (s *Scheduler) Start() {
    s.cron.AddFunc("* * * * *", s.checkReminders)
    s.cron.AddFunc("* * * * *", s.checkAutoConfirm)
    s.cron.AddFunc("* * * * *", s.checkDailySummaries)
    s.cron.AddFunc("* * * * *", s.checkWeeklySummaries)
    s.cron.AddFunc("* * * * *", s.checkExpiredPermissionRequests)

    // Fase 3:
    s.cron.AddFunc("* * * * *", s.checkMedicationReminders)
    s.cron.AddFunc("* * * * *", s.checkMedicationEscalation)

    s.cron.Start()
    log.Println("Scheduler started")
}
```

Como agora o scheduler usa o `Notifier` (Seção 10), o struct ganha um campo:

```go
type Scheduler struct {
    cron     *cron.Cron
    db       *DB
    cal      *CalendarClient
    cfg      *Config
    sendMsg  func(phone, text string) error  // mantido por compat — ainda usado em jobs antigos
    notifier Notifier                        // NOVO — usado pelos jobs de remédio/escalação
    eng      *EscalationEngine               // NOVO — Seção 9
}
```

E `NewScheduler` ganha os params correspondentes (Seção 10).

### 8.1 `checkMedicationReminders`

```go
// checkMedicationReminders varre todos os usuarios ativos e, para cada
// medicamento ativo, expande o RRULE no fuso vigente daquele dia para
// detectar ocorrências dentro da janela [now-60s, now+1s]. Para cada
// ocorrência:
//   1. Tenta INSERT em medication_intake_log (UNIQUE garante idempotência).
//   2. Cria pending_confirmation kind=medication com escalation_policy.
//   3. Envia mensagem natural via Notifier.
//
// Se o INSERT falhar por UNIQUE, o disparo já aconteceu (em outra invocação
// concorrente ou em restart pós-tick). Skip silencioso.
func (s *Scheduler) checkMedicationReminders() {
    users, err := s.db.ListActiveUsers()
    if err != nil {
        log.Printf("Scheduler: error listing users for medication reminders: %v", err)
        return
    }

    now := time.Now().UTC()
    windowStart := now.Add(-60 * time.Second)
    windowEnd := now.Add(1 * time.Second)

    for i := range users {
        s.checkUserMedicationReminders(&users[i], windowStart, windowEnd, now)
    }
}

func (s *Scheduler) checkUserMedicationReminders(user *User, windowStart, windowEnd, now time.Time) {
    meds, err := s.db.ListActiveMedications(user.ID)
    if err != nil {
        log.Printf("[%s] medication reminders: list failed: %v", user.Name, err)
        return
    }

    for _, m := range meds {
        scheds, err := s.db.ListSchedulesForMedication(m.ID)
        if err != nil {
            continue
        }
        for _, sched := range scheds {
            // Localiza fuso vigente para a janela atual (respeita travel periods).
            loc := s.db.GetEventTimezone(user.ID, now)
            occs, err := ExpandOccurrences(&sched, windowStart, windowEnd, loc)
            if err != nil {
                log.Printf("[%s] med %d: rrule expand failed: %v", user.Name, m.ID, err)
                continue
            }
            for _, occ := range occs {
                s.fireMedicationReminder(user, &m, &sched, occ)
            }
        }
    }
}

func (s *Scheduler) fireMedicationReminder(user *User, m *Medication, sched *MedicationSchedule, scheduledAt time.Time) {
    // 1. Lock idempotente: tenta INSERT no intake_log com status=pending.
    //    Se já existe (UNIQUE), saímos.
    err := s.db.CreateIntakeLogIfAbsent(m.ID, scheduledAt, IntakePending)
    if err != nil {
        if isUniqueViolation(err) {
            // Já disparado em tick anterior. Idempotente. Ok.
            return
        }
        log.Printf("[%s] med %d: intake log insert failed: %v", user.Name, m.ID, err)
        return
    }

    // 2. Cria pending_confirmation pra este lembrete.
    intent := IntentData{
        Medication: &MedicationIntent{
            MedicationID: m.ID,
            ScheduledAt:  scheduledAt,
            Reminder:     true,
        },
    }
    eventJSON, _ := json.Marshal(intent)
    policy := "medication_default"
    if sched.Critical {
        policy = "medication_critical"
    }
    pc := &PendingConfirmation{
        UserID:           user.ID,
        EventData:        string(eventJSON),
        Kind:             "medication",
        EscalationPolicy: &policy,
    }
    if err := s.db.CreatePendingConfirmation(pc); err != nil {
        log.Printf("[%s] med %d: create pending failed: %v", user.Name, m.ID, err)
        return
    }

    // 3. Envia mensagem natural ao usuário.
    msg := fmt.Sprintf("Hora do %s %s, %s. Pode confirmar quando tomar?",
        m.Name, m.Dose, firstName(user.Name))
    if m.Instructions != "" {
        msg += " Lembra: " + m.Instructions + "."
    }

    if err := s.notifier.Send(context.Background(), user, msg); err != nil {
        log.Printf("[%s] med %d: notifier send failed: %v", user.Name, m.ID, err)
        // Não desfaz o pending — próximo tick do escalation engine vai tentar
        // de novo (a primeira tentativa entra no contador).
        return
    }

    // 4. Audit.
    s.db.AuditLog(user.ID, "medication_reminder_sent",
        "", fmt.Sprintf("med=%d scheduled=%s", m.ID, scheduledAt.Format(time.RFC3339)))
}
```

### 8.2 `checkMedicationEscalation`

```go
// checkMedicationEscalation aplica EscalationPolicy a pending_confirmations
// kind=medication ainda em status=pending. Para cada um, decide:
//   - Se attempt < MaxAttempts e (now - last_attempt_at) >= Interval:
//     incrementa attempt, dispara nova mensagem (insistência crescente),
//     atualiza last_attempt_at e cria row em escalations.
//   - Se attempt == MaxAttempts e EscalateTo == family:
//     muda intake_log para 'escalated', resolve pending como 'escalated',
//     dispara mensagem para cada guardian em family_links com
//     notify_on_medication_miss=true. Cria row em escalations por guardian.
func (s *Scheduler) checkMedicationEscalation() {
    pendings, err := s.db.GetActiveMedicationPendings()
    if err != nil {
        log.Printf("Scheduler: get medication pendings: %v", err)
        return
    }
    now := time.Now().UTC()
    for _, pc := range pendings {
        s.eng.HandlePending(now, &pc)
    }
}
```

A lógica de tentativas/interval mora no `EscalationEngine` (Seção 9). O scheduler só descobre as candidatas e delega.

### 8.3 Idempotência sobre restart

Cenários cobertos:

- **Restart entre o `INSERT` no intake_log e o envio da mensagem**: na volta, o intake_log já tem row `pending`, o pending_confirmation já existe (transação garante), mas a mensagem nunca saiu. Solução: o próximo tick do `checkMedicationEscalation` vai ver pending sem `last_attempt_at` (NULL → muito antigo) e vai reenviar a primeira tentativa, criando row em `escalations` com `attempt_number=1`. Comportamento correto: idoso recebe 1 lembrete, atrasado por até 1 minuto.

- **Restart durante escalação**: `attempt_number` está persistido em `pending_confirmations`. Próximo tick vê `attempt_number=2`, `last_attempt_at=T-N`, e decide se hora de avançar para 3.

- **Dois processos rodando simultaneamente** (não deveria acontecer; só uma instância no MVP): `UNIQUE` em `medication_intake_log` e `UNIQUE` em `escalations(pc_id, attempt, recipient)` evitam duplicidade. O segundo processo pega `UNIQUE constraint failed`, log warning, continua.

### 8.4 Helpers DB usados

```go
func (db *DB) CreateIntakeLogIfAbsent(medID int64, scheduledAt time.Time, status IntakeStatus) error {
    _, err := db.conn.Exec(
        `INSERT INTO medication_intake_log (medication_id, scheduled_at, status)
         VALUES (?, ?, ?)`,
        medID, scheduledAt.UTC(), status,
    )
    return err
}

func (db *DB) GetActiveMedicationPendings() ([]PendingConfirmation, error) {
    rows, err := db.conn.Query(
        `SELECT pc.id, pc.user_id, pc.event_data, pc.status, pc.created_at,
                pc.kind, pc.escalation_policy, pc.last_attempt_at, pc.attempt_number,
                u.phone_number, u.name
         FROM pending_confirmations pc
         JOIN users u ON u.id = pc.user_id
         WHERE pc.status = 'pending' AND pc.kind = 'medication'`)
    // ... scan + map ...
}

func isUniqueViolation(err error) bool {
    return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// firstName extrai primeiro nome para mensagens informais ao idoso.
// "Antonia da Silva" -> "Antonia"
func firstName(full string) string {
    parts := strings.Fields(full)
    if len(parts) > 0 {
        return parts[0]
    }
    return full
}
```

---

## 9. EscalationPolicy — código completo

### Princípio de segurança farmacológica (regra dura)

Antes do código, fixar o princípio que governa **toda mensagem** que o bot envia sobre dose perdida:

> **O bot NUNCA recomenda tomar a dose atrasada nem "compensar" doses perdidas.** A decisão de tomar uma dose fora do horário cabe ao médico (ou em segunda instância à família que pode contatá-lo) — nunca ao bot. Algumas drogas têm janela curta de segurança (paracetamol+ibuprofeno, anticoagulantes, anti-hipertensivos como Losartana, hipoglicemiantes); dose dupla acidental pode causar dano sério.

Consequências práticas:

1. **Mensagens de escalação** (insistência) NÃO contêm "ainda dá tempo", "tome agora", "compense a dose", "não esqueça de tomar". Usam tom neutro/cuidadoso.
2. **Mensagem final** (ao idoso, attempt = MaxAttempts) explicita "vou anotar como não tomada" e encaminha pra médico/família.
3. **Mensagem ao guardian** explicita "anotei como dose não tomada" e diz textualmente que o bot **não orienta** sobre compensação por segurança.
4. **Quando o idoso responde "tomei agora, atrasado"** (cf. tool `marcar_remedio_tomado`): o bot registra `taken` (a decisão é dele), mas **não confirma com aprovação** ("ótimo, fez bem"). Resposta neutra — "anotado, %s" — sem reforço positivo. A regra equivalente vive no system prompt da persona companion (Fase 4) para o caso conversacional.
5. **Esta regra também aparece no system prompt principal e no companion** — Claude precisa internalizá-la para os casos em que sai do script da escalação automática.

A camada que materializa a regra é o `EscalationMsg` de cada `EscalationPolicy` (abaixo). Qualquer política nova herda a obrigação.

---

Arquivo novo: `bot/escalation.go`. Esta é a peça de "política como dado" defendida em D4.

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "strings"
    "time"
)

// escalationPolicies é o registry global de políticas. Adicionar política nova
// = adicionar entrada aqui. Não há código novo no engine.
var escalationPolicies = map[string]EscalationPolicy{
    "medication_default": {
        Name:        "medication_default",
        MaxAttempts: 3,
        Interval:    5 * time.Minute,
        EscalateTo:  EscalateToFamily,
        EscalationMsg: func(ec EscalationContext) string {
            if ec.IsFinalEscalation {
                return finalFamilyMsg(ec)
            }
            return insistMsg(ec)
        },
    },
    "medication_critical": {
        Name:        "medication_critical",
        MaxAttempts: 5,
        Interval:    3 * time.Minute,
        EscalateTo:  EscalateToFamily,
        EscalationMsg: func(ec EscalationContext) string {
            if ec.IsFinalEscalation {
                return finalFamilyMsg(ec)
            }
            return insistMsg(ec)
        },
    },
}

// insistMsg gera tom progressivamente mais cuidadoso conforme attempt sobe.
//
// IMPORTANTE — princípio de segurança farmacológica:
// O bot NUNCA recomenda tomar a dose atrasada nem "compensar" doses perdidas.
// Algumas drogas têm janela curta de segurança (dose dupla acidental de
// paracetamol+ibuprofeno, anticoagulantes, anti-hipertensivos) e a decisão
// de "tomar ou não tomar atrasado" cabe ao médico, não ao bot.
//
// Por isso as mensagens evoluem do tom "lembrete neutro" para
// "preocupação acolhedora" e finalmente "vou anotar e fica registrado",
// SEM jamais dizer "ainda dá tempo", "tome agora", "compense a dose"
// ou similar. A última tentativa explicitamente encaminha pra médico/família.
func insistMsg(ec EscalationContext) string {
    name := firstName(ec.User.Name)
    medName := "o remedio"
    if ec.Medication != nil {
        medName = ec.Medication.Name
    }
    switch ec.AttemptNumber {
    case 1:
        return fmt.Sprintf("Hora do %s, %s. Me avisa quando tomar, sem pressa.", medName, name)
    case 2:
        return fmt.Sprintf("%s, tudo bem por ai? Me avisa quando puder.", name)
    case 3:
        return fmt.Sprintf("%s, ainda nao tive noticia sobre o %s. Aconteceu alguma coisa? Estou aqui.", name, medName)
    case 4:
        return fmt.Sprintf("%s, fiquei pensando em voce. Me avisa quando puder, mesmo que so um \"oi\".", name)
    case 5:
        return fmt.Sprintf(
            "%s, vou anotar essa dose como nao tomada e avisar a familia. "+
                "Se a hora passou, nao tome agora sem antes falar com seu medico ou com a familia, ta?",
            name,
        )
    }
    return fmt.Sprintf("%s, esta tudo bem por ai?", name)
}

// finalFamilyMsg é a mensagem ao guardian quando escala.
// Tom sóbrio, factual; deixa a decisão clínica com a família/médico,
// não sugere ação específica.
func finalFamilyMsg(ec EscalationContext) string {
    elderName := firstName(ec.User.Name)
    medName := "o remedio"
    if ec.Medication != nil {
        medName = ec.Medication.Name
    }
    timeStr := ec.ScheduledAt.In(BRT()).Format("15h")
    return fmt.Sprintf(
        "Oi, %s nao confirmou que tomou %s das %s e nao respondeu apos varias tentativas. "+
            "Anotei como dose nao tomada. Vale falar com ela e, se necessario, conferir com o medico "+
            "se essa dose deve ou nao ser compensada — eu nao oriento isso por seguranca.",
        elderName, medName, timeStr,
    )
}

// EscalationEngine é stateless — toda decisão deriva do estado da DB.
type EscalationEngine struct {
    db       *DB
    notifier Notifier
}

func NewEscalationEngine(db *DB, n Notifier) *EscalationEngine {
    return &EscalationEngine{db: db, notifier: n}
}

// HandlePending decide se essa pending deve receber nova tentativa, escalar
// para família, ou ser deixada em paz (intervalo ainda não fechou).
func (e *EscalationEngine) HandlePending(now time.Time, pc *PendingConfirmation) {
    if pc.EscalationPolicy == nil || *pc.EscalationPolicy == "" {
        return // sem política = sem escalação
    }
    pol, ok := escalationPolicies[*pc.EscalationPolicy]
    if !ok {
        log.Printf("escalation: unknown policy %q on pending %d", *pc.EscalationPolicy, pc.ID)
        return
    }
    // Intervalo ainda não atingido?
    if pc.LastAttemptAt != nil && now.Sub(*pc.LastAttemptAt) < pol.Interval {
        return
    }

    user, err := e.db.GetUserByID(pc.UserID)
    if err != nil {
        log.Printf("escalation pc %d: user lookup: %v", pc.ID, err)
        return
    }

    var med *Medication
    if pc.Kind == "medication" {
        var data IntentData
        if err := json.Unmarshal([]byte(pc.EventData), &data); err == nil && data.Medication != nil && data.Medication.MedicationID > 0 {
            m, _ := e.db.GetMedicationByID(data.Medication.MedicationID)
            med = m
        }
    }

    // Decide se essa é a última tentativa (vai pra família) ou só insistência.
    nextAttempt := pc.AttemptNumber + 1
    isFinal := nextAttempt > pol.MaxAttempts && pol.EscalateTo == EscalateToFamily

    if isFinal {
        e.escalateToFamily(now, pc, user, med, pol)
        return
    }
    if nextAttempt > pol.MaxAttempts {
        // EscalateToNone ou self_only e atingiu limite → marca como missed.
        e.markMissedAndResolve(pc)
        return
    }

    // Insistência ao próprio user.
    ec := EscalationContext{
        User:          user,
        Medication:    med,
        ScheduledAt:   medScheduledAt(pc),
        AttemptNumber: nextAttempt,
        Recipient:     user,
    }
    msg := pol.EscalationMsg(ec)

    if err := e.notifier.Send(context.Background(), user, msg); err != nil {
        log.Printf("escalation pc %d: notifier failed: %v", pc.ID, err)
        return
    }
    if err := e.db.RecordEscalationAttempt(pc.ID, pol.Name, nextAttempt, user.ID, e.notifier.Channel(), now); err != nil {
        log.Printf("escalation pc %d: record attempt: %v", pc.ID, err)
        return
    }
    if err := e.db.UpdatePendingAttempt(pc.ID, nextAttempt, now); err != nil {
        log.Printf("escalation pc %d: update pending: %v", pc.ID, err)
    }
}

func (e *EscalationEngine) escalateToFamily(now time.Time, pc *PendingConfirmation, user *User, med *Medication, pol EscalationPolicy) {
    guardians, err := e.db.ListGuardiansForUser(user.ID, "notify_on_medication_miss")
    if err != nil {
        log.Printf("escalation pc %d: list guardians: %v", pc.ID, err)
    }

    // Se não há guardian, marca como missed e desiste.
    if len(guardians) == 0 {
        e.markMissedAndResolve(pc)
        return
    }

    for _, g := range guardians {
        ec := EscalationContext{
            User:              user,
            Medication:        med,
            ScheduledAt:       medScheduledAt(pc),
            AttemptNumber:     pc.AttemptNumber + 1,
            Recipient:         &g,
            IsFinalEscalation: true,
        }
        msg := pol.EscalationMsg(ec)
        if err := e.notifier.Send(context.Background(), &g, msg); err != nil {
            log.Printf("escalation pc %d: notify guardian %d: %v", pc.ID, g.ID, err)
            continue
        }
        if err := e.db.RecordEscalationAttempt(pc.ID, pol.Name, pc.AttemptNumber+1, g.ID, e.notifier.Channel(), now); err != nil {
            log.Printf("escalation pc %d: record family attempt: %v", pc.ID, err)
        }
    }

    // Atualiza intake_log para 'escalated', resolve pending.
    if pc.Kind == "medication" {
        var data IntentData
        if err := json.Unmarshal([]byte(pc.EventData), &data); err == nil && data.Medication != nil {
            e.db.UpdateIntakeStatus(data.Medication.MedicationID, data.Medication.ScheduledAt, IntakeEscalated, "")
        }
    }
    e.db.ResolvePendingConfirmation(pc.ID, "escalated")
    e.db.AuditLog(user.ID, "medication_escalated", "",
        fmt.Sprintf("pc=%d guardians=%d", pc.ID, len(guardians)))
}

func (e *EscalationEngine) markMissedAndResolve(pc *PendingConfirmation) {
    if pc.Kind == "medication" {
        var data IntentData
        if err := json.Unmarshal([]byte(pc.EventData), &data); err == nil && data.Medication != nil {
            e.db.UpdateIntakeStatus(data.Medication.MedicationID, data.Medication.ScheduledAt, IntakeMissed, "")
        }
    }
    e.db.ResolvePendingConfirmation(pc.ID, "missed")
    e.db.AuditLog(pc.UserID, "medication_missed", "", fmt.Sprintf("pc=%d", pc.ID))
}

func medScheduledAt(pc *PendingConfirmation) time.Time {
    var data IntentData
    if err := json.Unmarshal([]byte(pc.EventData), &data); err != nil || data.Medication == nil {
        return time.Time{}
    }
    return data.Medication.ScheduledAt
}
```

Helpers DB extras (em `bot/db_medication.go`):

```go
func (db *DB) UpdatePendingAttempt(pcID int64, attempt int, now time.Time) error {
    _, err := db.conn.Exec(
        `UPDATE pending_confirmations
         SET attempt_number = ?, last_attempt_at = ?
         WHERE id = ?`,
        attempt, now.UTC(), pcID)
    return err
}

func (db *DB) RecordEscalationAttempt(pcID int64, policyName string, attempt int, recipientID int64, channel string, now time.Time) error {
    _, err := db.conn.Exec(
        `INSERT INTO escalations
         (pending_confirmation_id, policy_name, attempt_number, scheduled_for,
          status, notifier_used, recipient_user_id, sent_at)
         VALUES (?, ?, ?, ?, 'sent', ?, ?, ?)`,
        pcID, policyName, attempt, now.UTC(), channel, recipientID, now.UTC())
    return err
}

func (db *DB) UpdateIntakeStatus(medID int64, scheduledAt time.Time, status IntakeStatus, responseText string) error {
    _, err := db.conn.Exec(
        `UPDATE medication_intake_log
         SET status = ?, confirmed_at = ?, response_text = ?
         WHERE medication_id = ? AND scheduled_at = ?`,
        status, time.Now().UTC(), responseText, medID, scheduledAt.UTC())
    return err
}

// ListGuardiansForUser depende da Fase 1 (family_links). flag pode ser
// "notify_on_medication_miss" ou outras flags futuras.
func (db *DB) ListGuardiansForUser(userID int64, flag string) ([]User, error) {
    // Implementação depende do schema final de family_links.
    // Esqueleto: SELECT u.* FROM users u JOIN family_links fl ON fl.guardian_user_id = u.id
    //            WHERE fl.elder_user_id = ? AND fl.<flag> = 1 AND u.is_active = 1
    // ...
}
```

---

## 10. Notifier refactor — diff conceitual

### Antes

`bot/scheduler.go` chama `s.sendMsg(phone, text)` direto (linhas 93, 129, 178, 195, 240). `bot/main.go:167` injeta `agent.sendMsg = handler.SendTextToPhone`. O scheduler também recebe `sendMsg` e usa o mesmo callback.

### Depois

Um nível de indireção: introduz `Notifier` (interface) e `WhatsAppNotifier` (impl única). Scheduler mantém `sendMsg` para os jobs antigos (não vamos mexer em código testado e estável só para uniformizar — gold-plating), mas os jobs novos usam `notifier`. Quando Twilio entrar (Fase 6+), troca-se a impl sem alterar nada do scheduler ou da escalação.

### Diff em `bot/main.go`

```go
// ... linhas existentes ...
handler := NewHandler(waClient, db, orchestrator)
agent.sendMsg = handler.SendTextToPhone

// NOVO:
notifier := NewWhatsAppNotifier(handler.SendTextToPhone)
escEng := NewEscalationEngine(db, notifier)

// Scheduler agora recebe notifier + engine. NewScheduler é estendido.
scheduler := NewScheduler(db, cal, cfg, handler.SendTextToPhone, notifier, escEng)
scheduler.Start()
defer scheduler.Stop()
```

### Diff em `bot/scheduler.go`

```go
type Scheduler struct {
    cron     *cron.Cron
    db       *DB
    cal      *CalendarClient
    cfg      *Config
    sendMsg  func(phone, text string) error // mantido pra jobs legados
    notifier Notifier                       // NOVO
    eng      *EscalationEngine              // NOVO
}

func NewScheduler(db *DB, cal *CalendarClient, cfg *Config,
    sendMsg func(phone, text string) error,
    notifier Notifier, eng *EscalationEngine) *Scheduler {
    return &Scheduler{
        cron:     cron.New(cron.WithLocation(time.Local)),
        db:       db,
        cal:      cal,
        cfg:      cfg,
        sendMsg:  sendMsg,
        notifier: notifier,
        eng:      eng,
    }
}
```

Nada mais muda em `scheduler.go` para os jobs antigos. Os novos (`checkMedicationReminders`, `checkMedicationEscalation`) usam `s.notifier`. Migração total para `Notifier` fica como follow-up de baixa prioridade — não bloqueia Fase 3.

### Considerações de teste

`Notifier` permite mock trivial:

```go
type recordingNotifier struct {
    sent []sentMsg
}
type sentMsg struct{ Recipient *User; Body string }

func (r *recordingNotifier) Send(_ context.Context, u *User, msg string) error {
    r.sent = append(r.sent, sentMsg{u, msg})
    return nil
}
func (r *recordingNotifier) Channel() string { return "test" }
```

Os testes da Seção 11 dependem disso.

---

## 11. Casos de teste

Arquivo: `bot/medication_test.go`. Convenção do projeto: usa `t.Run` para sub-tests, fixtures são structs locais.

### 11.1 RRULE: dose dentro da janela do scheduler

**Nome:** `TestMedicationReminder_FiresWithinWindow`
**Fixture:** Medication com `RRULE=FREQ=DAILY;BYHOUR=14;BYMINUTE=0`, `start_date=2026-05-09`. Mock clock `now = 2026-05-09 14:00:00 BRT`.
**Asserção:**
- `medication_intake_log` tem 1 row com `scheduled_at=2026-05-09T14:00 BRT` e `status='pending'`.
- `pending_confirmations` tem 1 row `kind='medication'` com `escalation_policy='medication_default'`.
- `recordingNotifier.sent` tem 1 entrada com `Recipient.ID == user.ID` e mensagem contendo o nome do remédio.

### 11.2 RRULE: dose fora da janela

**Nome:** `TestMedicationReminder_OutsideWindow_NoFire`
**Fixture:** Mesmo schedule, `now = 2026-05-09 14:02:00 BRT` (fora da janela `[now-60s, now+1s]`).
**Asserção:** `intake_log` vazio, `pending_confirmations` vazio, notifier não recebeu nada.

### 11.3 Idempotência: dois ticks no mesmo segundo

**Nome:** `TestMedicationReminder_DoubleFireIsIdempotent`
**Fixture:** Mesmo schedule. Chama `checkMedicationReminders()` duas vezes seguidas com mesmo `now`.
**Asserção:**
- `intake_log` tem **exatamente 1 row** (UNIQUE pegou o segundo).
- `notifier.sent` tem 1 entrada (segundo tick falhou no UNIQUE antes do envio, então nem mandou).
- Log captura 1 mensagem `UNIQUE constraint failed` em nível DEBUG (best-effort).

### 11.4 Restart no meio de escalação

**Nome:** `TestEscalation_SurvivesRestart`
**Fixture:** Pending criado em `T0`, recebeu attempt 1 em `T0`, attempt 2 em `T0+5min`. Simula restart: nova instância do `EscalationEngine` (sem estado em memória). `now = T0 + 11min` (intervalo 5min, attempt 3 deveria disparar).
**Asserção:**
- Engine lê `pending_confirmations.attempt_number=2`, `last_attempt_at=T0+5min`.
- Como `now - last_attempt_at = 6min ≥ 5min`, dispara attempt 3.
- `pending.attempt_number` vai pra 3, `last_attempt_at = now`.
- `escalations` tem 3 rows totais (2 pré-restart + 1 nova).
- Notifier recebeu 1 mensagem (a do attempt 3 — não duplica as anteriores).

### 11.5 Race condition: dois ticks de escalação simultâneos

**Nome:** `TestEscalation_ConcurrentTicksNoDouble`
**Fixture:** Pending pronto pra attempt 2. Duas goroutines chamam `eng.HandlePending()` em paralelo com mesmo `now`.
**Asserção:** `escalations` tem **1 row** com `attempt_number=2` (UNIQUE em `(pc_id, attempt, recipient)` pegou). `pending.attempt_number=2`. Notifier recebeu 1 ou 2 mensagens (race no Send é aceitável; o que não pode é DB inconsistente).

> **Nota:** Para tornar isto determinístico, recomendo serializar `HandlePending` por `pc.ID` via `sync.Mutex` per-PC — adicionar como follow-up se o teste flakiar. Mas o UNIQUE é a barreira correta: garantia de DB.

### 11.6 RRULE em travel period

**Nome:** `TestMedicationReminder_TravelPeriod`
**Fixture:** User cadastrado em BRT, schedule `FREQ=DAILY;BYHOUR=8`, `start_date=2026-05-09`. Travel period 2026-06-01 a 2026-06-10 em `Europe/Paris`. `now = 2026-06-05 11:00 UTC` (= 13:00 Paris, = 8:00 BRT).
**Asserção:** Lembrete dispara no instante BRT 8h, **não** Paris 8h. Ou seja, RRULE permanece "fixed" no fuso de cadastro. Mensagem ao usuário inclui referência discreta ao horário ("hora do remédio das 8h"). **Decisão arquitetural confirmada: nada de re-localizar RRULE para fuso de viagem no MVP.**

### 11.7 Crítico vs default

**Nome:** `TestEscalationPolicy_CriticalUsesShorterInterval`
**Fixture:** Dois medicamentos para mesmo user — `m1` default, `m2` `critical=true`. Ambos disparam em `T0`. Avança clock pra `T0+3min`.
**Asserção:**
- `pending` de `m1`: ainda não recebeu attempt 2 (interval=5min).
- `pending` de `m2`: recebeu attempt 2 (interval=3min).
- Após mais 2min (`T0+5min`): `m1` recebe attempt 2, `m2` recebe attempt 3.

### 11.8 Família avisada após N tentativas

**Nome:** `TestEscalation_NotifiesFamilyAfterMaxAttempts`
**Fixture:** User idoso com 2 guardians vinculados (`family_links` com `notify_on_medication_miss=true`). Pending em attempt 3 (max para `medication_default`). Avança clock 5min.
**Asserção:**
- Notifier recebe 2 mensagens (uma para cada guardian).
- `escalations` tem 2 rows novas (`attempt_number=4`, `recipient_user_id` distinto, `status='sent'`).
- `medication_intake_log` row passa para `status='escalated'`.
- `pending_confirmations.status='escalated'`.
- Audit log tem entrada `medication_escalated` com `details='pc=N guardians=2'`.

### 11.9 Sem guardian = missed, sem alerta

**Nome:** `TestEscalation_NoGuardianMarksMissed`
**Fixture:** Idem 11.8 mas sem nenhum guardian em `family_links`.
**Asserção:** Notifier não recebe nada novo. `intake_log.status='missed'`. `pending.status='missed'`. Audit `medication_missed`.

### 11.10 Confirmação fecha pending

**Nome:** `TestMarkRemedioTomado_ResolvesPending`
**Fixture:** Pending kind=medication ativo. User envia "tomei". Agente chama `marcar_remedio_tomado` sem ID.
**Asserção:**
- `pending.status='confirmed'`.
- `intake_log.status='taken'`, `confirmed_at` populado.
- Audit `medication_taken`.
- Próximo tick do scheduler de escalação não toca neste pending.

### 11.11 Skip com razão

**Nome:** `TestPularDose_RecordsReason`
**Fixture:** Pending ativo. User chama `pular_dose(reason='estou enjoado')`.
**Asserção:**
- `intake_log.status='skipped'`, `response_text='estou enjoado'`.
- `pending.status='skipped'`.
- Audit `medication_skipped` com detalhes incluindo razão.

### 11.12 Foto de receita extraída

**Nome:** `TestExtrairReceita_ExtractsAndQueuesItems`
**Fixture:** Mock Claude retorna 2 items (`Losartana 50mg, 1x/dia`; `Metformina 850mg, 8/8h`). Agente é chamado.
**Asserção:**
- Audit `prescription_image_processed` com JSON dos 2 items.
- Nenhum `medication` ainda persistido (sem confirmação do user).
- Mensagem do agente menciona o primeiro item ("Losartana 50mg, 1x ao dia. Em qual horário você prefere?").

### 11.13 Cadastro de remédio para outro usuário (target_user)

**Nome:** `TestCadastrarMedicamento_ForElder_RequiresFamilyLink`
**Fixture:** User responsável tenta `cadastrar_medicamento` com `target_user="Mãe"`. Sem `family_links`.
**Asserção:** Resposta nega (mensagem natural, não erro 500). Nenhum medicamento persistido.

Mesma fixture, mas com `family_link` válido: cria pending para `Mãe` (UserID = id da mãe), aguarda confirmação dela ou auto-confirm.

### 11.14 Confusão "vou tomar" vs "tomei"

**Nome:** `TestMarkRemedioTomado_FuturoNaoConfirma`
**Fixture:** Pending ativo. User envia "vou tomar daqui a pouco".
**Asserção:** Agente NÃO chama `marcar_remedio_tomado`. Pending permanece pending. Resposta natural do tipo "Ok, te aviso de novo em alguns minutos pra confirmar". Esta é responsabilidade do **system prompt** (instrução explícita: "tomei/já bebi/pronto" → marca; "vou tomar/depois/já já" → NÃO marca, só ack).

> Este teste é mais de integração / prompt-eval que unit. Roda numa harness separada que executa o agente real contra Claude com fixture de mensagens. Aceitável que falhe ocasionalmente — se taxa de erro > 5%, prompt precisa revisão.

### 11.14.1 Dose tardia: bot NÃO orienta tomar atrasado

**Nome:** `TestEscalationMessages_DoNotPushLateDose`
**Fixture:** Snapshot determinístico das mensagens geradas por `insistMsg` para attempts 1-5 nas duas políticas (`medication_default`, `medication_critical`) + `finalFamilyMsg`.
**Asserção (estática, regex):** nenhuma das mensagens contém termos proibidos: `ainda da tempo`, `tome agora`, `compense`, `compensa a dose`, `nao esqueca de tomar`. A mensagem de attempt = MaxAttempts contém alguma forma de "vou anotar" / "anotei" e referência a "medico" ou "familia". A mensagem `finalFamilyMsg` contém "nao oriento" + referência a médico.

> Justificativa: a regra de segurança farmacológica (§9) é arquitetural — não pode ser revertida sem teste falhando. Se uma mensagem nova for adicionada futuramente, este teste obriga revisão consciente.

### 11.14.2 Dose tomada atrasada — bot registra sem reforço positivo

**Nome:** `TestMarkRemedioTomado_LateDose_NeutralResponse`
**Fixture:** Pending criado às 14:00. User responde às 16:30: "tomei agora, atrasado".
**Asserção:** `marcar_remedio_tomado` é chamada (decisão é do user). `medication_intake_log.status='taken'`, `confirmed_at=16:30`. **Resposta do agente é neutra** ("anotado", "registrado") — não contém "otimo", "fez bem", "parabens", "ainda bem". Esta é responsabilidade do **system prompt** da persona companion (Fase 4 §3).

> Prompt-eval test, mesma harness do 11.14. Tolerância: < 5% de respostas com termos de reforço positivo na evolução de prompts.

### 11.15 RRULE inválido

**Nome:** `TestParseRRULE_RejectsBareFreq`
**Fixture:** `ParseRRULE("FREQ=DAILY")`.
**Asserção:** Erro com mensagem clara mencionando `BYHOUR`. Nenhum medicamento criado.

### 11.16 RRULE com END_DATE no passado

**Nome:** `TestExpandOccurrences_RespectsEndDate`
**Fixture:** Schedule com `end_date=2026-05-08`, expandindo em `2026-05-09 14:00`.
**Asserção:** `occs` vazio.

---

## 12. Plano de implementação granular

PRs sugeridos, em ordem. Cada um deve passar `cd bot && go test -v` antes de mergear.

1. **PR1 — Schema + migrations + tipos.** `db.go` ganha as quatro tabelas e as migrações aditivas. `medication.go` define tipos. `db_medication.go` ganha CRUD básico (Create/Get/List/Update). Testes: migração idempotente, CRUD round-trip. **Sem mudança de comportamento ainda.**

2. **PR2 — RRULE wrapper + tests.** `bot/rrule.go` com `ParseRRULE`, `ExpandOccurrences`, `DescribeRRULE`. Adiciona `github.com/teambition/rrule-go` ao `go.mod`. Testes: 11.6, 11.15, 11.16, casos de DST simples.

3. **PR3 — Notifier interface + WhatsAppNotifier.** Refactor mínimo de `main.go` para criar o notifier. `scheduler.go` ganha campos `notifier` e `eng` (placeholder, ainda nil). Testes: `recordingNotifier` mock + 2 testes de fumaça.

4. **PR4 — Tools básicas (sem foto).** `tools_medication.go` com handlers para as 6 tools. Schemas em `agent.go:buildToolDefinitions`. Audit log estendido. Testes: 11.10, 11.11, 11.13, validações de input.

5. **PR5 — Scheduler `checkMedicationReminders`.** Implementa o job, idempotência via UNIQUE. Testes: 11.1, 11.2, 11.3, 11.6.

6. **PR6 — EscalationEngine + `checkMedicationEscalation`.** `escalation.go` completo. Wireup no `main.go`. Testes: 11.4, 11.5, 11.7, 11.8, 11.9.

7. **PR7 — Tool `extrair_receita_imagem` + system prompt.** Adiciona a tool e o trecho de prompt instruindo o fluxo item-a-item sem menu numerado. Confirma que `bot/agent.go` envia imagens ao Claude (se não enviar, este PR adiciona). Testes: 11.12, 11.14.

8. **PR8 — Hardening + audit completo.** Completa todas as ações em audit (`medication_created`, `medication_edited`, etc), adiciona métricas/log estruturado nos eventos críticos, revisa system prompt da persona idoso pra garantir disclaimer médico em todas as ramificações.

9. **PR9 — Polimento + fixtures de QA.** Cria suite de fixtures realistas (idoso fictício "Antonia", responsável "Marcos") e roda end-to-end manual antes de cortar release.

Cada PR é mergeável independente. Feature fica behind feature flag `enable_medication=true` por user (campo novo em `users` ou em `user_memories`) — só usuários do programa-piloto têm os jobs ativos e tools visíveis.

---

## 13. Riscos da fase

### R1. Extração de receita errada

Claude Vision pode ler "Losartana 50mg" como "Losartana 500mg" ou inventar dose ausente. **Mitigação:**
- Sempre apresentar item-a-item para confirmação; nunca persistir direto.
- System prompt instrui: "se a dose não estiver clara na imagem, pergunte ao usuário; não invente."
- Audit guarda a extração crua para auditoria humana posterior.
- Disclaimer obrigatório no fim da extração: "Confere com a receita: vi X. Está certo?".

### R2. Idoso confunde "vou tomar" com "tomei"

**Mitigação:** dois níveis.
1. System prompt: instrução explícita listando gatilhos para marcar (`tomei`, `bebi`, `pronto`, `já está`) e gatilhos para NÃO marcar (`vou tomar`, `daqui a pouco`, `já já`).
2. Quando ambíguo, agente pergunta de novo: "Já tomou ou vai tomar daqui a pouco?". Insistência amigável é melhor que falso positivo.

### R3. DST / fuso

**Mitigação:** `rrule-go` lida com DST quando `DTStart.Location` é tz IANA. Política do MVP é "RRULE no fuso de cadastro", o que é simples e auditável. Documentar em help interno do bot que viajar não muda horário do remédio.

### R4. Escalação excessiva (família spammando)

**Mitigação:**
- `MaxAttempts` máximo de 5 (mesmo no `medication_critical`).
- Família recebe **uma única** mensagem na escalação final, não uma por tentativa.
- `family_links.notify_on_medication_miss` é **opt-in** (default false). Idoso explicitamente concede.
- Se idoso responder "tomei" entre escalação enviada e família ler, mensagem para família continua válida (factual: ele realmente atrasou). Não vale a pena retratar.
- Cooldown: se 3 escalações p/ família dispararam em 24h, o sistema pausa novas escalações por 6h e envia nota ao guardian: "Antonia perdeu 3 doses hoje. Está tudo bem?". Implementar como follow-up se aparecer no piloto.

### R5. RRULE complex / mal entendido pelo Claude

Claude pode gerar RRULE inválido para frases ambíguas como "de 2 em 2 dias menos finais de semana". **Mitigação:**
- `ParseRRULE` falha rápido com mensagem em PT-BR; agente reformula a pergunta ao usuário.
- Suporte limitado a `DAILY/WEEKLY/MONTHLY` + `BYHOUR/BYMINUTE/BYDAY/INTERVAL`. Casos exóticos viram pergunta humana.
- Fallback: se Claude não consegue gerar RRULE em 2 tentativas, agente pede ao user para descrever em palavras simples ("2x ao dia, 8h e 20h").

### R6. SQLite e escrita concorrente

WAL já habilitado em `db.go:48`. Mas escalation e reminders rodam no mesmo processo Go — não há contenção real. Risco de deadlock é baixo. Adicionar `busy_timeout=5000` (já presente) é suficiente.

### R7. Cron 1-min insuficiente para política critical

`medication_critical` tem interval de 3min. Cron de 1min comporta. Mas se algum dia precisarmos `interval=30s`, o cron 1-min fica grosso demais. **Mitigação:** quando isso surgir, mudar `EscalationPolicy.Interval` para algo `>= 1min` por contrato (validar no registry no boot), ou aumentar a frequência do cron para 30s.

### R8. Foto da receita com PII

Já discutido: imagem não persistida. Audit guarda só o texto extraído. Como dado médico é categoria especial sob LGPD, considerar campo `users.consented_to_medical_data=true` antes de qualquer cadastro de remédio. Pre-requisito de UX: na primeira interação com tema medicação, agente pede consentimento explícito. Implementar como follow-up no PR8.

### R9. Restart enquanto família já notificada

Se família foi notificada e o processo cai antes de marcar `intake_log.status='escalated'`, no restart a próxima volta vê pending ainda em pending e re-escala. **Mitigação:** transação que envolve `RecordEscalationAttempt` + `UpdateIntakeStatus` + `ResolvePendingConfirmation`. Se uma falha, todas falham. Implementar via método `db.FinalizeEscalation(pcID, medID, scheduledAt)` em transação única.

---

## 14. Checklist de pronto

### Schema e DB

- [ ] Tabelas `medications`, `medication_schedules`, `medication_intake_log`, `escalations` criadas.
- [ ] `pending_confirmations` ganhou `kind`, `escalation_policy`, `last_attempt_at`, `attempt_number`.
- [ ] Indexes criados (`idx_medications_user_active`, `idx_intake_med_time`, etc).
- [ ] Migrações aditivas idempotentes (rodar `migrate()` 2x não falha).
- [ ] Teste `TestPendingConfirmationsKindDefault` passa.

### Tipos e helpers

- [ ] `Medication`, `MedicationSchedule`, `MedicationIntakeLog`, `Escalation`, `EscalationPolicy`, `EscalationContext`, `Notifier`, `WhatsAppNotifier` definidos.
- [ ] `MedicationIntent` em `IntentData`.
- [ ] `ParseRRULE`, `ExpandOccurrences`, `DescribeRRULE` testados.
- [ ] `firstName` helper.

### Tools

- [ ] 6 tools registradas em `toolHandlers` e `buildToolDefinitions`.
- [ ] Tool `extrair_receita_imagem` registrada.
- [ ] System prompt instrui fluxo item-a-item sem menu numerado.
- [ ] System prompt distingue "tomei" vs "vou tomar".
- [ ] Disclaimer médico no system prompt da persona idoso.

### Scheduler

- [ ] `checkMedicationReminders` rodando via cron 1-min.
- [ ] `checkMedicationEscalation` rodando via cron 1-min.
- [ ] Idempotência via UNIQUE em `medication_intake_log` testada (PR5).
- [ ] Idempotência de escalação testada (PR6).
- [ ] Restart pós-attempt-2 retoma sem duplicar (teste 11.4).

### Notifier e Escalação

- [ ] `WhatsAppNotifier` injetado em `main.go`.
- [ ] `EscalationEngine` injetado em `Scheduler`.
- [ ] `escalationPolicies` registry tem `medication_default` e `medication_critical`.
- [ ] Mensagens de escalação progressivamente mais alarmadas.
- [ ] Fallback "sem guardian = missed" funciona.
- [ ] Notificação para família acontece em transação atômica com update de `intake_log` (R9).

### Audit

- [ ] Ações novas em audit: `medication_created`, `medication_edited`, `medication_canceled`, `medication_taken`, `medication_skipped`, `medication_missed`, `medication_escalated`, `prescription_image_processed`, `medication_reminder_sent`.
- [ ] `actionLabelsPT` em `audit.go` traduzido para todas elas.

### Foto de receita

- [ ] `bot/agent.go` envia imagens ao Claude (verificado / adicionado).
- [ ] Tool `extrair_receita_imagem` apenas extrai e enfileira; nunca persiste.
- [ ] Audit log da extração (sem imagem em si).
- [ ] Confirmação item-a-item testada (11.12).

### Family link

- [ ] `db.CanManageMedicationFor` existe e respeita `family_links` da Fase 1.
- [ ] `db.ListGuardiansForUser(userID, "notify_on_medication_miss")` existe.
- [ ] `family_links` migration da Fase 1 mergeada antes desta fase.

### Testes e QA

- [ ] Todos os testes em `medication_test.go` passam (`cd bot && go test -v -run Medication`).
- [ ] Teste end-to-end manual: cadastrar Losartana via texto → recebe lembrete → "tomei" → fecha pending → audit completo.
- [ ] Teste end-to-end manual: cadastrar via foto → confirma item 1 → confirma item 2 → recebe lembretes de ambos.
- [ ] Teste end-to-end manual: ignora 3 lembretes seguidos → guardian recebe alerta.
- [ ] Feature flag `enable_medication` testada (off = nada novo aparece para o usuário).

### Documentação

- [ ] `CLAUDE.md` atualizado mencionando o novo fluxo de medicamentos.
- [ ] Comentários em `escalation.go` explicando como adicionar política nova.
- [ ] README do bot menciona a dependência `rrule-go`.

