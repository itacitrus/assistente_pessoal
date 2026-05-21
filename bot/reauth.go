package main

import (
	"fmt"
	"log"
	"strings"
	"time"
)

// IsInvalidGrantErr returns true if err is a Google OAuth "invalid_grant"
// or "Token has been expired or revoked" error. These signal that the user's
// refresh token is no longer valid and they need to reauthorize.
func IsInvalidGrantErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "invalid_grant") || strings.Contains(msg, "Token has been expired or revoked")
}

// reauthNotifyInterval is the minimum time between consecutive reauth
// notifications to the same user. Prevents the scheduler (which runs every
// minute and will hit the same invalid_grant error on every cycle) from
// spamming the user with duplicate messages.
const reauthNotifyInterval = 12 * time.Hour

// SendReauthLinkIfDue sends the user a WhatsApp message with an OAuth
// authorization URL to reauthorize access to Google Calendar, IF the last
// notification was more than reauthNotifyInterval ago (or never).
//
// Returns (sent bool, err error). sent=true means the message was dispatched;
// sent=false means the rate-limit window blocked it. Errors are logged but
// not surfaced beyond the caller (best-effort side channel).
func SendReauthLinkIfDue(db *DB, cal *CalendarClient, sendMsg func(phone, text string) error, user *User, now time.Time) (bool, error) {
	notifiedAt, err := db.GetReauthNotifiedAt(user.ID)
	if err != nil {
		return false, fmt.Errorf("get reauth_notified_at: %w", err)
	}
	if notifiedAt != nil && now.Sub(*notifiedAt) < reauthNotifyInterval {
		return false, nil
	}

	state, err := db.CreateOAuthState(user.ID, oauthStateTTL)
	if err != nil {
		return false, fmt.Errorf("create oauth state: %w", err)
	}
	authURL := cal.AuthURL(state)
	msg := fmt.Sprintf(
		"Ops, sua autorização com o Google Calendar expirou (é uma limitação temporária enquanto meu app não é verificado pelo Google — acontece a cada 7 dias).\n\nReautorize aqui e eu volto a funcionar:\n\n%s",
		authURL,
	)
	if sendErr := sendMsg(user.PhoneNumber, msg); sendErr != nil {
		return false, fmt.Errorf("send reauth msg: %w", sendErr)
	}
	if dbErr := db.SetReauthNotifiedAt(user.ID, now); dbErr != nil {
		log.Printf("[%s] failed to persist reauth_notified_at: %v", user.Name, dbErr)
	}
	log.Printf("[%s] reauth link sent (token expired)", user.Name)
	return true, nil
}
