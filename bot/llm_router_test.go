package main

import (
	"context"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// ---------------------------------------------------------------------------
// Fakes de provider — reutilizados pelos testes de convergencia e fallback.
// ---------------------------------------------------------------------------

// fakeChat implementa llm.ChatProvider gravando o que recebeu.
type fakeChat struct {
	name    string
	reply   string
	err     error
	vision  bool
	noTools bool

	// alwaysCallTool != "" faz o fake pedir SEMPRE a mesma tool com os mesmos
	// argumentos — o cenario de loop infinito do DeepSeek.
	alwaysCallTool string
	toolArgs       string

	calls       int
	lastRequest llm.ChatRequest
}

func (f *fakeChat) Name() string         { return f.name }
func (f *fakeChat) SupportsTools() bool  { return !f.noTools }
func (f *fakeChat) SupportsVision() bool { return f.vision }

func (f *fakeChat) Chat(ctx context.Context, req llm.ChatRequest) (llm.ChatResponse, error) {
	f.calls++
	f.lastRequest = req
	if f.err != nil {
		return llm.ChatResponse{}, f.err
	}
	if f.alwaysCallTool != "" {
		args := f.toolArgs
		if args == "" {
			args = "{}"
		}
		return llm.ChatResponse{
			StopReason: llm.StopToolUse,
			ModelUsed:  f.name + "-model",
			Content: []llm.ContentBlock{{
				Type: "tool_use", ToolUseID: "call-1",
				ToolName: f.alwaysCallTool, ToolInput: []byte(args),
			}},
		}, nil
	}
	return llm.ChatResponse{
		StopReason: llm.StopEndTurn,
		ModelUsed:  f.name + "-model",
		Content:    []llm.ContentBlock{{Type: "text", Text: f.reply}},
		Usage:      llm.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

// fakeVision implementa llm.VisionProvider.
type fakeVision struct {
	desc  string
	err   error
	calls int
}

func (f *fakeVision) Name() string { return "fake-vision" }
func (f *fakeVision) DescribeImage(ctx context.Context, req llm.VisionRequest) (llm.VisionResponse, error) {
	f.calls++
	if f.err != nil {
		return llm.VisionResponse{}, f.err
	}
	return llm.VisionResponse{Text: f.desc}, nil
}

// ---------------------------------------------------------------------------
// Helpers de seed
// ---------------------------------------------------------------------------

func routerAgent(t *testing.T) *Agent {
	t.Helper()
	db := setupTestDB(t)
	return &Agent{db: db, cfg: &Config{OutputGuardMode: "log"}}
}

func seedUser(t *testing.T, db *DB, phone, name string, typ UserType) *User {
	t.Helper()
	u := &User{PhoneNumber: phone, Name: name}
	if err := db.CreateUser(u); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if typ != "" {
		if _, err := db.conn.Exec(`UPDATE users SET type = ? WHERE id = ?`, string(typ), u.ID); err != nil {
			t.Fatalf("set type: %v", err)
		}
		u.Type = typ
	}
	return u
}

// ---------------------------------------------------------------------------
// hasHealthSurface
// ---------------------------------------------------------------------------

func TestHasHealthSurfaceIdoso(t *testing.T) {
	a := routerAgent(t)
	u := seedUser(t, a.db, "5561900000001", "Fabio", UserTypeIdoso)
	if !a.hasHealthSurface(u) {
		t.Fatal("idoso sempre tem superficie de saude")
	}
}

func TestHasHealthSurfaceAdultoSoAgenda(t *testing.T) {
	a := routerAgent(t)
	u := seedUser(t, a.db, "5561900000002", "Ana", "")
	if a.hasHealthSurface(u) {
		t.Fatal("adulto sem medicacao e sem dependente nao tem superficie de saude")
	}
}

func TestHasHealthSurfaceAdultoComMedicacaoPropria(t *testing.T) {
	a := routerAgent(t)
	u := seedUser(t, a.db, "5561900000003", "Carlos", "")
	m := &Medication{UserID: u.ID, Name: "Losartana", Active: true}
	if err := a.db.CreateMedication(m); err != nil {
		t.Fatalf("CreateMedication: %v", err)
	}
	if !a.hasHealthSurface(u) {
		t.Fatal("adulto com medicacao ativa tem superficie de saude")
	}
}

// O caso que motiva rotear por usuario e nao por agente: o filho adulto
// cadastra o remedio da mae pelo caminho OPERACIONAL.
func TestHasHealthSurfaceResponsavelPorDependente(t *testing.T) {
	a := routerAgent(t)
	guardian := seedUser(t, a.db, "5561900000004", "Filho", "")
	dependent := seedUser(t, a.db, "5561900000005", "Mae", UserTypeIdoso)
	if _, err := a.db.LinkFamily(guardian.ID, dependent.ID, "filho"); err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}
	if !a.hasHealthSurface(guardian) {
		t.Fatal("responsavel por dependente carrega posologia pelo caminho operacional")
	}
}

func TestHasHealthSurfaceNilSeguro(t *testing.T) {
	a := routerAgent(t)
	if a.hasHealthSurface(nil) {
		t.Fatal("nil nao pode ser tratado como superficie de saude")
	}
}

// ---------------------------------------------------------------------------
// chatFor
// ---------------------------------------------------------------------------

func TestChatForRoteiaPelaSensibilidade(t *testing.T) {
	a := routerAgent(t)
	op := &fakeChat{name: "op"}
	cl := &fakeChat{name: "clinico"}
	a.chat, a.companionChat = op, cl

	plain := seedUser(t, a.db, "5561900000006", "Ana", "")
	if got := a.chatFor(plain); got.Name() != "op" {
		t.Fatalf("usuario sem saude foi pro %q", got.Name())
	}
	idoso := seedUser(t, a.db, "5561900000007", "Fabio", UserTypeIdoso)
	if got := a.chatFor(idoso); got.Name() != "clinico" {
		t.Fatalf("idoso foi pro %q", got.Name())
	}
}

// Degradar e melhor que nao responder a um idoso.
func TestChatForFallbackQuandoClinicoNil(t *testing.T) {
	a := routerAgent(t)
	a.chat = &fakeChat{name: "op"}
	a.companionChat = nil
	idoso := seedUser(t, a.db, "5561900000008", "Fabio", UserTypeIdoso)
	got := a.chatFor(idoso)
	if got == nil || got.Name() != "op" {
		t.Fatal("sem clinico, idoso cai no operacional")
	}
}

// ---------------------------------------------------------------------------
// engineNameFor
// ---------------------------------------------------------------------------

func TestEngineNameFor(t *testing.T) {
	a := routerAgent(t)
	if got := a.engineNameFor(&User{Type: UserTypeIdoso}); got != "companion" {
		t.Errorf("idoso -> %q", got)
	}
	if got := a.engineNameFor(&User{}); got != "operational" {
		t.Errorf("adulto -> %q", got)
	}
	if got := a.engineNameFor(nil); got != "operational" {
		t.Errorf("nil -> %q", got)
	}
}

// ---------------------------------------------------------------------------
// recordUsage
// ---------------------------------------------------------------------------

func TestRecordUsageGrava(t *testing.T) {
	a := routerAgent(t)
	u := seedUser(t, a.db, "5561900000009", "Ana", "")
	a.recordUsage(u, "operational", "deepseek", "deepseek-v4-flash",
		llm.Usage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 80}, 950)
	sums, err := a.db.SumLLMUsageSince(timeMinusHour())
	if err != nil {
		t.Fatal(err)
	}
	if sums["deepseek|deepseek-v4-flash"].InputTokens != 100 {
		t.Fatalf("nao gravou: %+v", sums)
	}
}

// Contabilidade nunca pode derrubar o turno: sem db, e no-op silencioso.
func TestRecordUsageSemDBNaoQuebra(t *testing.T) {
	a := &Agent{}
	a.recordUsage(&User{ID: 1}, "operational", "deepseek", "m", llm.Usage{}, 0)
}

func timeMinusHour() time.Time { return time.Now().Add(-time.Hour) }
