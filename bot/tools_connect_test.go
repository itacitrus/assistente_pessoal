package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// Sem Google conectado, a tool de agenda NÃO erra: devolve a mensagem que
// instrui o agente a oferecer conexão (em vez de só negar).
func TestBuscarAgenda_NotConnected_OffersConnect(t *testing.T) {
	db := setupTestDB(t)
	agent := mkTestAgent(t, db)
	user := &User{ID: 1, Name: "Fábio", GoogleCredentials: ""}

	out, err := handleBuscarAgenda(context.Background(), agent, user, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("não deveria erro, got %v", err)
	}
	if out != googleNotConnectedMsg {
		t.Fatalf("esperava googleNotConnectedMsg, got %q", out)
	}
	if !strings.Contains(out, "conectar_agenda") {
		t.Fatalf("mensagem deve orientar a chamar conectar_agenda: %q", out)
	}
}

// conectar_agenda com agenda já conectada apenas avisa (idempotente), sem
// precisar de cal/sendMsg.
func TestConectarAgenda_AlreadyConnected(t *testing.T) {
	db := setupTestDB(t)
	agent := mkTestAgent(t, db)
	user := &User{ID: 1, Name: "Fábio", GoogleCredentials: "enc-token"}

	out, err := handleConectarAgenda(context.Background(), agent, user, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(out), "conectada") {
		t.Fatalf("esperava aviso de já conectada, got %q", out)
	}
}
