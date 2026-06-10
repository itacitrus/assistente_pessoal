package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/liushuangls/go-anthropic/v2"
)

// TurnContext concentra os fatos temporais de UM turno, computados UMA única
// vez no topo de Run/RunProactive. Invariante do contrato de precisão temporal:
// um turno = um now = um fuso = um período. Todas as partes de prompt que
// dependem de tempo (relógio dinâmico, [AGORA], carimbos de histórico) e o
// output-guard leem DESTE struct — nunca chamam time.Now()/GetEventTimezone
// por conta própria. Sem isso, dois relógios amostrados em momentos diferentes
// podem discordar na fronteira de período (11:59→12:00) e o prompt afirma
// "manhã" enquanto o guard cobra "tarde" — coerência por construção, não por
// convenção.
type TurnContext struct {
	Now    time.Time
	Loc    *time.Location // fuso local do usuário (travel-aware via GetEventTimezone)
	Period string         // madrugada | manhã | tarde | noite (computado de Now em Loc)

	// Continuação: o bot falou há pouco com este usuário? (só preenchido p/ idoso)
	ContinuationOK     bool // true quando há fala recente do bot dentro da janela
	ContinuationAgeSec float64

	// Lembretes de remédio que ainda vão disparar HOJE (só preenchido p/ idoso).
	UpcomingMedsToday []upcomingReminder
}

// continuationWindow eh por quanto tempo, apos a ultima fala do bot, ainda
// tratamos o turno como "mesma conversa em andamento" — dentro disso o
// companheiro nao deve recomecar com saudacao. Cobre com folga o caso do idoso
// mandar a mensagem em pedacos (elderBufferDelay + tempo de geracao).
const continuationWindow = 6 * time.Minute

// newTurnContext computa o TurnContext do turno. Loc resolve via
// GetEventTimezone (honra período de viagem; fallback interno BRT). Campos de
// companion (continuação, lembretes restantes) só são computados para idoso —
// para os demais ficam zerados e o renderer [AGORA] nem é chamado.
func (a *Agent) newTurnContext(user *User, now time.Time) *TurnContext {
	tc := &TurnContext{Now: now, Loc: BRT()}
	if user != nil && a.db != nil {
		if loc := a.db.GetEventTimezone(user.ID, now); loc != nil {
			tc.Loc = loc
		}
	}
	tc.Period = periodOfDay(now.In(tc.Loc).Hour())

	if user != nil && user.Type == UserTypeIdoso && a.db != nil {
		if age, ok, err := a.db.SecondsSinceLastAssistantMessage(user.ID); err == nil && ok &&
			age <= continuationWindow.Seconds() {
			tc.ContinuationOK = true
			tc.ContinuationAgeSec = age
		}
		tc.UpcomingMedsToday = a.upcomingMedRemindersToday(user, now)
	}
	return tc
}

// periodOfDay mapeia hora local → período do dia em PT-BR. Fonte única da
// verdade para prompt E guard (ambos leem TurnContext.Period).
//
//	0-4   madrugada
//	5-11  manhã
//	12-17 tarde
//	18-23 noite
func periodOfDay(hour int) string {
	switch {
	case hour >= 0 && hour <= 4:
		return "madrugada"
	case hour >= 5 && hour <= 11:
		return "manhã"
	case hour >= 12 && hour <= 17:
		return "tarde"
	default:
		return "noite"
	}
}

// greetingForPeriod devolve a saudação de ABERTURA correta para o período.
// Madrugada não tem saudação de abertura própria em PT-BR — devolve "" e o
// renderer trata (acolhimento sem cumprimento formal).
func greetingForPeriod(period string) string {
	switch period {
	case "manhã":
		return "bom dia"
	case "tarde":
		return "boa tarde"
	case "noite":
		return "boa noite"
	}
	return ""
}

// weekdaysPTFull é o nome completo do dia da semana em minúsculas, para a
// linha de relógio do prompt dinâmico ("segunda-feira"). weekdaysPT (formatter)
// segue como a forma capitalizada-curta dos artefatos de evento.
var weekdaysPTFull = map[time.Weekday]string{
	time.Sunday:    "domingo",
	time.Monday:    "segunda-feira",
	time.Tuesday:   "terça-feira",
	time.Wednesday: "quarta-feira",
	time.Thursday:  "quinta-feira",
	time.Friday:    "sexta-feira",
	time.Saturday:  "sábado",
}

