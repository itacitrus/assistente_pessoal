package main

import (
	"sync"
	"testing"

	"go.mau.fi/whatsmeow"
)

// ClientHolder é a indireção atômica que deixa handler/watchdog lerem sempre o
// client vivo. Sem ela, o re-pareamento em runtime não conseguiria rewirar os
// consumidores sem reiniciar o processo.

func TestClientHolder_InicialNil(t *testing.T) {
	var h ClientHolder
	if got := h.Get(); got != nil {
		t.Fatalf("holder novo deveria devolver nil, veio %p", got)
	}
}

func TestClientHolder_SetGet(t *testing.T) {
	var h ClientHolder
	c := &whatsmeow.Client{}
	h.Set(c)
	if got := h.Get(); got != c {
		t.Fatalf("Get devolveu %p, esperava %p", got, c)
	}
}

func TestClientHolder_ConcurrentSetGet(t *testing.T) {
	var h ClientHolder
	c := &whatsmeow.Client{}
	h.Set(c)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = h.Get() }()
		go func() { defer wg.Done(); h.Set(&whatsmeow.Client{}) }()
	}
	wg.Wait()
	// Sem -race isto não prova travamento, mas com `go test -race` o teste
	// falha se Get/Set não estiverem sincronizados.
	if h.Get() == nil {
		t.Fatal("após sets concorrentes o holder não deveria estar nil")
	}
}
