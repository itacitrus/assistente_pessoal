package main

import (
	"fmt"
	"strings"
)

type PermissionManager struct {
	db *DB
}

func NewPermissionManager(db *DB) *PermissionManager {
	return &PermissionManager{db: db}
}

// Grant gives grantee permission to schedule events on grantor's calendar.
// Semantics: granteeID can create events on grantorID's calendar.
func (pm *PermissionManager) Grant(granteeID, grantorID int64) error {
	_, err := pm.db.conn.Exec(
		`INSERT OR IGNORE INTO calendar_permissions (grantor_id, grantee_id) VALUES (?, ?)`,
		grantorID, granteeID)
	return err
}

// Revoke removes grantee's permission to schedule on grantor's calendar.
func (pm *PermissionManager) Revoke(granteeID, grantorID int64) error {
	_, err := pm.db.conn.Exec(
		`DELETE FROM calendar_permissions WHERE grantor_id = ? AND grantee_id = ?`,
		grantorID, granteeID)
	return err
}

// CanScheduleFor returns true if granteeID has permission to schedule on grantorID's calendar.
func (pm *PermissionManager) CanScheduleFor(granteeID, grantorID int64) (bool, error) {
	var count int
	err := pm.db.conn.QueryRow(
		`SELECT COUNT(*) FROM calendar_permissions WHERE grantor_id = ? AND grantee_id = ?`,
		grantorID, granteeID).Scan(&count)
	return count > 0, err
}

// ListTargetsFor returns all users whose calendars granteeID can schedule on.
func (pm *PermissionManager) ListTargetsFor(granteeID int64) ([]User, error) {
	rows, err := pm.db.conn.Query(
		`SELECT u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
		 u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
		 u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at
		 FROM calendar_permissions cp
		 JOIN users u ON u.id = cp.grantor_id
		 WHERE cp.grantee_id = ?`, granteeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// ResolveByName finds a user by name (case-insensitive partial match).
// Returns nil if not found.
func (pm *PermissionManager) ResolveByName(name string) (*User, error) {
	rows, err := pm.db.conn.Query(
		`SELECT id, phone_number, name, google_calendar_id, google_credentials,
		 daily_summary_time, weekly_summary_day, weekly_summary_time,
		 reminder_before, auto_confirm_timeout, is_active, created_at
		 FROM users WHERE is_active = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	lower := strings.ToLower(name)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
			&u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
			&u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt); err != nil {
			return nil, err
		}
		if strings.Contains(strings.ToLower(u.Name), lower) {
			return &u, nil
		}
	}
	return nil, rows.Err()
}

// FormatAccessList formats the list of users the grantee can schedule for.
func (pm *PermissionManager) FormatAccessList(granteeName string, targets []User) string {
	if len(targets) == 0 {
		return fmt.Sprintf("%s, voce ainda nao tem permissao para agendar na agenda de ninguem.", granteeName)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%s, voce pode agendar na agenda de:\n", granteeName))
	for _, u := range targets {
		sb.WriteString(fmt.Sprintf("  - %s\n", u.Name))
	}
	return sb.String()
}

// RequestPermission creates a pending permission request in the DB and returns
// a message to send to the target user asking for approval.
func (pm *PermissionManager) RequestPermission(requester *User, target *User, eventDataJSON string) (string, error) {
	req := &PermissionRequest{
		RequesterID: requester.ID,
		TargetID:    target.ID,
		EventData:   eventDataJSON,
		Status:      "pending",
	}
	if err := pm.db.CreatePermissionRequest(req); err != nil {
		return "", fmt.Errorf("create permission request: %w", err)
	}

	msg := fmt.Sprintf(
		"%s quer criar um evento na sua agenda. Voce autoriza? Pode responder em texto livre — por exemplo: sim (so desta vez), sempre (autoriza permanente), ou nao.",
		requester.Name,
	)
	return msg, nil
}

// ResolvePermissionDecision represents the target user's decision on a pending
// permission request. Decoded by the LLM from the user's free-text reply.
type ResolvePermissionDecision string

const (
	DecisionAllowOnce   ResolvePermissionDecision = "once"
	DecisionAllowAlways ResolvePermissionDecision = "always"
	DecisionDeny        ResolvePermissionDecision = "deny"
)

// ResolvePendingPermission resolves the target user's pending permission request
// according to the decision. Returns (messageToTarget, messageToRequester,
// requesterPhone, error).
func (pm *PermissionManager) ResolvePendingPermission(target *User, decision ResolvePermissionDecision) (string, string, string, error) {
	req, err := pm.db.GetPendingPermissionRequest(target.ID)
	if err != nil {
		return "", "", "", fmt.Errorf("get pending permission request: %w", err)
	}
	requester, err := pm.db.GetUserByID(req.RequesterID)
	if err != nil {
		return "", "", "", fmt.Errorf("get requester: %w", err)
	}

	switch decision {
	case DecisionAllowOnce:
		if err := pm.db.ResolvePermissionRequest(req.ID, "approved_once"); err != nil {
			return "", "", "", err
		}
		return fmt.Sprintf("Ok! Permiti que %s crie o evento desta vez.", requester.Name),
			fmt.Sprintf("%s permitiu a criacao do evento (uma vez).", target.Name),
			requester.PhoneNumber, nil

	case DecisionAllowAlways:
		if err := pm.Grant(requester.ID, target.ID); err != nil {
			return "", "", "", err
		}
		if err := pm.db.ResolvePermissionRequest(req.ID, "approved_always"); err != nil {
			return "", "", "", err
		}
		return fmt.Sprintf("Ok! %s agora pode sempre agendar na sua agenda.", requester.Name),
			fmt.Sprintf("%s concedeu acesso permanente a voce.", target.Name),
			requester.PhoneNumber, nil

	case DecisionDeny:
		if err := pm.db.ResolvePermissionRequest(req.ID, "denied"); err != nil {
			return "", "", "", err
		}
		return fmt.Sprintf("Ok! Negei o acesso de %s.", requester.Name),
			fmt.Sprintf("%s negou sua solicitacao.", target.Name),
			requester.PhoneNumber, nil

	default:
		return "", "", "", fmt.Errorf("invalid decision: %s", decision)
	}
}
