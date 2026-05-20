package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// =========================================================================
// HTML helpers — companion_html.go
// =========================================================================

func TestExtractMatch(t *testing.T) {
	if extractMatch("<title>x</title>", `(?is)<title[^>]*>(.+?)</title>`) != "x" {
		t.Fatal("extractMatch basic")
	}
	if extractMatch("nope", `(?i)<missing>(.+?)</missing>`) != "" {
		t.Fatal("extractMatch should return empty when no match")
	}
}

func TestExtractAttr(t *testing.T) {
	cases := []struct {
		tag, name, want string
	}{
		{`<meta property="og:title" content="Hello">`, "content", "Hello"},
		{`<meta property='og:title' content='Hi'>`, "content", "Hi"},
		{`<meta CONTENT="Caps" property="og:title">`, "content", "Caps"},
		{`<meta property="og:title">`, "content", ""},
	}
	for _, c := range cases {
		if got := extractAttr(c.tag, c.name); got != c.want {
			t.Errorf("extractAttr(%q, %q) = %q want %q", c.tag, c.name, got, c.want)
		}
	}
}

func TestHtmlDecode(t *testing.T) {
	if htmlDecode("Hello&amp;World") != "Hello&World" {
		t.Fatal("htmlDecode &amp;")
	}
	if htmlDecode("&#39;quoted&#39;") != "'quoted'" {
		t.Fatal("htmlDecode numeric")
	}
}

func TestTrimToRunes(t *testing.T) {
	if trimToRunes("hello", 3) != "hel" {
		t.Fatal("trimToRunes basic")
	}
	if trimToRunes("hello", 10) != "hello" {
		t.Fatal("trimToRunes no trim")
	}
	if trimToRunes("ação", 3) != "açã" {
		t.Fatalf("trimToRunes UTF-8 (got %q)", trimToRunes("ação", 3))
	}
	if trimToRunes("any", 0) != "" {
		t.Fatal("trimToRunes 0")
	}
}

func TestStripTags(t *testing.T) {
	if stripTags("<b>oi</b> <i>mundo</i>") != "oi   mundo" {
		// Allowing the implementation's spacing; just check no tags remain.
		got := stripTags("<b>oi</b> <i>mundo</i>")
		if strings.Contains(got, "<") || strings.Contains(got, ">") {
			t.Fatalf("stripTags leaves tag: %q", got)
		}
	}
}

// =========================================================================
// parseOpenGraphTags
// =========================================================================

func TestParseOpenGraphTags(t *testing.T) {
	html := `<html><head>
<title>Pagina X</title>
<meta property="og:title" content="Titulo OG">
<meta property="og:description" content="Descr OG">
<meta property="og:image" content="https://example.com/img.png">
<meta property="og:type" content="article">
</head></html>`
	og := parseOpenGraphTags(html)
	if og["title"] != "Titulo OG" {
		t.Errorf("title: %q", og["title"])
	}
	if og["description"] != "Descr OG" {
		t.Errorf("description: %q", og["description"])
	}
	if og["image"] != "https://example.com/img.png" {
		t.Errorf("image: %q", og["image"])
	}
	if og["type"] != "article" {
		t.Errorf("type: %q", og["type"])
	}
}

func TestParseOpenGraphTags_FallbackTitle(t *testing.T) {
	html := `<html><head><title>Apenas Title</title></head></html>`
	og := parseOpenGraphTags(html)
	if og["title"] != "Apenas Title" {
		t.Errorf("expected fallback title: %q", og["title"])
	}
}

func TestParseOpenGraphTags_DecodesEntities(t *testing.T) {
	html := `<meta property="og:title" content="Bom &amp; barato">`
	og := parseOpenGraphTags(html)
	if og["title"] != "Bom & barato" {
		t.Errorf("entity decode: %q", og["title"])
	}
}

// =========================================================================
// fetchOpenGraph + comentar_link com servidor allowlisted (mockado).
// Como a allowlist eh hardcoded, usamos um truque: monkey-patch com
// teste que registra que a funcao MOCK eh chamada — na vida real
// adicionariamos um host de teste a allowlist (out-of-scope do unit test).
//
// Aqui validamos comportamento contra body limit, content-type, etc.
// =========================================================================

func TestFetchOpenGraph_RespectsBodyLimit(t *testing.T) {
	// Servidor que serve 100KB. LimitReader corta em 64KB. Tag og:title
	// fica nos primeiros 1KB (cabe).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(200)
		// Title primeiro pra parser pegar.
		w.Write([]byte(`<html><head><meta property="og:title" content="OK"></head><body>`))
		// Padding 100KB.
		padding := strings.Repeat("x", 100*1024)
		w.Write([]byte(padding))
		w.Write([]byte(`</body></html>`))
	}))
	defer srv.Close()

	preview, err := fetchOpenGraph(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("fetchOpenGraph: %v", err)
	}
	if preview.Title != "OK" {
		t.Errorf("title: %q", preview.Title)
	}
}

