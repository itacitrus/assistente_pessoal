package main

import (
	"html"
	"regexp"
	"strings"
	"sync"
)

// =========================================================================
// HTML / Open Graph helpers (Fase 4 — comentar_link)
// =========================================================================
//
// Implementacao caseira de parser OG (~50 linhas) — preferida a dependencia
// externa pra evitar adicionar mais um pacote. Tolerante a HTML real:
// atributos em qualquer ordem, com aspas simples/duplas, espacos extras.

// regexpCache eh um lru leve de regexp compilados, indexed por padrao.
// Na pratica usamos so 2-3 padroes — cabe sem evict.
var regexpCache sync.Map // map[string]*regexp.Regexp

// regexpMustCompile retorna o regexp compilado pra padrao s. Cache pra
// evitar custo em loops (parseOpenGraphTags e chamado por meta tag).
func regexpMustCompile(s string) *regexp.Regexp {
	if v, ok := regexpCache.Load(s); ok {
		return v.(*regexp.Regexp)
	}
	r := regexp.MustCompile(s)
	regexpCache.Store(s, r)
	return r
}

// extractMatch retorna o primeiro grupo capturado em pattern, ou "" se
// nao bater.
func extractMatch(s, pattern string) string {
	m := regexpMustCompile(pattern).FindStringSubmatch(s)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

// extractAttr extrai o valor do atributo `name` num fragmento de tag HTML.
// Aceita aspas simples ou duplas. Retorna "" se nao encontrar.
//
// Ex: extractAttr(`<meta property="og:title" content="Hello">`, "content")
//   -> "Hello"
func extractAttr(tag, name string) string {
	// content="..." ou content='...'
	pattern := `(?i)\b` + regexp.QuoteMeta(name) + `\s*=\s*"([^"]*)"`
	if v := extractMatch(tag, pattern); v != "" {
		return v
	}
	pattern2 := `(?i)\b` + regexp.QuoteMeta(name) + `\s*=\s*'([^']*)'`
	return extractMatch(tag, pattern2)
}

// htmlDecode decode entidades HTML simples (&amp;, &quot;, &#39;, &lt;,
// &gt;) — usa pacote stdlib.
func htmlDecode(s string) string {
	return html.UnescapeString(s)
}

// trimToRunes limita s a maxRunes runes (nao bytes — UTF-8 safe).
func trimToRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes])
}

// stripTags remove tags HTML simples — usado quando o description vem com
// HTML embedded.
func stripTags(s string) string {
	re := regexpMustCompile(`<[^>]*>`)
	return strings.TrimSpace(re.ReplaceAllString(s, " "))
}
