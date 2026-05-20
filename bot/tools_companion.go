package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// =========================================================================
// Tools do companion (Fase 4)
// =========================================================================
//
// alertar_familia       — escotilha unica para sinal serio de saude/risco.
// pausar_proatividade   — idoso pede tregua de mensagens proativas.
// comentar_imagem       — descricao de imagem via Haiku vision.
// comentar_link         — preview de Open Graph com domain allowlist.
//
// Todas tem guard explicito: user.Type==idoso. Operacional nao chama.
// Tools sao registradas em companionToolHandlers e mergeadas no registry
// global de tools.go::buildToolHandlers.

// companionToolHandlers eh o sub-registry da Fase 4. Mantido em mapa
// proprio pra preservar coesao por feature (mesma estrategia da Fase 3
// com medicationToolHandlers).
var companionToolHandlers = map[string]ToolHandler{
	"alertar_familia":      handleAlertarFamilia,
	"pausar_proatividade":  handlePausarProatividade,
	"comentar_imagem":      handleComentarImagem,
	"comentar_link":        handleComentarLink,
}

// -------------------------------------------------------------------------
// alertar_familia
// -------------------------------------------------------------------------

type alertarFamiliaParams struct {
	Severity          string `json:"severity"`
	Category          string `json:"category"`
	Reason            string `json:"reason"`
	RecommendedAction string `json:"recommended_action"`
}

// AlertarFamiliaResult eh o JSON que volta pro modelo depois do tool call.
// O agent DEVE respeitar DiscloseToElder na sua resposta — esta eh a
// fronteira de confianca: dizer ao idoso que avisou a familia em casos
// psicologicos/violencia pode quebrar permanentemente a relacao.
type AlertarFamiliaResult struct {
	DiscloseToElder bool     `json:"disclose_to_elder"`
	SuggestedTone   string   `json:"suggested_tone"`
	Note            string   `json:"note"`
	SentTo          []string `json:"sent_to,omitempty"`
	FailedFor       []string `json:"failed_for,omitempty"`
	Cooldown        bool     `json:"cooldown,omitempty"`
}

// disclosurePolicy mapeia categoria -> orientacao de transparencia ao idoso.
// E DADO, nao codigo: politica nova = entrada nova no map. Nunca uma logica
// "if/else" espalhada pelo handler.
//
// A barreira de privacidade entre idoso e Lurch eh fundamento da feature —
// quebra-la inutiliza o produto pra esse idoso especifico (ele para de
// confiar). Por isso usamos data driven (nao codigo solto) e testamos
// table-driven em TestAlertarFamilia_DisclosurePolicyByCategory.
var disclosurePolicy = map[string]struct {
	Disclose bool
	Tone     string
	Note     string
}{
	"medico_fisico": {
		Disclose: true,
		Tone: "calmo e tranquilizador, mencione que avisou alguem da familia (ele ja sabe que esta passando mal — saber que a ajuda esta a caminho conforta), reforce 192 (SAMU) se for sintoma agudo",
		Note: "Pode dizer: 'avisei sua filha pra ela ficar atenta, ta?'",
	},
	"psicologico": {
		Disclose: false,
		Tone: "acolhedor, presente, sem sair do papel de amigo. NAO mencione que avisou a familia. Mencione 188 (CVV) como se fosse so uma sugestao de canal de apoio que voce conhece, nao como contramedida ao alerta. Continue a escuta — ele te confiou algo intimo, nao o desampare agora.",
		Note: "REGRA DURA: nunca dizer 'avisei sua filha' nem equivalente. A confianca dele em voce e fundamento da intervencao continuar funcionando.",
	},
	"violencia": {
		Disclose: false,
		Tone: "absolutamente normal, como se a conversa nao tivesse mudado de tom. Idoso pode estar sendo monitorado pelo agressor neste momento — qualquer sinal de mudanca de comportamento na conversa pode escalar o risco fisico dele.",
		Note: "REGRA DURA: nao mencione, nao mude o tom da conversa, nao ofereca 188/192 explicitamente. Se ele perguntar diretamente 'voce vai contar pra alguem', responda evasivo em registro classico (ex: 'fique tranquilo, estou aqui te escutando, e isso por enquanto'). A familia ja foi alertada com urgencia.",
	},
	"negligencia": {
		Disclose: false,
		Tone: "normal, sem alarme. Mesma logica da vigilancia da violencia — pode haver vigilancia.",
		Note: "REGRA DURA: nao mencione. A intervencao tem que vir de fora.",
	},
	"outros": {
		Disclose: false,
		Tone: "discreto. Em duvida, prefira nao mencionar — confianca uma vez quebrada nao volta. Use discricao por padrao.",
		Note: "Categoria ambigua — escolhi discricao por padrao. Se voce achar que faz sentido contar, pondere antes de dizer.",
	},
}

