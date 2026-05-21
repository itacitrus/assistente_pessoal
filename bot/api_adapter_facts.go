package main

import (
	"context"
	"strings"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
)

// =========================================================================
// Adapter: profile-facts (web/UI) — "o que o Zello sabe sobre voce"
// =========================================================================

// profilePeopleMax limita quantas pessoas do contexto social retornamos.
const profilePeopleMax = 10

// profileTripsMax limita quantas viagens retornamos.
const profileTripsMax = 6

// ProfileFacts agrega relacoes (vinculos familiares + memorias de relacao),
// pessoas do contexto social e viagens do usuario. Privacidade: memos de risco
// (category prefixada com "risco:") NUNCA sao incluidos.
func (a *apiAdapter) ProfileFacts(ctx context.Context, userID int64) (api.ProfileFactsResponse, error) {
	resp := api.ProfileFactsResponse{
		Relations: []api.RelationFact{},
		People:    []api.PersonFact{},
		Trips:     []api.TripFact{},
	}

	// Relacoes: dependentes + guardioes (vinculos familiares).
	deps, err := a.db.GetDependents(userID)
	if err != nil {
		return resp, err
	}
	for _, fl := range deps {
		if fl.Other == nil {
			continue
		}
		resp.Relations = append(resp.Relations, api.RelationFact{
			Name:     fl.Other.Name,
			Relation: relationLabel(fl.Relationship),
			Kind:     "dependent",
		})
	}
	guards, err := a.db.GetGuardians(userID)
	if err != nil {
		return resp, err
	}
	for _, fl := range guards {
		if fl.Other == nil {
			continue
		}
		resp.Relations = append(resp.Relations, api.RelationFact{
			Name:     fl.Other.Name,
			Relation: relationLabel(fl.Relationship),
			Kind:     "guardian",
		})
	}

	// Memorias de relacao -> relations; contato/relacao/social_context ->
	// people. Uma unica leitura de memorias por categoria relevante.
	relMems, err := a.db.GetMemories(userID, "relacao")
	if err != nil {
		return resp, err
	}
	for _, m := range relMems {
		if isRiskMemo(m.Category) {
			continue
		}
		resp.Relations = append(resp.Relations, api.RelationFact{
			Name:     humanizeMemoryKey(m.Key),
			Relation: m.Value,
			Kind:     "memory",
		})
	}

	// People: contato + relacao + social_context (sem risco:*).
	for _, cat := range []string{"contato", "relacao", "social_context"} {
		mems, err := a.db.GetMemories(userID, cat)
		if err != nil {
			return resp, err
		}
		for _, m := range mems {
			if isRiskMemo(m.Category) {
				continue
			}
			if len(resp.People) >= profilePeopleMax {
				break
			}
			resp.People = append(resp.People, api.PersonFact{
				Name:   humanizeMemoryKey(m.Key),
				Detail: m.Value,
			})
		}
	}

	// Trips: viagens futuras (inclui as em andamento). ListTravelPeriods com
	// onlyFuture=true ja exclui as totalmente passadas.
	trips, err := a.db.ListTravelPeriods(userID, true)
	if err != nil {
		return resp, err
	}
	for _, t := range trips {
		if len(resp.Trips) >= profileTripsMax {
			break
		}
		resp.Trips = append(resp.Trips, api.TripFact{
			Label:       "Viagem",
			Destination: t.LocationName,
			Start:       t.StartDate.Format(dateLayout),
			End:         t.EndDate.Format(dateLayout),
		})
	}

	resp.Available = len(resp.Relations) > 0 || len(resp.People) > 0 || len(resp.Trips) > 0
	return resp, nil
}

// isRiskMemo detecta memos sensiveis de risco (category prefixada "risco:").
func isRiskMemo(category string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(category)), "risco:")
}

// humanizeMemoryKey converte uma key de memoria (ex: "medico_dr_roberto") em
// texto legivel ("Medico Dr Roberto"). Best-effort — keys ja legiveis passam
// quase intactas.
func humanizeMemoryKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	// Remove prefixo "tipo:" comum em chaves estruturadas (ex: "evento:...").
	if i := strings.IndexByte(key, ':'); i > 0 && i < len(key)-1 {
		key = key[i+1:]
	}
	key = strings.ReplaceAll(key, "_", " ")
	key = strings.ReplaceAll(key, "-", " ")
	return capitalizeFirst(strings.TrimSpace(key))
}

// relationLabel devolve um rotulo legivel para o relationship do vinculo.
// Mantem o valor cru quando ja eh humano; capitaliza a primeira letra.
func relationLabel(rel string) string {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "Familiar"
	}
	return capitalizeFirst(rel)
}