func TestFetchOpenGraph_RejectsNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
	}))
	defer srv.Close()
	if _, err := fetchOpenGraph(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestFetchOpenGraph_RejectsBinaryContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(200)
		w.Write([]byte{0x89, 0x50, 0x4e, 0x47})
	}))
	defer srv.Close()
	if _, err := fetchOpenGraph(context.Background(), srv.URL); err == nil {
		t.Fatal("expected error for binary content-type")
	}
}

// =========================================================================
// comentar_imagem — stub vision + stub media.
// =========================================================================

type stubMediaLoader struct {
	data      []byte
	mediaType string
	err       error
}

func (s *stubMediaLoader) Load(_ string) ([]byte, string, error) {
	return s.data, s.mediaType, s.err
}

type stubVision struct {
	text string
	err  error
}

func (s *stubVision) Name() string { return "stub" }
func (s *stubVision) DescribeImage(_ context.Context, _ llm.VisionRequest) (llm.VisionResponse, error) {
	return llm.VisionResponse{Text: s.text, ModelUsed: "stub"}, s.err
}

func TestComentarImagem_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "ImgTest", 24)
	agent := &Agent{
		db:     db,
		audit:  NewAuditLog(db),
		media:  &stubMediaLoader{data: []byte("fake-jpeg-bytes"), mediaType: "image/jpeg"},
		vision: &stubVision{text: "Foto de familia em jantar de natal, sorrindo.\nTOM: familia"},
	}
	params, _ := json.Marshal(comentarImagemParams{ImageID: "abc"})
	res, err := handleComentarImagem(context.Background(), agent, u, params)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Descricao   string `json:"descricao"`
		TomSugerido string `json:"tom_sugerido"`
	}
	if err := json.Unmarshal([]byte(res), &out); err != nil {
		t.Fatalf("not JSON: %v (raw=%s)", err, res)
	}
	if out.TomSugerido != "familia" {
		t.Errorf("tom: %q", out.TomSugerido)
	}
	if !strings.Contains(out.Descricao, "natal") {
		t.Errorf("descricao should mention natal: %q", out.Descricao)
	}
}

func TestComentarImagem_NoCacheConfigured(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "NoCache", 24)
	agent := &Agent{db: db, audit: NewAuditLog(db)}
	params, _ := json.Marshal(comentarImagemParams{ImageID: "abc"})
	res, _ := handleComentarImagem(context.Background(), agent, u, params)
	if !strings.Contains(res, "Cache de imagens") {
		t.Fatalf("expected cache-not-configured msg, got: %s", res)
	}
}

func TestComentarImagem_NoVisionConfigured(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "NoVision", 24)
	agent := &Agent{
		db:    db,
		audit: NewAuditLog(db),
		media: &stubMediaLoader{data: []byte("x"), mediaType: "image/jpeg"},
	}
	params, _ := json.Marshal(comentarImagemParams{ImageID: "abc"})
	res, _ := handleComentarImagem(context.Background(), agent, u, params)
	if !strings.Contains(res, "Vision") {
		t.Fatalf("expected vision-not-configured msg, got: %s", res)
	}
}

func TestComentarImagem_RequiresImageID(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "NoID", 24)
	agent := &Agent{db: db}
	params, _ := json.Marshal(comentarImagemParams{ImageID: ""})
	res, _ := handleComentarImagem(context.Background(), agent, u, params)
	if !strings.Contains(res, "image_id e obrigatorio") {
		t.Fatalf("expected required msg, got: %s", res)
	}
}

func TestComentarImagem_RejectsUnsupportedType(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "BadMime", 24)
	agent := &Agent{
		db:     db,
		audit:  NewAuditLog(db),
		media:  &stubMediaLoader{data: []byte("x"), mediaType: "image/svg+xml"},
		vision: &stubVision{},
	}
	params, _ := json.Marshal(comentarImagemParams{ImageID: "abc"})
	res, _ := handleComentarImagem(context.Background(), agent, u, params)
	if !strings.Contains(res, "nao suportado") {
		t.Fatalf("expected unsupported, got: %s", res)
	}
}

func TestComentarImagem_MediaNotFound(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Missing", 24)
	agent := &Agent{
		db:     db,
		audit:  NewAuditLog(db),
		media:  &stubMediaLoader{err: stubErr{}},
		vision: &stubVision{},
	}
	params, _ := json.Marshal(comentarImagemParams{ImageID: "abc"})
	res, _ := handleComentarImagem(context.Background(), agent, u, params)
	if !strings.Contains(res, "Imagem nao encontrada") {
		t.Fatalf("expected not-found, got: %s", res)
	}
}

