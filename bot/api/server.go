package api

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/synthesis"
)

// Server eh o handler container do /api/v1/*. Mount() registra todas as
// rotas em um *http.ServeMux. A funcao foi pensada pra co-existir com o
// startOAuthServer existente — main.go cria um mux compartilhado e passa
// pra ambos.
//
// Campos publicos sao injetados via Config no NewServer().
type Server struct {
	store          Store
	webBaseURL     string
	pathPrefix     string
	allowedOrigins []string
	cookieSecure   bool
	cookieDomain   string
	statusCache    *statusCache
	insightsCache  *insightsCache
	insightsTTL    time.Duration
	reportClient   synthesis.ReportClient
	// adminPhones eh o allowlist (digitos normalizados) de quem pode usar a
	// area admin / "ver como". Privilegio vive na config de deploy (env
	// ADMIN_PHONES) — nao em dado editavel —, entao nao da pra escalar por
	// bug de banco.
	adminPhones map[string]struct{}
	// insightsInFlight deduplica o regen assincrono de insights por (user,days).
	insightsInFlight sync.Map // map[string]struct{}
}

// route prefixa um pattern de rota com o pathPrefix configurado. Em dev
// local pathPrefix eh "" e as rotas ficam /api/v1/*. Em producao, atras do
// ALB compartilhado que so roteia /assistente/* pra esta instancia, o
// prefixo eh "/assistente" e as rotas viram /assistente/api/v1/*.
func (s *Server) route(p string) string { return s.pathPrefix + p }

// Config encapsula os parametros de configuracao do api.Server. Mantido
// como struct pra evitar argumento posicional gigante em NewServer.
type Config struct {
	Store          Store
	WebBaseURL     string        // ex: "https://app.lurch.com.br" — usado no link do magic
	PathPrefix     string        // ex: "/assistente" em prod (ALB). "" em dev local.
	AllowedOrigins []string      // CORS allowlist. Ex: ["https://app.lurch.com.br"]
	CookieSecure   bool          // true em prod (https). false em dev local (http://localhost:3000)
	CookieDomain   string        // "" = host-only (dev). "zello.chat" em prod: cookie compartilhado entre app (zello.chat) e api (api.zello.chat) — SSR le em zello.chat.
	StatusCacheTTL time.Duration // default 60s; aceita 0 = usa default
	// InsightsCacheTTL eh o TTL do cache de GET /me/insights. Default 6h;
	// aceita 0 = usa default. Insights via Sonnet sao caros e mudam devagar.
	InsightsCacheTTL time.Duration
	// ReportClient eh o provider Sonnet usado pelo sub-agente de insights de
	// agenda. Pode ser nil — handler trata como "insights indisponiveis".
	ReportClient synthesis.ReportClient
	// AdminPhones eh o allowlist de telefones (qualquer formato; normalizado
	// internamente pra so digitos) com privilegio de admin no painel. Vazio =
	// ninguem tem acesso admin (area fica invisivel/403).
	AdminPhones []string
}

// NewServer constroi com defaults ajuiziados. Caller eh responsavel pelo
// Store (adapter) e pelo WebBaseURL.
func NewServer(cfg Config) *Server {
	if cfg.StatusCacheTTL <= 0 {
		cfg.StatusCacheTTL = 60 * time.Second
	}
	if cfg.InsightsCacheTTL <= 0 {
		cfg.InsightsCacheTTL = 6 * time.Hour
	}
	if len(cfg.AllowedOrigins) == 0 {
		cfg.AllowedOrigins = []string{"http://localhost:3000"}
	}
	// Normaliza WebBaseURL — strip trailing slash. Sem isso, /auth/verify
	// vira //auth/verify quando se concatena.
	cfg.WebBaseURL = strings.TrimRight(cfg.WebBaseURL, "/")
	// pathPrefix tambem sem trailing slash; "" fica "" (dev local).
	cfg.PathPrefix = strings.TrimRight(cfg.PathPrefix, "/")
	admins := make(map[string]struct{}, len(cfg.AdminPhones))
	for _, p := range cfg.AdminPhones {
		if d := digitsOnly(p); d != "" {
			admins[d] = struct{}{}
		}
	}
	return &Server{
		store:          cfg.Store,
		webBaseURL:     cfg.WebBaseURL,
		pathPrefix:     cfg.PathPrefix,
		allowedOrigins: cfg.AllowedOrigins,
		cookieSecure:   cfg.CookieSecure,
		cookieDomain:   strings.TrimSpace(cfg.CookieDomain),
		statusCache:    newStatusCache(cfg.StatusCacheTTL),
		insightsCache:  newInsightsCache(cfg.InsightsCacheTTL),
		insightsTTL:    cfg.InsightsCacheTTL,
		reportClient:   cfg.ReportClient,
		adminPhones:    admins,
	}
}

