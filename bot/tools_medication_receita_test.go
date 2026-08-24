package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func receitaAgent(t *testing.T) (*Agent, *User) {
	t.Helper()
	db := setupTestDB(t)
	a := &Agent{db: db, cfg: &Config{}, audit: NewAuditLog(db)}
	u := seedUser(t, db, "5561922220000", "Ana", "")
	return a, u
}

func seedCatalogo(t *testing.T, db *DB, nome, principio, conc string) {
	t.Helper()
	_, err := db.conn.Exec(
		`INSERT INTO drug_catalog (commercial_name, active_ingredient, concentration, norm_name, norm_ingredient)
		 VALUES (?, ?, ?, ?, ?)`,
		nome, principio, conc, normalizeName(nome), normalizeName(principio))
	if err != nil {
		t.Fatalf("seed catalogo: %v", err)
	}
}

// Item SEM correspondencia no catalogo saia identico a um item lido
// corretamente. Com visao limitada a 384 tokens/imagem, esse e justamente o
// caso do nome inventado — e o idoso pode so dizer "sim".
func TestReceitaSinalizaItemSemLastroNoCatalogo(t *testing.T) {
	a, u := receitaAgent(t)
	seedCatalogo(t, a.db, "DIPIRONA SODICA", "dipirona", "500 MG")

	out, err := handleExtrairReceitaImagem(context.Background(), a, u,
		json.RawMessage(`{"items":[{"name":"Xyzabcina Fantasia","dose":"50mg"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "sem correspondência no catálogo") {
		t.Fatalf("item sem lastro nao foi sinalizado: %q", out)
	}
}

// Item COM correspondencia continua recebendo a sugestao do catalogo, nao o
// alerta de ausencia.
func TestReceitaItemComLastroNaoRecebeAlerta(t *testing.T) {
	a, u := receitaAgent(t)
	seedCatalogo(t, a.db, "DIPIRONA SODICA", "dipirona", "500 MG")

	out, err := handleExtrairReceitaImagem(context.Background(), a, u,
		json.RawMessage(`{"items":[{"name":"dipirona","dose":"500mg"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sem correspondência no catálogo") {
		t.Fatalf("item com lastro recebeu alerta indevido: %q", out)
	}
}

// Catalogo vazio (teste, primeira subida antes da ingestao CMED) nao pode
// virar alerta em todo item — ruido treina o agente a ignorar o alerta.
func TestReceitaCatalogoVazioNaoGeraRuido(t *testing.T) {
	a, u := receitaAgent(t) // sem seed
	out, err := handleExtrairReceitaImagem(context.Background(), a, u,
		json.RawMessage(`{"items":[{"name":"Dipirona","dose":"500mg"},{"name":"Losartana","dose":"50mg"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "sem correspondência no catálogo") {
		t.Fatalf("catalogo vazio virou ruido: %q", out)
	}
}

// O handler NAO persiste — isso e do desenho (humano confirma antes de
// cadastrar_medicamento), nao uma regressao.
func TestReceitaNaoPersisteNada(t *testing.T) {
	a, u := receitaAgent(t)
	seedCatalogo(t, a.db, "DIPIRONA SODICA", "dipirona", "500 MG")

	if _, err := handleExtrairReceitaImagem(context.Background(), a, u,
		json.RawMessage(`{"items":[{"name":"Dipirona","dose":"500mg"}]}`)); err != nil {
		t.Fatal(err)
	}
	meds, _ := a.db.ListActiveMedications(u.ID)
	if len(meds) != 0 {
		t.Fatalf("handler persistiu %d medicamentos — ele nao deveria persistir nada", len(meds))
	}
}

// Lista vazia continua pedindo nova foto em vez de inventar.
func TestReceitaSemItensPedeNovaFoto(t *testing.T) {
	a, u := receitaAgent(t)
	out, err := handleExtrairReceitaImagem(context.Background(), a, u, json.RawMessage(`{"items":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(out), "não consegui identificar") {
		t.Fatalf("resposta = %q", out)
	}
}
