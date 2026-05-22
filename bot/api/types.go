// Package api expoe os endpoints REST do painel web do Lurch (Fase 2 do
// plano de idosos). Ele eh isolado em um sub-package porque `bot` eh
// `package main` — nao da pra importar dele de fora. A interface Store +
// DTOs neste arquivo sao a fronteira: o main implementa a Store via
// adapter (`api_adapter.go`) e injeta no Mount().
//
// Toda a logica de auth, validacao, CSRF, CORS e rate limit vive aqui.
// Nada de logica de negocio — Store eh thin (cria, busca, atualiza). A
// orquestracao real (BuildDependentStatus, magic link send) entra via
// callbacks tipados.
package api

import (
	"time"
)

// User eh o DTO compartilhado com o frontend Next.js. Mapeado pra
// `web/types/api.ts` no plano §4. Nao expomos credenciais Google — derivamos
// `GoogleConnected` no adapter.
//
// Campos obrigatorios usam zero-value como sentinel ausente em PATCH (ver
// PreferencesPatch que usa ponteiros pra distinguir "nao mexeu" de "limpou").
type User struct {
	ID                       int64     `json:"id"`
	PhoneNumber              string    `json:"phone_number"`
	Name                     string    `json:"name"`
	Type                     string    `json:"type"`
	DailySummaryTime         string    `json:"daily_summary_time"`
	WeeklySummaryDay         string    `json:"weekly_summary_day"`
	WeeklySummaryTime        string    `json:"weekly_summary_time"`
	ReminderBefore           string    `json:"reminder_before"`
	AutoConfirmTimeout       string    `json:"auto_confirm_timeout"`
	InactivityThresholdHours int       `json:"inactivity_threshold_hours"`
	GoogleConnected          bool      `json:"google_connected"`
	IsActive                 bool      `json:"is_active"`
	CreatedAt                time.Time `json:"created_at"`
}

// FamilyLink reflete a tabela family_links. As prefs vivem aninhadas em
// `notify` pra alinhar com o codigo Go existente.
type FamilyLink struct {
	ID            int64     `json:"id"`
	GuardianID    int64     `json:"guardian_id"`
	DependentID   int64     `json:"dependent_id"`
	Relationship  string    `json:"relationship"`
	Notify        Notify    `json:"notify"`
	ConsentStatus string    `json:"consent_status"`
	CreatedAt     time.Time `json:"created_at"`
}

// Notify eh subset das flags por canal.
type Notify struct {
	OnMedicationMiss bool `json:"on_medication_miss"`
	OnInactivity     bool `json:"on_inactivity"`
	OnSevereSignal   bool `json:"on_severe_signal"`
}

// DependentSummary eh o que `GET /family/dependents` retorna em loop.
type DependentSummary struct {
	User User       `json:"user"`
	Link FamilyLink `json:"link"`
}

// PreferencesPatch eh o body de `PATCH /users/me`. Ponteiros distinguem
// "campo ausente no JSON" (nil) de "campo presente com valor" (set). Sem
// isso, nao da pra "deixar como esta" vs "passou ”" — campo nao opcional.
type PreferencesPatch struct {
	Name                     *string `json:"name,omitempty"`
	DailySummaryTime         *string `json:"daily_summary_time,omitempty"`
	WeeklySummaryDay         *string `json:"weekly_summary_day,omitempty"`
	WeeklySummaryTime        *string `json:"weekly_summary_time,omitempty"`
	ReminderBefore           *string `json:"reminder_before,omitempty"`
	AutoConfirmTimeout       *string `json:"auto_confirm_timeout,omitempty"`
	InactivityThresholdHours *int    `json:"inactivity_threshold_hours,omitempty"`
}