// validSeverities define o conjunto canonico — match exato com schema.
var validSeverities = map[string]bool{
	"info":     true,
	"warn":     true,
	"critical": true,
}

// severityCooldown define o intervalo minimo entre dois disparos da mesma
// severity pro mesmo idoso. info nao tem cooldown (e so log).
func severityCooldown(s string) time.Duration {
	switch s {
	case "critical":
		return 1 * time.Hour
	case "warn":
		return 6 * time.Hour
	default:
		return 0
	}
}

func handleAlertarFamilia(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p alertarFamiliaParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	// Guard duro: tool so faz sentido pra idoso. Charles operacional nao
	// deveria chamar isso — nao tem contexto familiar adequado.
	if user == nil || user.Type != UserTypeIdoso {
		userType := ""
		if user != nil {
			userType = string(user.Type)
		}
		log.Printf("[%s] alertar_familia chamada mas user.Type=%s — ignorando", safeName(user), userType)
		return "Esta ferramenta so esta disponivel no modo companion.", nil
	}

	// Validacao defensiva — Anthropic ja valida pelo schema, mas DeepSeek
	// pode ser mais leniente. Nunca enviar com severity desconhecida.
	p.Severity = strings.ToLower(strings.TrimSpace(p.Severity))
	if !validSeverities[p.Severity] {
		return "severity invalido. Use info, warn ou critical.", nil
	}
	if strings.TrimSpace(p.Reason) == "" {
		return "reason e obrigatorio.", nil
	}

	// Categoria desconhecida -> fallback "outros" (default seguro = discricao).
	p.Category = strings.ToLower(strings.TrimSpace(p.Category))
	if _, ok := disclosurePolicy[p.Category]; !ok {
		log.Printf("[%s] alertar_familia: category invalida %q, fallback=outros", user.Name, p.Category)
		p.Category = "outros"
	}
	pol := disclosurePolicy[p.Category]

	// Cooldown: se ja houve severe_signal recente para esse idoso na
	// mesma severity, nao reenvia mensagem ao guardian (evita spam em
	// rajada de ambiguidade). Ainda registra a row pra observabilidade
	// — usar policy_name="severe_signal_supressed".
	cooldown := severityCooldown(p.Severity)
	if cooldown > 0 && agent != nil && agent.db != nil {
		recent, err := agent.db.HasRecentSevereSignalEscalation(user.ID, p.Severity, cooldown)
		if err != nil {
			log.Printf("[%s] alertar_familia: HasRecentSevereSignalEscalation: %v", user.Name, err)
			// Continua mesmo com erro de DB — preferimos enviar duplicado a
			// silenciar alerta legitimo.
		}
		if recent {
			log.Printf("[%s] alertar_familia: cooldown ativo para severity=%s — registrando sem reenvio",
				user.Name, p.Severity)
			details := fmt.Sprintf(
				"severity=%s|category=%s|reason=%s|cooldown=true",
				p.Severity, p.Category, sanitizeForDetails(p.Reason),
			)
			_, _ = agent.db.RecordSevereSignalEscalation(
				user.ID, "severe_signal_supressed", p.Severity, details, 0, "", time.Now(),
			)
			if agent.audit != nil {
				agent.audit.LogAlertarFamilia(user.ID, p.Severity, p.Category, p.Reason, nil, nil, true)
			}
			result := AlertarFamiliaResult{
				DiscloseToElder: pol.Disclose,
				SuggestedTone:   pol.Tone,
				Note:            "FAMILIA JA NOTIFICADA RECENTEMENTE (cooldown ativo). Nao reenviei. " + pol.Note,
				Cooldown:        true,
			}
			out, _ := json.Marshal(result)
			return string(out), nil
		}
	}

	// Lista guardians, filtra opt-in para sinais sérios.
	allGuardians, err := agent.db.GetGuardians(user.ID)
	if err != nil {
		return "", fmt.Errorf("get guardians: %w", err)
	}
	guardians := make([]FamilyLink, 0, len(allGuardians))
	for _, g := range allGuardians {
		if g.Notify.OnSevereSignal {
			guardians = append(guardians, g)
		}
	}

	msg := formatFamilyAlertMessage(user, p)

	// Envia pra cada guardian. Erro em um nao bloqueia outros. Cada envio
	// vira uma row em escalations(policy=severe_signal).
	var sentTo []string
	var failedFor []string
	now := time.Now()
	for _, g := range guardians {
		if g.Other == nil {
			continue
		}
		gName := g.Other.Name
		gPhone := g.Other.PhoneNumber
		if agent.sendMsg == nil {
			failedFor = append(failedFor, gName)
			continue
		}
		if err := agent.sendMsg(gPhone, msg); err != nil {
			log.Printf("alertar_familia: send to %s (%s): %v", gName, gPhone, err)
			failedFor = append(failedFor, gName)
			continue
		}
		sentTo = append(sentTo, gName)

		// Registra row em escalations.
		details := fmt.Sprintf(
			"severity=%s|category=%s|reason=%s|recommended_action=%s",
			p.Severity, p.Category, sanitizeForDetails(p.Reason), sanitizeForDetails(p.RecommendedAction),
		)
		_, escErr := agent.db.RecordSevereSignalEscalation(
			user.ID, "severe_signal", p.Severity, details, g.Other.ID, "whatsapp", now,
		)
		if escErr != nil {
			log.Printf("alertar_familia: record escalation: %v", escErr)
		}
	}

	// Audit (estruturado pipe-separated).
	if agent.audit != nil {
		agent.audit.LogAlertarFamilia(user.ID, p.Severity, p.Category, p.Reason, sentTo, failedFor, false)
	}

	log.Printf("[%s] alertar_familia severity=%s category=%s sent_to=%v failed_for=%v",
		user.Name, p.Severity, p.Category, sentTo, failedFor)

	// Resultado pro modelo — JSON com orientacao de transparencia.
	result := AlertarFamiliaResult{
		DiscloseToElder: pol.Disclose,
		SuggestedTone:   pol.Tone,
		Note:            pol.Note,
		SentTo:          sentTo,
		FailedFor:       failedFor,
	}

	// Caso especial: nenhum guardian opt-in. Bot sem escotilha real.
	// Registra a tentativa pra observabilidade (Fase 5 detecta idosos
	// orfaos), mantem discricao do mapa, mas avisa o agente do gap.
	if len(guardians) == 0 {
		details := fmt.Sprintf(
			"severity=%s|category=%s|reason=%s|orphan=true",
			p.Severity, p.Category, sanitizeForDetails(p.Reason),
		)
		if agent.db != nil {
			_, _ = agent.db.RecordSevereSignalEscalation(
				user.ID, "severe_signal", p.Severity, details, 0, "", now,
			)
		}
		result.Note = "AVISO: nenhum familiar cadastrado. " + result.Note +
			" Considere mencionar 188/192 como canal de apoio se severity=critical e category permite (medico_fisico SIM, psicologico em tom de sugestao leve, violencia/negligencia NAO)."
	} else if len(sentTo) == 0 {
		result.Note = "FALHA AO ENVIAR a todos os familiares cadastrados. " + result.Note +
			" Registrado em log."
	}

	out, err := json.Marshal(result)
	if err != nil {
		// Fallback: serializacao nao deveria falhar com structs simples,
		// mas se falhar, retorna texto que ainda preserve a regra dura.
		log.Printf("alertar_familia: json.Marshal result: %v", err)
		if pol.Disclose {
			return fmt.Sprintf("Alerta enviado para: %s. severity=%s. Pode mencionar ao idoso que avisou.",
				strings.Join(sentTo, ", "), p.Severity), nil
		}
		return fmt.Sprintf("Alerta enviado para: %s. severity=%s. NAO mencione ao idoso que voce avisou.",
			strings.Join(sentTo, ", "), p.Severity), nil
	}
	return string(out), nil
}

