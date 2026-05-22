package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// CookieName eh o nome do cookie httpOnly que carrega o token plaintext
// da sessao. SameSite=Strict — defesa primaria contra CSRF; o middleware
// RequireOrigin eh defesa em profundidade pra POST/PATCH/DELETE.
const CookieName = "zello_session"

// userContextKey eh o tipo privado da chave de context — evita colisao
// com chaves de outros packages (linter avisa se usar string crua).
type userContextKey struct{}

// realUserKey carrega o DONO real da sessao, sempre — mesmo durante uma
// impersonacao de admin. userContextKey carrega o usuario EFETIVO (o alvo,
// quando o admin esta "vendo como"). Handlers de negocio usam o efetivo
// (operam sobre os dados certos); checagens de privilegio usam o real.
type realUserKey struct{}

// userFromContext extrai o user EFETIVO injetado pelo RequireAuth (o alvo da
// impersonacao, quando ativa; senao o proprio dono da sessao). Retorna nil se
// nao logado — handlers protegidos chamam apenas dentro de RequireAuth,
// entao teoricamente sempre retorna != nil; defensivo retorna nil.
func userFromContext(ctx context.Context) *User {
	v := ctx.Value(userContextKey{})
	if v == nil {
		return nil
	}
	u, ok := v.(*User)
	if !ok {
		return nil
	}
	return u
}

// realUserFromContext extrai o DONO real da sessao (nunca o impersonado).
// Usar em gates de privilegio (admin) e em auditoria de quem agiu.
func realUserFromContext(ctx context.Context) *User {
	v := ctx.Value(realUserKey{})
	if v == nil {
		return nil
	}
	u, ok := v.(*User)
	if !ok {
		return nil
	}
	return u
}

// sessionIDFromContext extrai o id da sessao validada pelo RequireAuth.
func sessionIDFromContext(ctx context.Context) int64 {
	v := ctx.Value(sessionIDKey{})
	if v == nil {
		return 0
	}
	id, _ := v.(int64)
	return id
}

type sessionIDKey struct{}

// RequireAuth eh o middleware: le cookie, valida sessao no Store, atualiza
// last_used_at via TouchSession, injeta User no context. Retorna 401 com
// envelope estruturado em qualquer falha.
//
// Concorrencia: se 2 requests da mesma sessao chegam em paralelo, ambas
// chamam TouchSession — UPDATE eh idempotente (last_used_at = now no
// segundo recente).
func (s *Server) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Sessao ausente. Faca login pelo painel.")
			return
		}
		ctx := r.Context()
		sessID, userID, impersonatedID, err := s.store.GetActiveSessionByToken(ctx, cookie.Value)
		if err != nil {
			// Cookie ruim ou sessao expirada — limpa cookie pra evitar loop.
			clearSessionCookie(w, s.cookieSecure, s.cookieDomain)
			switch {
			case errors.Is(err, ErrSessionExpired):
				writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Sua sessão expirou. Faça login de novo.")
			case errors.Is(err, ErrSessionInvalid), errors.Is(err, ErrNotFound):
				writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Sessão inválida.")
			default:
				writeError(w, http.StatusInternalServerError, CodeInternal, "Erro ao validar sessão.")
			}
			return
		}
		// Sliding window — atualiza expires_at + last_used_at. Falha silenciosa
		// (log no adapter): nao bloqueia request por write race.
		_ = s.store.TouchSession(ctx, sessID)

		realUser, err := s.store.GetUserByID(ctx, userID)
		if err != nil {
			clearSessionCookie(w, s.cookieSecure, s.cookieDomain)
			writeError(w, http.StatusUnauthorized, CodeUnauthorized, "Usuário da sessão não existe mais.")
			return
		}

		// Usuario efetivo = real, por padrao. Se a sessao carrega uma
		// impersonacao E o dono real eh admin, troca o efetivo pro alvo. A
		// dupla barreira (coluna setada + dono admin) garante que gravar a
		// coluna sozinho nao concede acesso.
		effective := realUser
		if impersonatedID > 0 && impersonatedID != realUser.ID && s.isAdmin(realUser) {
			if target, terr := s.store.GetUserByID(ctx, impersonatedID); terr == nil {
				effective = target
			}
			// Alvo sumiu (deletado): cai no fluxo normal como o proprio admin.
			// Nao limpamos a coluna aqui (read path) — o stop explicito limpa.
		}

		ctx = context.WithValue(ctx, userContextKey{}, effective)
		ctx = context.WithValue(ctx, realUserKey{}, realUser)
		ctx = context.WithValue(ctx, sessionIDKey{}, sessID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireOrigin eh defesa CSRF em profundidade pra rotas mutativas. Bloqueia
// POST/PATCH/DELETE sem header Origin OU com Origin fora do allowlist.
//
// Justificativa: SameSite=Strict ja bloqueia cookie cross-site na maior parte
// dos navegadores modernos, mas:
//   - alguns clientes ignoram SameSite (navegadores antigos, fetch de dentro
//     de iframes em condicoes especificas);
//   - tracking de bug de SameSite "lax-by-default" deixou janelas abertas;
//   - defesa em profundidade: dois mecanismos > um.
func (s *Server) RequireOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			// Sem Origin em request mutativo: bloqueia.
			writeError(w, http.StatusForbidden, CodeOriginForbidden, "Origin header obrigatório.")
			return
		}
		if !s.originAllowed(origin) {
			writeError(w, http.StatusForbidden, CodeOriginForbidden, "Origin não autorizado.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// CORS responde pre-flight (OPTIONS) e seta os headers necessarios para
// requests com credentials de outro origin (frontend Next.js em dominio
// separado). Allowlist de origins do Server.
func (s *Server) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && s.originAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Vary", "Origin")
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Origin")
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed eh case-sensitive (RFC 6454 §6.1). Trim espacos no allowlist
// pra tolerar config sloppy.
func (s *Server) originAllowed(origin string) bool {
	for _, allowed := range s.allowedOrigins {
		if strings.TrimSpace(allowed) == origin {
			return true
		}
	}
	return false
}

// clearSessionCookie expira o cookie no cliente. Usado em logout e em
// auth failures pra evitar loop de "tenta validar -> falha -> tenta de novo".
func clearSessionCookie(w http.ResponseWriter, secure bool, domain string) {
	c := &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		Domain:   domain, // "" = host-only (dev); "zello.chat" compartilha app+api
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, c)
}

// setSessionCookie grava o cookie HttpOnly com o plaintext.
// Max-Age = 30d = SessionTTL no main package; aqui ficamos com numero
// literal pra api ficar 100% standalone.
func setSessionCookie(w http.ResponseWriter, plaintext string, secure bool, domain string) {
	const maxAgeSeconds = 60 * 60 * 24 * 30 // 30 dias
	c := &http.Cookie{
		Name:     CookieName,
		Value:    plaintext,
		Path:     "/",
		Domain:   domain, // "" = host-only (dev); "zello.chat" compartilha app+api (SSR le em zello.chat, fetch vai pra api.zello.chat)
		MaxAge:   maxAgeSeconds,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
	http.SetCookie(w, c)
}
