package main

import (
	"encoding/base64"
	"image/png"
	"strings"
	"testing"
)

// qrPNGDataURI renderiza a string do QR do WhatsApp num data-URI PNG que o
// front exibe direto num <img>. Sem isso o web teria que carregar uma lib de QR
// no cliente — o bot já tem rsc.io/qr transitivamente.

func TestQRPNGDataURI_ProduzPNGValido(t *testing.T) {
	uri, err := qrPNGDataURI("2@abcDEF123456,supersecretkey,==")
	if err != nil {
		t.Fatalf("qrPNGDataURI: %v", err)
	}
	const prefix = "data:image/png;base64,"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("data-URI mal formada: %.30q...", uri)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatalf("base64 inválido: %v", err)
	}
	img, err := png.Decode(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("PNG não decodifica: %v", err)
	}
	if b := img.Bounds(); b.Dx() == 0 || b.Dy() == 0 {
		t.Fatalf("imagem vazia: %v", b)
	}
}

func TestQRPNGDataURI_VazioÉErro(t *testing.T) {
	if _, err := qrPNGDataURI(""); err == nil {
		t.Fatal("string vazia deveria retornar erro, não um QR")
	}
}