// formatFamilyAlertMessage monta o texto WhatsApp enviado aos guardians.
// Tom escala com severity: critical eh direto e curto, warn eh cuidadoso,
// info eh informacional. Sempre inclui nome do idoso e o que ele disse.
func formatFamilyAlertMessage(elder *User, p alertarFamiliaParams) string {
	var sb strings.Builder
	switch p.Severity {
	case "critical":
		sb.WriteString(fmt.Sprintf("URGENTE — %s precisa de atencao agora.\n\n", elder.Name))
	case "warn":
		sb.WriteString(fmt.Sprintf("Atencao — %s deu um sinal preocupante.\n\n", elder.Name))
	case "info":
		sb.WriteString(fmt.Sprintf("Aviso — %s mencionou algo a observar.\n\n", elder.Name))
	}
	sb.WriteString(fmt.Sprintf("O que ele(a) me contou: %s\n", p.Reason))
	if strings.TrimSpace(p.RecommendedAction) != "" {
		sb.WriteString(fmt.Sprintf("\nSugestao: %s\n", p.RecommendedAction))
	}
	if p.Severity == "critical" {
		sb.WriteString(
			"\nSe nao conseguir contato direto, " +
				"lembre-se: 188 (CVV — apoio emocional 24h) e 192 (SAMU — emergencia medica).\n",
		)
	}
	sb.WriteString("\n— Lurch (companion de " + elder.Name + ")")
	return sb.String()
}

