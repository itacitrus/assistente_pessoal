package main

import (
	"context"
	"errors"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

// Migração LID do WhatsApp: DMs passam a chegar com Sender @lid em vez do
// telefone. Estes testes travam a regra de resolução inbound:
//  1. SenderAlt (sender_pn do próprio stanza) é a fonte primária;
//  2. o mapping LID→PN do store é fallback;
//  3. LID irresolúvel NUNCA vira "phone" — descarta (ok=false), senão o
//     cadastrado cai no funil de vendas e o LID é persistido como telefone
//     em users/leads (corrupção permanente).

type fakeLIDResolver struct {
	pn  types.JID
	err error
}

func (f *fakeLIDResolver) GetPNForLID(_ context.Context, _ types.JID) (types.JID, error) {
	return f.pn, f.err
}

func lidInfo(sender, senderAlt types.JID) *types.MessageInfo {
	return &types.MessageInfo{MessageSource: types.MessageSource{Sender: sender, SenderAlt: senderAlt}}
}

func TestResolveSenderJID(t *testing.T) {
	pn := types.NewJID("5511999999999", types.DefaultUserServer)
	pnAD := types.JID{User: "5511999999999", Server: types.DefaultUserServer, Device: 3}
	lid := types.NewJID("123456789012345", types.HiddenUserServer)
	lidAD := types.JID{User: "123456789012345", Server: types.HiddenUserServer, Device: 7}

	storeErr := errors.New("no mapping")

	cases := []struct {
		name     string
		sender   types.JID
		alt      types.JID
		store    *fakeLIDResolver
		wantUser string
		wantOK   bool
	}{
		{"sender já é telefone: passa direto", pn, types.JID{}, &fakeLIDResolver{err: storeErr}, "5511999999999", true},
		{"sender telefone com device: normaliza ToNonAD", pnAD, types.JID{}, &fakeLIDResolver{err: storeErr}, "5511999999999", true},
		{"lid com SenderAlt: alt é autoritativo, store nem consultado", lid, pn, &fakeLIDResolver{err: storeErr}, "5511999999999", true},
		{"lid AD com SenderAlt AD: ambos normalizados", lidAD, pnAD, &fakeLIDResolver{err: storeErr}, "5511999999999", true},
		{"lid sem alt: store resolve", lid, types.JID{}, &fakeLIDResolver{pn: pn}, "5511999999999", true},
		{"lid sem alt e store falha: descarta (nunca LID como phone)", lid, types.JID{}, &fakeLIDResolver{err: storeErr}, "123456789012345", false},
		{"lid sem alt e store devolve vazio sem erro: descarta", lid, types.JID{}, &fakeLIDResolver{}, "123456789012345", false},
		{"alt com server inesperado (@lid) é ignorado: cai no store", lid, lidAD, &fakeLIDResolver{pn: pn}, "5511999999999", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveSenderJID(lidInfo(tc.sender, tc.alt), tc.store)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if got.User != tc.wantUser {
				t.Fatalf("resolved user = %q, want %q", got.User, tc.wantUser)
			}
			if ok && got.Server != types.DefaultUserServer {
				t.Fatalf("resolved server = %q, want %q (telefone)", got.Server, types.DefaultUserServer)
			}
			if got.Device != 0 {
				t.Fatalf("resolved device = %d, want 0 (ToNonAD)", got.Device)
			}
		})
	}
}
