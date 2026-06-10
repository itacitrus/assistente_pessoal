package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Output-guard determinístico (contrato P1): estágio pós-geração/pré-envio que
// verifica invariantes sobre fatos que o Go já tem (período do TurnContext,
// toolsCalled, payloads de tool) — generalização do padrão provado de
// reconcileTakenAfterTurn. Cobre os TRÊS caminhos LLM→usuário: Run,
// runCompanion e RunProactive.
//
// Invariantes:
//
//	I1  saudação/despedida não pode contradizer o período computado (enforce)
//	I2a promessa futura de aviso sem lastro (tool deste turno ou cron vigente) (enforce)
//	I2b claim perfectivo ("marquei/anotei") sem tool correspondente (AUDIT-ONLY)
//	I3  narração de mecânica de transporte não provocada (strip silencioso)
//	I4  display de OK_CRIADO deve aparecer verbatim na resposta (enforce)
//	I5  carimbo de histórico ecoado no início (strip; SEMPRE ativo, todo modo)
//
// Ações (enforce): UMA reescrita tool-less com feedback → re-checagem → strip
// determinístico → safe-template período-correto. O guard nunca devolve vazio
// e NUNCA re-executa tools (regenerate recebe só texto). Em modo log, audita
// sem alterar (exceto I5). Violação real é rara — o grounding de P0 existe
// para que este guard quase nunca dispare.

type guardMode string

const (
	guardOff     guardMode = "off"
	guardLog     guardMode = "log"
	guardEnforce guardMode = "enforce"
)

// regenerateFn faz UMA chamada de reescrita SEM tools sobre o transcript do
// turno (o caller fecha sobre messages/systemParts/provider). O texto violador
// já está no transcript como último turno assistant; feedback vira um turno
// user "[CORREÇÃO DO SISTEMA] ...". Jamais persiste nada.
type regenerateFn func(feedback string) (string, error)

type guardInput struct {
	User        *User
	TC          *TurnContext
	UserMsg     string // turno atual do usuário ("" no proativo)
	ToolsCalled []string
	ToolResults []string
	Engine      string // "operational" | "companion" | "proactive"
}

type guardViolation struct {
	Invariant string
	Detail    string
	Sentence  string // sentença ofensora (alvo do strip)
	Feedback  string // instrução de correção pro regenerate
}

const guardRegenLatencyBudget = 20 * time.Second
const guardMinTextLen = 8

// guardOutput aplica as invariantes e devolve o texto final (nunca vazio) e a
// ação tomada ("none", "log", "regenerated", "stripped", "template").
func (a *Agent) guardOutput(in guardInput, text string, regen regenerateFn) (string, string) {
	// I5 — sempre ativo, em qualquer modo: carimbo ecoado nunca chega ao
	// usuário (persistOutbound gravaria verbatim e realimentaria o padrão).
	cleaned := stripLeadingStamp(text)
	if cleaned != text {
		a.auditGuard(in, "I5", "carimbo ecoado removido", "strip")
	}
	text = cleaned

	mode := a.guardModeFor(in.User)
	if mode == guardOff || strings.TrimSpace(text) == "" {
		return text, "none"
	}

	enforceable, strippable := a.detectViolations(in, text)
	if len(enforceable) == 0 && len(strippable) == 0 {
		return text, "none"
	}
	for _, v := range append(append([]guardViolation{}, enforceable...), strippable...) {
		a.auditGuard(in, v.Invariant, v.Detail, string(mode))
	}
	if mode == guardLog {
		return text, "log"
	}

	action := "none"

	// I3 (strip-class): remoção silenciosa da sentença, sem regenerate.
	if len(strippable) > 0 {
		text = stripSentences(text, strippable)
		action = "stripped"
	}

	if len(enforceable) > 0 {
		// Cascata S1: regenerate 1× (tool-less) → strip → safe-template.
		regenerated := false
		if regen != nil && in.TC != nil && time.Since(in.TC.Now) < guardRegenLatencyBudget {
			var fb []string
			for _, v := range enforceable {
				fb = append(fb, v.Feedback)
			}
			if rewritten, err := regen(strings.Join(fb, " ")); err == nil {
				rewritten = stripLeadingStamp(rewritten)
				if e2, s2 := a.detectViolations(in, rewritten); len(e2) == 0 && len(s2) == 0 && strings.TrimSpace(rewritten) != "" {
					log.Printf("[%s] guard: violação corrigida via regenerate (%s)", in.User.Name, in.Engine)
					return rewritten, "regenerated"
				}
				regenerated = true
			} else {
				log.Printf("[%s] guard: regenerate falhou: %v", in.User.Name, err)
			}
		}
		_ = regenerated

		// Strip determinístico das sentenças ofensoras.
		text = stripSentences(text, enforceable)
		action = "stripped"

		// Terminal: vazio, curto demais ou ainda violando → template seguro.
		if eRest, _ := a.detectViolations(in, text); strings.TrimSpace(text) == "" ||
			len(strings.TrimSpace(text)) < guardMinTextLen || len(eRest) > 0 {
			text = guardSafeTemplate(in.TC, firstName(in.User.Name))
			action = "template"
		}
	}

	if strings.TrimSpace(text) == "" {
		text = guardSafeTemplate(in.TC, firstName(in.User.Name))
		action = "template"
	}
	return text, action
}