// sanitizeForDetails normaliza um valor para o blob pipe-separated do
// audit/escalations: tira pipes e quebras de linha pra nao quebrar o
// parser ad-hoc.
func sanitizeForDetails(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "|", "/")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 500 {
		s = s[:500] + "...[truncated]"
	}
	return s
}

func safeName(u *User) string {
	if u == nil {
		return "<nil>"
	}
	return u.Name
}

// -------------------------------------------------------------------------
// pausar_proatividade
// -------------------------------------------------------------------------

type pausarProatividadeParams struct {
	Dias int `json:"dias"`
}

func handlePausarProatividade(_ context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p pausarProatividadeParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	if user == nil || user.Type != UserTypeIdoso {
		return "Pausa de proatividade so aplica em modo companion.", nil
	}
	if p.Dias < 1 {
		p.Dias = 1
	}
	if p.Dias > 30 {
		p.Dias = 30
	}
	if err := agent.db.PauseProactive(user.ID, p.Dias); err != nil {
		return "", fmt.Errorf("pause proactive: %w", err)
	}
	if agent.audit != nil {
		agent.audit.Log(user.ID, "pausar_proatividade", "", fmt.Sprintf("dias=%d", p.Dias))
	}
	return fmt.Sprintf("Combinado, nao te incomodo por %d dia(s). Volto depois.", p.Dias), nil
}

// -------------------------------------------------------------------------
// comentar_imagem
// -------------------------------------------------------------------------

type comentarImagemParams struct {
	ImageID     string `json:"image_id"`
	ContextHint string `json:"context_hint"`
}

// MediaLoader eh a interface que o handler espera pra carregar a imagem
// do cache de midia. Fase 4 nao implementa o cache (vem em PR-MEDIA-1
// dedicado, com whatsmeow), mas a interface fica pronta — testes injetam
// um stub.
type MediaLoader interface {
	Load(id string) (data []byte, mediaType string, err error)
}

// agent.media eh um campo opcional. Quando nil, comentar_imagem retorna
// erro estruturado — companion entao pede pro idoso contar do que se
// trata. Quando setado e vision provider tambem disponivel, faz a chamada
// real ao Haiku.
//
// Nota: declarado em fields adicionais via tipo MediaLoader (ver
// agent_media.go). Na Fase 4 deixamos o gancho mas nao injetamos o
// cache (PR-MEDIA-1 da Fase 4 fara isso).

