package main

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func guardAgentEnforce() *Agent {
	return &Agent{cfg: &Config{OutputGuardMode: "enforce"}}
}

func guardAgentLog() *Agent {
	return &Agent{cfg: &Config{OutputGuardMode: "log"}}
}

func tcAt(hour, min int) *TurnContext {
	loc := BRT()
	now := time.Date(2026, 6, 9, hour, min, 0, 0, loc)
	return &TurnContext{Now: now, Loc: loc, Period: periodOfDay(hour)}
}

// tcAtFresh devolve um TurnContext "recém-iniciado" (Now ~ agora) para os
// testes de regenerate — o orçamento de latência compara contra wall clock.
func tcAtFresh(period string) *TurnContext {
	return &TurnContext{Now: time.Now(), Loc: BRT(), Period: period}
}

var guardUser = &User{ID: 1, Name: "Simone Alves", PhoneNumber: "5561900000000", Type: UserTypeIdoso}

// ---------------------------------------------------------------------------
// I1 — saudação × período
// ---------------------------------------------------------------------------

// O caso literal do B3: "Boa noite, descanse bem 🌙" às 07:20.
func TestGuard_NightGreetingAtMorningStripped(t *testing.T) {
	a := guardAgentEnforce()
	tc := tcAtFresh("manhã")

	// Sem regen disponível → strip; única sentença era a ofensora → template.
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "Obrigada", Engine: "companion"},
		"Boa noite, Simone — descanse bem! 🌙", nil)
	if action != "template" {
		t.Fatalf("esperava template (única sentença era ofensora), got action=%s out=%q", action, out)
	}
	if !strings.Contains(out, "Bom dia") {
		t.Errorf("template deveria ser período-correto (manhã), got %q", out)
	}
	if strings.Contains(out, "🌙") || strings.Contains(strings.ToLower(out), "boa noite") {
		t.Errorf("saída não pode manter a violação, got %q", out)
	}

	// Com sentença legítima junto → strip remove só a ofensora.
	out, action = a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "Obrigada", Engine: "companion"},
		"Anastrozol e Pristiq anotados. Boa noite, Simone — descanse bem! 🌙", nil)
	if action != "stripped" {
		t.Fatalf("esperava stripped, got %s (%q)", action, out)
	}
	if !strings.Contains(out, "anotados") || strings.Contains(strings.ToLower(out), "boa noite") {
		t.Errorf("strip deveria preservar a parte legítima e remover a despedida, got %q", out)
	}
}

func TestGuard_RegenerateFixesViolation(t *testing.T) {
	a := guardAgentEnforce()
	tc := tcAtFresh("manhã")
	regenCalls := 0
	regen := func(feedback string) (string, error) {
		regenCalls++
		if !strings.Contains(feedback, "manhã") {
			t.Errorf("feedback deveria citar o período, got %q", feedback)
		}
		return "Remédios anotados! Bom dia, Simone — qualquer coisa me chama.", nil
	}
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "Obrigada", Engine: "companion"},
		"Tudo anotado. Boa noite, descanse bem!", regen)
	if action != "regenerated" || regenCalls != 1 {
		t.Fatalf("esperava regenerated com 1 chamada, got action=%s calls=%d", action, regenCalls)
	}
	if !strings.Contains(out, "Bom dia") {
		t.Errorf("reescrita período-correta deveria ser aceita, got %q", out)
	}

	// Regenerate que AINDA viola → cai pra strip (nunca segunda chamada).
	regenCalls = 0
	badRegen := func(string) (string, error) {
		regenCalls++
		return "Boa noite de novo!", nil
	}
	_, action = a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "Obrigada", Engine: "companion"},
		"Anotado. Boa noite, descanse bem!", badRegen)
	if regenCalls != 1 {
		t.Fatalf("regenerate deve ser chamado NO MÁXIMO 1×, got %d", regenCalls)
	}
	if action != "stripped" && action != "template" {
		t.Fatalf("reescrita violadora deveria cair em strip/template, got %s", action)
	}

	// Regenerate com erro → cascata segue para strip sem propagar.
	_, action = a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "Obrigada", Engine: "companion"},
		"Anotado. Boa noite!", func(string) (string, error) { return "", errors.New("api down") })
	if action != "stripped" && action != "template" {
		t.Fatalf("erro no regenerate deveria degradar pra strip/template, got %s", action)
	}
}