// guardModeFor resolve o modo efetivo: OUTPUT_GUARD_MODE (default log) com
// canário por telefone (GUARD_ENFORCE_PHONES promove log→enforce).
func (a *Agent) guardModeFor(user *User) guardMode {
	mode := guardLog
	if a.cfg != nil && a.cfg.OutputGuardMode != "" {
		mode = guardMode(a.cfg.OutputGuardMode)
	}
	if mode == guardLog && user != nil && a.cfg != nil {
		for _, p := range a.cfg.GuardEnforcePhones {
			if p == user.PhoneNumber {
				return guardEnforce
			}
		}
	}
	return mode
}

func (a *Agent) auditGuard(in guardInput, invariant, detail, mode string) {
	log.Printf("[%s] guard_violation %s (%s, mode=%s): %s", in.User.Name, invariant, in.Engine, mode, detail)
	if a.audit != nil {
		a.audit.Log(in.User.ID, "guard_violation", invariant,
			fmt.Sprintf("engine=%s|mode=%s|%s", in.Engine, mode, detail))
	}
}

// ---------------------------------------------------------------------------
// Detecção
// ---------------------------------------------------------------------------

// detectViolations devolve (enforce-class, strip-class). I2b é audit-only e é
// auditada aqui dentro (não entra em nenhuma das listas de ação).
func (a *Agent) detectViolations(in guardInput, text string) (enforceable, strippable []guardViolation) {
	if in.TC != nil {
		enforceable = append(enforceable, detectI1PeriodMismatch(text, in.TC.Period, in.UserMsg)...)
		enforceable = append(enforceable, detectI2aUngroundedPromise(text, in.ToolsCalled, a.groundedPromiseTimes(in))...)
	}
	enforceable = append(enforceable, detectI4DisplayCitation(text, in.ToolResults)...)
	strippable = append(strippable, detectI3TransportNarration(text, in.UserMsg)...)

	// I2b — audit-only no launch (promoção a enforce só com FP<2% nos logs).
	for _, v := range detectI2bUngroundedClaim(text, in.ToolsCalled) {
		a.auditGuard(in, v.Invariant, v.Detail, "audit-only")
	}
	return enforceable, strippable
}

var moonEmoji = "🌙"

var greetingTokens = []string{"bom dia", "boa tarde", "boa noite"}

