package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// =========================================================================
// Fase 5 — SnapshotWriter adapter (impl concreta)
// =========================================================================
//
// Esta eh a impl concreta da interface `SnapshotWriter` (Fase 4 deixou
// noopSnapshotWriter como default). main.go injeta isso via
// agent.WithSnapshotWriter.
//
// Fluxo de MaybeUpdateSnapshot (chamado pelo handler apos conversa
// significativa):
//
//   1. Carrega user, valida que eh idoso (defesa em profundidade — handler
//      ja deveria filtrar).
//   2. Calcula dia local em fuso do user (ou BRT default).
//   3. Carrega snapshot existente do dia (pra update incremental).
//   4. Carrega mensagens do dia, medicacao, memos `risco:*`, alertas
//      gerados hoje.
//   5. Pula se consent revoked em TODOS os family_links do user.
//   6. Chama synthesis.WriteSnapshot.
//   7. UPSERT em psych_state_daily.
//   8. Se SafetyAlertNeeded != nil, dispara handleSafetyAlertFromWriter
//      (reusa pipeline alertar_familia).
//   9. Audit log: psych_snapshot_written ou psych_snapshot_failed.
//
// Nao bloqueante: caller (handler.flushBuffer) chama em goroutine com
// timeout de 30s. Erros sao logados mas nao propagados ao usuario.

// snapshotWriterImpl implementa SnapshotWriter (interface no pacote main).
// Recebe AnalysisProvider (Haiku) injetado em main.go.
type snapshotWriterImpl struct {
	db       *DB
	audit    *AuditLog
	analysis llm.AnalysisProvider
	sendMsg  func(phone, text string) error
	// nowFunc permite teste injetar relogio fixo. Em prod: time.Now.
	nowFunc func() time.Time
}

// NewSnapshotWriter constroi o writer adapter. analysis pode ser nil em
// ambientes onde LLM nao esta disponivel (testes, CLI) — neste caso,
// MaybeUpdateSnapshot vira no-op e loga warning.
func NewSnapshotWriter(db *DB, audit *AuditLog, analysis llm.AnalysisProvider, sendMsg func(phone, text string) error) *snapshotWriterImpl {
	return &snapshotWriterImpl{
		db:       db,
		audit:    audit,
		analysis: analysis,
		sendMsg:  sendMsg,
		nowFunc:  time.Now,
	}
}

// withNow permite testes injetarem relogio fixo. Nao exposto.
func (w *snapshotWriterImpl) withNow(f func() time.Time) *snapshotWriterImpl {
	if f == nil {
		f = time.Now
	}
	w.nowFunc = f
	return w
}

