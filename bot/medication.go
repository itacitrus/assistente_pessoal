package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// =========================================================================
// Tipos de medicacao (Fase 3)
// =========================================================================

// Medication eh o cadastro mestre. Um medication tem 1..N schedules.
// Active=false eh o soft-delete: lembretes futuros param, mas o historico
// (medication_intake_log) eh preservado.
type Medication struct {
	ID              int64
	UserID          int64  // dono (quem toma o remedio)
	Name            string // "Losartana"
	Dose            string // "50mg" — texto livre (pode ser "1 colher", "2 gotas")
	Instructions    string // "tomar com agua em jejum" — texto livre
	Active          bool
	CreatedByUserID int64 // pode ser != UserID (responsavel cadastrou pro idoso)
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// MedicationSchedule eh um RRULE iCal aplicado ao medication.
// Critical=true muda a politica de escalacao (intervalos menores, mais
// tentativas) — ver bot/escalation.go::escalationPolicies.
type MedicationSchedule struct {
	ID           int64
	MedicationID int64
	RRULE        string     // ex: "FREQ=DAILY;BYHOUR=8,14,20;BYMINUTE=0"
	StartDate    time.Time  // YYYY-MM-DD parseado em BRT (ou tz do user no cadastro)
	EndDate      *time.Time // nil = tratamento continuo
	Critical     bool
	CreatedAt    time.Time
}

// MedicationIntakeLog eh o historico real de tomadas.
// UNIQUE(medication_id, scheduled_at) eh a chave de idempotencia do
// scheduler — duas chamadas no mesmo segundo nao geram duas rows.
type MedicationIntakeLog struct {
	ID           int64
	MedicationID int64
	ScheduledAt  time.Time // UTC, instante exato da ocorrencia prevista
	Status       IntakeStatus
	ConfirmedAt  *time.Time
	ResponseText string
	CreatedAt    time.Time
}

// IntakeStatus enumera os estados de uma tomada agendada. O CHECK constraint
// no DDL espelha exatamente esta lista.
type IntakeStatus string

const (
	IntakePending   IntakeStatus = "pending"   // lembrete enviado, aguardando resposta
	IntakeTaken     IntakeStatus = "taken"     // user confirmou tomada
	IntakeSkipped   IntakeStatus = "skipped"   // user explicitamente pulou
	IntakeMissed    IntakeStatus = "missed"    // sem guardian / esgotou tentativas
	IntakeEscalated IntakeStatus = "escalated" // escalou pra familia
)

// Escalation eh uma row por tentativa por destinatario. Um pending_confirmation
// pode ter varias rows: tentativas 1..N pro proprio user, mais uma row por
// guardian na escalacao final.
type Escalation struct {
	ID                    int64
	PendingConfirmationID int64
	PolicyName            string
	AttemptNumber         int
	ScheduledFor          time.Time // UTC, quando o disparo foi agendado
	Status                EscalationStatus
	NotifierUsed          string // "whatsapp" | "voice" (futuro)
	RecipientUserID       int64
	SentAt                *time.Time
	CreatedAt             time.Time
}

// EscalationStatus enumera os estados de uma row em escalations.
type EscalationStatus string

const (
	EscPending           EscalationStatus = "pending"
	EscSent              EscalationStatus = "sent"
	EscAcknowledged      EscalationStatus = "acknowledged"
	EscEscalatedToFamily EscalationStatus = "escalated_to_family"
	EscFailed            EscalationStatus = "failed"
)

// EscalationTarget controla quem recebe a escalacao final.
type EscalationTarget string

const (
	// EscalateToFamily notifica guardians via family_links (com flag
	// notify_on_medication_miss=1).
	EscalateToFamily EscalationTarget = "family"
	// EscalateToSelfOnly insiste ate MaxAttempts mas nao avisa familia.
	EscalateToSelfOnly EscalationTarget = "self_only"
	// EscalateToNone marca como missed sem qualquer alerta.
	EscalateToNone EscalationTarget = "none"
)

// EscalationContext eh passado ao formatter da policy pra renderizar mensagens.
type EscalationContext struct {
	User              *User       // quem deveria responder (dono do remedio)
	Medication        *Medication // contexto opcional (pode ser nil em politicas genericas)
	ScheduledAt       time.Time   // UTC
	AttemptNumber     int         // 1, 2, 3...
	Recipient         *User       // proprio user OU guardian
	IsFinalEscalation bool        // true quando attempt > MaxAttempts e indo pra familia
}

// EscalationPolicy eh a abstracao "politica como dado". Politica nova =
// nova entrada em escalationPolicies em escalation.go. Nao precisa codigo
// novo no engine.
type EscalationPolicy struct {
	Name          string
	MaxAttempts   int
	Interval      time.Duration // entre tentativas
	EscalateTo    EscalationTarget
	EscalationMsg func(ctx EscalationContext) string
}

// =========================================================================
// Notifier — abstracao do canal de envio
// =========================================================================

// Notifier abstrai o canal de saida. Hoje so WhatsApp esta implementado;
// Twilio voz vem na Fase 6 sem mudar nada do scheduler ou da escalacao.
type Notifier interface {
	Send(ctx context.Context, recipient *User, message string) error
	Channel() string // "whatsapp", "voice"
}

// WhatsAppNotifier embrulha o callback sendMsg(phone, text) que ja existe
// em handler.SendTextToPhone. Mantem compat com o resto do bot e isola
// o scheduler/escalacao do detalhe de transporte.
type WhatsAppNotifier struct {
	sendMsg func(phone, text string) error
}

// NewWhatsAppNotifier constroi um notifier que despacha via callback de envio
// de WhatsApp. Erra se sendMsg for nil — chamadas em testes geralmente
// preferem implementar a interface diretamente (ver recordingNotifier nos
// testes).
func NewWhatsAppNotifier(sendMsg func(phone, text string) error) *WhatsAppNotifier {
	return &WhatsAppNotifier{sendMsg: sendMsg}
}

// Send entrega message para recipient via WhatsApp. Defensivo contra recipient
// nil pra evitar panic em estados inesperados (ex: race com delete de user).
func (n *WhatsAppNotifier) Send(_ context.Context, recipient *User, message string) error {
	if recipient == nil {
		return errors.New("WhatsAppNotifier.Send: nil recipient")
	}
	if n.sendMsg == nil {
		return errors.New("WhatsAppNotifier.Send: sendMsg callback not configured")
	}
	return n.sendMsg(recipient.PhoneNumber, message)
}

// Channel retorna o nome do canal usado por este notifier. Persistido em
// escalations.notifier_used pra observabilidade.
func (n *WhatsAppNotifier) Channel() string { return "whatsapp" }

// =========================================================================
// Sentinels e helpers
// =========================================================================

// Sentinels de medicacao e escalacao.
var (
	// ErrMedicationNotFound indica que o medication_id nao existe ou o usuario
	// pediu por nome e nao encontrou.
	ErrMedicationNotFound = errors.New("medication not found")

	// ErrMedicationNotPermitted indica que o ator pediu pra mexer em remedio
	// de outro usuario sem family_link valido.
	ErrMedicationNotPermitted = errors.New("not permitted to manage medication for this user")

	// ErrIntakeLogDuplicate eh devolvido por CreateIntakeLogIfAbsent quando
	// o registro ja existe (UNIQUE constraint). Tratado como sinal idempotente
	// pelo scheduler — nao eh erro real.
	ErrIntakeLogDuplicate = errors.New("intake log already exists for this scheduled_at")
)

// firstName extrai primeiro nome para mensagens informais.
// "Antonia da Silva" -> "Antonia". Vazio devolve vazio (sem panic).
func firstName(full string) string {
	for i := 0; i < len(full); i++ {
		if full[i] == ' ' || full[i] == '\t' {
			if i == 0 {
				continue
			}
			return full[:i]
		}
	}
	return full
}

// medScheduledAt extrai o ScheduledAt embutido em pc.EventData (quando o
// pending eh um lembrete de medicacao). Devolve zero-time se a estrutura
// nao for de medicacao ou estiver corrompida.
func medScheduledAt(pc *PendingConfirmation) time.Time {
	data := parseMedicationIntent(pc)
	if data == nil {
		return time.Time{}
	}
	return data.ScheduledAt
}

// medMedicationID idem para o MedicationID.
func medMedicationID(pc *PendingConfirmation) int64 {
	data := parseMedicationIntent(pc)
	if data == nil {
		return 0
	}
	return data.MedicationID
}

// parseMedicationIntent decodifica pc.EventData e devolve o sub-objeto
// MedicationIntent. Devolve nil se nao parseavel ou se o pending nao eh de
// medicacao.
func parseMedicationIntent(pc *PendingConfirmation) *MedicationIntent {
	if pc == nil || pc.EventData == "" {
		return nil
	}
	var data IntentData
	if err := json.Unmarshal([]byte(pc.EventData), &data); err != nil {
		return nil
	}
	return data.Medication
}