// DependentPatch eh o body de PATCH /family/dependents/{id}. Subconjunto
// editavel pelo guardian.
type DependentPatch struct {
	Name                     *string `json:"name,omitempty"`
	Phone                    *string `json:"phone,omitempty"`
	DailySummaryTime         *string `json:"daily_summary_time,omitempty"`
	WeeklySummaryDay         *string `json:"weekly_summary_day,omitempty"`
	WeeklySummaryTime        *string `json:"weekly_summary_time,omitempty"`
	ReminderBefore           *string `json:"reminder_before,omitempty"`
	InactivityThresholdHours *int    `json:"inactivity_threshold_hours,omitempty"`
	// Active liga/desliga a conta do dependente (pausa lembretes/proatividade).
	// Reversivel; nao apaga dados. nil = nao mexe.
	Active *bool `json:"active,omitempty"`
}

// NotifyPatch eh body de PATCH /family/links/{id}/notify.
type NotifyPatch struct {
	OnMedicationMiss *bool `json:"on_medication_miss,omitempty"`
	OnInactivity     *bool `json:"on_inactivity,omitempty"`
	OnSevereSignal   *bool `json:"on_severe_signal,omitempty"`
}

// CreateDependentRequest eh body de POST /family/dependents.
type CreateDependentRequest struct {
	Name         string `json:"name"`
	Phone        string `json:"phone"`
	Relationship string `json:"relationship"`
	Timezone     string `json:"timezone,omitempty"` // futuro — schema nao tem ainda
}

// CreateDependentResponse retorna user + link criados.
type CreateDependentResponse struct {
	User User       `json:"user"`
	Link FamilyLink `json:"link"`
}

// =========================================================================
// Family / medicacao do dependente
// =========================================================================

// MedicationItem eh a forma publica de um medicamento do dependente.
// `Schedule` eh texto humano em PT-BR (ex: "Todos os dias as 08:00 e 20:00").
// CONTRATO ESPELHADO 1:1 PELO FRONTEND — nao renomear campos.
type MedicationItem struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Dose         string `json:"dose"`
	Instructions string `json:"instructions"`
	Schedule     string `json:"schedule"`
	Active       bool   `json:"active"`
	// EndsAt eh a data (YYYY-MM-DD) em que o tratamento termina, quando
	// temporario. nil/omitido = tratamento continuo (sem data de termino).
	// O frontend usa pra mostrar "até DD/MM" e o selo de temporario.
	EndsAt *string `json:"ends_at,omitempty"`
	// ToleranceMinutes: carencia apos o horario antes de avisar a familia.
	ToleranceMinutes int `json:"tolerance_minutes"`
	// LateDosePolicy: orientacao para dose atrasada. Um de: consult_doctor
	// (padrao), skip, take_keep_next, take_recalculate.
	LateDosePolicy string `json:"late_dose_policy"`
	// Campos estruturados do PRIMEIRO schedule, para o form de edicao
	// pre-preencher (o texto humano `Schedule` cobre todos os schedules). Times
	// em "HH:MM"; Frequency "daily"|"weekly"; Days subset mon..sun (weekly).
	Times     []string `json:"times"`
	Frequency string   `json:"frequency"`
	Days      []string `json:"days,omitempty"`
}

// MedicationsResponse eh o payload de GET /family/dependents/{id}/medications.
type MedicationsResponse struct {
	Medications []MedicationItem `json:"medications"`
}

// CreateMedicationRequest eh o body de POST /family/dependents/{id}/medications.
// frequency: "daily" (ignora Days) | "weekly" (usa Days, subset de mon..sun).
// times: 1-6 horarios no formato HH:MM.
type CreateMedicationRequest struct {
	Name         string   `json:"name"`
	Dose         string   `json:"dose"`
	Instructions string   `json:"instructions"`
	Times        []string `json:"times"`
	Frequency    string   `json:"frequency"`
	Days         []string `json:"days,omitempty"`
	// Duration eh opcional. nil = tratamento continuo (sem data de termino).
	Duration *MedicationDuration `json:"duration,omitempty"`
	// ToleranceMinutes: carencia (min) apos o horario antes de avisar a
	// familia. 0/omitido = default do backend (30). Configurado pelo responsavel.
	ToleranceMinutes int `json:"tolerance_minutes,omitempty"`
	// LateDosePolicy: orientacao para dose atrasada. Vazio = consult_doctor.
	// Aceita: consult_doctor, skip, take_keep_next, take_recalculate.
	LateDosePolicy string `json:"late_dose_policy,omitempty"`
}

