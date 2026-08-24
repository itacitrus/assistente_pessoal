package main

import (
	"encoding/json"
	"testing"
)

// Loop de call identica e o modo de falha agentico mais documentado do
// DeepSeek — o modelo chega a escrever "vou parar" e reemite a mesma call.
func TestBreakerCortaCallRepetida(t *testing.T) {
	b := NewToolBreaker(2)
	args := json.RawMessage(`{"data":"2026-08-24"}`)
	if !b.Allow("buscar_agenda", args) {
		t.Fatal("1a chamada tem que passar")
	}
	if !b.Allow("buscar_agenda", args) {
		t.Fatal("2a chamada tem que passar")
	}
	if b.Allow("buscar_agenda", args) {
		t.Fatal("3a chamada identica tem que ser cortada")
	}
	if !b.Tripped() {
		t.Fatal("breaker deveria estar acionado")
	}
}

// Ordem de chave no JSON nao pode enganar o dedupe.
func TestBreakerNormalizaOrdemDasChaves(t *testing.T) {
	b := NewToolBreaker(1)
	b.Allow("t", json.RawMessage(`{"a":1,"b":2}`))
	if b.Allow("t", json.RawMessage(`{"b":2,"a":1}`)) {
		t.Fatal("mesma call com chaves em outra ordem deveria ser cortada")
	}
}

func TestBreakerNormalizaEspacoEAninhamento(t *testing.T) {
	b := NewToolBreaker(1)
	b.Allow("t", json.RawMessage(`{"a":{"x":1,"y":[1,2]}}`))
	if b.Allow("t", json.RawMessage(`{ "a" : { "y" : [1,2], "x" : 1 } }`)) {
		t.Fatal("mesma call com espaco/ordem diferente deveria ser cortada")
	}
}

func TestBreakerNaoCortaCallsDiferentes(t *testing.T) {
	b := NewToolBreaker(1)
	b.Allow("buscar_agenda", json.RawMessage(`{"data":"2026-08-24"}`))
	if !b.Allow("buscar_agenda", json.RawMessage(`{"data":"2026-08-25"}`)) {
		t.Fatal("argumento diferente e outra call")
	}
	if !b.Allow("criar_evento", json.RawMessage(`{"data":"2026-08-24"}`)) {
		t.Fatal("tool diferente e outra call")
	}
}

func TestBreakerNaoAcionadoNoCaminhoFeliz(t *testing.T) {
	b := NewToolBreaker(2)
	b.Allow("a", json.RawMessage(`{}`))
	b.Allow("b", json.RawMessage(`{}`))
	if b.Tripped() {
		t.Fatal("calls distintas nao podem acionar o breaker")
	}
}

// JSON invalido nao pode derrubar o loop — cai no texto cru.
func TestBreakerToleraJSONInvalido(t *testing.T) {
	b := NewToolBreaker(1)
	if !b.Allow("t", json.RawMessage(`{quebrado`)) {
		t.Fatal("primeira chamada passa mesmo com JSON invalido")
	}
	if b.Allow("t", json.RawMessage(`{quebrado`)) {
		t.Fatal("repeticao do mesmo JSON invalido deveria ser cortada")
	}
}

func TestNormalizeToolArgsEstavel(t *testing.T) {
	a := normalizeToolArgs(json.RawMessage(`{"b":[3,{"z":1,"a":2}],"a":"x"}`))
	c := normalizeToolArgs(json.RawMessage(`{"a":"x","b":[3,{"a":2,"z":1}]}`))
	if a != c {
		t.Fatalf("normalizacao instavel:\n%s\n%s", a, c)
	}
}