// isAdmin retorna true se o telefone do usuario estiver no allowlist de admin.
// Comparacao por digitos normalizados — robusta a "+55", espacos e mascaras.
func (s *Server) isAdmin(u *User) bool {
	if u == nil || len(s.adminPhones) == 0 {
		return false
	}
	_, ok := s.adminPhones[digitsOnly(u.PhoneNumber)]
	return ok
}

// digitsOnly extrai apenas os digitos de s. Normaliza telefones do allowlist
// e do user pra comparacao estavel.
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Mount registra todas as rotas /api/v1/* em mux. CORS sempre por fora;
// auth e CSRF sao aplicados onde aplicaveis.
//
// Rotas sao registradas com pattern raiz e o "router interno" eh o switch
// em handle{Auth,User,Family}Routes — escolha pragmatica pra suportar
// caminhos com path params (ex: /family/dependents/{id}) sem importar
// gorilla/chi (regra: sem deps novas).
func (s *Server) Mount(mux *http.ServeMux) {
	// Auth eh publico (sem RequireAuth). Logout precisa auth (revoga sessao).
	mux.Handle(s.route("/api/v1/auth/request-link"),
		s.CORS(s.RequireOrigin(http.HandlerFunc(s.handleRequestLink))))
	mux.Handle(s.route("/api/v1/auth/verify"),
		s.CORS(s.RequireOrigin(http.HandlerFunc(s.handleVerify))))
	mux.Handle(s.route("/api/v1/auth/logout"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleLogout)))))

	// Me / preferences.
	mux.Handle(s.route("/api/v1/me"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMe))))
	mux.Handle(s.route("/api/v1/users/me"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleUpdateMe)))))

	// Me / agenda + insights (GET, auth). Leitura — sem RequireOrigin
	// (segue o padrao de /api/v1/me, que tambem eh GET autenticado).
	mux.Handle(s.route("/api/v1/me/agenda"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMeAgenda))))
	mux.Handle(s.route("/api/v1/me/agenda/events"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMeAgendaEvents))))
	mux.Handle(s.route("/api/v1/me/insights"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMeInsights))))
	mux.Handle(s.route("/api/v1/me/activity"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMeActivity))))
	mux.Handle(s.route("/api/v1/me/profile-facts"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMeProfileFacts))))

	// Curadoria manual das "pessoas na sua vida" (POST/PATCH/DELETE). Grava
	// memorias que o Zello passa a conhecer. RequireOrigin protege as mutacoes.
	mux.Handle(s.route("/api/v1/me/people"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleMyPeopleCollection)))))

	// Conexao com o Google Calendar do proprio titular. POST (emite token de
	// uso unico) — RequireOrigin protege contra CSRF.
	mux.Handle(s.route("/api/v1/me/google/connect-url"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleMeGoogleConnect)))))

	// Medicacao do proprio titular. Colecao (GET list, POST create) + recurso
	// por id (DELETE). RequireOrigin nas mutacoes (POST/DELETE) — o GET tambem
	// passa, sem custo (origin so eh exigido em metodos mutativos).
	mux.Handle(s.route("/api/v1/me/medications"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleMyMedicationsCollection)))))
	mux.Handle(s.route("/api/v1/me/medications/"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleMyMedicationResource)))))

	// Historico de tomadas do proprio titular (GET). Janela via ?days, filtro
	// opcional ?medication_id.
	mux.Handle(s.route("/api/v1/me/intakes"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMyIntakes))))

	// Busca no catalogo de medicamentos (GET, autocomplete do cadastro). So
	// leitura de dado publico — RequireAuth basta, sem RequireOrigin.
	mux.Handle(s.route("/api/v1/me/drugs/search"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleDrugSearch))))

	// Family — colecao.
	mux.Handle(s.route("/api/v1/family/dependents"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleDependentsCollection)))))

	// Family — recursos por id. ServeMux nao casa wildcards, entao o
	// handler unico abaixo encaminha pelo metodo + parsing manual do path.
	mux.Handle(s.route("/api/v1/family/dependents/"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleDependentResource)))))

	mux.Handle(s.route("/api/v1/family/links/"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleLinkResource)))))

	// Admin — busca de usuarios (GET) + impersonacao "ver como" (POST/DELETE).
	// O gate de privilegio vive nos handlers (requireAdmin sobre o dono real
	// da sessao). RequireOrigin protege os metodos mutativos de /impersonate.
	mux.Handle(s.route("/api/v1/admin/users"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleAdminUsers))))
	mux.Handle(s.route("/api/v1/admin/impersonate"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleAdminImpersonate)))))
}

// handleDependentsCollection roteia GET (list) vs POST (create) pra coleção.
func (s *Server) handleDependentsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListDependents(w, r)
	case http.MethodPost:
		s.handleCreateDependent(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
	}
}

// handleDependentResource roteia recursos /family/dependents/{id} e seus
// sub-paths /status, /timeline. Path parsing manual — vale a pena pra evitar
// dependencia.
func (s *Server) handleDependentResource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.route("/api/v1/family/dependents/"))
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota não encontrada.")
		return
	}
	depID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || depID <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "ID do dependente inválido.")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			s.handleUpdateDependent(w, r, depID)
		case http.MethodDelete:
			s.handleUnlinkDependent(w, r, depID)
		default:
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		}
		return
	}
	// Sub-resource. Pode ser simples ("status", "timeline", "medications") ou
	// aninhado ("medications/{medId}").
	subParts := strings.SplitN(parts[1], "/", 2)
	sub := subParts[0]
	switch sub {
	case "status":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
			return
		}
		s.handleDependentStatus(w, r, depID)
	case "timeline":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
			return
		}
		s.handleDependentTimeline(w, r, depID)
	case "medications":
		s.routeDependentMedications(w, r, depID, subParts)
	case "alerts":
		s.routeDependentAlerts(w, r, depID, subParts)
	case "intakes":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
			return
		}
		s.handleListDependentIntakes(w, r, depID)
	case "welcome":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
			return
		}
		s.handleResendWelcome(w, r, depID)
	case "google":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
			return
		}
		s.handleDependentGoogleConnect(w, r, depID)
	default:
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota não encontrada.")
	}
}

// routeDependentMedications roteia /family/dependents/{id}/medications e
// /family/dependents/{id}/medications/{medId}. subParts[0] == "medications".
func (s *Server) routeDependentMedications(w http.ResponseWriter, r *http.Request, depID int64, subParts []string) {
	// Coletivo: /medications  (sem id, ou trailing slash vazio)
	if len(subParts) == 1 || strings.TrimSpace(subParts[1]) == "" {
		switch r.Method {
		case http.MethodGet:
			s.handleListDependentMedications(w, r, depID)
		case http.MethodPost:
			s.handleCreateDependentMedication(w, r, depID)
		default:
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		}
		return
	}
	// Item: /medications/{medId}
	medID, err := strconv.ParseInt(strings.Trim(subParts[1], "/"), 10, 64)
	if err != nil || medID <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "ID do medicamento inválido.")
		return
	}
	switch r.Method {
	case http.MethodPatch:
		s.handleUpdateDependentMedication(w, r, depID, medID)
	case http.MethodDelete:
		s.handleDeleteDependentMedication(w, r, depID, medID)
	default:
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
	}
}

// routeDependentAlerts roteia /family/dependents/{id}/alerts/{alertId}/review.
// subParts[0] == "alerts". Unica acao por enquanto: POST .../review.
func (s *Server) routeDependentAlerts(w http.ResponseWriter, r *http.Request, depID int64, subParts []string) {
	if len(subParts) == 1 || strings.TrimSpace(subParts[1]) == "" {
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota não encontrada.")
		return
	}
	rest := strings.SplitN(strings.Trim(subParts[1], "/"), "/", 2)
	alertID, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil || alertID <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "ID do alerta inválido.")
		return
	}
	action := ""
	if len(rest) == 2 {
		action = rest[1]
	}
	if action != "review" {
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota não encontrada.")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
		return
	}
	s.handleReviewDependentAlert(w, r, depID, alertID)
}

// handleLinkResource roteia /family/links/{id}/notify.
func (s *Server) handleLinkResource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.route("/api/v1/family/links/"))
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota não encontrada.")
		return
	}
	linkID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || linkID <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "ID do vínculo inválido.")
		return
	}
	if parts[1] == "notify" {
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Método não permitido.")
			return
		}
		s.handleUpdateNotify(w, r, linkID)
		return
	}
	writeError(w, http.StatusNotFound, CodeNotFound, "Rota não encontrada.")
}
