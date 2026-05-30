package main

import "testing"

func TestClassifyTakenIntent_Taken(t *testing.T) {
	taken := []string{
		"tomei",
		"Tomei",
		"tomei tudo",
		"tomei todos",
		"tomei sim",
		"já tomei",
		"ja tomei as 7h",
		"Já tomei às 7h",
		"acabei de tomar",
		"tomei agora",
		"aplicado",
		"apliquei",
		"já apliquei",
		"passei a pomada",
		"engoli o comprimido",
		"tomei o 4mag",
		"tomado",
	}
	for _, m := range taken {
		if got := classifyTakenIntent(m); got != IntentTaken {
			t.Errorf("classifyTakenIntent(%q) = %v, want IntentTaken", m, got)
		}
	}
}

func TestClassifyTakenIntent_TerseConfirmations(t *testing.T) {
	// Confirmacoes tersas comuns de idoso em resposta a lembrete (caso Simone:
	// "Bom dia" + "Pronto"). So agem com dose pendente/recente — aqui validamos
	// so a classificacao.
	taken := []string{
		"Pronto",
		"pronto",
		"Bom dia\nPronto",
		"prontinho",
		"já foi",
		"ja foi",
		"feito",
		"já fiz",
	}
	for _, m := range taken {
		if got := classifyTakenIntent(m); got != IntentTaken {
			t.Errorf("classifyTakenIntent(%q) = %v, want IntentTaken", m, got)
		}
	}
	// "pronto" seguido de adiamento NAO conta (negacao-primeiro).
	if got := classifyTakenIntent("pronto, vou tomar mais tarde"); got == IntentTaken {
		t.Errorf("'pronto, vou tomar mais tarde' = IntentTaken, want NOT taken")
	}
}

func TestClassifyTakenIntent_Deferred(t *testing.T) {
	// Negacao/adiamento NUNCA pode virar tomada.
	deferred := []string{
		"vou tomar mais tarde",
		"ainda vou tomar",
		"ainda não tomei",
		"ainda nao tomei",
		"não tomei ainda",
		"nao tomei",
		"nem tomei",
		"esqueci de tomar",
		"vou tomar daqui a pouco",
		"tomo mais tarde",
		"deixa pra depois",
		"to indo tomar agora",
		"vou aplicar a pomada depois",
	}
	for _, m := range deferred {
		if got := classifyTakenIntent(m); got == IntentTaken {
			t.Errorf("classifyTakenIntent(%q) = IntentTaken, want NOT taken (got deferral/none)", m)
		}
	}
}

func TestClassifyTakenIntent_SocialAndNone(t *testing.T) {
	none := []string{
		"tomei um café",
		"tomei cafe agora",
		"tomei um banho",
		"tomei sol de manhã",
		"tomei um susto com o trovão",
		"bom dia, tudo bem?",
		"obrigado, viu",
		"que calor hoje",
	}
	for _, m := range none {
		if got := classifyTakenIntent(m); got == IntentTaken {
			t.Errorf("classifyTakenIntent(%q) = IntentTaken, want NOT taken", m)
		}
	}
}

func TestClassifyTakenIntent_SocialButRealMed(t *testing.T) {
	// "tomei um café e já apliquei a pomada" — ha sinal real de remedio (pomada/
	// apliquei) alem do social, entao conta como tomada.
	real := []string{
		"tomei um café e já apliquei a pomada",
		"depois do banho engoli o comprimido",
	}
	for _, m := range real {
		if got := classifyTakenIntent(m); got != IntentTaken {
			t.Errorf("classifyTakenIntent(%q) = %v, want IntentTaken (sinal real de remedio presente)", m, got)
		}
	}
}

func TestClassifyTakenIntent_MixedDeferralWins(t *testing.T) {
	// "tomei o losartan mas o resto vou tomar mais tarde" — conservador: o
	// adiamento derruba o veredito inteiro (limitacao conhecida e documentada).
	if got := classifyTakenIntent("tomei o losartan mas o resto vou tomar mais tarde"); got == IntentTaken {
		t.Errorf("mixed com adiamento deveria NAO ser IntentTaken, got %v", got)
	}
}
