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
	GetActiveSessionByToken(ctx context.Context, plaintext string) (sessionID, userID int64, err error)
	TouchSession(ctx context.Context, sessionID int64) error
	RevokeSession(ctx context.Context, sessionID int64) error

	// Rate limit ----------------------------------------------------------
	CountRecentLoginAttempts(ctx context.Context, phone string, window time.Duration) (int, error)
	CountRecentLoginAttemptsByIP(ctx context.Context, ip string, window time.Duration) (int, error)
	RecordLoginAttempt(ctx context.Context, phone, ip string) error

	// User update ---------------------------------------------------------
	UpdateUserPreferences(ctx context.Context, userID int64, p PreferencesPatch) (*User, error)

	// Family --------------------------------------------------------------
	CreateDependent(ctx context.Context, guardianID int64, req CreateDependentRequest) (*User, *FamilyLink, error)
	ListDependents(ctx context.Context, guardianID int64) ([]DependentSummary, error)
	UpdateDependent(ctx context.Context, guardianID, dependentID int64, p DependentPatch) (*User, error)
	UpdateNotifyPrefs(ctx context.Context, guardianID, linkID int64, p NotifyPatch) (*FamilyLink, error)
	GetFamilyLink(ctx context.Context, linkID int64) (*FamilyLink, error)
	IsGuardianOf(ctx context.Context, guardianID, dependentID int64) (bool, error)
	GetDependentConsent(ctx context.Context, guardianID, dependentID int64) (string, error)

	// Status & timeline ---------------------------------------------------
	BuildDependentStatus(ctx context.Context, guardianID, dependentID int64, days int) (*StatusResponse, error)
	GetTimeline(ctx context.Context, dependentID int64, days int) ([]SnapshotPoint, error)

	// Medicacao do dependente ---------------------------------------------
	// Todas validam IsGuardianOf(guardianID, dependentID) internamente e
	// retornam ErrNotFound quando o guardiao nao eh autorizado ou o
	// medicamento nao pertence ao dependente.
	ListDependentMedications(ctx context.Context, guardianID, dependentID int64) ([]MedicationItem, error)
	CreateDependentMedication(ctx context.Context, guardianID, dependentID int64, in CreateMedicationRequest) (*MedicationItem, error)
	DeactivateDependentMedication(ctx context.Context, guardianID, dependentID, medID int64) error

	// Me / agenda ----------------------------------------------------------
	// UpcomingEvents le o Google Calendar do proprio usuario (proximos 14d,
	// ordenado por start asc). Retorna lista vazia se o usuario nao tem
	// Google conectado — nunca erro por isso.
	UpcomingEvents(ctx context.Context, userID int64) ([]AgendaEvent, error)
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
	// ProfileFacts retorna os fatos que o Zello conhece do usuario:
	// relacoes (dependentes/guardioes/memorias), pessoas do contexto social e
	// viagens. Le o DB direto. available=false quando tudo vazio.
	ProfileFacts(ctx context.Context, userID int64) (ProfileFactsResponse, error)

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
)