// MaybeUpdateSnapshot eh a entrypoint da interface. Ver doc do tipo.
func (w *snapshotWriterImpl) MaybeUpdateSnapshot(ctx context.Context, userID int64) error {
	if w == nil || w.db == nil {
		return errors.New("snapshot writer not configured")
	}
	if w.analysis == nil {
		log.Printf("[snapshot writer] analysis provider nao configurado — skip user=%d", userID)
		return nil
	}

	user, err := w.db.GetUserByID(userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if user.Type != UserTypeIdoso {
		// Defesa em profundidade — handler ja deveria filtrar.
		return nil
	}

	// Resolve fuso do user. Fase 5 nao tem coluna timezone em users; usamos
	// GetEventTimezone que considera viagem ativa (default BRT).
	tz := w.db.GetEventTimezone(user.ID, w.nowFunc())
	if tz == nil {
		tz = BRT()
	}

	now := w.nowFunc()
	localNow := now.In(tz)
	dayDate := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, tz)

	// Skip se consent revoked em todos os family_links — sem responsavel
	// pra ler, nao adianta gerar snapshot. Mas se ha pelo menos 1 com
	// consent active, gera (responsavel pode consultar).
	if w.consentRevokedForAllGuardians(user.ID) {
		log.Printf("[snapshot writer] consent revoked em todos os links de %s — skip", user.Name)
		return nil
	}

	prev, err := w.db.GetSnapshot(user.ID, dayDate)
	if err != nil {
		return fmt.Errorf("get prev snapshot: %w", err)
	}

	msgs, err := w.db.GetMessagesSinceForUser(user.ID, dayDate)
	if err != nil {
		return fmt.Errorf("get msgs: %w", err)
	}
	// Filtra mensagens do dia (em local tz). Defesa contra GetMessagesSinceForUser
	// trazer mensagens atravessando midnight UTC mas pertencentes a outro dia local.
	dayMsgs := filterMessagesInLocalDay(msgs, dayDate, tz)
	if len(dayMsgs) == 0 {
		// Nada novo no dia — nada a inferir. NAO erro.
		return nil
	}

	intake, _ := w.db.GetMedicationIntakeOnDay(user.ID, dayDate, tz)
	taken, missed := splitMedicationIntake(intake)

	riskMemos, _ := w.db.GetSocialContextRiskMemos(user.ID, 10)
	alerts, _ := w.db.GetAlertsOnDay(user.ID, dayDate, tz)

	in := synthesis.SnapshotInput{
		User: synthesis.User{
			ID:       user.ID,
			Name:     user.Name,
			Timezone: tz.String(),
		},
		Date:                   dayDate,
		PreviousSnapshot:       prev,
		NewMessages:            dayMsgs,
		MedicationsTakenToday:  taken,
		MedicationsMissedToday: missed,
		SocialContextRiskMemos: riskMemos,
		AlertasGerados:         alerts,
	}

	out, err := synthesis.WriteSnapshot(ctx, w.analysis, in)
	if err != nil {
		if w.audit != nil {
			w.audit.Log(user.ID, "psych_snapshot_failed", "", sanitizeAuditReason(err.Error()))
		}
		log.Printf("[snapshot writer] WriteSnapshot user=%d: %v", user.ID, err)
		return err
	}

	counts := synthesis.SnapshotCounts{
		NConversations:  countSessions(dayMsgs),
		NMessages:       len(dayMsgs),
		DurationMinutes: estimateDurationMinutes(dayMsgs),
	}
	snap := out.ToDailySnapshot(user.ID, dayDate, counts)
	if err := w.db.UpsertPsychSnapshot(&snap); err != nil {
		log.Printf("[snapshot writer] Upsert user=%d: %v", user.ID, err)
		if w.audit != nil {
			w.audit.Log(user.ID, "psych_snapshot_failed", "", sanitizeAuditReason(err.Error()))
		}
		return err
	}
	if w.audit != nil {
		w.audit.Log(user.ID, "psych_snapshot_written", "",
			fmt.Sprintf("via=trigger|date=%s|confidence=%d", dayDate.Format("2006-01-02"), out.Confidence))
	}
	log.Printf("[snapshot writer] user=%s date=%s confidence=%d safety=%t",
		user.Name, dayDate.Format("2006-01-02"), out.Confidence, out.SafetyAlertNeeded != nil)

	if out.SafetyAlertNeeded != nil {
		w.handleSafetyAlertFromWriter(ctx, user, dayDate, out.SafetyAlertNeeded)
	}
	return nil
}

// handleSafetyAlertFromWriter dispara escalation pra familia quando o writer
// detecta sinal grave que o companion (DeepSeek) nao chamou. Reusa o pipeline
// de alertar_familia (Fase 4): notifica guardians com NotifyOnSevereSignal=true,
// registra escalation com policy=severe_signal_safety_net, audita.
func (w *snapshotWriterImpl) handleSafetyAlertFromWriter(ctx context.Context, elder *User, day time.Time, sa *synthesis.SafetyAlert) {
	guardians, err := w.db.GetGuardians(elder.ID)
	if err != nil {
		log.Printf("[safety_net] get guardians: %v", err)
		return
	}

	now := w.nowFunc()
	details := fmt.Sprintf(
		"severity=%s|category=%s|reason=%s|recommended=%s|date=%s|via=writer_safety_net",
		sa.Severity, sa.Category, sanitizeForDetails(sa.Reason),
		sanitizeForDetails(sa.Recommended), day.Format("2006-01-02"),
	)
	msg := formatSafetyNetMessage(elder, sa)

	var sentTo, failedFor []string
	for _, g := range guardians {
		if g.Other == nil {
			continue
		}
		if !g.Notify.OnSevereSignal {
			continue
		}
		// Consent revoked = nao envia.
		consent, _ := w.db.GetDependentConsent(g.GuardianID, g.DependentID)
		if consent == ConsentRevoked {
			continue
		}
		if w.sendMsg == nil {
			failedFor = append(failedFor, g.Other.Name)
			continue
		}
		if err := w.sendMsg(g.Other.PhoneNumber, msg); err != nil {
			log.Printf("[safety_net] send to %s: %v", g.Other.Name, err)
			failedFor = append(failedFor, g.Other.Name)
			continue
		}
		sentTo = append(sentTo, g.Other.Name)
		// Registra row em escalations (reusa RecordSevereSignalEscalation).
		_, escErr := w.db.RecordSevereSignalEscalation(
			elder.ID, "severe_signal_safety_net", sa.Severity,
			details, g.Other.ID, "whatsapp", now,
		)
		if escErr != nil {
			log.Printf("[safety_net] record escalation: %v", escErr)
		}
	}

	if w.audit != nil {
		// Audit estruturado pra observabilidade.
		w.audit.Log(elder.ID, "safety_alert_from_writer", "",
			fmt.Sprintf("date=%s|severity=%s|category=%s|sent_to=%d|failed_for=%d",
				day.Format("2006-01-02"), sa.Severity, sa.Category, len(sentTo), len(failedFor)))
	}
	_ = ctx // ctx mantido em signature para futura chamada com timeout se necessario
}