// "descanse bem" SOZINHO nunca dispara (soneca pós-almoço é empatia correta).
func TestGuard_NapWishAtAfternoonNotStripped(t *testing.T) {
	a := guardAgentEnforce()
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "vou deitar um pouco depois do almoço", Engine: "companion"},
		"Boa ideia! Descanse bem, depois me conta como foi o almoço.", nil)
	if action != "none" {
		t.Fatalf("'descanse bem' sozinho não pode disparar I1, got action=%s out=%q", action, out)
	}
}

// Madrugada herda as despedidas da noite (insone à 00:30).
func TestGuard_BoaNoiteAtMadrugadaAllowed(t *testing.T) {
	a := guardAgentEnforce()
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("madrugada"), UserMsg: "vou tentar dormir agora", Engine: "companion"},
		"Boa noite, Simone — descanse bem! 🌙", nil)
	if action != "none" {
		t.Fatalf("boa noite à madrugada é legítimo, got action=%s out=%q", action, out)
	}
	_ = out
}

// Supressão de espelhamento: usuário despediu com "boa noite" de manhã —
// regra ESPELHE precede; guard não age.
func TestGuard_MirroredFarewellNotStripped(t *testing.T) {
	a := guardAgentEnforce()
	_, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "até, boa noite", Engine: "companion"},
		"Até mais, Simone! Boa noite pra você também.", nil)
	if action != "none" {
		t.Fatalf("espelhamento de despedida do usuário não pode ser punido, got %s", action)
	}
}

// "bom dia" à noite é o par invertido — também viola.
func TestGuard_MorningGreetingAtNight(t *testing.T) {
	a := guardAgentEnforce()
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("noite"), UserMsg: "oi", Engine: "companion"},
		"Bom dia, Simone! Como passou a noite?", nil)
	if action == "none" {
		t.Fatalf("bom dia à noite deveria violar I1, got none (%q)", out)
	}
}

// ---------------------------------------------------------------------------
// I2a — promessa futura sem lastro (o caso 23:58 do B4)
// ---------------------------------------------------------------------------

func TestReminderPromiseSafeguard_DetectsUngroundedPromise(t *testing.T) {
	a := guardAgentEnforce()
	tc := tcAtFresh("noite")

	// O caso literal do B4: promete 23:58 sem nenhuma tool.
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "Me lembre novamente às 23:58h", Engine: "companion"},
		"Pronto, te lembro às 23:58h sobre o aniversário do Clóvis.", nil)
	if action == "none" {
		t.Fatalf("promessa sem lastro deveria violar I2a, got none (%q)", out)
	}
	if strings.Contains(out, "23:58") && strings.Contains(out, "te lembro") {
		t.Errorf("a promessa falsa não pode sobreviver em enforce, got %q", out)
	}

	// Com agendar_lembrete chamada neste turno → promessa verdadeira, passa.
	_, action = a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "me lembra às 23:58",
		ToolsCalled: []string{"agendar_lembrete"}, Engine: "companion"},
		"Pronto, te lembro às 23:58 sobre o aniversário do Clóvis.", nil)
	if action != "none" {
		t.Fatalf("promessa lastreada em agendar_lembrete deveria passar, got %s", action)
	}

	// Lastreada em cron de remédio vigente (19:00 em UpcomingMedsToday) → passa.
	tcMed := tcAtFresh("tarde")
	tcMed.UpcomingMedsToday = []upcomingReminder{{at: time.Date(2026, 6, 9, 19, 0, 0, 0, BRT()), names: []string{"Losartana"}}}
	_, action = a.guardOutput(guardInput{User: guardUser, TC: tcMed, UserMsg: "você me lembra do remédio?", Engine: "companion"},
		"Fica tranquila, te lembro às 19:00 do Losartana.", nil)
	if action != "none" {
		t.Fatalf("promessa lastreada no cron de medicação é VERDADEIRA, got %s", action)
	}

	// Oferta interrogativa não é promessa.
	_, action = a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "amanhã tem consulta", Engine: "companion"},
		"Quer que eu te avise às 8h?", nil)
	if action != "none" {
		t.Fatalf("oferta interrogativa não pode violar I2a, got %s", action)
	}
}

