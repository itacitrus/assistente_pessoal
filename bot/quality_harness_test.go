package main

import (
	"context"
	"regexp"
	"strings"
	"testing"
)

// Este harness reprovou o gemini-2.5-flash-lite antes do deploy: 0/5 tool
// calls e 3/5 narrando persistencia sem persistir, apesar de ser o mais
// barato e o recomendado pela pesquisa. deepseek-v4-flash fez 5/5 e 0/5.
//
// Entra na suite permanente — nao e um teste de uma vez so. Trocar de
// provider no futuro passa por aqui.

var (
	narraPersistenciaRe = regexp.MustCompile(`(?i)\b(anotei|anotado|anotadinho|registrei|registrado|cadastrei|cadastrado|guardei|salvei|marquei|marcado|deixar registrado)\b`)
	menuNumeradoRe      = regexp.MustCompile(`(?m)^\s*[1-9][)\.]\s+\S`)
	markdownPesadoRe    = regexp.MustCompile(`(?m)\*\*|^#{1,6}\s`)
)

// checkResponseQuality devolve a lista de problemas encontrados numa resposta
// que iria pro WhatsApp. Vazio = resposta limpa.
func checkResponseQuality(text string, toolsCalled []string) []string {
	var problems []string
	if m := cjkRe.FindString(text); m != "" {
		problems = append(problems, "CJK: "+m)
	}
	if markdownPesadoRe.MatchString(text) {
		problems = append(problems, "markdown que o WhatsApp nao renderiza")
	}
	if menuNumeradoRe.MatchString(text) {
		problems = append(problems, "menu numerado (o bot e IA, nao chatbot)")
	}
	if m := narraPersistenciaRe.FindString(text); m != "" && len(toolsCalled) == 0 {
		problems = append(problems, "narrou persistencia sem chamar tool: "+m)
	}
	return problems
}

func TestHarnessPegaNarrarSemPersistir(t *testing.T) {
	cases := []string{
		"Metformina 850mg, anotado!",
		"Pode deixar que eu deixo anotado aqui: omeprazol 20mg",
		"Vou deixar aqui anotadinho pra gente",
		"Pronto, marquei sua consulta",
	}
	for _, c := range cases {
		if len(checkResponseQuality(c, nil)) == 0 {
			t.Errorf("harness nao pegou narracao sem tool: %q", c)
		}
	}
}

// Com a tool chamada, a mesma frase e legitima — o harness nao pode punir o
// comportamento correto.
func TestHarnessAceitaNarrarComTool(t *testing.T) {
	p := checkResponseQuality("Pronto, anotei aqui!", []string{"cadastrar_medicamento"})
	if len(p) != 0 {
		t.Fatalf("com tool chamada nao ha violacao: %v", p)
	}
}

func TestHarnessPegaCJK(t *testing.T) {
	if len(checkResponseQuality("好的, tudo certo", []string{"x"})) == 0 {
		t.Error("CJK nao pego")
	}
}

func TestHarnessPegaMarkdown(t *testing.T) {
	for _, c := range []string{"Hoje: **14:00**", "## Agenda"} {
		if len(checkResponseQuality(c, []string{"x"})) == 0 {
			t.Errorf("markdown nao pego: %q", c)
		}
	}
}

func TestHarnessPegaMenuNumerado(t *testing.T) {
	if len(checkResponseQuality("Escolha:\n1) agenda\n2) remedio", []string{"x"})) == 0 {
		t.Error("menu numerado nao pego")
	}
}

func TestHarnessNaoAcusaRespostaLimpa(t *testing.T) {
	limpas := []string{
		"Hoje você tem reunião com o contador às 14h e buscar a Ana às 17h30.",
		"Bom dia, Fábio! Já tomou o remédio da manhã?",
		"Tudo certo 😊 qualquer coisa é só chamar.",
		"São 3 compromissos hoje — o primeiro é às 9h.",
	}
	for _, c := range limpas {
		if p := checkResponseQuality(c, []string{"buscar_agenda"}); len(p) != 0 {
			t.Errorf("falso positivo em %q: %v", c, p)
		}
	}
}

// ---------------------------------------------------------------------------
// Pipeline ponta a ponta: guard + sanitizador entregam texto limpo.
// ---------------------------------------------------------------------------

func TestPipelineEntregaTextoLimpo(t *testing.T) {
	a := convergenceAgent(t)
	a.chat = &fakeChat{name: "op", reply: "Hoje: **14:00** reuniao 好的"}
	u := seedUser(t, a.db, "5561933330000", "Ana", "")

	out, err := a.Run(context.Background(), u, "o que tenho hoje", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "**") {
		t.Fatalf("markdown vazou pro transporte: %q", out)
	}
	if cjkRe.MatchString(out) {
		t.Fatalf("CJK vazou pro transporte: %q", out)
	}
}

// O pipeline do idoso tambem tem que entregar limpo — e o publico onde um
// caractere chines quebra mais confianca.
func TestPipelineCompanionEntregaTextoLimpo(t *testing.T) {
	a := convergenceAgent(t)
	cl := &fakeChat{name: "clinico", reply: "**Losartana** 好的 tomada às 8h"}
	a.chat, a.companionChat = &fakeChat{name: "op"}, cl
	u := seedUser(t, a.db, "5561933330001", "Fabio", UserTypeIdoso)

	out, err := a.Run(context.Background(), u, "ja tomei", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := checkResponseQuality(out, []string{"marcar_remedio_tomado"}); len(p) != 0 {
		t.Fatalf("pipeline do companion entregou texto sujo: %v (%q)", p, out)
	}
}
