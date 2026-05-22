package main

import (
	"strings"
	"testing"
)

// O invariante "nunca narre sem persistir" para AGENDA tem que viver no nucleo
// social sempre-ativo — nao no bloco de remedio (que so entra quando
// medContextActive). Marcar/desmarcar compromisso e' acao com efeito e nao
// passa por medContextActive. Regressao: bot confirmou "marquei a hamburgada"
// sem chamar criar_evento porque a regra dura nao estava no nucleo.
func TestCompanionCoreEnforcesAgendaPersistence(t *testing.T) {
	core := buildCompanionCore("Fábio")
	for _, want := range []string{"criar_evento", "editar_evento", "cancelar_evento"} {
		if !strings.Contains(core, want) {
			t.Errorf("nucleo social deveria citar a tool %q", want)
		}
	}
	// A acao de agenda nao deve depender do contexto de remedio para carregar a
	// regra dura: o nucleo (sem pharma) precisa exigir a chamada da tool.
	if medContextActive("marque pra amanha a hamburgada aqui em casa 18h", nil, false) {
		t.Fatal("pedido de agenda nao deveria ativar contexto de remedio — a regra dura tem que estar no nucleo")
	}
	low := strings.ToLower(core)
	if !strings.Contains(low, "marcou") || !strings.Contains(low, "retorno") {
		t.Error("nucleo social deveria conter a regra dura de nao confirmar marcacao sem o retorno da tool")
	}
}
