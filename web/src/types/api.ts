/**
 * Tipos compartilhados com a API REST do bot Go (`bot/api/types.go`).
 *
 * Backend e a fonte da verdade — os shapes aqui sao espelho 1:1 do JSON
 * que o backend serializa. Mudancas exigem coordenacao com `bot/api/types.go`.
 *
 * Convencoes:
 * - snake_case identico ao JSON do backend.
 * - Onde o backend usa nested struct (ex: Notify dentro de FamilyLink),
 *   manter o objeto aninhado em TS.
 * - Campos com `time.Time` no Go viram `string` (ISO8601).
 * - Campos com `*time.Time` (ponteiro) viram `string | undefined`.
 */

// ---- Enums fechados ----

export type UserType = "comum" | "responsavel" | "idoso";

/**
 * Dia da semana em ingles minusculo. Backend valida via mapa exato
 * `validWeeklyDay` em api/validation.go (sunday..saturday).
 */
export type WeekDay =
  | "sunday"
  | "monday"
  | "tuesday"
  | "wednesday"
  | "thursday"
  | "friday"
  | "saturday";

export type ReminderBefore = "15m" | "30m" | "1h" | "2h" | "4h";

export type AutoConfirmTimeout = "30m" | "1h" | "2h" | "4h" | "never";

/** Severidade de alerta familiar — espelha api.AlertSummary.Severity. */
export type AlertSeverity = "info" | "warn" | "critical";

/** Status de alerta familiar — espelha api.AlertSummary.Status. */
export type AlertStatus = "open" | "acked" | "resolved";

/**
 * Tendencia psicologica — espelha synthesis.ReportOutput.Tendencia,
 * exposto via api.SynthesisSummary.Tendencia.
 */
export type Tendencia =
  | "melhorando"
  | "estavel"
  | "piorando"
  | "instavel"
  | "indeterminado";

/**
 * Nivel de preocupacao — espelha synthesis.ReportOutput.NivelPreocupacao.
 */
export type NivelPreocupacao =
  | "tranquilo"
  | "atencao"
  | "atencao_alta"
  | "indeterminado";

/**
 * Status de consentimento do dependente — espelha link.consent_status.
 * Apenas "active" libera os endpoints /status e /timeline.
 */
export type ConsentStatus = "active" | "revoked";

// ---- Recursos ----

/** Espelha api.User. */
export interface User {
  id: number;
  phone_number: string; // 12-13 digitos, sem mascara
  name: string;
  /** Backend usa "type" — nao "user_type". */
  type: UserType;
  daily_summary_time: string; // "HH:MM"
  weekly_summary_day: WeekDay;
  weekly_summary_time: string; // "HH:MM"
  reminder_before: ReminderBefore;
  auto_confirm_timeout: AutoConfirmTimeout;
  inactivity_threshold_hours: number; // 4..168
  google_connected: boolean;
  is_active: boolean;
  created_at: string; // ISO8601
}

/** Espelha api.Notify (subset das flags por canal). */
export interface NotifyPrefs {
  on_medication_miss: boolean;
  on_inactivity: boolean;
  on_severe_signal: boolean;
}

/** Espelha api.FamilyLink. */
export interface FamilyLink {
  id: number;
  guardian_id: number;
  dependent_id: number;
  /** Backend usa "relationship" — nao "relation". */
  relationship: string;
  notify: NotifyPrefs;
  consent_status: ConsentStatus;
  created_at: string;
}

/** Espelha api.DependentSummary — item da lista de dependentes. */
export interface DependentEntry {
  user: User;
  link: FamilyLink;
}

/** Espelha api.DependentRef — forma compacta usada em status/timeline. */
export interface DependentRef {
  id: number;
  name: string;
}

// ---- Status / dashboard (Fase 5) ----

/**
 * Espelha api.SnapshotPoint vindo do backend.
 *
 * Convencao do backend: 0 representa "sem dado" pra Humor/Energia/Sociabilidade
 * /Autocuidado (vide Fase 5 §3 do plano). O frontend converte 0 -> null na
 * borda do client (`normalizeSnapshot` em `lib/api/family.ts`) para preservar
 * a ergonomia de "sem dado" diferenciado de 1 (escala 1..5).
 */
export interface SnapshotPointRaw {
  date: string; // YYYY-MM-DD
  humor: number;
  energia: number;
  sociabilidade: number;
  autocuidado: number;
  confidence: number;
}

/**
 * SnapshotPoint normalizado para o frontend: scores 0 do backend viram null.
 * O componente PsychTimeline consome esta forma.
 */
export interface SnapshotPoint {
  date: string; // YYYY-MM-DD
  humor: number | null; // 1..5 ou null = sem dado
  energia: number | null;
  sociabilidade: number | null;
  autocuidado: number | null;
  confidence: number | null; // 1..5 ou null (0 do backend = sem dado)
}

/** Espelha api.MedicationStats. */
export interface MedicationStats {
  scheduled: number;
  taken: number;
  missed: number;
  skipped: number;
  pending: number;
  adherence_frac: number; // 0..1
}

/** Espelha api.ProactiveStats. */
export interface ProactiveStats {
  last_7d: number;
  last_attempt_at?: string; // ISO8601
  last_acked: boolean;
}

/** Espelha api.AlertSummary — sem campo "message" por privacidade. */
export interface AlertSummary {
  id: number;
  policy_name: string;
  severity: AlertSeverity;
  status: AlertStatus;
  created_at: string;
}

