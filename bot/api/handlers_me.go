package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// handleMeAgenda — GET /api/v1/me/agenda. Visao factual da agenda do proprio
// usuario logado: proximos eventos (Google Calendar) + atividade recente
// (action_log). Sem cache — payload barato, sem chamada de LLM.
func (s *Server) handleMeAgenda(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}

	upcoming, err := s.store.UpcomingEvents(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao carregar agenda.")
		return
	}
	if upcoming == nil {
		upcoming = []AgendaEvent{}
	}

	activity, err := s.store.RecentActivity(r.Context(), user.ID, 8)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao carregar atividade recente.")
		return
	}
	if activity == nil {
		activity = []ActivityItem{}
	}

	writeJSON(w, http.StatusOK, AgendaResponse{
		GoogleConnected: user.GoogleConnected,
		Upcoming:        upcoming,
		RecentActivity:  activity,
	})
}

// handleMeActivity — GET /api/v1/me/activity?limit=100. Historico completo das
// acoes relevantes do usuario (allowlist), mais recentes primeiro. default
// limit 50, max 200. Nao audita (consulta pura — auditar poluiria o proprio
// log que essa rota le).
func (s *Server) handleMeActivity(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	limit := parseLimitQuery(r, 50, 200)
	items, err := s.store.ActivityHistory(r.Context(), user.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao carregar atividade.")
		return
	}
	if items == nil {
		items = []ActivityItem{}
	}
	writeJSON(w, http.StatusOK, ActivityResponse{Items: items})
}

// parseLimitQuery extrai e clampa o param `limit`. <=0 ou ausente -> def.
func parseLimitQuery(r *http.Request, def, max int) int {
	q := r.URL.Query().Get("limit")
	if q == "" {
		return def
	}
	n, err := strconv.Atoi(q)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

// handleMeInsights — GET /api/v1/me/insights?days=30. Insights de IA (Sonnet)
// sobre o uso da agenda do proprio usuario. Cache em memoria por user com TTL
// longo (~6h) — insights sao caros e padroes mudam devagar.
//
// available=false quando nao ha dado suficiente OU o provider nao esta
// configurado. Nesse caso devolve insights:[] e um summary curto explicando,
// SEM chamar o modelo.
func (s *Server) handleMeInsights(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	days := parseDaysQuery(r, 30, 365)

	// A geracao (Sonnet) NAO roda no caminho do request — deixava o dashboard
	// do titular lento no login. Servimos os insights persistidos (rapido) e
	// regeneramos em background quando ficam "stale" (mais velhos que o TTL) ou
	// ausentes. Camadas: L1 cache em memoria, L2 persistido no banco.
	cacheKey := fmt.Sprintf("%d-%d", user.ID, days)
	if cached, ok := s.insightsCache.Get(cacheKey); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	stored, err := s.store.GetUserInsights(r.Context(), user.ID, days)
	if err == nil {
		// Persistido existe. Se envelheceu, dispara regen em background — mas
		// serve o atual na hora (sem bloquear).
		if time.Since(stored.GeneratedAt) >= s.insightsTTL {
			s.triggerInsightsRegen(user.ID, days)
		} else {
			s.insightsCache.Set(cacheKey, stored)
		}
		writeJSON(w, http.StatusOK, stored)
		return
	}
	if !errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao carregar insights.")
		return
	}

	// Nada persistido ainda (primeiro acesso) -> gera em background e devolve
	// um placeholder "preparando". O frontend mostra estado e dá auto-refresh.
	s.triggerInsightsRegen(user.ID, days)
	writeJSON(w, http.StatusOK, InsightsResponse{
		GeneratedAt: time.Now().UTC(),
		PeriodDays:  days,
		Available:   false,
		Pending:     true,
		Summary:     "Estamos preparando seus insights — aparecem aqui em instantes.",
		Insights:    []InsightItem{},
	})
}

