package main

import (
	"context"
	"fmt"
	"log"
)

type Orchestrator struct {
	agent         *Agent
	transcription *TranscriptionClient
	db            *DB
}

func NewOrchestrator(agent *Agent, transcription *TranscriptionClient, db *DB) *Orchestrator {
	return &Orchestrator{agent: agent, transcription: transcription, db: db}
}

func (o *Orchestrator) Process(ctx context.Context, user *User, message string) (string, error) {
	// Save user message to history
	o.db.AddConversationMessage(user.ID, "user", message)

	// Run agent
	response, err := o.agent.Run(ctx, user, message)
	if err != nil {
		log.Printf("[%s] Agent error: %v", user.Name, err)
		return "", fmt.Errorf("agent: %w", err)
	}

	return response, nil
}

// HandlePermissionResponse processes "1"/"2"/"3" responses from a target user
// about a pending cross-user permission request. Returns the reply message or
// empty string if no pending request exists.
func (o *Orchestrator) HandlePermissionResponse(ctx context.Context, user *User, choice string) (string, bool, error) {
	_, err := o.db.GetPendingPermissionRequest(user.ID)
	if err != nil {
		// No pending permission request for this user
		return "", false, nil
	}

	perms := NewPermissionManager(o.db)
	msgToTarget, msgToRequester, requesterPhone, err := perms.HandlePermissionResponse(user, choice)
	if err != nil {
		return "", false, fmt.Errorf("handle permission response: %w", err)
	}

	// Notify requester
	if o.agent.sendMsg != nil && requesterPhone != "" && msgToRequester != "" {
		o.agent.sendMsg(requesterPhone, msgToRequester)
	}

	// Log the action
	audit := NewAuditLog(o.db)
	action := "deny_access"
	switch choice {
	case "1":
		action = "grant_access_once"
	case "2":
		action = "grant_access"
	}
	audit.Log(user.ID, action, "", "resposta a solicitacao de acesso")

	return msgToTarget, true, nil
}
