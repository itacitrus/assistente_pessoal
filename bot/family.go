package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// UserType discrimina a persona do usuario. Persona dinamica via system
// prompt switch (ver overview D1). Tipos NAO sao mutuamente exclusivos no
// nivel conceitual — um responsavel tambem tem agenda propria — mas a coluna
// users.type carrega a persona PRIMARIA pra fins de prompt e scheduler.
// Pra responsabilidade familiar, usar family_links (tabela paralela).
type UserType string

const (
	UserTypeComum       UserType = "comum"
	UserTypeIdoso       UserType = "idoso"
	UserTypeResponsavel UserType = "responsavel"
)

// allUserTypes eh a lista canonica de tipos validos. Mantido em sync com
// o CHECK constraint conceitual e com ValidateUserType.
var allUserTypes = []UserType{
	UserTypeComum,
	UserTypeIdoso,
	UserTypeResponsavel,
}

// IsValid retorna true se ut for um dos tipos reconhecidos.
func (ut UserType) IsValid() bool {
	for _, v := range allUserTypes {
		if v == ut {
			return true
		}
	}
	return false
}

// ValidateUserType retorna erro descritivo se t nao for um tipo conhecido.
// Usar antes de qualquer write em users.type.
func ValidateUserType(t UserType) error {
	if !t.IsValid() {
		return fmt.Errorf("invalid user type %q (valid: comum, idoso, responsavel)", t)
	}
	return nil
}

// FamilyNotifyPrefs encapsula as flags de notificacao de um vinculo familiar.
// Default semantico (zero-value): todas false. CUIDADO: o default DO BANCO
// eh true — quem usa este struct para WRITE tem que setar explicitamente.
// Pra construir com defaults do banco, use DefaultFamilyNotifyPrefs().
type FamilyNotifyPrefs struct {
	OnMedicationMiss bool `json:"on_medication_miss"`
	OnInactivity     bool `json:"on_inactivity"`
	OnSevereSignal   bool `json:"on_severe_signal"`
}

// DefaultFamilyNotifyPrefs retorna prefs com todos os canais ligados,
// igualando o DEFAULT do schema.
func DefaultFamilyNotifyPrefs() FamilyNotifyPrefs {
	return FamilyNotifyPrefs{
		OnMedicationMiss: true,
		OnInactivity:     true,
		OnSevereSignal:   true,
	}
}

// FamilyLink representa um vinculo guardian -> dependent.
// Quando retornado por GetDependents/GetGuardians, o campo Other carrega o
// usuario do "outro lado" do vinculo (o dependente, ou o guardiao, conforme
// a query). Isso evita o caller fazer N+1 lookups por nome/telefone.
type FamilyLink struct {
	ID           int64             `json:"id"`
	GuardianID   int64             `json:"guardian_id"`
	DependentID  int64             `json:"dependent_id"`
	Relationship string            `json:"relationship"`
	Notify       FamilyNotifyPrefs `json:"notify"`
	CreatedAt    time.Time         `json:"created_at"`

	// Other eh o usuario do outro lado do vinculo, populado pelos getters
	// de listagem (GetDependents/GetGuardians). Nao populado por
	// IsGuardianOf, LinkFamily etc. Pode ser nil em contextos onde nao foi
	// hidratado.
	Other *User `json:"other,omitempty"`
}

// Sentinels familiares.
var (
	// ErrFamilyLinkNotFound indica que o par (guardian, dependent) nao existe.
	ErrFamilyLinkNotFound = errors.New("family link not found")

	// ErrFamilyLinkSelfLink eh retornado quando guardian_id == dependent_id.
	ErrFamilyLinkSelfLink = errors.New("family link cannot be self-referential")

	// ErrFamilyLinkDuplicate indica violacao do UNIQUE(guardian_id, dependent_id).
	ErrFamilyLinkDuplicate = errors.New("family link already exists")

	// ErrFamilyLinkUserNotFound indica que guardian_id ou dependent_id
	// nao referencia um user existente (defesa em profundidade — SQLite nao
	// valida FKs por default).
	ErrFamilyLinkUserNotFound = errors.New("family link references non-existent user")
)

// scanUserExtras lida com colunas opcionais (NULL) que vivem em users
// alem do conjunto basico. Mantem o codigo dos getters limpo.
// Defensivo: se ut.Valid for false (cenario de race em deploy paralelo
// antes da migracao rodar), assume UserTypeComum.
func scanUserExtras(u *User, ut sql.NullString, lastMsg sql.NullTime) {
	if ut.Valid && ut.String != "" {
		u.Type = UserType(ut.String)
	} else {
		u.Type = UserTypeComum
	}
	if lastMsg.Valid {
		t := lastMsg.Time
		u.LastUserMessageAt = &t
	}
}