// MedicationDuration descreve por quanto tempo o tratamento dura. O backend
// resolve isto numa data de termino (end_date do schedule):
//   - kind="continuous": sem termino (Count/Unit/Until ignorados).
//   - kind="period":     termina em hoje + Count*Unit (unit: days|weeks|months).
//   - kind="until":      termina na data Until (YYYY-MM-DD).
//
// Coletar tanto periodo relativo ("por 3 semanas") quanto data absoluta
// ("até 15/06") cobre as duas formas naturais de prescricao temporaria.
type MedicationDuration struct {
	Kind  string `json:"kind"`
	Count int    `json:"count,omitempty"`
	Unit  string `json:"unit,omitempty"`
	Until string `json:"until,omitempty"`
}

// SnapshotPoint eh um ponto da timeline. Confidence < 3 ainda eh retornado
// — o frontend decide visualmente como mostrar (low confidence styling).
type SnapshotPoint struct {
	Date          string `json:"date"` // YYYY-MM-DD
	Humor         int    `json:"humor"`
	Energia       int    `json:"energia"`
	Sociabilidade int    `json:"sociabilidade"`
	Autocuidado   int    `json:"autocuidado"`
	Confidence    int    `json:"confidence"`
}

// TimelineResponse eh o payload de GET /family/dependents/{id}/timeline.
type TimelineResponse struct {
	Dependent DependentRef    `json:"dependent"`
	Days      int             `json:"days"`
	Snapshots []SnapshotPoint `json:"snapshots"`
}

// DependentRef eh a forma compacta do dependente em respostas que ja
// carregam mais payload (timeline, status).
type DependentRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// StatusResponse eh o payload de GET /family/dependents/{id}/status. Reflete
// o DependentStatusReport do main package, mas sem expor structs internos
// (synthesis.*) — facilita evoluir o backend sem quebrar o frontend.
type StatusResponse struct {
	Dependent         DependentRef     `json:"dependent"`
	Days              int              `json:"days"`
	DaysSinceLastTalk int              `json:"days_since_last_talk"`
	LastUserMessageAt *time.Time       `json:"last_user_message_at,omitempty"`
	Medication        MedicationStats  `json:"medication"`
	ProactiveAttempts ProactiveStats   `json:"proactive_attempts"`
	AlertsOpen        []AlertSummary   `json:"alerts_open"`
	Snapshots         []SnapshotPoint  `json:"snapshots"`
	Synthesis         SynthesisSummary `json:"synthesis"`
	// SynthesisAvailable=false quando ainda nao ha sintese persistida (idoso
	// novo). O frontend mostra "sendo preparada" em vez do texto placeholder.
	SynthesisAvailable bool       `json:"synthesis_available"`
	// SynthesisGeneratedAt eh quando a sintese servida foi gerada (nil se nao
	// ha sintese ainda). Frontend pode exibir "atualizada há X".
	SynthesisGeneratedAt *time.Time `json:"synthesis_generated_at,omitempty"`
}

// MedicationStats eh subset publico do synthesis.MedicationStats.
type MedicationStats struct {
	Scheduled     int     `json:"scheduled"`
	Taken         int     `json:"taken"`
	Missed        int     `json:"missed"`
	Skipped       int     `json:"skipped"`
	Pending       int     `json:"pending"`
	AdherenceFrac float64 `json:"adherence_frac"`
}

// ProactiveStats eh subset publico de ProactiveAttemptsStats.
type ProactiveStats struct {
	Last7d        int        `json:"last_7d"`
	LastAttemptAt *time.Time `json:"last_attempt_at,omitempty"`
	LastAcked     bool       `json:"last_acked"`
}