func handleComentarImagem(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p comentarImagemParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}
	if strings.TrimSpace(p.ImageID) == "" {
		return "image_id e obrigatorio.", nil
	}

	if agent == nil || agent.media == nil {
		return "Cache de imagens nao configurado neste ambiente. Pede pro idoso te contar do que se trata.", nil
	}
	if agent.vision == nil {
		return "Vision provider nao configurado. Pede pro idoso te contar do que se trata.", nil
	}

	media, mediaType, err := agent.media.Load(p.ImageID)
	if err != nil {
		return fmt.Sprintf("Imagem nao encontrada (id=%s).", p.ImageID), nil
	}
	if !isSupportedImageType(mediaType) {
		return fmt.Sprintf("Tipo de imagem nao suportado: %s.", mediaType), nil
	}

	prompt := "Descreva esta imagem que um idoso recebeu numa conversa de WhatsApp. " +
		"Foco no que humanamente e interessante: pessoas, lugar, comida, animal, evento. " +
		"Nao descreva pixels, composicao ou estilo fotografico. Nao infira sentimentos " +
		"clinicos. Em PT-BR, 2-3 frases. Ao final, em uma linha separada, classifique o " +
		"tom em: familia | meme | paisagem | comida | religioso | humoristico | outros. " +
		"Formato: 'TOM: <classe>'."
	if p.ContextHint != "" {
		prompt += "\n\nContexto adicional: " + p.ContextHint
	}

	visionCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	resp, err := agent.vision.DescribeImage(visionCtx, llm.VisionRequest{
		Prompt:     prompt,
		ImageMedia: mediaType,
		ImageData:  base64.StdEncoding.EncodeToString(media),
		MaxTokens:  300,
	})
	if err != nil {
		return "", fmt.Errorf("vision: %w", err)
	}

	desc, tone := splitDescTone(resp.Text)
	if agent.audit != nil {
		agent.audit.Log(user.ID, "comentar_imagem", p.ImageID,
			fmt.Sprintf("tone=%s tokens_in=%d tokens_out=%d", tone,
				resp.Usage.InputTokens, resp.Usage.OutputTokens))
	}

	out := struct {
		Descricao   string `json:"descricao"`
		TomSugerido string `json:"tom_sugerido"`
	}{Descricao: desc, TomSugerido: tone}
	j, _ := json.Marshal(out)
	return string(j), nil
}

// isSupportedImageType retorna true se mediaType for um formato que o
// Haiku vision aceita.
func isSupportedImageType(t string) bool {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "image/jpeg", "image/jpg", "image/png", "image/webp", "image/gif":
		return true
	}
	return false
}

// splitDescTone separa "descricao\nTOM: classe" — tom default = "outros".
func splitDescTone(text string) (desc, tone string) {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	tone = "outros"
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(strings.ToUpper(l), "TOM:") {
			tone = strings.ToLower(strings.TrimSpace(l[4:]))
			lines = lines[:i]
			break
		}
	}
	desc = strings.TrimSpace(strings.Join(lines, " "))
	return
}

// -------------------------------------------------------------------------
// comentar_link
// -------------------------------------------------------------------------

type comentarLinkParams struct {
	URL string `json:"url"`
}

// LinkPreview e o output de comentar_link. Json devolvido ao modelo.
type LinkPreview struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	ImageURL    string `json:"image_url,omitempty"`
	Host        string `json:"host"`
	OGType      string `json:"og_type,omitempty"`
}

const (
	linkFetchTimeout = 3 * time.Second
	linkMaxBody      = 64 * 1024 // 64KB
	linkMaxRedirects = 2
	linkUserAgent    = "Lurch-Bot/1.0 (+https://lurch.bot/about)"
)

// httpClientFactory permite injetar transports custom em testes — default
// usa http.DefaultTransport via http.Client.
var httpClientFactory = func() *http.Client {
	return &http.Client{
		Timeout: linkFetchTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= linkMaxRedirects {
				return fmt.Errorf("too many redirects")
			}
			host := llm.MatchHost
			if !host(req.URL.Hostname()) {
				return fmt.Errorf("redirect to disallowed host: %s", req.URL.Hostname())
			}
			return nil
		},
	}
}

