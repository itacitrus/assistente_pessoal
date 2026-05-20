package llm

import "strings"

// linkAllowedHosts e a lista canonica de dominios cujo metadata Open
// Graph pode ser fetchado pelo companion. Subdominio direto e aceito
// (m. e www. sao normalizados pelo caller). Dominios fora da lista
// retornam mensagem amigavel sem fetch.
//
// Convencao: hostname sem prefixo (sem www., sem m.). MatchHost normaliza
// antes de checar.
var linkAllowedHosts = map[string]bool{
	// Redes sociais
	"instagram.com": true,
	"facebook.com":  true,
	"youtube.com":   true,
	"youtu.be":      true,
	"tiktok.com":    true,
	"twitter.com":   true,
	"x.com":         true,

	// News majors brasileiros
	"g1.globo.com":        true,
	"globo.com":           true,
	"folha.uol.com.br":    true,
	"estadao.com.br":      true,
	"uol.com.br":          true,
	"noticias.uol.com.br": true,
	"bbc.com":             true,
	"bbc.co.uk":           true,
	"cnnbrasil.com.br":    true,

	// Saude / qualidade de vida
	"drauziovarella.uol.com.br": true,
	"saude.gov.br":              true,
}

// MatchHost retorna true se o host (com ou sem www./m.) bate exatamente
// com algum dominio na allowlist OU eh subdominio direto de um permitido.
//
// Normalizacao:
//   - lowercase
//   - trim spaces
//   - remove prefixos "www." e "m." (1 nivel cada)
//
// Subdominio: aceita "blog.globo.com" se "globo.com" estiver na lista,
// mas NAO aceita "qualquer.coisa.com" porque "coisa.com" nao esta.
//
// Hostnames vazios devolvem false.
func MatchHost(host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	if linkAllowedHosts[h] {
		return true
	}
	// Subdominio direto: ex. blog.globo.com → globo.com
	for allowed := range linkAllowedHosts {
		if strings.HasSuffix(h, "."+allowed) {
			return true
		}
	}
	return false
}

// LinkAllowed eh alias publico de MatchHost — mantido pra retro-compat.
func LinkAllowed(host string) bool { return MatchHost(host) }

// NormalizeHost expoe o normalizador (lowercase + trim + remove www./m.)
// pra callers fora do pacote (handler de comentar_link usa pra preencher
// o LinkPreview.Host).
func NormalizeHost(host string) string { return normalizeHost(host) }

// normalizeHost aplica lowercase, trim e tira www./m..
func normalizeHost(host string) string {
	h := strings.ToLower(strings.TrimSpace(host))
	h = strings.TrimPrefix(h, "www.")
	h = strings.TrimPrefix(h, "m.")
	return h
}