/** Espelha api.SynthesisSummary. */
export interface SynthesisSummary {
  tendencia: Tendencia;
  resumo: string;
  nivel_preocupacao: NivelPreocupacao;
  comparacao?: string;
  /** Singular no backend — uma string opcional, nao array. */
  ponto_de_atencao?: string;
  recomendacoes_carinhosas?: string[];
}

/**
 * Forma "raw" da resposta de GET /family/dependents/{id}/status, exatamente
 * como o backend serializa (snapshots com 0=sem dado). O client normaliza
 * para `DependentStatus` antes de devolver pros consumidores.
 */
export interface DependentStatusRaw {
  dependent: DependentRef;
  days: number;
  days_since_last_talk: number;
  last_user_message_at?: string;
  medication: MedicationStats;
  proactive_attempts: ProactiveStats;
  alerts_open: AlertSummary[];
  snapshots: SnapshotPointRaw[];
  synthesis: SynthesisSummary;
}

/**
 * Forma normalizada de DependentStatusRaw — snapshots com 0->null nos
 * scores psicologicos.
 */
export interface DependentStatus {
  dependent: DependentRef;
  days: number;
  days_since_last_talk: number;
  last_user_message_at?: string;
  medication: MedicationStats;
  proactive_attempts: ProactiveStats;
  alerts_open: AlertSummary[];
  snapshots: SnapshotPoint[];
  synthesis: SynthesisSummary;
}

/**
 * Forma "raw" de GET /family/dependents/{id}/timeline (snapshots crus).
 */
export interface DependentTimelineRaw {
  dependent: DependentRef;
  days: number;
  snapshots: SnapshotPointRaw[];
}

/** Forma normalizada de DependentTimelineRaw — 0->null nos scores. */
export interface DependentTimeline {
  dependent: DependentRef;
  days: number;
  snapshots: SnapshotPoint[];
}

// ---- Painel "me" (agenda + insights) ----

/**
 * Espelha api.AgendaEvent — evento futuro vindo do Google Calendar do usuario.
 * `end` e `null` para eventos sem hora de termino; `all_day` marca eventos de
 * dia inteiro (onde a hora deve ser ignorada na renderizacao).
 */
export interface AgendaEvent {
  id: string;
  title: string;
  start: string; // ISO8601
  end: string | null; // ISO8601 ou null
  all_day: boolean;
  location: string;
}

/**
 * Espelha api.ActivityItem — item do feed de atividade recente. `action` e o
 * identificador da acao (ex: "criar_evento"); `label` e o texto ja pronto pra
 * exibicao; `at` e o instante ISO8601 da acao.
 */
export interface ActivityItem {
  action: string;
  label: string;
  at: string; // ISO8601
}

/**
 * Espelha api.AgendaResponse — GET /api/v1/me/agenda.
 * Quando `google_connected` e false, `upcoming` vem vazio.
 */
export interface AgendaResponse {
  google_connected: boolean;
  upcoming: AgendaEvent[];
  recent_activity: ActivityItem[];
}

/** Tipo de insight — define o icone e o tom do card de insight. */
export type InsightKind =
  | "pattern"
  | "health"
  | "social"
  | "productivity"
  | "other";

/**
 * Espelha api.Insight — observacao individual gerada por IA sobre o padrao
 * de uso do usuario.
 */
export interface Insight {
  title: string;
  detail: string;
  kind: InsightKind;
}

/**
 * Espelha api.InsightsResponse — GET /api/v1/me/insights?days=30.
 * Quando `available` e false, `summary`/`insights` podem vir vazios e a UI
 * mostra um estado calmo de "ainda aprendendo".
 */
export interface InsightsResponse {
  generated_at: string; // ISO8601
  period_days: number;
  available: boolean;
  summary: string;
  insights: Insight[];
}

// ---- Bodies dos requests ----

/** Body de POST /api/v1/auth/request-link. */
export interface RequestLinkBody {
  phone: string;
}

/** Body de POST /api/v1/auth/verify. */
export interface VerifyTokenBody {
  token: string;
}

/** Body de PATCH /api/v1/users/me — espelha api.PreferencesPatch. */
export interface UpdateMeBody {
  name?: string;
  daily_summary_time?: string;
  weekly_summary_day?: WeekDay;
  weekly_summary_time?: string;
  reminder_before?: ReminderBefore;
  auto_confirm_timeout?: AutoConfirmTimeout;
  inactivity_threshold_hours?: number;
}

/** Body de POST /api/v1/family/dependents — espelha api.CreateDependentRequest. */
export interface CreateDependentBody {
  name: string;
  phone: string; // pode vir com mascara; backend normaliza
  relationship: string;
  timezone?: string;
}

/** Body de PATCH /api/v1/family/dependents/{id} — espelha api.DependentPatch. */
export interface UpdateDependentBody {
  name?: string;
  daily_summary_time?: string;
  weekly_summary_day?: WeekDay;
  weekly_summary_time?: string;
  reminder_before?: ReminderBefore;
  inactivity_threshold_hours?: number;
}

/** Body de PATCH /api/v1/family/links/{id}/notify — espelha api.NotifyPatch. */
export interface UpdateLinkNotifyBody {
  on_medication_miss?: boolean;
  on_inactivity?: boolean;
  on_severe_signal?: boolean;
}

// ---- Envelope de erro ----

export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
  };
}
