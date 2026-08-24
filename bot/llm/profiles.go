package llm

// Profile descreve as particularidades de um backend OpenAI-compatible.
// DeepSeek e Gemini falam o mesmo protocolo; o que muda e endpoint, modelo
// default e alguns campos proprietarios. Um tradutor, varios perfis.
type Profile struct {
	Name         string
	BaseURL      string
	DefaultModel string

	// DisableThinking manda {"thinking":{"type":"disabled"}} no request.
	// Campo proprietario da DeepSeek: no V4 o raciocinio vem LIGADO por
	// default e consome o max_tokens — com orcamento baixo o content volta
	// vazio, o que no WhatsApp do idoso vira mensagem em branco.
	DisableThinking bool

	// SupportsVisionInChat indica se o modelo de chat aceita bloco de imagem.
	// DeepSeek chat: false (recusa com HTTP 400 "does not support image");
	// a visao vive num modelo separado. Gemini: true.
	SupportsVisionInChat bool
}

// ProfileDeepSeek — deepseek-v4-flash.
//
// NAO usar deepseek-chat nem deepseek-reasoner: foram descontinuados em
// 24/07/2026. Ainda resolvem como alias nao-documentado, mas sairam do
// /models e da pagina de precos.
//
// Flash e nao Pro: o Pro custa 3,1x mais e pontua PIOR em benchmark de agente.
func ProfileDeepSeek() Profile {
	return Profile{
		Name:            "deepseek",
		BaseURL:         "https://api.deepseek.com",
		DefaultModel:    "deepseek-v4-flash",
		DisableThinking: true,
	}
}

// ProfileDeepSeekVision — modelo de visao da DeepSeek. Experimental: teto de
// 384 tokens por imagem, sem OCR, sem PDF. Por isso a visao NUNCA decide
// medicamento — quem decide e o drug_catalog (CMED + matching fonetico).
func ProfileDeepSeekVision() Profile {
	p := ProfileDeepSeek()
	p.DefaultModel = "deepseek-v4-flash-vision-exp"
	p.SupportsVisionInChat = true
	return p
}

// ProfileGemini — endpoint OpenAI-compatible do Google.
func ProfileGemini() Profile {
	return Profile{
		Name:                 "gemini",
		BaseURL:              "https://generativelanguage.googleapis.com/v1beta/openai",
		DefaultModel:         "gemini-3.6-flash",
		SupportsVisionInChat: true,
	}
}

// ProfileByName resolve o perfil pelo nome usado nas env vars de papel.
func ProfileByName(name string) (Profile, bool) {
	switch name {
	case "deepseek":
		return ProfileDeepSeek(), true
	case "deepseek-vision":
		return ProfileDeepSeekVision(), true
	case "gemini":
		return ProfileGemini(), true
	}
	return Profile{}, false
}
