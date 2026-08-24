package main

import (
	"regexp"
	"strings"
)

var (
	// **negrito** -> *negrito* (WhatsApp usa UM asterisco).
	mdBoldRe = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	// __sublinhado__ -> _sublinhado_
	mdUnderRe = regexp.MustCompile(`__([^_\n]+)__`)
	// Headings de markdown: WhatsApp nao renderiza, mostra o # literal.
	mdHeadingRe = regexp.MustCompile(`(?m)^[ \t]*#{1,6}[ \t]*`)
)

// sanitizeForWhatsApp converte markdown que o WhatsApp NAO renderiza para o
// equivalente que ele renderiza.
//
// O prompt ja proibe ** e ## desde sempre, mas isso era garantido apenas pela
// obediencia do modelo — a Anthropic obedecia. O DeepSeek emite ** com
// frequencia (confirmado em teste contra a API real: "Hoje: **14:00**"), e o
// WhatsApp mostra o asterisco literal na tela do idoso.
//
// Gap NOVO criado pela migracao. Ultima linha deterministica antes do
// transporte, aplicada nos tres engines.
func sanitizeForWhatsApp(text string) string {
	// ${1} e nao $1: o Go interpreta "$1_" como o grupo de nome "1_", que nao
	// existe, e expande pra vazio. Com "*$1*" passava por acidente (asterisco
	// nao e caractere valido de nome).
	out := mdBoldRe.ReplaceAllString(text, "*${1}*")
	out = mdUnderRe.ReplaceAllString(out, "_${1}_")
	out = mdHeadingRe.ReplaceAllString(out, "")
	// Remove espaco no fim das linhas sem mexer nas quebras.
	lines := strings.Split(out, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}