// triggerInsightsRegen regenera os insights de agenda em background (Sonnet),
// no maximo um por (user, days) de cada vez. Persiste o resultado — inclusive
// available=false (sem dado suficiente / provider ausente), pra nao re-disparar
// a cada load. Em falha de IA, NAO persiste (proximo load tenta de novo).
func (s *Server) triggerInsightsRegen(userID int64, days int) {
	key := fmt.Sprintf("%d-%d", userID, days)
	if _, busy := s.insightsInFlight.LoadOrStore(key, struct{}{}); busy {
		return
	}
	go func() {
		defer s.insightsInFlight.Delete(key)
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()

		in, err := s.store.AgendaInsightsData(ctx, userID, days)
		if err != nil {
			log.Printf("insights regen: gather user=%d: %v", userID, err)
			return
		}
		in.PeriodDays = days

		resp := &InsightsResponse{
			GeneratedAt: time.Now().UTC(),
			PeriodDays:  days,
			Insights:    []InsightItem{},
		}
		if s.reportClient == nil || !in.HasEnoughData() {
			// Sem dado suficiente: persiste available=false (com summary
			// explicativo) pra servir instantaneo e nao re-disparar.
			resp.Available = false
			resp.Summary = insightsUnavailableSummary(in)
			if serr := s.store.SaveUserInsights(ctx, userID, days, resp); serr != nil {
				log.Printf("insights regen: save (unavailable) user=%d: %v", userID, serr)
			}
			return
		}

		out, err := synthesis.AgendaInsights(ctx, s.reportClient, in)
		if err != nil {
			// Falha de IA: nao persiste (retry no proximo load). Audita.
			s.store.Audit(ctx, userID, "me_insights_generated", "",
				fmt.Sprintf("days=%d|status=error", days))
			log.Printf("insights regen: AgendaInsights user=%d: %v", userID, err)
			return
		}
		items := make([]InsightItem, 0, len(out.Insights))
		for _, ins := range out.Insights {
			items = append(items, InsightItem{Title: ins.Title, Detail: ins.Detail, Kind: ins.Kind})
		}
		resp.Available = true
		resp.Summary = out.Summary
		resp.Insights = items
		if serr := s.store.SaveUserInsights(ctx, userID, days, resp); serr != nil {
			log.Printf("insights regen: save user=%d: %v", userID, serr)
		}
		s.store.Audit(ctx, userID, "me_insights_generated", "",
			fmt.Sprintf("days=%d|status=ok|insights=%d|persisted=true", days, len(items)))
	}()
}

// handleMeGoogleConnect — POST /api/v1/me/google/connect-url. Devolve a URL
// de consentimento do Google Calendar para o proprio usuario logado, com um
// state opaco de uso unico ja embutido. O frontend redireciona o navegador
// pra essa URL; ao autorizar, o callback OAuth grava as credenciais e marca
// o state como usado. POST (nao GET) porque emite um token de uso unico —
// efeito colateral, protegido por RequireOrigin contra CSRF.
func (s *Server) handleMeGoogleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	url, err := s.store.GoogleConnectURL(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Não foi possível gerar o link de conexão agora.")
		return
	}
	s.store.Audit(r.Context(), user.ID, "google_connect_url_issued", "", "target=self")
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

// handleMeProfileFacts — GET /api/v1/me/profile-facts. Fatos que o Zello
// conhece do usuario logado (relacoes, pessoas do contexto social, viagens).
// Nao audita (consulta).
func (s *Server) handleMeProfileFacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Não autenticado.")
		return
	}
	facts, err := s.store.ProfileFacts(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao carregar perfil.")
		return
	}
	// Defesa em profundidade: garante slices nao-nil mesmo se o Store esquecer.
	if facts.Relations == nil {
		facts.Relations = []RelationFact{}
	}
	if facts.People == nil {
		facts.People = []PersonFact{}
	}
	if facts.Trips == nil {
		facts.Trips = []TripFact{}
	}
	writeJSON(w, http.StatusOK, facts)
}

// insightsUnavailableSummary monta uma mensagem curta e honesta quando nao ha
// dado suficiente pra gerar insights.
func insightsUnavailableSummary(in synthesis.AgendaInsightsInput) string {
	if !in.GoogleConnected {
		return "Conecte seu Google Agenda e use o assistente por alguns dias para ver insights sobre seus compromissos."
	}
	return "Ainda não há compromissos ou atividade suficiente para gerar insights. Continue usando o assistente."
}