// ---------------------------------------------------------------------------
// I2b — claim perfectivo é AUDIT-ONLY (recall verdadeiro nunca é corrompido)
// ---------------------------------------------------------------------------

func TestGuard_PerfectiveClaimIsAuditOnly(t *testing.T) {
	a := guardAgentEnforce()
	text := "Marquei sim, foi ontem — quinta às 14h com o Dr. Paulo."
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "você marcou o dentista?", Engine: "companion"},
		text, nil)
	if action != "none" || out != text {
		t.Fatalf("recall verdadeiro não pode ser alterado (I2b é audit-only), got action=%s out=%q", action, out)
	}

	// Mesmo sem marcador de passado: claim perfectivo segue audit-only.
	text2 := "Anotei aqui na minha lista."
	out, action = a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "anota aí", Engine: "companion"},
		text2, nil)
	if action != "none" || out != text2 {
		t.Fatalf("I2b não pode virar enforcement no v1, got action=%s out=%q", action, out)
	}
}

// ---------------------------------------------------------------------------
// I3 — narração de transporte
// ---------------------------------------------------------------------------

func TestGuard_TransportArtifactNarrationStripped(t *testing.T) {
	a := guardAgentEnforce()
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "Cortar cabelo segunda 8:00", Engine: "operational"},
		"Marcado! Cortar cabelo segunda às 08:00. Mensagem veio em dobro, mas criei só uma vez.", nil)
	if action != "stripped" {
		t.Fatalf("narração de transporte deveria ser stripada, got %s (%q)", action, out)
	}
	if strings.Contains(out, "em dobro") {
		t.Errorf("narração de encanamento não pode chegar ao usuário, got %q", out)
	}
	if !strings.Contains(out, "Marcado") {
		t.Errorf("conteúdo legítimo deveria sobreviver, got %q", out)
	}
}

func TestGuard_TransportNarrationKeptWhenUserAskedAboutIt(t *testing.T) {
	a := guardAgentEnforce()
	text := "Veio em dobro mesmo, mas relaxa — criei só uma vez."
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"),
		UserMsg: "desculpa, acho que mandei duas vezes", Engine: "operational"}, text, nil)
	if action != "none" || out != text {
		t.Fatalf("usuário levantou a duplicação — resposta sobre ela é correta, got action=%s out=%q", action, out)
	}
}

// ---------------------------------------------------------------------------
// I4 — citação do display de OK_CRIADO
// ---------------------------------------------------------------------------

func TestGuard_DisplayCitationEnforced(t *testing.T) {
	a := guardAgentEnforce()
	display := "Evento criado: *Ir ao Madre Tereza*\nQuarta, 10/06 (AMANHÃ) às 16:00"
	toolResults := []string{"OK_CRIADO|display=" + display}

	// Display presente → passa.
	_, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "amanhã 16h madre tereza",
		ToolsCalled: []string{"criar_evento"}, ToolResults: toolResults, Engine: "operational"},
		display+"\n\nCriado!", nil)
	if action != "none" {
		t.Fatalf("display citado verbatim deveria passar, got %s", action)
	}

	// Display ausente (modelo freehandeou a data) → regenerate com o display
	// injetado no feedback; reescrita que o cita é aceita.
	regen := func(feedback string) (string, error) {
		if !strings.Contains(feedback, display) {
			t.Errorf("feedback do I4 deveria injetar o display, got %q", feedback)
		}
		return display + "\n\nProntinho!", nil
	}
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "amanhã 16h madre tereza",
		ToolsCalled: []string{"criar_evento"}, ToolResults: toolResults, Engine: "operational"},
		"Evento criado pra quinta às 16h!", regen)
	if action != "regenerated" || !strings.Contains(out, display) {
		t.Fatalf("I4 deveria forçar a citação via regenerate, got action=%s out=%q", action, out)
	}
}