// renderAgoraPart é o renderer puro do bloco [AGORA] do companion — a fusão
// determinística de período-do-dia + continuação + último-contato-do-dia num
// ÚNICO bloco que não pode se autocontradizer (antes eram três strings mantidas
// separadamente "concordando" por escopo manual — o hazard que gerou o
// "boa noite às 7h"). Redação positiva-primeiro: dizemos qual É a saudação
// certa; a proibição é uma cláusula curta, sem lista enumerada de tokens
// (pink-elephant em modelo pequeno).
func renderAgoraPart(tc *TurnContext, userFirstName string) string {
	local := tc.Now.In(tc.Loc)
	var b strings.Builder
	fmt.Fprintf(&b, "[AGORA] Agora é %s — %s, %02d:%02d (hora local dele).\n",
		strings.ToUpper(tc.Period), weekdaysPTFull[local.Weekday()], local.Hour(), local.Minute())

	switch tc.Period {
	case "madrugada":
		b.WriteString("É de madrugada: se ele estiver acordado a essa hora, acolha com calma, sem cumprimento formal de abertura. " +
			"Despedida de fim de noite (\"boa noite\", \"descanse bem\") é natural agora. NUNCA cumprimente com \"bom dia\" ou \"boa tarde\".\n")
	case "noite":
		b.WriteString("A saudação certa agora é \"boa noite\". ")
	default: // manhã / tarde
		fmt.Fprintf(&b, "A saudação certa agora é %q. ", greetingForPeriod(tc.Period))
		b.WriteString("Despedida de fim de dia (\"boa noite\", \"descanse bem\", \"até amanhã\") NÃO combina com este horário — encerre de forma aberta (\"estou por aqui\", \"qualquer coisa me chama\").\n")
	}

	if tc.ContinuationOK {
		b.WriteString("Você acabou de falar com " + userFirstName + " há instantes, NESTA mesma conversa: " +
			"NÃO recomece com saudação nem repita uma pergunta que você já fez agora. " +
			"Responda só o que ele mandou por último, curto e natural — como UMA pessoa que continua a conversa.\n")
	}

	if len(tc.UpcomingMedsToday) > 0 {
		b.WriteString("Ainda há lembrete(s) de remédio HOJE: ")
		for i, r := range tc.UpcomingMedsToday {
			if i > 0 {
				b.WriteString("; ")
			}
			b.WriteString(r.at.In(tc.Loc).Format("15:04"))
			b.WriteString(" (")
			b.WriteString(strings.Join(r.names, ", "))
			b.WriteString(")")
		}
		b.WriteString(". Portanto este NÃO é o último contato do dia — não se despeça como se fosse.\n")
	} else if tc.Period == "noite" {
		b.WriteString("Não há mais lembretes de remédio hoje. Se a conversa estiver se encerrando e fizer sentido, " +
			"pode se despedir desejando boa noite/bom descanso.\n")
	}

	return strings.TrimRight(b.String(), "\n")
}

// appendCompanionAgoraPart injeta o bloco [AGORA] no prompt do idoso. No-op
// para não-idosos (o operacional já recebe período pela linha de relógio do
// prompt dinâmico). Parte dinâmica — nunca cacheada.
func (a *Agent) appendCompanionAgoraPart(parts []anthropic.MessageSystemPart, user *User, tc *TurnContext) []anthropic.MessageSystemPart {
	if user == nil || user.Type != UserTypeIdoso || tc == nil {
		return parts
	}
	return append(parts, anthropic.MessageSystemPart{Type: "text", Text: renderAgoraPart(tc, firstName(user.Name))})
}

// leadingStampRe casa um carimbo de histórico ecoado no INÍCIO da resposta —
// "[hoje 09:12] ", "[ontem 14:00] ", "[amanhã 08:00] ", "[ter 03/06 14:00] ".
// Os carimbos são metadados que o serializador injeta no contexto; modelos
// (DeepSeek em especial) imitam prefixos estruturais das próprias falas
// anteriores, e persistOutbound gravaria o eco verbatim — realimentando o
// padrão a cada turno. Strip determinístico antes do envio fecha o loop.
var leadingStampRe = regexp.MustCompile(`^\s*\[(?:hoje|ontem|amanhã|seg|ter|qua|qui|sex|sáb|dom)[^\]]{0,20}\]\s*`)

// stripLeadingStamp remove carimbos de histórico ecoados no início do texto
// (inclusive repetidos: "[hoje 10:00] [hoje 09:55] oi" → "oi").
func stripLeadingStamp(s string) string {
	for {
		out := leadingStampRe.ReplaceAllString(s, "")
		if out == s {
			return s
		}
		s = out
	}
}
