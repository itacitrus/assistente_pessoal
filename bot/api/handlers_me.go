package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// handleMeAgenda — GET /api/v1/me/agenda. Visao factual da agenda do proprio
// usuario logado: proximos eventos (Google Calendar) + atividade recente
// (action_log). Sem cache — payload barato, sem chamada de LLM.
func (s *Server) handleMeAgenda(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Metodo nao permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
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

// handleMeInsights — GET /api/v1/me/insights?days=30. Insights de IA (Sonnet)
// sobre o uso da agenda do proprio usuario. Cache em memoria por user com TTL
// longo (~6h) — insights sao caros e padroes mudam devagar.
//
// available=false quando nao ha dado suficiente OU o provider nao esta
// configurado. Nesse caso devolve insights:[] e um summary curto explicando,
// SEM chamar o modelo.
func (s *Server) handleMeInsights(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Metodo nao permitido.")
		return
	}
	user := userFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Nao autenticado.")
		return
	}
	days := parseDaysQuery(r, 30, 365)

	cacheKey := fmt.Sprintf("%d-%d", user.ID, days)
	if cached, ok := s.insightsCache.Get(cacheKey); ok {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	in, err := s.store.AgendaInsightsData(r.Context(), user.ID, days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao montar dados de insights.")
		return
	}
	in.PeriodDays = days

	// Sem dado suficiente ou provider ausente -> available=false, sem gastar
	// Sonnet. Cacheamos pra nao reprocessar a montagem de dados num refresh-loop.
	if s.reportClient == nil || !in.HasEnoughData() {
		resp := &InsightsResponse{
			GeneratedAt: time.Now().UTC(),
			PeriodDays:  days,
			Available:   false,
			Summary:     insightsUnavailableSummary(in),
			Insights:    []InsightItem{},
		}
		s.insightsCache.Set(cacheKey, resp)
		writeJSON(w, http.StatusOK, resp)
		return
	}

	out, err := synthesis.AgendaInsights(r.Context(), s.reportClient, in)
	if err != nil {
		// Falha de IA nao deve derrubar a UI: devolvemos available=false
		// degradado e auditamos. Nao cacheamos a falha (TTL curto via ausencia).
		s.store.Audit(r.Context(), user.ID, "me_insights_generated",
			"", fmt.Sprintf("days=%d|status=error", days))
		writeJSON(w, http.StatusOK, InsightsResponse{
			GeneratedAt: time.Now().UTC(),
			PeriodDays:  days,
			Available:   false,
			Summary:     "Nao foi possivel gerar os insights agora. Tente novamente mais tarde.",
			Insights:    []InsightItem{},
		})
		return
	}

	items := make([]InsightItem, 0, len(out.Insights))
	for _, ins := range out.Insights {
		items = append(items, InsightItem{
			Title:  ins.Title,
			Detail: ins.Detail,
			Kind:   ins.Kind,
		})
	}
	resp := &InsightsResponse{
		GeneratedAt: time.Now().UTC(),
		PeriodDays:  days,
		Available:   true,
		Summary:     out.Summary,
		Insights:    items,
	}
	s.insightsCache.Set(cacheKey, resp)
	s.store.Audit(r.Context(), user.ID, "me_insights_generated",
		"", fmt.Sprintf("days=%d|status=ok|insights=%d", days, len(items)))
	writeJSON(w, http.StatusOK, resp)
}

// insightsUnavailableSummary monta uma mensagem curta e honesta quando nao ha
// dado suficiente pra gerar insights.
func insightsUnavailableSummary(in synthesis.AgendaInsightsInput) string {
	if !in.GoogleConnected {
		return "Conecte seu Google Agenda e use o assistente por alguns dias para ver insights sobre seus compromissos."
	}
	return "Ainda nao ha compromissos ou atividade suficiente para gerar insights. Continue usando o assistente."
}
