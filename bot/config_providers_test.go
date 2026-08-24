package main

import (
	"strings"
	"testing"
)

// setMinimalEnv seta o minimo que LoadConfig exige, deixando as vars de LLM
// a cargo de cada teste.
func setMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("GOOGLE_CLIENT_ID", "gid")
	t.Setenv("GOOGLE_CLIENT_SECRET", "gsecret")
	t.Setenv("ENCRYPTION_KEY", strings.Repeat("a", 64))
	// Limpa tudo que possa vazar do ambiente do desenvolvedor.
	for _, k := range []string{
		"ANTHROPIC_API_KEY", "DEEPSEEK_API_KEY", "GEMINI_API_KEY",
		"DEEPSEEK_MODEL", "DEEPSEEK_BASE_URL", "GEMINI_MODEL",
		"LLM_PROVIDER_COMPANION", "LLM_PROVIDER_OPERACIONAL", "LLM_PROVIDER_CLINICO",
		"LLM_PROVIDER_SALES", "LLM_PROVIDER_ANALYSIS", "LLM_PROVIDER_REPORT",
		"LLM_PROVIDER_VISION", "OUTPUT_GUARD_MODE", "GUARD_ENFORCE_PHONES",
	} {
		t.Setenv(k, "")
	}
}

// A migracao inteira depende disto: o bot tem que subir SEM chave da
// Anthropic. Era exatamente o que impedia o sistema de voltar ao ar com o
// cartao cancelado.
func TestLoadConfigNaoExigeAnthropicQuandoTudoDeepSeek(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("bot tem que subir sem chave Anthropic: %v", err)
	}
	if cfg.LLMProviderOperacional != "deepseek" {
		t.Fatalf("default do operacional = %q, quer deepseek", cfg.LLMProviderOperacional)
	}
	if cfg.LLMProviderClinico != "deepseek" {
		t.Fatalf("default do clinico = %q, quer deepseek", cfg.LLMProviderClinico)
	}
	if cfg.LLMProviderVision != "deepseek-vision" {
		t.Fatalf("default da visao = %q, quer deepseek-vision", cfg.LLMProviderVision)
	}
}

// Se um papel aponta pra anthropic, ai sim a chave e obrigatoria — falhar no
// boot e melhor que descobrir no turno de um usuario.
func TestLoadConfigExigeChaveDoProviderEscolhido(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("LLM_PROVIDER_REPORT", "anthropic")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("papel apontando pra anthropic sem chave deveria falhar no boot")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("erro nao aponta a var faltante: %v", err)
	}
}

func TestLoadConfigExigeChaveGemini(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("LLM_PROVIDER_CLINICO", "gemini")
	_, err := LoadConfig()
	if err == nil || !strings.Contains(err.Error(), "GEMINI_API_KEY") {
		t.Fatalf("clinico=gemini sem chave deveria falhar apontando GEMINI_API_KEY: %v", err)
	}
}

func TestDeepSeekModelDefault(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeepSeekModel != "deepseek-v4-flash" {
		t.Fatalf("modelo = %q, quer deepseek-v4-flash (deepseek-chat foi aposentado)", cfg.DeepSeekModel)
	}
}

func TestDeepSeekModelSobrescrivelPorEnv(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("DEEPSEEK_MODEL", "deepseek-v4-pro")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeepSeekModel != "deepseek-v4-pro" {
		t.Fatalf("modelo = %q", cfg.DeepSeekModel)
	}
}

// Provider desconhecido tem que falhar no boot, nao cair em fallback
// silencioso — foi assim que o companion ficou dormente sem ninguem notar.
func TestProviderDesconhecidoFalhaNoBoot(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("LLM_PROVIDER_OPERACIONAL", "gpt5")
	_, err := LoadConfig()
	if err == nil {
		t.Fatal("provider desconhecido tem que falhar no boot")
	}
	if !strings.Contains(err.Error(), "gpt5") {
		t.Fatalf("erro nao mostra o valor invalido: %v", err)
	}
}

// .env antigo com LLM_PROVIDER_COMPANION nao pode mudar de comportamento em
// silencio no deploy.
func TestCompanionLegadoViraClinico(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("LLM_PROVIDER_COMPANION", "anthropic")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMProviderClinico != "anthropic" {
		t.Fatalf("clinico = %q, quer herdar do LLM_PROVIDER_COMPANION legado", cfg.LLMProviderClinico)
	}
	// O operacional NAO herda — o legado so falava do companion.
	if cfg.LLMProviderOperacional != "deepseek" {
		t.Fatalf("operacional = %q, nao deveria herdar do legado", cfg.LLMProviderOperacional)
	}
}

// LLM_PROVIDER_CLINICO explicito ganha do legado.
func TestClinicoExplicitoGanhaDoLegado(t *testing.T) {
	setMinimalEnv(t)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant")
	t.Setenv("DEEPSEEK_API_KEY", "sk-x")
	t.Setenv("LLM_PROVIDER_COMPANION", "anthropic")
	t.Setenv("LLM_PROVIDER_CLINICO", "deepseek")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLMProviderClinico != "deepseek" {
		t.Fatalf("clinico = %q, quer deepseek (explicito ganha)", cfg.LLMProviderClinico)
	}
}

// ---------------------------------------------------------------------------
// Construcao de providers
// ---------------------------------------------------------------------------

func TestBuildChatProviderRespeitaModelo(t *testing.T) {
	cfg := &Config{DeepSeekAPIKey: "k", DeepSeekModel: "deepseek-v4-pro"}
	p := buildChatProvider(cfg, "deepseek")
	if p.Name() != "deepseek" {
		t.Fatalf("Name = %q", p.Name())
	}
	if p.SupportsVision() {
		t.Fatal("chat da DeepSeek nao tem vision")
	}
}

func TestBuildVisionProviderUsaModeloDeVisao(t *testing.T) {
	cfg := &Config{DeepSeekAPIKey: "k", DeepSeekModel: "deepseek-v4-flash"}
	v := buildVisionProvider(cfg, "deepseek-vision")
	if v.Name() != "deepseek" {
		t.Fatalf("Name = %q", v.Name())
	}
}

// DEEPSEEK_MODEL nao pode sobrescrever o modelo de VISAO — sao modelos
// diferentes e o de chat recusa imagem com HTTP 400.
func TestDeepSeekModelNaoVazaProPerfilDeVisao(t *testing.T) {
	cfg := &Config{DeepSeekAPIKey: "k", DeepSeekModel: "deepseek-v4-pro"}
	_, prof := providerParts(cfg, "deepseek-vision")
	if prof.DefaultModel != "deepseek-v4-flash-vision-exp" {
		t.Fatalf("perfil de visao virou %q — DEEPSEEK_MODEL vazou", prof.DefaultModel)
	}
	if !prof.SupportsVisionInChat {
		t.Fatal("perfil de visao tem que aceitar imagem")
	}
}
