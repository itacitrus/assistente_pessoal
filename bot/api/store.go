package api

import (
	"context"
	"errors"
	"time"
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

	// Audit ---------------------------------------------------------------
	Audit(ctx context.Context, userID int64, action, target, details string)

	// SendMagicLink eh um callback que dispara mensagem WhatsApp com a URL
	// "{webBaseURL}/auth/verify?token=<plaintext>". A URL completa eh
	// montada pelo api package — store recebe so o texto pronto.
	SendMagicLink(ctx context.Context, phone, message string) error
}

// Store-level sentinels. Adapter mapeia erros internos de *DB pra estes.
var (
	ErrNotFound        = errors.New("api: not found")
	ErrConflict        = errors.New("api: conflict")
	ErrSessionInvalid  = errors.New("api: session invalid")
	ErrSessionExpired  = errors.New("api: session expired")
	ErrConsentRevoked  = errors.New("api: consent revoked")
	ErrValidation      = errors.New("api: validation failed")
)
