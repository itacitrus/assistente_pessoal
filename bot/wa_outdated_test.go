package main

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/store"
)

// O 405 "client outdated" derrubou o bot por 2 dias (23-25/nov/2026): o
// WhatsApp passou a recusar a versão embutida no whatsmeow e nada reagia.

func withWAVersion(t *testing.T) {
	orig := store.GetWAVersion()
	t.Cleanup(func() { store.SetWAVersion(orig) })
}

func TestRecoverOutdated_VersaoNovaAplicaEReconecta(t *testing.T) {
	withWAVersion(t)
	cur := store.GetWAVersion()
	newer := store.WAVersionContainer{cur[0], cur[1], cur[2] + 1}
	reconnects := 0

	err := recoverOutdated(context.Background(),
		func(context.Context) (*store.WAVersionContainer, error) { return &newer, nil },
		func() error { reconnects++; return nil })

	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if store.GetWAVersion() != newer {
		t.Fatalf("versão não aplicada: %v", store.GetWAVersion())
	}
	if reconnects != 1 {
		t.Fatalf("esperava 1 reconexão, veio %d", reconnects)
	}
}

func TestRecoverOutdated_MesmaVersaoNaoReconecta(t *testing.T) {
	withWAVersion(t)
	cur := store.GetWAVersion()
	reconnects := 0

	err := recoverOutdated(context.Background(),
		func(context.Context) (*store.WAVersionContainer, error) { return &cur, nil },
		func() error { reconnects++; return nil })

	if err == nil {
		t.Fatal("mesma versão deveria devolver erro (bump da lib necessário)")
	}
	if reconnects != 0 {
		t.Fatalf("não deveria reconectar (loop 405), veio %d", reconnects)
	}
}

func TestRecoverOutdated_FalhaNoFetchNaoMexeNaVersao(t *testing.T) {
	withWAVersion(t)
	cur := store.GetWAVersion()
	reconnects := 0

	err := recoverOutdated(context.Background(),
		func(context.Context) (*store.WAVersionContainer, error) { return nil, errors.New("timeout") },
		func() error { reconnects++; return nil })

	if err == nil || reconnects != 0 || store.GetWAVersion() != cur {
		t.Fatalf("fetch falho deveria abortar sem efeitos: err=%v reconnects=%d ver=%v", err, reconnects, store.GetWAVersion())
	}
}
