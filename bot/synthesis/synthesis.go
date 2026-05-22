// Package synthesis hospeda os dois sub-agentes longitudinais da Fase 5:
//
//	WriteSnapshot — Haiku 4.5. Atualiza psych_state_daily com 1 linha por
//	                (user, dia). Le mensagens recentes do dia + medicacao
//	                + memos `risco:*`, escreve scores/sinais abstratos.
//	Synthesize    — Sonnet 4.6/4.7. Le N dias de snapshots + stats agregados,
//	                produz relatorio acolhedor pra responsavel familiar.
//
// Contrato de privacidade:
//   - WriteSnapshot pode VER mensagens recentes do dia, mas o OUTPUT eh
//     abstrato (scores + observacoes). Rejeita persistir citacao literal,
//     termo clinico ou fofoca social via ValidateSnapshotOutput.
//   - Synthesize NUNCA ve texto cru de conversa — recebe apenas
//     psych_state_daily + medication stats + alerts agregados. Rejeita
//     citacao literal e termo clinico via ValidateReportOutput.
//   - Ambas funcoes sao puras: SEM tools, SEM historico de conversa, SEM
//     acesso a arquivos. So fazem 1 chamada de LLM e devolvem.
//
// Erros sentinela: ErrParse | ErrValidation | ErrAPI. Caller pode usar
// errors.Is pra decidir entre retry, fallback degradado e log estruturado.
package synthesis

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// =========================== Tipos compartilhados ============================

// Memory espelha uma linha de user_memories. O writer SO recebe memos com
// Key comecando em "risco:" — caller filtra ANTES de chamar (defesa em
// profundidade contra fofoca social atravessar fronteira).
type Memory struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ConversationMessage eh uma versao enxuta de mensagem do user/bot. So o
// writer ve isso — Synthesize NAO recebe nem deve receber.
type ConversationMessage struct {
	Role      string    `json:"role"` // "user" | "assistant"
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// MedicationIntake eh uma dose escalonada num dia, com status final.
type MedicationIntake struct {
	MedicationName string    `json:"medication_name"`
	ScheduledAt    time.Time `json:"scheduled_at"`
	Status         string    `json:"status"` // taken|missed|skipped|pending|escalated
}

// Alert eh um alerta JA disparado pelo sistema (alertar_familia ou writer
// passado). Passado ao writer pra ele NAO duplicar safety_alert_needed
// quando ja existe alerta correspondente hoje.
type Alert struct {
	PolicyName string    `json:"policy_name"`
	Severity   string    `json:"severity"`
	CreatedAt  time.Time `json:"created_at"`
}

// User eh a projecao slim que ambos sub-agentes precisam.
type User struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Timezone string `json:"timezone,omitempty"` // IANA (default vazio = BRT)
}

