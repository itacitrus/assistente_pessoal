package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// FallbackChat envolve dois ChatProviders e troca pro secundario quando o
// primario falha de forma recuperavel.
//
// O fallback anterior era de BOOT: main.go escolhia o provider uma vez na
// inicializacao. Se a DeepSeek caisse as 3h da manha, o companion ficava
// morto ate alguem reiniciar o container — e num bot de medicacao cada
// janela perdida e uma dose potencialmente nao tomada.
//
// Erro NAO recuperavel (400, schema invalido) propaga sem tentar o
// secundario: e bug nosso, e trocar de provider so mascararia.
type FallbackChat struct {
	primary   llm.ChatProvider
	secondary llm.ChatProvider
	// onFailover e chamado com o erro que causou a troca. Usado pra alertar
	// o admin quando a causa e saldo/credencial.
	onFailover func(error)
}

// NewFallbackChat monta o par. secondary nil devolve o primario puro — nao
// ha o que envolver.
func NewFallbackChat(primary, secondary llm.ChatProvider, onFailover func(error)) llm.ChatProvider {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return &FallbackChat{primary: primary, secondary: secondary, onFailover: onFailover}
}

func (f *FallbackChat) Name() string         { return f.primary.Name() }
func (f *FallbackChat) SupportsTools() bool  { return f.primary.SupportsTools() }
func (f *FallbackChat) SupportsVision() bool { return f.primary.SupportsVision() }

func (f *FallbackChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	resp, err := f.primary.Chat(ctx, req)
	if err == nil {
		return resp, nil
	}
	// Erro de request invalido e nosso: nao mascarar trocando de provider.
	if !llm.IsRetryable(err) && !llm.IsAuthOrBalance(err) {
		return resp, err
	}
	if f.onFailover != nil {
		f.onFailover(err)
	}
	log.Printf("llm fallback: %s falhou (%v) — tentando %s", f.primary.Name(), err, f.secondary.Name())

	// Se o secundario nao suporta vision e o request tem imagem, a troca
	// quebraria de outro jeito. Melhor devolver o erro original.
	if !f.secondary.SupportsVision() && requestHasImage(req) {
		return resp, err
	}
	return f.secondary.Chat(ctx, req)
}

// requestHasImage diz se o turno carrega bloco de imagem.
func requestHasImage(req llm.ChatRequest) bool {
	for _, m := range req.Messages {
		for _, c := range m.Content {
			if c.Type == "image" {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Alerta de admin
// ---------------------------------------------------------------------------

// alertDedupeWindow evita transformar uma falha continua num flood de
// WhatsApp. Uma hora e suficiente pra o admin agir sem virar spam.
const alertDedupeWindow = time.Hour

var (
	alertMu   sync.Mutex
	alertSeen = map[string]time.Time{}
)

// alertAdminOnce manda uma mensagem pros telefones de admin, no maximo uma
// vez por hora por chave.
//
// Existe por causa do incidente que motivou a migracao: saldo da Anthropic
// zerou e o bot gerou 1.818 invalid_request_error em 72h sem NINGUEM ser
// avisado. Silenciar falha de credencial e o pior modo de falha do sistema.
func (a *Agent) alertAdminOnce(key, msg string) {
	if a.sendMsg == nil || a.cfg == nil {
		return
	}
	phones := a.cfg.AdminPhones
	if len(phones) == 0 {
		return
	}
	alertMu.Lock()
	last, seen := alertSeen[key]
	if seen && time.Since(last) < alertDedupeWindow {
		alertMu.Unlock()
		return
	}
	alertSeen[key] = time.Now()
	alertMu.Unlock()

	for _, p := range phones {
		if err := a.sendMsg(p, msg); err != nil {
			log.Printf("alerta admin para %s falhou: %v", p, err)
		}
	}
}

// llmFailoverAlert monta o callback de failover pro provider de um papel.
func (a *Agent) llmFailoverAlert(role string) func(error) {
	return func(err error) {
		if !llm.IsAuthOrBalance(err) {
			return // indisponibilidade passageira nao vira alerta
		}
		a.alertAdminOnce("llm_auth_"+role,
			"⚠️ Lurch: o provider de LLM do papel \""+role+"\" recusou a chamada por credencial ou saldo.\n\n"+
				err.Error()+"\n\nO bot está usando o provider reserva enquanto isso. "+
				"Sem correção, as respostas vão degradar ou parar.")
	}
}