// detectI1PeriodMismatch — sinais de ALTA precisão apenas:
//   - 🌙 fora de noite/madrugada;
//   - par de saudação errado em POSIÇÃO de saudação (primeira/última sentença);
//   - madrugada herda as despedidas da noite ("boa noite" à 00:30 é legítimo);
//   - "descanse bem"/"até amanhã" sozinhos NUNCA disparam;
//   - supressão de espelhamento: se o USUÁRIO usou a saudação neste turno, a
//     regra ESPELHE do companion tem precedência e o guard não age.
func detectI1PeriodMismatch(text, period, userMsg string) []guardViolation {
	var out []guardViolation
	lower := strings.ToLower(text)
	userLower := strings.ToLower(userMsg)

	nightish := period == "noite" || period == "madrugada"
	if strings.Contains(text, moonEmoji) && !nightish && !strings.Contains(userMsg, moonEmoji) {
		out = append(out, guardViolation{
			Invariant: "I1",
			Detail:    fmt.Sprintf("emoji 🌙 em período %s", period),
			Sentence:  sentenceContaining(text, moonEmoji),
			Feedback:  fmt.Sprintf("Sua resposta usou o emoji de lua, mas agora é %s — remova qualquer despedida de fim de noite.", period),
		})
	}

	correct := greetingForPeriod(period)
	// Posições de saudação = primeira/última sentença COM letras: um fragmento
	// só-emoji no fim ("... descanse bem! 🌙") não pode "esconder" a despedida
	// real da posição final.
	var lettered []string
	for _, s := range splitSentences(lower) {
		if strings.ContainsFunc(s, unicode.IsLetter) {
			lettered = append(lettered, s)
		}
	}
	if len(lettered) == 0 {
		return out
	}
	positions := []string{lettered[0]}
	if len(lettered) > 1 {
		positions = append(positions, lettered[len(lettered)-1])
	}
	for _, g := range greetingTokens {
		if g == correct {
			continue
		}
		// Madrugada herda despedidas da noite.
		if period == "madrugada" && g == "boa noite" {
			continue
		}
		// Espelhamento: usuário usou a saudação → companion espelha; não agir.
		if strings.Contains(userLower, g) {
			continue
		}
		for _, s := range positions {
			if strings.Contains(s, g) {
				out = append(out, guardViolation{
					Invariant: "I1",
					Detail:    fmt.Sprintf("%q em período %s", g, period),
					Sentence:  sentenceContaining(text, g),
					Feedback:  fmt.Sprintf("Sua resposta dizia %q, mas agora é %s — a saudação certa é %q.", g, period, correct),
				})
				break
			}
		}
	}
	return out
}

var promiseRe = regexp.MustCompile(`(?i)\b(te (lembro|aviso|chamo)|pode deixar|deixa comigo|n[ãa]o (vou )?esquec)`)
var timeRefRe = regexp.MustCompile(`(?i)(\b\d{1,2}[:h]\d{2}\b|\b\d{1,2}\s*h(oras)?\b|amanh[ãa]|mais tarde|daqui a)`)
var clockRe = regexp.MustCompile(`\b(\d{1,2})[:h](\d{2})\b`)

// promiseGroundingTools: chamou uma destas neste turno → a promessa tem lastro.
var promiseGroundingTools = map[string]bool{
	"agendar_lembrete":           true,
	"criar_evento":               true,
	"criar_evento_outro_usuario": true,
}

// detectI2aUngroundedPromise — promessa FUTURA de aviso/lembrete numa sentença
// não-interrogativa com referência temporal, sem tool de lastro neste turno e
// com horário que não bate com nenhum mecanismo vigente (remédios de hoje ∪
// lembretes pendentes). Promessa lastreada em cron existente é VERDADEIRA
// ("te lembro às 19h" com remédio às 19h) e passa.
func detectI2aUngroundedPromise(text string, toolsCalled []string, groundedTimes map[string]bool) []guardViolation {
	for _, t := range toolsCalled {
		if promiseGroundingTools[t] {
			return nil
		}
	}
	var out []guardViolation
	for _, s := range splitSentences(text) {
		if isInterrogative(s) {
			continue
		}
		if !promiseRe.MatchString(s) || !timeRefRe.MatchString(s) {
			continue
		}
		// Horário explícito que bate com lastro vigente → promessa verdadeira.
		if m := clockRe.FindStringSubmatch(s); m != nil {
			hhmm := fmt.Sprintf("%02s:%s", m[1], m[2])
			if groundedTimes[hhmm] {
				continue
			}
		}
		out = append(out, guardViolation{
			Invariant: "I2a",
			Detail:    fmt.Sprintf("promessa sem lastro: %.80q", s),
			Sentence:  s,
			Feedback: "Você prometeu avisar/lembrar num horário, mas NENHUM lembrete foi agendado de verdade. " +
				"Não prometa aviso futuro sem a ferramenta agendar_lembrete; reescreva sem essa promessa (ou diga que pode agendar se a pessoa quiser).",
		})
	}
	return out
}