// DailySnapshot espelha uma linha de psych_state_daily. Usada como input
// (PreviousSnapshot) e como output do writer (apos o caller chamar
// ToDailySnapshot pra preencher counts).
type DailySnapshot struct {
	UserID             int64     `json:"user_id"`
	SnapshotDate       time.Time `json:"snapshot_date"`
	HumorScore         int       `json:"humor_score"` // 0 = NULL no banco
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

// MissedDose eh referenciada em MedicationStats — uma dose que ficou pra
// tras nos ultimos 7d.
type MissedDose struct {
	MedicationName string    `json:"medication_name"`
	ScheduledAt    time.Time `json:"scheduled_at"`
}

// MedicationStats eh agregado de 7d construido pelo caller (db helper).
// Synthesize recebe a versao "wire" (medicationStatsW) — stats internas
// nao saem do servidor.
type MedicationStats struct {
	Scheduled int `json:"scheduled"`
	Taken     int `json:"taken"`
	Missed    int `json:"missed"`
	Skipped   int `json:"skipped"`
	Pending   int `json:"pending"`
	// Unknown: doses de remedios sem exigencia de confirmacao, nao confirmadas.
	// Ficam FORA do denominador da aderencia.
	Unknown       int          `json:"unknown"`
	AdherenceFrac float64      `json:"adherence_frac"`
	MissedDoses   []MissedDose `json:"missed_doses"`
}

// =========================== WRITER ============================

// SnapshotInput eh tudo que WriteSnapshot precisa pra inferir o snapshot do
// dia. Caller eh responsavel por:
//   - Filtrar SocialContextRiskMemos pra so memos `risco:*` (fronteira de
//     privacidade — fofoca social NUNCA pode chegar aqui).
//   - Preencher PreviousSnapshot com a linha existente em psych_state_daily
//     (se existir) — writer faz update incremental, nao reescreve do zero.
//   - Preencher AlertasGerados com escalations criadas hoje pro user, pra
//     o writer evitar duplicar safety_alert_needed.
type SnapshotInput struct {
	User                   User                  `json:"user"`
	Date                   time.Time             `json:"date"`
	PreviousSnapshot       *DailySnapshot        `json:"previous_snapshot,omitempty"`
	NewMessages            []ConversationMessage `json:"new_messages"`
	MedicationsTakenToday  []MedicationIntake    `json:"medications_taken_today"`
	MedicationsMissedToday []MedicationIntake    `json:"medications_missed_today"`
	SocialContextRiskMemos []Memory              `json:"social_context_risk_memos"`
	AlertasGerados         []Alert               `json:"alertas_gerados_hoje"`
}

// SnapshotOutput eh o que o writer (Haiku) emite. Caller chama
// ValidateSnapshotOutput antes de persistir; se falhar, descarta e audita.
//
// HumorScore == 0 (e demais *Score) significa "sem dado pra inferir nessa
// dimensao" — vira NULL no banco via UpsertPsychSnapshot. NAO interpretar
// como "muito baixo".
type SnapshotOutput struct {
	HumorScore         int          `json:"humor_score"`
	HumorNuance        string       `json:"humor_nuance"`
	EnergiaScore       int          `json:"energia_score"`
	SociabilidadeScore int          `json:"sociabilidade_score"`
	AutocuidadoScore   int          `json:"autocuidado_score"`
	SinaisObservados   []string     `json:"sinais_observados"`
	EventosDia         []string     `json:"eventos_dia"`
	Confidence         int          `json:"confidence"`
	SafetyAlertNeeded  *SafetyAlert `json:"safety_alert_needed,omitempty"`
}

// SafetyAlert eh disparado quando o writer detecta sinal grave que o
// companion (DeepSeek) NAO acionou via alertar_familia. Permite o writer
// servir como ultima linha de defesa.
//
// Category espelha o enum da tool alertar_familia (Fase 4). Se vier valor
// fora do enum, ValidateSnapshotOutput rejeita — writer nao pode inventar
// categoria nova.
type SafetyAlert struct {
	Severity    string `json:"severity"`    // info|warn|critical
	Category    string `json:"category"`    // medico_fisico|psicologico|violencia|negligencia|outros
	Reason      string `json:"reason"`      // 1 frase observacional, sem citacao
	Recommended string `json:"recommended"` // 1 frase de acao gentil ao responsavel
}

// ToDailySnapshot converte o output do writer pra linha de DB. Caller passa
// counts (n_conversations, n_messages, duration_minutes) computados a partir
// das mensagens — writer NAO ecoa counts pra evitar inconsistencia.
func (o SnapshotOutput) ToDailySnapshot(userID int64, date time.Time, counts SnapshotCounts) DailySnapshot {
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

// SnapshotCounts agrega contagens estatisticas que o caller computa a
// partir das mensagens do dia (writer nao precisa contar — eh determinista).
type SnapshotCounts struct {
	NConversations  int
	NMessages       int
	DurationMinutes int
}

// =========================== REPORT ============================

// ReportInput eh tudo que Synthesize precisa pra produzir o relatorio
// longitudinal. NAO inclui mensagens/memos/transcricoes — apenas snapshots
// abstratos + stats agregados.
type ReportInput struct {
	Dependent         User            `json:"dependent"`
	Days              int             `json:"days"` // janela default 14
	Snapshots         []DailySnapshot `json:"snapshots"`
	MedicationStats   MedicationStats `json:"medication_stats"`
	OpenAlerts        []Alert         `json:"open_alerts"`
	LastUserMessageAt sql.NullTime    `json:"-"`
}

// ReportOutput eh o relatorio acolhedor que vai pro responsavel. Validacao
// dura via ValidateReportOutput rejeita citacao literal, termo clinico,
// recomendacao medicamentosa.
type ReportOutput struct {
	Tendencia               string   `json:"tendencia"`
	Comparacao              string   `json:"comparacao"`
	HumorRecente            string   `json:"humor_recente"`
	PontoDeAtencao          string   `json:"ponto_de_atencao"`
	Resumo                  string   `json:"resumo"`
	RecomendacoesCarinhosas []string `json:"recomendacoes_carinhosas"`
	NivelPreocupacao        string   `json:"nivel_preocupacao"`
}

// =========================== Enums ============================

// validCategories espelha o enum da tool alertar_familia (Fase 4 §8.1).
// Se SafetyAlertNeeded.Category vier fora deste set, ValidateSnapshotOutput
// rejeita — writer nao pode inventar categoria nova (downstream depende
// disso pra decidir disclosurePolicy).
var validCategories = map[string]bool{
	"medico_fisico": true,
	"psicologico":   true,
	"violencia":     true,
	"negligencia":   true,
	"outros":        true,
}

var validTendencia = map[string]bool{
	"melhorando":    true,
	"estavel":       true,
	"piorando":      true,
	"instavel":      true,
	"indeterminado": true,
}

var validNivel = map[string]bool{
	"tranquilo":    true,
	"atencao":      true,
	"atencao_alta": true,
	"indeterminado": true,
}

var validSeverity = map[string]bool{
	"info":     true,
	"warn":     true,
	"critical": true,
}

// =========================== Erros sentinela ============================

// Erros sentinela permitem caller diferenciar parse de validacao de API.
// Use errors.Is pra decidir retry vs. log+fallback.
var (
	ErrParse      = errors.New("synthesis: parse error")
	ErrValidation = errors.New("synthesis: validation error")
	ErrAPI        = errors.New("synthesis: api error")
)

// =========================== Provider interfaces ============================

// AnalysisClient eh a interface que WriteSnapshot espera (subset de
// llm.AnalysisProvider). Mantida em separado para permitir mock em tests
// sem importar llm completo.
type AnalysisClient interface {
	Analyze(ctx context.Context, req llm.AnalysisRequest) (llm.AnalysisResponse, error)
}

// ReportClient eh a interface que Synthesize espera (subset de
// llm.ReportProvider). Mantida em separado pra mock em tests.
type ReportClient interface {
	Synthesize(ctx context.Context, req llm.ReportRequest) (llm.ReportResponse, error)
}