// scanUserPhase4 hidrata os campos Fase 4 (companion). Defensivo: se a
// coluna nao existir (banco pre-migracao), os ponteiros sql.NullX virao
// invalidos e os campos do User ficam zero — comportamento equivalente ao
// default ("nunca pausado", "threshold 24h").
func scanUserPhase4(u *User, threshold sql.NullInt64, pausedUntil sql.NullTime) {
	if threshold.Valid && threshold.Int64 > 0 {
		u.InactivityThresholdHours = int(threshold.Int64)
	} else {
		u.InactivityThresholdHours = 24
	}
	if pausedUntil.Valid {
		t := pausedUntil.Time
		u.ProactivePausedUntil = &t
	}
}

// SetUserType atualiza users.type apos validar o valor. Retorna
// ErrUserNotFound se o id nao existe.
func (db *DB) SetUserType(userID int64, t UserType) error {
	if err := ValidateUserType(t); err != nil {
		return err
	}
	res, err := db.conn.Exec(`UPDATE users SET type = ? WHERE id = ?`, string(t), userID)
	if err != nil {
		return fmt.Errorf("set user type: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// MarkUserMessageReceived registra o timestamp da ultima mensagem RECEBIDA
// do usuario (chamada do handler de WhatsApp). Usado por checkInactivity
// na Fase 4. Sempre persiste em UTC.
func (db *DB) MarkUserMessageReceived(userID int64, ts time.Time) error {
	res, err := db.conn.Exec(
		`UPDATE users SET last_user_message_at = ? WHERE id = ?`,
		ts.UTC(), userID,
	)
	if err != nil {
		return fmt.Errorf("mark user message received: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserNotFound
	}
	return nil
}

// LinkFamily cria um vinculo guardian -> dependent. Retorna:
//   - ErrFamilyLinkSelfLink     se guardianID == dependentID
//   - ErrFamilyLinkUserNotFound se algum dos ids nao existe
//   - ErrFamilyLinkDuplicate    se ja existe vinculo com mesmo par
//
// O relationship eh livre ("filha", "esposa", etc); pode ser "" se desconhecido.
// As prefs de notificacao iniciam em DefaultFamilyNotifyPrefs() (todas true).
func (db *DB) LinkFamily(guardianID, dependentID int64, relationship string) (*FamilyLink, error) {
	if guardianID == dependentID {
		return nil, ErrFamilyLinkSelfLink
	}
	// Defesa em profundidade — SQLite nao enforca FK por default.
	if _, err := db.GetUserByID(guardianID); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrFamilyLinkUserNotFound
		}
		return nil, fmt.Errorf("validate guardian: %w", err)
	}
	if _, err := db.GetUserByID(dependentID); err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, ErrFamilyLinkUserNotFound
		}
		return nil, fmt.Errorf("validate dependent: %w", err)
	}

	prefs := DefaultFamilyNotifyPrefs()
	res, err := db.conn.Exec(
		`INSERT INTO family_links
			(guardian_id, dependent_id, relationship,
			 notify_on_medication_miss, notify_on_inactivity, notify_on_severe_signal)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		guardianID, dependentID, relationship,
		prefs.OnMedicationMiss, prefs.OnInactivity, prefs.OnSevereSignal,
	)
	if err != nil {
		// Detecta UNIQUE / CHECK violation por substring — modernc.org/sqlite
		// nao expoe codigos estaveis.
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, ErrFamilyLinkDuplicate
		}
		if strings.Contains(err.Error(), "CHECK") {
			// Caso raro: CHECK constraint pegou self-link que escapou da
			// validacao em Go (improvavel, mas defense in depth).
			return nil, ErrFamilyLinkSelfLink
		}
		return nil, fmt.Errorf("insert family link: %w", err)
	}
	id, _ := res.LastInsertId()
	return &FamilyLink{
		ID:           id,
		GuardianID:   guardianID,
		DependentID:  dependentID,
		Relationship: relationship,
		Notify:       prefs,
		CreatedAt:    time.Now().UTC(),
	}, nil
}

// UnlinkFamily remove o vinculo (guardian, dependent). Retorna
// ErrFamilyLinkNotFound se nao existia. Hard-delete por design (audit_log
// eh fonte primaria de historico — ver §9.5 do plano).
func (db *DB) UnlinkFamily(guardianID, dependentID int64) error {
	res, err := db.conn.Exec(
		`DELETE FROM family_links WHERE guardian_id = ? AND dependent_id = ?`,
		guardianID, dependentID,
	)
	if err != nil {
		return fmt.Errorf("unlink family: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFamilyLinkNotFound
	}
	return nil
}

// GetDependents retorna todos os vinculos onde guardianID eh o responsavel,
// com o User do dependente preenchido em FamilyLink.Other. Ordenado por
// nome do dependente (case-insensitive) pra UX previsivel.
func (db *DB) GetDependents(guardianID int64) ([]FamilyLink, error) {
	rows, err := db.conn.Query(
		`SELECT fl.id, fl.guardian_id, fl.dependent_id, fl.relationship,
				fl.notify_on_medication_miss, fl.notify_on_inactivity, fl.notify_on_severe_signal,
				fl.created_at,
				u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
				u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
				u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at,
				u.type, u.last_user_message_at,
				u.inactivity_threshold_hours, u.proactive_paused_until
		 FROM family_links fl
		 JOIN users u ON u.id = fl.dependent_id
		 WHERE fl.guardian_id = ?
		 ORDER BY LOWER(u.name) ASC`, guardianID,
	)
	if err != nil {
		return nil, fmt.Errorf("query dependents: %w", err)
	}
	defer rows.Close()

	var links []FamilyLink
	for rows.Next() {
		var fl FamilyLink
		var u User
		var ut sql.NullString
		var lastMsg sql.NullTime
		var thresh sql.NullInt64
		var paused sql.NullTime
		if err := rows.Scan(
			&fl.ID, &fl.GuardianID, &fl.DependentID, &fl.Relationship,
			&fl.Notify.OnMedicationMiss, &fl.Notify.OnInactivity, &fl.Notify.OnSevereSignal,
			&fl.CreatedAt,
			&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
			&ut, &lastMsg, &thresh, &paused,
		); err != nil {
			return nil, err
		}
		scanUserExtras(&u, ut, lastMsg)
		scanUserPhase4(&u, thresh, paused)
		fl.Other = &u
		links = append(links, fl)
	}
	return links, rows.Err()
}

// GetGuardians retorna todos os vinculos onde dependentID eh o cuidado,
// com o User do guardiao preenchido em FamilyLink.Other. Ordenado por
// nome do guardiao (case-insensitive).
func (db *DB) GetGuardians(dependentID int64) ([]FamilyLink, error) {
	rows, err := db.conn.Query(
		`SELECT fl.id, fl.guardian_id, fl.dependent_id, fl.relationship,
				fl.notify_on_medication_miss, fl.notify_on_inactivity, fl.notify_on_severe_signal,
				fl.created_at,
				u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
				u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
				u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at,
				u.type, u.last_user_message_at,
				u.inactivity_threshold_hours, u.proactive_paused_until
		 FROM family_links fl
		 JOIN users u ON u.id = fl.guardian_id
		 WHERE fl.dependent_id = ?
		 ORDER BY LOWER(u.name) ASC`, dependentID,
	)
	if err != nil {
		return nil, fmt.Errorf("query guardians: %w", err)
	}
	defer rows.Close()

	var links []FamilyLink
	for rows.Next() {
		var fl FamilyLink
		var u User
		var ut sql.NullString
		var lastMsg sql.NullTime
		var thresh sql.NullInt64
		var paused sql.NullTime
		if err := rows.Scan(
			&fl.ID, &fl.GuardianID, &fl.DependentID, &fl.Relationship,
			&fl.Notify.OnMedicationMiss, &fl.Notify.OnInactivity, &fl.Notify.OnSevereSignal,
			&fl.CreatedAt,
			&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
			&ut, &lastMsg, &thresh, &paused,
		); err != nil {
			return nil, err
		}
		scanUserExtras(&u, ut, lastMsg)
		scanUserPhase4(&u, thresh, paused)
		fl.Other = &u
		links = append(links, fl)
	}
	return links, rows.Err()
}

// IsGuardianOf retorna true se existe vinculo (guardianID -> dependentID).
// Equivalente conceitual ao CanScheduleFor de PermissionManager, mas em
// dimensao familiar e nao de calendario.
func (db *DB) IsGuardianOf(guardianID, dependentID int64) (bool, error) {
	var n int
	err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM family_links WHERE guardian_id = ? AND dependent_id = ?`,
		guardianID, dependentID,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("is guardian of: %w", err)
	}
	return n > 0, nil
}

// UpdateNotifyPreferences sobrescreve as flags de notificacao de um vinculo.
// Retorna ErrFamilyLinkNotFound se linkID nao existe.
func (db *DB) UpdateNotifyPreferences(linkID int64, prefs FamilyNotifyPrefs) error {
	res, err := db.conn.Exec(
		`UPDATE family_links
			SET notify_on_medication_miss = ?,
				notify_on_inactivity      = ?,
				notify_on_severe_signal   = ?
		  WHERE id = ?`,
		prefs.OnMedicationMiss, prefs.OnInactivity, prefs.OnSevereSignal, linkID,
	)
	if err != nil {
		return fmt.Errorf("update notify preferences: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrFamilyLinkNotFound
	}
	return nil
}
