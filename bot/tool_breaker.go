package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ToolBreaker corta repeticao da MESMA tool com os MESMOS argumentos dentro
// de um turno.
//
// Loop de call identica e o modo de falha agentico mais documentado do
// DeepSeek: o modelo chega a verbalizar que vai parar e reemite a mesma call
// assim mesmo. Sem corte client-side isso queima token e trava o turno do
// usuario — que num bot de medicacao significa lembrete nao entregue.
type ToolBreaker struct {
	maxRepeat int
	seen      map[string]int
	tripped   bool
}

// NewToolBreaker cria o breaker com o teto de repeticoes por (tool, args).
func NewToolBreaker(maxRepeat int) *ToolBreaker {
	if maxRepeat < 1 {
		maxRepeat = 1
	}
	return &ToolBreaker{maxRepeat: maxRepeat, seen: map[string]int{}}
}

// Allow registra a chamada e devolve false quando ela ja passou do teto.
func (b *ToolBreaker) Allow(name string, args json.RawMessage) bool {
	key := name + "|" + normalizeToolArgs(args)
	b.seen[key]++
	if b.seen[key] > b.maxRepeat {
		b.tripped = true
		return false
	}
	return true
}

// Tripped diz se o breaker cortou alguma chamada neste turno.
func (b *ToolBreaker) Tripped() bool { return b.tripped }

// normalizeToolArgs devolve uma forma canonica do JSON de argumentos, com as
// chaves ordenadas e sem espaco — senao a mesma call com as chaves em outra
// ordem escapa do dedupe. JSON invalido cai no texto cru (que ja e sinal de
// problema, mas nao pode derrubar o loop).
func normalizeToolArgs(args json.RawMessage) string {
	var v any
	if err := json.Unmarshal(args, &v); err != nil {
		return strings.Join(strings.Fields(string(args)), "")
	}
	return canonicalJSON(v)
}

// canonicalJSON serializa com ordem estavel de chaves, recursivamente.
func canonicalJSON(v any) string {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		sb.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "%q:%s", k, canonicalJSON(t[k]))
		}
		sb.WriteByte('}')
		return sb.String()
	case []any:
		var sb strings.Builder
		sb.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(canonicalJSON(e))
		}
		sb.WriteByte(']')
		return sb.String()
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}