// claimToTools — tabela claim perfectivo → tools que o lastreiam (I2b).
var claimToTools = []struct {
	re    *regexp.Regexp
	tools []string
}{
	{regexp.MustCompile(`(?i)\b(marquei|agendei|criei)\b`), []string{"criar_evento", "criar_evento_outro_usuario", "agendar_lembrete"}},
	{regexp.MustCompile(`(?i)\b(anotei|salvei|registrei)\b`), []string{"salvar_memoria", "marcar_remedio_tomado", "cadastrar_medicamento", "agendar_lembrete"}},
	{regexp.MustCompile(`(?i)\bcadastrei\b`), []string{"cadastrar_medicamento"}},
	{regexp.MustCompile(`(?i)\b(cancelei|desmarquei)\b`), []string{"cancelar_evento", "cancelar_medicamento", "cancelar_lembrete", "cancelar_viagem"}},
}

var pastRefRe = regexp.MustCompile(`(?i)\b(ontem|j[áa]\b|como (te )?falei|semana passada|m[êe]s passado|outro dia|na [ée]poca)`)
var quotedSpanRe = regexp.MustCompile(`"[^"]*"|“[^”]*”`)

// detectI2bUngroundedClaim — claim perfectivo de ação sem a tool
// correspondente NESTE turno. AUDIT-ONLY: recall verdadeiro ("marquei sim,
// foi ontem") é a forma normal do pretérito em PT-BR; regenerate aqui
// corromperia fatos verdadeiros. Exclusões: interrogativas, spans citados,
// marcadores de referência passada.
func detectI2bUngroundedClaim(text string, toolsCalled []string) []guardViolation {
	called := map[string]bool{}
	for _, t := range toolsCalled {
		called[t] = true
	}
	var out []guardViolation
	for _, s := range splitSentences(text) {
		if isInterrogative(s) || pastRefRe.MatchString(s) {
			continue
		}
		clean := quotedSpanRe.ReplaceAllString(s, "")
		for _, c := range claimToTools {
			if !c.re.MatchString(clean) {
				continue
			}
			grounded := false
			for _, t := range c.tools {
				if called[t] {
					grounded = true
					break
				}
			}
			if !grounded {
				out = append(out, guardViolation{
					Invariant: "I2b",
					Detail:    fmt.Sprintf("claim sem tool: %.80q (tools=%v)", s, toolsCalled),
				})
			}
		}
	}
	return out
}

// transportNarrationRe — frases sobre a mecânica de entrega que o modelo
// jamais deveria narrar espontaneamente (artefato de transporte ≠ conversa).
var transportNarrationRe = regexp.MustCompile(`(?i)(veio em dobro|em duplicidade|mensagem (veio |chegou )?(duplicada|repetida|em dobro)|(recebi|chegou) (a mesma mensagem|duas vezes)|mandou (a mesma mensagem|duas vezes))`)

// userRaisedDupRe — o usuário MESMO levantou a duplicação; aí falar dela é a
// resposta correta (não ignorar o que a pessoa perguntou).
var userRaisedDupRe = regexp.MustCompile(`(?i)(duas vezes|em dobro|repetid|duplicad|de novo sem querer|mandei errado)`)

func detectI3TransportNarration(text, userMsg string) []guardViolation {
	if userRaisedDupRe.MatchString(userMsg) {
		return nil
	}
	var out []guardViolation
	for _, s := range splitSentences(text) {
		if transportNarrationRe.MatchString(s) {
			out = append(out, guardViolation{
				Invariant: "I3",
				Detail:    fmt.Sprintf("narração de transporte: %.80q", s),
				Sentence:  s,
			})
		}
	}
	return out
}

