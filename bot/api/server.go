package api

import (
	"net/http"
	"strconv"
	"strings"
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
	statusCache    *statusCache
	insightsCache  *insightsCache
	reportClient   synthesis.ReportClient
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
	StatusCacheTTL time.Duration // default 60s; aceita 0 = usa default
	// InsightsCacheTTL eh o TTL do cache de GET /me/insights. Default 6h;
	// aceita 0 = usa default. Insights via Sonnet sao caros e mudam devagar.
	InsightsCacheTTL time.Duration
	// ReportClient eh o provider Sonnet usado pelo sub-agente de insights de
	// agenda. Pode ser nil — handler trata como "insights indisponiveis".
	ReportClient synthesis.ReportClient
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
	return &Server{
		store:          cfg.Store,
		webBaseURL:     cfg.WebBaseURL,
		pathPrefix:     cfg.PathPrefix,
		allowedOrigins: cfg.AllowedOrigins,
		cookieSecure:   cfg.CookieSecure,
		statusCache:    newStatusCache(cfg.StatusCacheTTL),
		insightsCache:  newInsightsCache(cfg.InsightsCacheTTL),
		reportClient:   cfg.ReportClient,
	}
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
	mux.Handle(s.route("/api/v1/me/insights"),
		s.CORS(s.RequireAuth(http.HandlerFunc(s.handleMeInsights))))

	// Family — colecao.
	mux.Handle(s.route("/api/v1/family/dependents"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleDependentsCollection)))))

	// Family — recursos por id. ServeMux nao casa wildcards, entao o
	// handler unico abaixo encaminha pelo metodo + parsing manual do path.
	mux.Handle(s.route("/api/v1/family/dependents/"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleDependentResource)))))

	mux.Handle(s.route("/api/v1/family/links/"),
		s.CORS(s.RequireOrigin(s.RequireAuth(http.HandlerFunc(s.handleLinkResource)))))
}

// handleDependentsCollection roteia GET (list) vs POST (create) pra coleção.
func (s *Server) handleDependentsCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListDependents(w, r)
	case http.MethodPost:
		s.handleCreateDependent(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Metodo nao permitido.")
	}
}

// handleDependentResource roteia recursos /family/dependents/{id} e seus
// sub-paths /status, /timeline. Path parsing manual — vale a pena pra evitar
// dependencia.
func (s *Server) handleDependentResource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.route("/api/v1/family/dependents/"))
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota nao encontrada.")
		return
	}
	depID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || depID <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "ID do dependente invalido.")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch:
			s.handleUpdateDependent(w, r, depID)
		default:
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Metodo nao permitido.")
		}
		return
	}
	// Sub-resource.
	sub := parts[1]
	switch {
	case sub == "status":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Metodo nao permitido.")
			return
		}
		s.handleDependentStatus(w, r, depID)
	case sub == "timeline":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Metodo nao permitido.")
			return
		}
		s.handleDependentTimeline(w, r, depID)
	default:
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota nao encontrada.")
	}
}

// handleLinkResource roteia /family/links/{id}/notify.
func (s *Server) handleLinkResource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, s.route("/api/v1/family/links/"))
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" {
		writeError(w, http.StatusNotFound, CodeNotFound, "Rota nao encontrada.")
		return
	}
	linkID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || linkID <= 0 {
		writeError(w, http.StatusBadRequest, CodeValidation, "ID do vinculo invalido.")
		return
	}
	if parts[1] == "notify" {
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, CodeValidation, "Metodo nao permitido.")
			return
		}
		s.handleUpdateNotify(w, r, linkID)
		return
	}
	writeError(w, http.StatusNotFound, CodeNotFound, "Rota nao encontrada.")
}
