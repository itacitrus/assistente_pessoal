package api

import (
	"context"
	"errors"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// Store eh a fronteira de persistencia que o api package precisa. O main
// package implementa via adapter (api_adapter.go) que delega pra *DB,
// AuditLog, e callbacks de envio (WhatsApp). Manter Store thin permite
// mockar facil em testes — handlers_test.go usa fakeStore proprio.
//
// Convencoes:
//   - Todos os time.Time retornam UTC.
//   - User.PhoneNumber sempre apenas digitos com prefixo 55.
//   - Erros opacos: ErrNotFound pra "nao existe", ErrConflict pra "ja existe",
//     o resto eh erro generico (caller responde 500).
type Store interface {
	// Auth ----------------------------------------------------------------
	// GetUserByPhone retorna o user OU ErrNotFound. Phone normalizado.
	GetUserByPhone(ctx context.Context, phone string) (*User, error)
	GetUserByID(ctx context.Context, id int64) (*User, error)

	// Sessions ------------------------------------------------------------
	CreatePendingSession(ctx context.Context, userID int64, ip, userAgent string) (sessionID int64, plaintext string, err error)
	ActivateSession(ctx context.Context, plaintext string) (userID int64, sessionID int64, err error)
	// GetActiveSessionByToken retorna o id da sessao, o dono real (userID) e
	// o usuario impersonado (impersonatedUserID; 0 = nenhum). O middleware so
	// honra a impersonacao se o dono real for admin.
	GetActiveSessionByToken(ctx context.Context, plaintext string) (sessionID, userID, impersonatedUserID int64, err error)
	TouchSession(ctx context.Context, sessionID int64) error
	RevokeSession(ctx context.Context, sessionID int64) error
	// SetSessionImpersonation grava o alvo de "ver como" na sessao (admin).
	// targetUserID == 0 limpa a impersonacao ("sair da visao").
	SetSessionImpersonation(ctx context.Context, sessionID, targetUserID int64) error

	// Rate limit ----------------------------------------------------------
	CountRecentLoginAttempts(ctx context.Context, phone string, window time.Duration) (int, error)
	CountRecentLoginAttemptsByIP(ctx context.Context, ip string, window time.Duration) (int, error)
	RecordLoginAttempt(ctx context.Context, phone, ip string) error

	// User update ---------------------------------------------------------
	UpdateUserPreferences(ctx context.Context, userID int64, p PreferencesPatch) (*User, error)

	// Admin ---------------------------------------------------------------
	// SearchUsers busca usuarios por nome ou telefone (match parcial). Query
	// vazia retorna os mais recentes. So a tela de admin usa — gate de
	// privilegio fica no handler.
	SearchUsers(ctx context.Context, query string, limit int) ([]User, error)

	// Google Calendar -----------------------------------------------------
	// GoogleConnectURL gera a URL de consentimento do Google Calendar para
	// userID, embutindo um state opaco de uso unico vinculado a esse user.
	// O titular usa pra redirecionar o proprio navegador; o guardiao usa pra
	// montar o link que o Zello envia ao dependente por WhatsApp. Retorna erro
	// se o cliente de calendario nao estiver configurado.
	GoogleConnectURL(ctx context.Context, userID int64) (string, error)

	// Family --------------------------------------------------------------
	CreateDependent(ctx context.Context, guardianID int64, req CreateDependentRequest) (*User, *FamilyLink, error)
	ListDependents(ctx context.Context, guardianID int64) ([]DependentSummary, error)
	UpdateDependent(ctx context.Context, guardianID, dependentID int64, p DependentPatch) (*User, error)
	// UnlinkDependent remove o vinculo guardiao->dependente (reversivel: o
	// idoso e seus dados permanecem; basta revincular). Valida IsGuardianOf.
	UnlinkDependent(ctx context.Context, guardianID, dependentID int64) error
	UpdateNotifyPrefs(ctx context.Context, guardianID, linkID int64, p NotifyPatch) (*FamilyLink, error)
	GetFamilyLink(ctx context.Context, linkID int64) (*FamilyLink, error)
	IsGuardianOf(ctx context.Context, guardianID, dependentID int64) (bool, error)
	GetDependentConsent(ctx context.Context, guardianID, dependentID int64) (string, error)

	// Status & timeline ---------------------------------------------------
	BuildDependentStatus(ctx context.Context, guardianID, dependentID int64, days int) (*StatusResponse, error)
	GetTimeline(ctx context.Context, dependentID int64, days int) ([]SnapshotPoint, error)
	// ReviewDependentAlert marca um alerta como revisado pelo responsavel
	// (status->acknowledged, grava reviewer/nota). Valida IsGuardianOf e que o
	// alerta pertence ao dependente. Retorna (false) se nada casou (alerta
	// inexistente, de outro dependente, ou ja revisado).
	ReviewDependentAlert(ctx context.Context, guardianID, dependentID, alertID int64, note string) (bool, error)

	// Medicacao do dependente ---------------------------------------------
	// Todas validam IsGuardianOf(guardianID, dependentID) internamente e
	// retornam ErrNotFound quando o guardiao nao eh autorizado ou o
	// medicamento nao pertence ao dependente.
	ListDependentMedications(ctx context.Context, guardianID, dependentID int64) ([]MedicationItem, error)
	CreateDependentMedication(ctx context.Context, guardianID, dependentID int64, in CreateMedicationRequest) (*MedicationItem, error)
	UpdateDependentMedication(ctx context.Context, guardianID, dependentID, medID int64, in CreateMedicationRequest) (*MedicationItem, error)
	DeactivateDependentMedication(ctx context.Context, guardianID, dependentID, medID int64) error
	// ListDependentIntakes devolve o historico de tomadas do dependente nos
	// ultimos `days` dias. medID != 0 filtra um unico medicamento (validado como
	// pertencente ao dependente). Ordenado por scheduled_at desc.
	ListDependentIntakes(ctx context.Context, guardianID, dependentID int64, days int, medID int64) ([]IntakeEntry, error)

	// Medicacao do proprio usuario (titular). Mesmo motor de lembrete/escalacao
	// dos dependentes; dono == criador. Sem checagem de guardiao — eh o proprio
	// usuario gerenciando os remedios dele.
	ListMyMedications(ctx context.Context, userID int64) ([]MedicationItem, error)
	CreateMyMedication(ctx context.Context, userID int64, in CreateMedicationRequest) (*MedicationItem, error)
	UpdateMyMedication(ctx context.Context, userID, medID int64, in CreateMedicationRequest) (*MedicationItem, error)
	DeactivateMyMedication(ctx context.Context, userID, medID int64) error
	// ListMyIntakes: historico de tomadas do proprio titular nos ultimos `days`
	// dias. medID != 0 filtra um medicamento (validado como do proprio user).
	ListMyIntakes(ctx context.Context, userID int64, days int, medID int64) ([]IntakeEntry, error)

	// Me / agenda ----------------------------------------------------------
	// UpcomingEvents le o Google Calendar do proprio usuario (proximos 14d,
	// ordenado por start asc). Retorna lista vazia se o usuario nao tem
	// Google conectado — nunca erro por isso.
	UpcomingEvents(ctx context.Context, userID int64) ([]AgendaEvent, error)
	// EventsInRange le os eventos do Google Calendar no intervalo [from, to) —
	// usado pela visao de calendario mensal navegavel. Lista vazia se sem
	// Google conectado.
	EventsInRange(ctx context.Context, userID int64, from, to time.Time) ([]AgendaEvent, error)
	// RecentActivity le as ultimas `limit` entradas RELEVANTES (allowlist
	// IsRelevantActivity) do action_log do usuario, mais recentes primeiro.
	RecentActivity(ctx context.Context, userID int64, limit int) ([]ActivityItem, error)
	// ActivityHistory le o historico completo (ate `limit`) das acoes
	// relevantes do usuario, mais recentes primeiro. Mesmo filtro de
	// RecentActivity, sem teto fixo de 8.
	ActivityHistory(ctx context.Context, userID int64, limit int) ([]ActivityItem, error)
	// AgendaInsightsData monta o input do sub-agente de insights lendo
	// calendar (passado + futuro) + action_log agregado por tipo de acao.
	AgendaInsightsData(ctx context.Context, userID int64, days int) (synthesis.AgendaInsightsInput, error)
	// GetUserInsights le os insights de agenda PERSISTIDOS (Sonnet) de
	// (userID, days). Retorna ErrNotFound quando ainda nao ha geracao.
	GetUserInsights(ctx context.Context, userID int64, days int) (*InsightsResponse, error)
	// SaveUserInsights grava os insights persistidos. Chamado pelo regen
	// assincrono — nunca no caminho da requisicao.
	SaveUserInsights(ctx context.Context, userID int64, days int, resp *InsightsResponse) error
	// ProfileFacts retorna os fatos que o Zello conhece do usuario:
	// relacoes (dependentes/guardioes/memorias), pessoas do contexto social e
	// viagens. Le o DB direto. available=false quando tudo vazio.
	ProfileFacts(ctx context.Context, userID int64) (ProfileFactsResponse, error)

	// CreatePersonFact grava uma pessoa/relacao na vida do usuario como memoria
	// (category derivada do tipo, key = nome, value = detalhe). Retorna
	// ErrConflict se ja existir uma entrada com o mesmo (category, key).
	CreatePersonFact(ctx context.Context, userID int64, in PersonFactRequest) error
	// UpdatePersonFact edita uma pessoa/relacao existente, identificada por
	// OriginalCategory+OriginalKey. Se o nome (e portanto a key) ou o tipo (a
	// category) mudarem, a memoria antiga eh removida e a nova criada
	// atomicamente. ErrNotFound se a original nao existir; ErrConflict se o
	// novo (category, key) colidir com outra entrada.
	UpdatePersonFact(ctx context.Context, userID int64, in PersonFactRequest) error
	// DeletePersonFact remove a memoria (category, key) do usuario.
	DeletePersonFact(ctx context.Context, userID int64, category, key string) error

	// Catalogo de medicamentos -------------------------------------------
	// ResolveDrug busca no catalogo (ANVISA/CMED) ate `limit` apresentacoes
	// que melhor correspondem a `query`, com correcao fuzzy/fonetica. Usado
	// pelo autocomplete do cadastro de remedio. query vazia ou catalogo nao
	// populado -> lista vazia (sem erro).
	ResolveDrug(ctx context.Context, query string, limit int) ([]DrugMatch, error)

	// Audit ---------------------------------------------------------------
	Audit(ctx context.Context, userID int64, action, target, details string)

	// SendMagicLink eh um callback que dispara mensagem WhatsApp com a URL
	// "{webBaseURL}/auth/verify?token=<plaintext>". A URL completa eh
	// montada pelo api package — store recebe so o texto pronto.
	SendMagicLink(ctx context.Context, phone, message string) error

	// SendWhatsApp envia uma mensagem WhatsApp generica para `phone` (apenas
	// digitos, prefixo 55). Usado para mensagens transacionais que nao sao
	// magic link — ex: boas-vindas ao dependente recem-criado. O envio eh
	// best-effort do ponto de vista do caller: falha nao deve abortar o fluxo
	// de negocio que a originou.
	SendWhatsApp(ctx context.Context, phone, message string) error
}

// Store-level sentinels. Adapter mapeia erros internos de *DB pra estes.
var (
	ErrNotFound       = errors.New("api: not found")
	ErrConflict       = errors.New("api: conflict")
	ErrSessionInvalid = errors.New("api: session invalid")
	ErrSessionExpired = errors.New("api: session expired")
	ErrConsentRevoked = errors.New("api: consent revoked")
	ErrValidation     = errors.New("api: validation failed")
	// ErrMedicationDuplicate: ja existe um medicamento ativo igual (mesmo nome,
	// dose e horario). Handlers mapeiam pra 409.
	ErrMedicationDuplicate = errors.New("api: duplicate medication")
)