// ---------------------------------------------------------------------------
// I5 — eco de carimbo (sempre ativo, qualquer modo)
// ---------------------------------------------------------------------------

func TestGuard_StripsEchoedHistoryStamp(t *testing.T) {
	// Mesmo com guard OFF, carimbo ecoado nunca vaza (persistOutbound gravaria
	// verbatim e o padrão se reforçaria a cada turno).
	a := &Agent{cfg: &Config{OutputGuardMode: "off"}}
	out, _ := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "oi", Engine: "companion"},
		"[hoje 09:12] Bom dia, Simone!", nil)
	if out != "Bom dia, Simone!" {
		t.Fatalf("carimbo ecoado deveria ser removido mesmo em modo off, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// Modos, template e não-vazio
// ---------------------------------------------------------------------------

func TestGuard_LogModeAuditsWithoutAltering(t *testing.T) {
	a := guardAgentLog()
	text := "Boa noite, Simone — descanse bem! 🌙"
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "Obrigada", Engine: "companion"},
		text, nil)
	if action != "log" || out != text {
		t.Fatalf("modo log audita sem alterar, got action=%s out=%q", action, out)
	}
}

func TestGuard_CanaryPhonePromotesToEnforce(t *testing.T) {
	a := &Agent{cfg: &Config{OutputGuardMode: "log", GuardEnforcePhones: []string{guardUser.PhoneNumber}}}
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "Obrigada", Engine: "companion"},
		"Boa noite, Simone!", nil)
	if action == "log" || strings.Contains(strings.ToLower(out), "boa noite") {
		t.Fatalf("canário deveria receber enforce, got action=%s out=%q", action, out)
	}
}

func TestGuard_NeverReturnsEmpty(t *testing.T) {
	a := guardAgentEnforce()
	// Única sentença, violadora, sem regen → template período-correto.
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "oi", Engine: "companion"},
		"Boa noite! 🌙", nil)
	if strings.TrimSpace(out) == "" {
		t.Fatal("guard JAMAIS devolve vazio")
	}
	if action != "template" || !strings.Contains(out, "Boa tarde") {
		t.Fatalf("terminal deveria ser o template de tarde, got action=%s out=%q", action, out)
	}
	if !strings.Contains(out, "Simone") {
		t.Errorf("template usa o primeiro nome, got %q", out)
	}
}

// O regenerate é estruturalmente tool-less: o guard só conhece regenerateFn
// (texto→texto). Este teste prova que a cascata nunca invoca handlers de tool
// — o contador global de toolHandlers permanece intocado por construção, e a
// reescrita aceita vem apenas do callback.
func TestGuard_RegenerateNeverReexecutesTools(t *testing.T) {
	a := guardAgentEnforce()
	called := 0
	regen := func(string) (string, error) {
		called++
		return "Bom dia! Evento mantido como combinado.", nil
	}
	_, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "oi",
		ToolsCalled: []string{"criar_evento"}, ToolResults: []string{"OK_CRIADO|display=Evento criado: *X*\nQuarta, 10/06 às 16:00"},
		Engine: "operational"},
		"Boa noite! Criei seu evento.", regen)
	if called != 1 {
		t.Fatalf("exatamente 1 chamada de reescrita, got %d", called)
	}
	// A reescrita não cita o display (I4) → segue na cascata sem NOVA chamada.
	if action == "regenerated" {
		t.Fatalf("reescrita sem display não poderia ser aceita (I4 ainda viola)")
	}
}

// ---------------------------------------------------------------------------
// Fixes do painel de revisão da implementação
// ---------------------------------------------------------------------------

