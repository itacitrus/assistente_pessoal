package api

// relevantActivityActions eh o allowlist de acoes que importam para o usuario
// final na visao de atividade. Tudo que nao esta aqui eh considerado ruido de
// sistema/consulta (login, consultas de status, sintese, snapshots) e nunca
// aparece nem em /me/agenda.recent_activity nem em /me/activity.
//
// Mantido como set (map[string]struct{}) para lookup O(1) e exposto via
// IsRelevantActivity para o adapter (main package) reusar o MESMO filtro —
// single source of truth.
var relevantActivityActions = map[string]struct{}{
	"criar_evento":                {},
	"editar_evento":               {},
	"cancelar_evento":             {},
	"family_link_created":         {},
	"family_link_removed":         {},
	"family_notify_prefs_updated": {},
	"medication_created":          {},
	"medication_edited":           {},
	"medication_canceled":         {},
	"medication_taken":            {},
	"medication_skipped":          {},
	"medication_missed":           {},
	"medication_escalated":        {},
	"prescription_image_processed": {},
	"alertar_familia":             {},
	"pausar_proatividade":         {},
	"grant_access":                {},
	"revoke_access":               {},
	"comentar_imagem":             {},
	"comentar_link":               {},
}

// RelevantActivityActions devolve o allowlist como slice (ordem nao garantida)
// para callers que precisam montar uma clausula SQL IN (...). O conteudo eh o
// mesmo set de relevantActivityActions.
func RelevantActivityActions() []string {
	out := make([]string, 0, len(relevantActivityActions))
	for a := range relevantActivityActions {
		out = append(out, a)
	}
	return out
}

// IsRelevantActivity informa se uma acao do action_log eh relevante para a
// visao de atividade do usuario (allowlist). Acoes de sistema/consulta
// retornam false.
func IsRelevantActivity(action string) bool {
	_, ok := relevantActivityActions[action]
	return ok
}