// AlertSummary eh subset publico de FamilyAlert (sem message: privacidade).
type AlertSummary struct {
	ID         int64     `json:"id"`
	PolicyName string    `json:"policy_name"`
	Severity   string    `json:"severity"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
}

// SynthesisSummary eh subset publico de synthesis.ReportOutput.
type SynthesisSummary struct {
	Tendencia               string   `json:"tendencia"`
	Resumo                  string   `json:"resumo"`
	NivelPreocupacao        string   `json:"nivel_preocupacao"`
	Comparacao              string   `json:"comparacao,omitempty"`
	PontoDeAtencao          string   `json:"ponto_de_atencao,omitempty"`
	RecomendacoesCarinhosas []string `json:"recomendacoes_carinhosas"`
}

// =========================================================================
// Me / agenda (GET /api/v1/me/agenda)
// =========================================================================

// AgendaResponse eh o payload de GET /api/v1/me/agenda. Visao factual da
// agenda do proprio usuario logado. CONTRATO ESPELHADO 1:1 PELO FRONTEND —
// nao renomear campos.
type AgendaResponse struct {
	GoogleConnected bool           `json:"google_connected"`
	Upcoming        []AgendaEvent  `json:"upcoming"`
	RecentActivity  []ActivityItem `json:"recent_activity"`
}

// AgendaEvent eh um evento futuro do calendario do usuario. End pode ser nil
// (evento sem fim explicito). AllDay marca eventos de dia inteiro.
type AgendaEvent struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Start    time.Time  `json:"start"`
	End      *time.Time `json:"end"`
	AllDay   bool       `json:"all_day"`
	Location string     `json:"location"`
}

// ActivityItem eh uma entrada recente do action_log do usuario. Label eh a
// descricao PT-BR amigavel da acao.
type ActivityItem struct {
	Action string    `json:"action"`
	Label  string    `json:"label"`
	At     time.Time `json:"at"`
}

// ActivityResponse eh o payload de GET /api/v1/me/activity. Historico
// completo (limitado) das acoes relevantes do usuario. CONTRATO ESPELHADO 1:1
// PELO FRONTEND — nao renomear campos.
type ActivityResponse struct {
	Items []ActivityItem `json:"items"`
}

// =========================================================================
// Me / insights (GET /api/v1/me/insights)
// =========================================================================

// InsightsResponse eh o payload de GET /api/v1/me/insights. Insights de IA
// sobre o uso da agenda do proprio usuario. CONTRATO ESPELHADO 1:1 PELO
// FRONTEND — nao renomear campos.
type InsightsResponse struct {
	GeneratedAt time.Time     `json:"generated_at"`
	PeriodDays  int           `json:"period_days"`
	Available   bool          `json:"available"`
	Summary     string        `json:"summary"`
	Insights    []InsightItem `json:"insights"`
	// Pending=true quando ainda nao ha insights persistidos e a geracao foi
	// disparada em background (primeiro acesso). O frontend mostra "preparando"
	// e da auto-refresh. NAO eh persistido (so existe na resposta placeholder).
	Pending bool `json:"pending,omitempty"`
}

// InsightItem eh um insight individual. Kind ∈ pattern|health|social|productivity|other.
type InsightItem struct {
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Kind   string `json:"kind"`
}

// =========================================================================
// Me / profile-facts (GET /api/v1/me/profile-facts)
// =========================================================================

// ProfileFactsResponse eh o payload de GET /api/v1/me/profile-facts —
// "o que o Zello sabe sobre voce". CONTRATO ESPELHADO 1:1 PELO FRONTEND.
type ProfileFactsResponse struct {
	Available bool           `json:"available"`
	Relations []RelationFact `json:"relations"`
	People    []PersonFact   `json:"people"`
	Trips     []TripFact     `json:"trips"`
}

// RelationFact eh um vinculo familiar/relacao do usuario. Kind ∈ dependent|guardian|memory.
type RelationFact struct {
	Name     string `json:"name"`
	Relation string `json:"relation"`
	Kind     string `json:"kind"`
}

// PersonFact eh uma pessoa que o Zello conhece do contexto social do usuario.
type PersonFact struct {
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// TripFact eh uma viagem (passada recente ou futura) do usuario.
type TripFact struct {
	Label       string `json:"label"`
	Destination string `json:"destination"`
	Start       string `json:"start"` // YYYY-MM-DD
	End         string `json:"end"`   // YYYY-MM-DD
}