// BLOCKER do painel: I4 sem regen disponível JAMAIS pode degradar para
// template — o evento FOI criado; trocar a confirmação por saudação faria o
// usuário re-pedir e duplicar. Terminal determinístico: anexa o display.
func TestGuard_I4AppendsDisplayInsteadOfTemplate(t *testing.T) {
	a := guardAgentEnforce()
	display := "Evento criado: *Dentista*\nQuarta, 11/06 (AMANHÃ) às 10:00"
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "marca dentista amanhã 10h",
		ToolsCalled: []string{"criar_evento"}, ToolResults: []string{"OK_CRIADO|display=" + display},
		Engine: "operational"},
		"Pronto! Marquei o dentista pra amanhã às 10h.", nil)
	if action != "appended" {
		t.Fatalf("I4 sem regen deveria ANEXAR o display, got action=%s out=%q", action, out)
	}
	if !strings.Contains(out, display) {
		t.Fatalf("display tem que estar na saída, got %q", out)
	}
	if !strings.Contains(out, "Pronto!") {
		t.Errorf("a prosa original deveria sobreviver, got %q", out)
	}
	if strings.Contains(out, "Estou por aqui se precisar") {
		t.Errorf("template de saudação não pode substituir confirmação de evento, got %q", out)
	}
}

// O orçamento de regen é ancorado na ENTRADA do guard, não no início do turno:
// um turno de tool pesado (TC.Now velho) ainda tem direito ao regenerate.
func TestGuard_RegenBudgetAnchoredAtGuardEntry(t *testing.T) {
	a := guardAgentEnforce()
	tc := &TurnContext{Now: time.Now().Add(-25 * time.Second), Loc: BRT(), Period: "tarde"}
	display := "Evento criado: *Dentista*\nQuarta, 11/06 (AMANHÃ) às 10:00"
	regenCalls := 0
	regen := func(string) (string, error) {
		regenCalls++
		return display + "\n\nProntinho!", nil
	}
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tc, UserMsg: "marca",
		ToolsCalled: []string{"criar_evento"}, ToolResults: []string{"OK_CRIADO|display=" + display},
		Engine: "operational"},
		"Marquei o dentista!", regen)
	if regenCalls != 1 || action != "regenerated" {
		t.Fatalf("turno lento ainda deveria regenerar (budget no guard, não no turno), got calls=%d action=%s out=%q", regenCalls, action, out)
	}
}

// "te lembro às 19h" (forma falada dominante) lastreada no cron das 19:00.
func TestGuard_BareHourPromiseGrounded(t *testing.T) {
	a := guardAgentEnforce()
	tcMed := tcAtFresh("tarde")
	tcMed.UpcomingMedsToday = []upcomingReminder{{at: time.Date(2026, 6, 9, 19, 0, 0, 0, BRT()), names: []string{"Losartana"}}}
	for _, text := range []string{
		"Fica tranquila, te lembro às 19h do Losartana.",
		"Fica tranquila, te lembro às 19 horas do Losartana.",
	} {
		if _, action := a.guardOutput(guardInput{User: guardUser, TC: tcMed, UserMsg: "me lembra do remédio?", Engine: "companion"},
			text, nil); action != "none" {
			t.Errorf("promessa hora-seca lastreada deveria passar: %q got action=%s", text, action)
		}
	}
	// "daqui a 2h" é duração, não horário 02:00 — sem lastro às 02:00, viola.
	tcMed.UpcomingMedsToday = nil
	if _, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "oi", Engine: "companion"},
		"Pode deixar, te aviso daqui a 2h.", nil); action == "none" {
		t.Error("promessa por duração sem lastro deveria violar I2a")
	}
}

// adiar_remedio lastreia a promessa do próprio turno; deferred_until lastreia
// a reafirmação em turno posterior.
func TestGuard_AdiarRemedioGroundsPromise(t *testing.T) {
	a := guardAgentEnforce()
	if _, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("tarde"), UserMsg: "vou tomar às 18:40",
		ToolsCalled: []string{"adiar_remedio"}, Engine: "companion"},
		"Combinado, te lembro às 18:40 então.", nil); action != "none" {
		t.Fatalf("promessa no turno do adiar_remedio é verdadeira, got %s", action)
	}
}