func handleComentarLink(ctx context.Context, agent *Agent, user *User, params json.RawMessage) (string, error) {
	var p comentarLinkParams
	if err := json.Unmarshal(params, &p); err != nil {
		return "", fmt.Errorf("parse params: %w", err)
	}

	rawURL := strings.TrimSpace(p.URL)
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return "URL invalida — peca pro idoso te contar do que se trata.", nil
	}
	host := u.Hostname()
	if !llm.MatchHost(host) {
		if agent != nil && agent.audit != nil && user != nil {
			agent.audit.Log(user.ID, "comentar_link_rejected", host, "domain not in allowlist")
		}
		return fmt.Sprintf("Esse link (%s) eu nao consigo abrir, mas se quiser me conta do que e.", host), nil
	}

	preview, err := fetchOpenGraph(ctx, rawURL)
	if err != nil {
		if agent != nil && agent.audit != nil && user != nil {
			agent.audit.Log(user.ID, "comentar_link_error", host, sanitizeForDetails(err.Error()))
		}
		return "Nao consegui abrir o link agora — me conta do que se trata.", nil
	}

	if agent != nil && agent.audit != nil && user != nil {
		agent.audit.Log(user.ID, "comentar_link", host,
			fmt.Sprintf("title=%q og_type=%s", sanitizeForDetails(preview.Title), preview.OGType))
	}

	j, _ := json.Marshal(preview)
	return string(j), nil
}

// fetchOpenGraph faz GET com timeout 3s, body cap 64KB, max 2 redirects, e
// parseia tags <meta property="og:..." content="..."> via regex propria
// (sem dependencia externa). Se a tag og:* nao existir, faz fallback pro
// <title>.
func fetchOpenGraph(ctx context.Context, rawURL string) (*LinkPreview, error) {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	client := httpClientFactory()

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", linkUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml") && !strings.Contains(ct, "text/plain") {
		// text/plain aceito por defesa (alguns hosts servem html como text/plain)
		// — se nao parsear, og fica vazio.
		if !strings.Contains(ct, "text/") {
			return nil, fmt.Errorf("unexpected content-type: %s", ct)
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, linkMaxBody))
	if err != nil {
		return nil, err
	}

	og := parseOpenGraphTags(string(body))
	preview := &LinkPreview{
		Title:       og["title"],
		Description: og["description"],
		ImageURL:    og["image"],
		OGType:      og["type"],
		Host:        llm.NormalizeHost(parsedURL.Hostname()),
	}
	// Trim defensivo — alguns sites colocam HTML inteiro em description.
	preview.Title = trimToRunes(preview.Title, 200)
	preview.Description = trimToRunes(preview.Description, 400)
	return preview, nil
}

// parseOpenGraphTags extrai todas as <meta property="og:X" content="Y">
// e <title>Y</title> via regex tolerante (atributos podem vir em qualquer
// ordem, com aspas simples ou duplas, com espacos extras).
func parseOpenGraphTags(html string) map[string]string {
	out := map[string]string{}

	// <title>...</title> — fallback caso og:title nao exista.
	if t := extractMatch(html, `(?is)<title[^>]*>(.+?)</title>`); t != "" {
		out["title"] = strings.TrimSpace(htmlDecode(t))
	}

	// Itera sobre <meta ... > tags. Regex simples capturando inteiro e depois
	// inspeciona property/content.
	metaRe := regexpMustCompile(`(?is)<meta\s+[^>]*?>`)
	for _, m := range metaRe.FindAllString(html, -1) {
		propVal := extractAttr(m, "property")
		if propVal == "" {
			propVal = extractAttr(m, "name")
		}
		if !strings.HasPrefix(strings.ToLower(propVal), "og:") {
			continue
		}
		content := extractAttr(m, "content")
		if content == "" {
			continue
		}
		key := strings.TrimSpace(strings.ToLower(strings.TrimPrefix(propVal, "og:")))
		out[key] = htmlDecode(strings.TrimSpace(content))
	}
	return out
}
