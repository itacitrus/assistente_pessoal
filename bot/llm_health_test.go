package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
	openai "github.com/sashabaranov/go-openai"
)

// O fallback tem que ser POR REQUEST. Antes main.go escolhia o provider uma
// vez no boot: DeepSeek fora as 3h deixava o companion morto ate alguem
// reiniciar o container — e lembrete de medicacao perdido e dose perdida.
func TestFallbackTrocaDeProviderNoErro(t *testing.T) {
	primary := &fakeChat{name: "deepseek", err: &openai.APIError{HTTPStatusCode: 503}}
	secondary := &fakeChat{name: "anthropic", reply: "resposta do backup"}
	fc := NewFallbackChat(primary, secondary, nil)

	resp, err := fc.Chat(context.Background(), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if secondary.calls != 1 {
		t.Fatalf("secundario acionado %d vezes, quer 1", secondary.calls)
	}
	if collectLLMText(resp.Content) != "resposta do backup" {
		t.Fatalf("resposta veio do provider errado: %q", collectLLMText(resp.Content))
	}
}

// 400 e bug nosso: trocar de provider so mascararia.
func TestFallbackNaoTrocaEmRequestInvalido(t *testing.T) {
	primary := &fakeChat{name: "deepseek", err: &openai.APIError{HTTPStatusCode: 400}}
	secondary := &fakeChat{name: "anthropic", reply: "backup"}
	fc := NewFallbackChat(primary, secondary, nil)

	if _, err := fc.Chat(context.Background(), llm.ChatRequest{}); err == nil {
		t.Fatal("400 tem que propagar")
	}
	if secondary.calls != 0 {
		t.Fatal("secundario nao devia ter sido chamado num 400")
	}
}

// Saldo esgotado foi exatamente o modo do incidente: 1818 erros em 72h sem
// ninguem ser avisado.
func TestSaldoEsgotadoDisparaCallback(t *testing.T) {
	var got error
	primary := &fakeChat{name: "deepseek", err: &openai.APIError{HTTPStatusCode: 402, Message: "Insufficient Balance"}}
	fc := NewFallbackChat(primary, &fakeChat{name: "b", reply: "ok"}, func(e error) { got = e })

	fc.Chat(context.Background(), llm.ChatRequest{})
	if got == nil {
		t.Fatal("saldo esgotado tem que notificar")
	}
	if !llm.IsAuthOrBalance(got) {
		t.Fatalf("erro notificado nao e de saldo: %v", got)
	}
}

// Sem secundario, o wrapper devolve o primario puro (nada a envolver).
func TestFallbackSemSecundarioDevolvePrimario(t *testing.T) {
	primary := &fakeChat{name: "deepseek", reply: "ok"}
	if got := NewFallbackChat(primary, nil, nil); got != llm.ChatProvider(primary) {
		t.Fatal("sem secundario deveria devolver o primario sem wrapper")
	}
}

// Trocar pra um provider sem vision quando o turno tem imagem so trocaria um
// erro por outro.
func TestFallbackNaoTrocaSeSecundarioNaoTemVision(t *testing.T) {
	primary := &fakeChat{name: "ds-vision", vision: true, err: &openai.APIError{HTTPStatusCode: 503}}
	secondary := &fakeChat{name: "sem-vision", vision: false, reply: "backup"}
	fc := NewFallbackChat(primary, secondary, nil)

	req := llm.ChatRequest{Messages: []llm.Message{{
		Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "image", ImageData: "QUJD"}},
	}}}
	if _, err := fc.Chat(context.Background(), req); err == nil {
		t.Fatal("deveria propagar o erro original")
	}
	if secondary.calls != 0 {
		t.Fatal("secundario sem vision nao devia receber turno com imagem")
	}
}

// ---------------------------------------------------------------------------
// alertAdminOnce
// ---------------------------------------------------------------------------

func TestAlertAdminOnceDeduplica(t *testing.T) {
	alertMu.Lock()
	alertSeen = map[string]time.Time{}
	alertMu.Unlock()

	var sent int
	a := &Agent{
		cfg:     &Config{AdminPhones: []string{"5561999999999"}},
		sendMsg: func(phone, text string) error { sent++; return nil },
	}
	a.alertAdminOnce("saldo_test", "saldo acabou")
	a.alertAdminOnce("saldo_test", "saldo acabou")
	if sent != 1 {
		t.Fatalf("avisos enviados = %d, quer 1 (dedupe de 1h)", sent)
	}
}

func TestAlertAdminOnceSemTelefoneNaoQuebra(t *testing.T) {
	a := &Agent{cfg: &Config{}, sendMsg: func(string, string) error { return nil }}
	a.alertAdminOnce("k", "msg") // nao pode entrar em panico
}

func TestAlertAdminOnceSemSendMsgNaoQuebra(t *testing.T) {
	a := &Agent{cfg: &Config{AdminPhones: []string{"5561"}}}
	a.alertAdminOnce("k2", "msg")
}

// Indisponibilidade passageira nao pode virar alerta — senao o admin aprende
// a ignorar a notificacao.
func TestFailoverAlertIgnoraErroPassageiro(t *testing.T) {
	alertMu.Lock()
	alertSeen = map[string]time.Time{}
	alertMu.Unlock()

	var sent int
	a := &Agent{
		cfg:     &Config{AdminPhones: []string{"5561999999999"}},
		sendMsg: func(string, string) error { sent++; return nil },
	}
	a.llmFailoverAlert("clinico")(&openai.APIError{HTTPStatusCode: 503})
	if sent != 0 {
		t.Fatal("503 passageiro nao pode virar alerta de admin")
	}
	a.llmFailoverAlert("clinico")(errors.New("Insufficient Balance"))
	if sent != 1 {
		t.Fatalf("saldo deveria alertar: sent=%d", sent)
	}
}