// Sintagma nominal não é saudação: "uma boa noite de sono" de manhã é empatia.
func TestGuard_NounPhraseGreetingNotFlagged(t *testing.T) {
	a := guardAgentEnforce()
	text := "Vou torcer por uma boa noite de sono mais tarde, viu."
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "dormi mal", Engine: "companion"},
		text, nil)
	if action != "none" || out != text {
		t.Fatalf("sintagma 'boa noite de sono' não pode disparar I1, got action=%s out=%q", action, out)
	}
	// Mas "boa noite de novo" É saudação repetida — viola de manhã.
	if _, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "oi", Engine: "companion"},
		"Boa noite de novo!", nil); action == "none" {
		t.Error("'boa noite de novo' é saudação e deveria violar de manhã")
	}
}

// Strip preserva estrutura: display multi-linha citado corretamente sobrevive
// intacto quando OUTRA sentença é removida.
func TestGuard_StripPreservesMultilineDisplay(t *testing.T) {
	a := guardAgentEnforce()
	display := "Evento criado: *Dentista*\nQuarta, 11/06 (AMANHÃ) às 10:00"
	text := display + "\n\nBoa noite, descanse bem! 🌙"
	out, action := a.guardOutput(guardInput{User: guardUser, TC: tcAtFresh("manhã"), UserMsg: "marca dentista",
		ToolsCalled: []string{"criar_evento"}, ToolResults: []string{"OK_CRIADO|display=" + display},
		Engine: "operational"}, text, nil)
	if action != "stripped" {
		t.Fatalf("esperava strip da despedida, got %s (%q)", action, out)
	}
	if !strings.Contains(out, display) {
		t.Fatalf("display multi-linha tem que sobreviver INTACTO ao strip, got %q", out)
	}
	if strings.Contains(strings.ToLower(out), "boa noite") {
		t.Errorf("despedida deveria ter sido removida, got %q", out)
	}
}

// |nota= é advisory: I4 só ancora no núcleo do display.
func TestGuard_I4IgnoresNotaSegment(t *testing.T) {
	display := "Evento criado: *Reunião*\nQuinta, 12/06 às 09:00"
	result := "OK_CRIADO|display=" + display + "\n|nota=Esse horário já passou hoje. Marquei pra amanhã nesse horário."
	// Resposta cita só o núcleo (nota parafraseada) → passa.
	if v := detectI4DisplayCitation(display+"\n\nObs: como já tinha passado, ficou pra amanhã.", []string{result}); len(v) != 0 {
		t.Fatalf("nota parafraseada não pode violar I4, got %d violações", len(v))
	}
	// Resposta sem o núcleo → viola, e o Append é só o núcleo.
	v := detectI4DisplayCitation("Marquei!", []string{result})
	if len(v) != 1 || v[0].Append != display {
		t.Fatalf("I4 deveria ancorar no núcleo sem a nota, got %+v", v)
	}
}

// I2b audita exatamente UMA vez por turno (métrica de promoção não infla).
func TestGuard_I2bAuditedOncePerTurn(t *testing.T) {
	db := setupTestDB(t)
	u := &User{PhoneNumber: "5511912121212", Name: "Aud Teste", Type: UserTypeIdoso}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	a := &Agent{db: db, audit: NewAuditLog(db), cfg: &Config{OutputGuardMode: "enforce"}}
	// Texto com claim I2b E violação I1 (força a cascata a re-detectar 2-3×).
	a.guardOutput(guardInput{User: u, TC: tcAtFresh("manhã"), UserMsg: "oi", Engine: "companion"},
		"Anotei tudo aqui. Boa noite, descanse bem!", nil)
	var n int
	if err := db.conn.QueryRow(
		`SELECT COUNT(*) FROM action_log WHERE user_id=? AND action='guard_violation' AND target_user='I2b'`,
		u.ID).Scan(&n); err != nil {
		t.Fatalf("count audit: %v", err)
	}
	if n != 1 {
		t.Fatalf("I2b deveria auditar exatamente 1× por turno, got %d", n)
	}
}