// formatSafetyNetMessage monta texto WhatsApp para safety_net (writer detected).
// Tom mais sobrio que alertar_familia direto — eh padrao detectado por
// observador externo, nao chamada do companion no calor da conversa.
func formatSafetyNetMessage(elder *User, sa *synthesis.SafetyAlert) string {
	prefix := "Aviso — observamos sinais que merecem atencao em " + elder.Name + "."
	switch sa.Severity {
	case "critical":
		prefix = "URGENTE — " + elder.Name + " apresentou sinais que precisam de atencao agora."
	case "warn":
		prefix = "Atencao — observamos algo preocupante em " + elder.Name + "."
	}
	body := prefix + "\n\nObservacao: " + sa.Reason
	if sa.Recommended != "" {
		body += "\n\nSugestao: " + sa.Recommended
	}
	body += "\n\n— Lurch (companion de " + elder.Name + ")"
	return body
}

// consentRevokedForAllGuardians eh true quando NAO ha nenhum link com
// consent='active'. Usado pra pular geracao de snapshot quando idoso revogou
// pra todos os responsaveis.
func (w *snapshotWriterImpl) consentRevokedForAllGuardians(userID int64) bool {
	guardians, err := w.db.GetGuardians(userID)
	if err != nil || len(guardians) == 0 {
		// Sem guardians cadastrados — geramos snapshot mesmo assim
		// (responsavel pode ser cadastrado depois e ja ler historico).
		return false
	}
	for _, g := range guardians {
		consent, _ := w.db.GetDependentConsent(g.GuardianID, g.DependentID)
		if consent != ConsentRevoked {
			return false
		}
	}
	return true
}

// =========================================================================
// helpers compartilhados (reusados por scheduler de catchup)
// =========================================================================

// splitMedicationIntake separa lista por status (taken vs missed). Inputs
// de outras categorias (skipped, pending) sao ignorados — o writer so
// precisa do binario tomado/perdido pra inferir autocuidado.
func splitMedicationIntake(intake []synthesis.MedicationIntake) (taken, missed []synthesis.MedicationIntake) {
	for _, i := range intake {
		switch i.Status {
		case "taken":
			taken = append(taken, i)
		case "missed", "escalated":
			missed = append(missed, i)
		}
	}
	return
}

// filterMessagesInLocalDay deixa passar apenas mensagens cujo timestamp,
// convertido pra fuso `tz`, cai no dia [dayStart, dayStart+24h). Serve
// pra esquilos de timezone — mensagem as 23:30 UTC de 9-mai pode ser 20:30
// BRT do dia 9, nao 22:30 BRT do dia 9.
func filterMessagesInLocalDay(msgs []synthesis.ConversationMessage, dayStart time.Time, tz *time.Location) []synthesis.ConversationMessage {
	if tz == nil {
		tz = time.UTC
	}
	dayEnd := dayStart.Add(24 * time.Hour)
	out := make([]synthesis.ConversationMessage, 0, len(msgs))
	for _, m := range msgs {
		mt := m.Timestamp.In(tz)
		if (mt.Equal(dayStart) || mt.After(dayStart)) && mt.Before(dayEnd) {
			out = append(out, m)
		}
	}
	return out
}

// countSessions divide mensagens em "sessoes" usando gap de 30min sem
// mensagem como separador. Exposto pra usos externos (catchup).
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

// estimateDurationMinutes soma duracao de cada sessao (gap de 30min separa
// sessoes). Sessao de 1 mensagem so conta 0min — eh pontual, nao se mediu
// engajamento.
func estimateDurationMinutes(msgs []synthesis.ConversationMessage) int {
	if len(msgs) < 2 {
		return 0
	}
	total := 0.0
	sessionStart := msgs[0].Timestamp
	prev := msgs[0].Timestamp
	for i := 1; i < len(msgs); i++ {
		m := msgs[i]
		if m.Timestamp.Sub(prev) > 30*time.Minute {
			total += prev.Sub(sessionStart).Minutes()
			sessionStart = m.Timestamp
		}
		prev = m.Timestamp
	}
	total += prev.Sub(sessionStart).Minutes()
	if total < 0 {
		total = 0
	}
	return int(total)
}