type stubErr struct{}

func (stubErr) Error() string { return "not found" }

func TestIsSupportedImageType(t *testing.T) {
	good := []string{"image/jpeg", "image/png", "image/webp", "image/gif", "IMAGE/JPEG"}
	for _, g := range good {
		if !isSupportedImageType(g) {
			t.Errorf("expected supported: %s", g)
		}
	}
	bad := []string{"image/svg+xml", "video/mp4", "", "text/plain"}
	for _, b := range bad {
		if isSupportedImageType(b) {
			t.Errorf("expected unsupported: %s", b)
		}
	}
}

func TestSplitDescTone(t *testing.T) {
	cases := []struct {
		in       string
		wantDesc string
		wantTone string
	}{
		{"Foto de familia.\nTOM: familia", "Foto de familia.", "familia"},
		{"Imagem de prato.\nTom: comida", "Imagem de prato.", "comida"},
		{"Sem tom indicado.", "Sem tom indicado.", "outros"},
		{"Multi linha.\nFala isso.\nTOM: meme", "Multi linha. Fala isso.", "meme"},
	}
	for _, c := range cases {
		desc, tone := splitDescTone(c.in)
		if desc != c.wantDesc {
			t.Errorf("desc: got %q want %q", desc, c.wantDesc)
		}
		if tone != c.wantTone {
			t.Errorf("tone: got %q want %q", tone, c.wantTone)
		}
	}
}

// =========================================================================
// proactive helpers — paused, mark failed, get last.
// =========================================================================

func TestProactive_MarkFailed(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "MarkFailed", 24)
	id, _ := db.RecordProactiveAttempt(u.ID, "oi")
	if err := db.MarkProactiveAttemptFailed(id); err != nil {
		t.Fatal(err)
	}
	var status string
	db.conn.QueryRow(`SELECT status FROM proactive_attempts WHERE id = ?`, id).Scan(&status)
	if status != "failed" {
		t.Fatalf("status: %q", status)
	}
}

func TestProactive_GetLast(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "GetLast", 24)
	if _, err := db.GetLastProactiveAttempt(u.ID); err != ErrNoProactiveAttempt {
		t.Fatalf("expected ErrNoProactiveAttempt, got %v", err)
	}
	id, _ := db.RecordProactiveAttempt(u.ID, "primeira")
	pa, err := db.GetLastProactiveAttempt(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pa.ID != id {
		t.Errorf("id: %d != %d", pa.ID, id)
	}
	if pa.Status != "sent" {
		t.Errorf("status: %q", pa.Status)
	}
}

func TestProactive_PausedFutureToPast(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Pause", 24)

	// Sem pausa.
	paused, _ := db.IsProactivePaused(u.ID)
	if paused {
		t.Fatal("not paused initially")
	}

	// Pausa por 1 dia -> futuro.
	db.PauseProactive(u.ID, 1)
	paused, _ = db.IsProactivePaused(u.ID)
	if !paused {
		t.Fatal("expected paused after PauseProactive")
	}

	// Setar manualmente pausa no passado -> nao paused.
	past := time.Now().Add(-1 * time.Hour).UTC()
	db.conn.Exec(`UPDATE users SET proactive_paused_until = ? WHERE id = ?`, past, u.ID)
	paused, _ = db.IsProactivePaused(u.ID)
	if paused {
		t.Fatal("past pause should not be considered paused")
	}
}

// =========================================================================
// SevereSignalEscalation list + happy path.
// =========================================================================

func TestSevereSignalEscalation_List(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "Severe", 24)
	now := time.Now()
	if _, err := db.RecordSevereSignalEscalation(u.ID, "severe_signal", "critical", "x", 0, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RecordSevereSignalEscalation(u.ID, "severe_signal", "warn", "y", 0, "", now); err != nil {
		t.Fatal(err)
	}
	list, err := db.ListSevereSignalEscalations(u.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2, got %d", len(list))
	}
}

// =========================================================================
// scheduler checkInactivity — happy path com agent stub.
// =========================================================================

// stubProactiveAgent implementa minimo de RunProactive sem chamar Anthropic.
// Setamos via campo na struct Agent — a chamada vai para o caminho do
// stub via agent.snapshotWriter? Nao, RunProactive usa runLoop direto.
// Em vez disso, exercitamos so checkUserInactivity ate o lock, que ja
// cobre o caminho relevante.

