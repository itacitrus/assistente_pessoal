package main

import (
	"context"
	"encoding/json"
	"fmt"
)

type responderPermissaoParams struct {
	Decision string `json:"decision"` // "once" | "always" | "deny"
}

func handleResponderPermissao(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p responderPermissaoParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	var decision ResolvePermissionDecision
	switch p.Decision {
	case "once":
		decision = DecisionAllowOnce
	case "always":
		decision = DecisionAllowAlways
	case "deny":
		decision = DecisionDeny
	default:
		return fmt.Sprintf("Decisao invalida: %q. Use once, always ou deny.", p.Decision), nil
	}

	msgToTarget, msgToRequester, requesterPhone, err := agent.perms.ResolvePendingPermission(user, decision)
	if err != nil {
		return "", fmt.Errorf("resolve permission: %w", err)
	}

	if agent.sendMsg != nil && requesterPhone != "" && msgToRequester != "" {
		agent.sendMsg(requesterPhone, msgToRequester)
	}

	action := "deny_access"
	switch decision {
	case DecisionAllowOnce:
		action = "grant_access_once"
	case DecisionAllowAlways:
		action = "grant_access"
	}
	agent.audit.Log(user.ID, action, "", "resposta a solicitacao de acesso")

	return msgToTarget, nil
}