// detectI4DisplayCitation — quando criar_evento/agendar_lembrete retornaram
// um payload display=, a resposta DEVE conter o display verbatim (REGRA DE
// CITAÇÃO, agora enforçada): matching de substring exato, zero NLP, zero
// falso positivo.
func detectI4DisplayCitation(text string, toolResults []string) []guardViolation {
	var out []guardViolation
	for _, r := range toolResults {
		display, ok := strings.CutPrefix(r, "OK_CRIADO|display=")
		if !ok {
			display, ok = strings.CutPrefix(r, "LEMBRETE_SALVO|display=")
		}
		if !ok {
			continue
		}
		// Nota de ajuste no fim do payload não faz parte do display verbatim.
		if i := strings.Index(display, "\n(Esse horário já passou"); i >= 0 {
			display = display[:i]
		}
		display = strings.TrimSpace(display)
		if display == "" || strings.Contains(text, display) {
			continue
		}
		out = append(out, guardViolation{
			Invariant: "I4",
			Detail:    fmt.Sprintf("display de OK_CRIADO ausente: %.80q", display),
			Sentence:  "", // não há sentença a remover; o fix é citar
			// Display RAW (sem %q): o modelo precisa reproduzi-lo verbatim —
			// um \n escapado o induziria a escrever a sequência literal.
			Feedback: "Sua resposta não citou o resultado oficial do evento criado. " +
				"Inclua este texto VERBATIM, sem alterar data/hora:\n" + display,
		})
	}
	return out
}

// groundedPromiseTimes — horários (HH:MM, fuso local) em que JÁ existe
// mecanismo de aviso para este usuário: lembretes de remédio restantes hoje
// (TurnContext) ∪ lembretes pontuais pendentes (P2). Promessa nesses horários
// é verdadeira.
func (a *Agent) groundedPromiseTimes(in guardInput) map[string]bool {
	out := map[string]bool{}
	if in.TC == nil {
		return out
	}
	for _, r := range in.TC.UpcomingMedsToday {
		out[r.at.In(in.TC.Loc).Format("15:04")] = true
	}
	if a.db != nil && in.User != nil {
		if pend, err := a.db.ListPendingReminders(in.User.ID); err == nil {
			for _, p := range pend {
				out[p.FireAt.In(in.TC.Loc).Format("15:04")] = true
			}
		}
	}
	return out
}

// guardSafeTemplate — terminal determinístico: resposta período-correta,
// curta e natural, construída em Go. Nunca vazia, nunca um non-sequitur de
// protocolo. Usada só quando regenerate E strip falharam.
func guardSafeTemplate(tc *TurnContext, first string) string {
	period := ""
	if tc != nil {
		period = tc.Period
	}
	switch period {
	case "manhã":
		return "Bom dia, " + first + "! Estou por aqui se precisar."
	case "tarde":
		return "Boa tarde, " + first + "! Estou por aqui se precisar."
	case "noite":
		return "Boa noite, " + first + "! Estou por aqui se precisar."
	default:
		return first + ", estou por aqui se precisar, viu?"
	}
}

// ---------------------------------------------------------------------------
// Texto utilitário
// ---------------------------------------------------------------------------

// splitSentences quebra em sentenças por pontuação final ou quebra de linha.
// Conservador de propósito: melhor sentença "grande demais" que strip que
// mutila português no meio (público inclui idosos confusos).
func splitSentences(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" {
			out = append(out, s)
		}
		b.Reset()
	}
	for _, r := range text {
		switch r {
		case '\n':
			flush()
		case '.', '!', '?', '…':
			b.WriteRune(r)
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return out
}

// sentenceContaining devolve a sentença (forma original) que contém needle.
func sentenceContaining(text, needle string) string {
	needleLower := strings.ToLower(needle)
	for _, s := range splitSentences(text) {
		if strings.Contains(strings.ToLower(s), needleLower) {
			return s
		}
	}
	return ""
}

// stripSentences remove as sentenças ofensoras preservando o resto.
func stripSentences(text string, violations []guardViolation) string {
	targets := map[string]bool{}
	for _, v := range violations {
		if s := strings.TrimSpace(v.Sentence); s != "" {
			targets[strings.ToLower(s)] = true
		}
	}
	if len(targets) == 0 {
		return text
	}
	var kept []string
	for _, s := range splitSentences(text) {
		if targets[strings.ToLower(s)] {
			continue
		}
		kept = append(kept, s)
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

func isInterrogative(s string) bool {
	if strings.Contains(s, "?") {
		return true
	}
	lower := strings.ToLower(s)
	for _, marker := range []string{"quer que", "posso ", "se você quiser", "se quiser"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}