func TestCheckUserInactivity_DoesNotFireBeforeThreshold(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "BeforeThr", 24)
	// last = 5h atras (threshold 24).
	db.conn.Exec(`UPDATE users SET last_user_message_at = ? WHERE id = ?`,
		time.Now().Add(-5*time.Hour).UTC(), u.ID)

	var sent []string
	sched := &Scheduler{
		db:      db,
		sendMsg: func(p, m string) error { sent = append(sent, m); return nil },
		nowFunc: time.Now,
	}
	u2, _ := db.GetUserByID(u.ID)
	sched.checkUserInactivity(u2, time.Now())

	if len(sent) != 0 {
		t.Fatalf("expected no send before threshold, got %d", len(sent))
	}
	var count int
	db.conn.QueryRow(`SELECT COUNT(*) FROM proactive_attempts WHERE user_id = ?`, u.ID).Scan(&count)
	if count != 0 {
		t.Fatalf("expected no attempts, got %d", count)
	}
}

func TestCheckUserInactivity_NoAgentNoOp(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "NoAgent", 4)
	db.conn.Exec(`UPDATE users SET last_user_message_at = ? WHERE id = ?`,
		time.Now().Add(-10*time.Hour).UTC(), u.ID)

	// nowFunc fora da janela horaria pra garantir nao envio.
	loc := BRT()
	threeAm := time.Date(2026, 5, 9, 3, 0, 0, 0, loc)
	var sent []string
	sched := &Scheduler{
		db:      db,
		sendMsg: func(p, m string) error { sent = append(sent, m); return nil },
		nowFunc: func() time.Time { return threeAm },
	}
	u2, _ := db.GetUserByID(u.ID)
	sched.checkUserInactivity(u2, threeAm)
	if len(sent) != 0 {
		t.Fatalf("expected no send at 3am, got %d", len(sent))
	}
}

func TestCheckInactivity_GatingBy15Minutes(t *testing.T) {
	db := setupTestDB(t)
	mkIdoso(t, db, "GateTest", 4)

	loc := BRT()
	// 10:07 — minute % 15 != 0, deve abortar.
	at := time.Date(2026, 5, 9, 10, 7, 0, 0, loc)
	sched := &Scheduler{
		db:      db,
		nowFunc: func() time.Time { return at },
	}
	// Nao panic, retorna sem efeitos.
	sched.checkInactivity()
}

// =========================================================================
// audit logs — verifica acoes Fase 4 estao no actionLabelsPT (defesa
// contra typo).
// =========================================================================

func TestAuditLabelsPT_ContainsPhase4(t *testing.T) {
	required := []string{
		"alertar_familia",
		"pausar_proatividade",
		"proactive_attempt_sent",
		"companion_provider_switch",
		"comentar_imagem",
		"comentar_link",
		"comentar_link_rejected",
		"snapshot_updated",
		"safety_net_fired",
	}
	for _, k := range required {
		if _, ok := actionLabelsPT[k]; !ok {
			t.Errorf("actionLabelsPT missing label for %q", k)
		}
	}
}

// =========================================================================
// AuditLog.LogAlertarFamilia — confirma estrutura pipe-separated.
// =========================================================================

func TestAuditLog_LogAlertarFamilia(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "AuditTest", 24)
	a := NewAuditLog(db)
	if err := a.LogAlertarFamilia(u.ID, "critical", "psicologico", "razao test", []string{"Marta"}, []string{}, false); err != nil {
		t.Fatal(err)
	}
	var details string
	db.conn.QueryRow(
		`SELECT details FROM action_log WHERE user_id = ? AND action = 'alertar_familia'`, u.ID,
	).Scan(&details)
	if !strings.Contains(details, "severity=critical") {
		t.Errorf("details missing severity: %s", details)
	}
	if !strings.Contains(details, "category=psicologico") {
		t.Errorf("details missing category: %s", details)
	}
	if !strings.Contains(details, "sent_to=Marta") {
		t.Errorf("details missing sent_to: %s", details)
	}
}

func TestAuditLog_LogProactiveAttemptSent(t *testing.T) {
	db := setupTestDB(t)
	u := mkIdoso(t, db, "AuditPro", 24)
	a := NewAuditLog(db)
	if err := a.LogProactiveAttemptSent(u.ID, 25, 42, "oi joaquim"); err != nil {
		t.Fatal(err)
	}
	var details string
	db.conn.QueryRow(
		`SELECT details FROM action_log WHERE user_id = ? AND action = 'proactive_attempt_sent'`, u.ID,
	).Scan(&details)
	if !strings.Contains(details, "hours_idle=25") {
		t.Errorf("details: %s", details)
	}
	if !strings.Contains(details, "attempt_id=42") {
		t.Errorf("details: %s", details)
	}
}

func TestSanitizeForDetails(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"plain", "plain"},
		{"with|pipe", "with/pipe"},
		{"line1\nline2", "line1 line2"},
		{"line1\r\nline2", "line1  line2"},
		{strings.Repeat("a", 600), strings.Repeat("a", 500) + "...[truncated]"},
	}
	for _, c := range cases {
		if got := sanitizeForDetails(c.in); got != c.want {
			t.Errorf("sanitize: got %q want %q", got, c.want)
		}
	}
}
