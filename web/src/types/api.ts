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

  // Campos extras presentes somente no payload de GET /api/v1/me (MeResponse).
  // `is_admin` reflete o DONO REAL da sessao (segue true durante "ver como").
  is_admin?: boolean;
  // `viewing_as` != null quando o admin esta visualizando o painel de outra
  // pessoa via impersonacao; identifica de quem eh o painel exibido.
  viewing_as?: ViewingAs | null;
}

/** Identifica o usuario-alvo de uma impersonacao em curso (MeResponse). */
export interface ViewingAs {
  id: number;
  name: string;
  phone_number: string;
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

/** Status de uma ocorrência de dose. Espelha api.IntakeEntry.Status. */
export type IntakeStatus =
  | "pending"
  | "taken"
  | "skipped"
  | "missed"
  | "escalated"
  | "unknown";

/** Espelha api.IntakeEntry — uma ocorrência de dose no histórico de tomadas. */
export interface IntakeEntry {
  medication_id: number;
  medication_name: string;
  dose: string;
  scheduled_at: string; // ISO8601 (UTC)
  status: IntakeStatus;
  confirmed_at?: string; // ISO8601, presente quando tomada/registrada
}

/** Espelha api.IntakesResponse — payload de GET .../intakes. */
export interface IntakesResponse {
  intakes: IntakeEntry[];
  days: number;
}

/** Espelha api.MedicationStats. */
export interface MedicationStats {
  scheduled: number;
  taken: number;
  missed: number;
  skipped: number;
  pending: number;
  /** Doses de remédios que não exigem confirmação e não foram confirmadas —
   *  "não sei". Ficam FORA do denominador da aderência. */
  unknown: number;
  adherence_frac: number; // 0..1
}

/** Espelha api.ProactiveStats. */
export interface ProactiveStats {
  last_7d: number;
  last_attempt_at?: string; // ISO8601
  last_acked: boolean;
}

/**
 * Espelha api.AlertSummary. Nunca traz a conversa crua; para sinais
 * preocupantes traz `summary` (o que foi observado) e `recommended` (sugestão)
 * — resumos do LLM, o mesmo que já chega ao responsável por WhatsApp.
 */
export interface AlertSummary {
  id: number;
  policy_name: string;
  severity: AlertSeverity;
  status: AlertStatus;
  created_at: string;
  summary?: string;
  recommended?: string;
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
  synthesis_available?: boolean;
  synthesis_generated_at?: string;
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
  /** false quando a síntese ainda está sendo gerada (idoso novo). */
  synthesis_available?: boolean;
  /** ISO de quando a síntese servida foi gerada. */
  synthesis_generated_at?: string;
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

/**
 * Espelha api.AgendaEventsResponse — GET /api/v1/me/agenda/events?from&to.
 * Eventos do intervalo pedido, para a visão de calendário mensal navegável.
 */
export interface AgendaEventsResponse {
  google_connected: boolean;
  events: AgendaEvent[];
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
  /** true quando os insights ainda estão sendo gerados em background (primeiro
   * acesso). A UI mostra "preparando" e dá auto-refresh. */
  pending?: boolean;
}

// ---- Atividade (historico completo) ----

/**
 * Espelha api.ActivityResponse — GET /api/v1/me/activity?limit=100.
 * Mesmo shape de item do feed do dashboard (`ActivityItem`), porem com a
 * lista completa (ate `limit` eventos relevantes ja filtrados pelo backend).
 */
export interface ActivityResponse {
  items: ActivityItem[];
}

// ---- Fatos de perfil ("o que o Zello sabe sobre voce") ----

/**
 * Tipo de relacao aprendida sobre o usuario. Espelha o campo `kind` de
 * api.RelationFact: dependentes que ele cuida, guardioes que cuidam dele, ou
 * pessoas memorizadas em conversas.
 */
export type RelationKind = "dependent" | "guardian" | "memory";

/** Espelha api.RelationFact — uma pessoa ligada ao usuario por relacao.
 * `editable` true (kind=memory) -> `category`/`key` identificam a memoria crua
 * para edicao/remocao. Vinculos familiares vem editable=false. */
export interface RelationFact {
  name: string;
  relation: string;
  kind: RelationKind;
  editable: boolean;
  category?: string;
  key?: string;
}

/** Espelha api.PersonFact — pessoa citada nas conversas com um detalhe livre.
 * Sempre editavel; `category`/`key` identificam a memoria crua. */
export interface PersonFact {
  name: string;
  detail: string;
  editable: boolean;
  category?: string;
  key?: string;
}

/** Tipo escolhido na UI ao cadastrar/editar uma pessoa na vida. */
export type PersonFactType = "relacao" | "pessoa";

/** Corpo de POST/PATCH /api/v1/me/people. Em edicao, `original_category` e
 * `original_key` identificam a entrada existente. */
export interface PersonFactBody {
  name: string;
  detail: string;
  type: PersonFactType;
  original_category?: string;
  original_key?: string;
}

/** Espelha api.TripFact — uma viagem conhecida do usuario. */
export interface TripFact {
  label: string;
  destination: string;
  start: string; // ISO8601 ou data livre
  end: string; // ISO8601 ou data livre
}

/**
 * Espelha api.ProfileFacts — GET /api/v1/me/profile-facts.
 * Quando `available` e false (ou tudo vazio), a UI mostra um estado calmo de
 * "o Zello vai aprendendo conforme conversam". Todos os arrays podem vir [].
 */
export interface ProfileFacts {
  available: boolean;
  relations: RelationFact[];
  people: PersonFact[];
  trips: TripFact[];
}

// ---- Medicamentos do dependente ----

/**
 * Espelha api.MedicationItem — GET /api/v1/family/dependents/{id}/medications.
 * `schedule` ja vem como texto humano pronto para exibicao (ex: "Todo dia as
 * 08:00 e 20:00"). `active` indica se o lembrete esta ativo.
 */
export interface MedicationItem {
  id: number;
  name: string;
  dose: string;
  instructions: string;
  schedule: string;
  active: boolean;
  /** Data de término (YYYY-MM-DD) quando temporário; ausente = contínuo. */
  ends_at?: string;
  /** Carência (min) após o horário antes de avisar a família em segredo. */
  tolerance_minutes: number;
  /** Orientação para dose atrasada, definida pelo responsável. */
  late_dose_policy: LateDosePolicy;
  /** true = exige confirmação (cobra + escala). false = só lembra; dose não
   *  confirmada vira "não sei". */
  require_confirmation: boolean;
  /** Campos estruturados do primeiro schedule, para o form de edição pré-preencher. */
  times: string[];
  frequency: MedicationFrequency;
  days?: MedicationWeekDay[];
}

/**
 * Orientação do responsável para quando o idoso passa do horário. O bot relata
 * ao idoso deixando claro que é recomendação do responsável, não médica.
 * Espelha bot.LateDosePolicy.
 */
export type LateDosePolicy =
  | "consult_doctor"
  | "skip"
  | "take_keep_next"
  | "take_recalculate";

/** Espelha o envelope de GET .../medications. */
export interface MedicationsResponse {
  medications: MedicationItem[];
}

/** Frequencia de um lembrete de remedio. */
export type MedicationFrequency = "daily" | "weekly";

/**
 * Dia da semana abreviado em ingles minusculo, como o backend espera no body
 * de criacao de medicamento (`days`). Distinto de `WeekDay` (que e por extenso,
 * usado nas preferencias de resumo).
 */
export type MedicationWeekDay =
  | "mon"
  | "tue"
  | "wed"
  | "thu"
  | "fri"
  | "sat"
  | "sun";

/**
 * Body de POST /api/v1/family/dependents/{id}/medications.
 * `times`: 1-6 horarios "HH:MM". `days` obrigatorio quando frequency="weekly".
 */
export interface CreateMedicationBody {
  name: string;
  dose: string;
  instructions: string;
  times: string[]; // "HH:MM", 1-6 itens
  frequency: MedicationFrequency;
  days?: MedicationWeekDay[]; // obrigatorio se weekly
  /** Duração do tratamento. Ausente/continuous = contínuo (sem término). */
  duration?: MedicationDuration;
  /** Carência (min) antes de avisar a família. Omitido = 30 (default backend). */
  tolerance_minutes?: number;
  /** Orientação para dose atrasada. Omitido = consult_doctor. */
  late_dose_policy?: LateDosePolicy;
  /** Id da apresentação no catálogo ANVISA/CMED quando o nome foi escolhido no
   *  autocomplete. Omitido = digitado livre. */
  catalog_id?: number;
  /** Exigir confirmação de toma. Omitido = true (default seguro). */
  require_confirmation?: boolean;
}

/** Candidato do catálogo de medicamentos (ANVISA/CMED) para o autocomplete. */
export interface DrugMatch {
  id: number;
  commercial_name: string;
  active_ingredient: string;
  concentration: string;
  presentation: string;
  product_type: string;
  tarja: string;
  confidence: number;
}

export interface DrugSearchResponse {
  matches: DrugMatch[];
}

/** Unidade do período relativo de um tratamento temporário. */
export type MedicationDurationUnit = "days" | "weeks" | "months";

/**
 * Espelha api.MedicationDuration. O backend resolve isto numa data de término:
 * - kind="continuous": sem término.
 * - kind="period": hoje + count*unit (ex: 3 semanas).
 * - kind="until": termina na data `until` (YYYY-MM-DD).
 */
export interface MedicationDuration {
  kind: "continuous" | "period" | "until";
  count?: number;
  unit?: MedicationDurationUnit;
  until?: string; // "YYYY-MM-DD"
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
  /** E.164 BR ("55" + DDD + número), só dígitos. */
  phone?: string;
  daily_summary_time?: string;
  weekly_summary_day?: WeekDay;
  weekly_summary_time?: string;
  reminder_before?: ReminderBefore;
  inactivity_threshold_hours?: number;
  /** Liga/desliga a conta do dependente (pausa lembretes/proatividade). */
  active?: boolean;
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
