package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// Gates do P3 (B1 — pupuzinho): o modelo inventou uma precondição de "nome
// completo" que NÃO existe no código (handleCriarEvento persiste qualquer
// Title). O fix é completude de prompt+schema nos três pontos que alimentam
// AMBAS as engines; estes testes pinam cada um contra remoção futura.

func TestOperationalPromptForbidsFullNameOverAsk(t *testing.T) {
	op := buildSystemPromptStableOperational("Giovanni")
	for _, want := range []string{
		"APELIDO/IDENTIFICADOR JÁ É NOME",
		"NUNCA peça \"nome completo\"",
		"salva como X",
		"is_birthday=true, title=\"Aniversário <pessoa>\"",
	} {
		if !strings.Contains(op, want) {
			t.Errorf("prompt operacional faltando %q (regra do apelido)", want)
		}
	}
	// Worked example do cenário-bandeira.
	if !strings.Contains(op, "pupuzinho") || !strings.Contains(op, "NÃO pergunte nome completo") {
		t.Error("prompt operacional faltando o worked example do pupuzinho")
	}
}

func TestCompanionCoreForbidsFullNameOverAsk(t *testing.T) {
	core := buildCompanionCore("Fábio")
	for _, want := range []string{"apelido", "NUNCA peça nome", "salva como X", "is_birthday=true"} {
		if !strings.Contains(core, want) {
			t.Errorf("companion core faltando %q (paridade da regra do apelido)", want)
		}
	}
}

func TestCriarEventoTitleSchemaCarriesNicknameRule(t *testing.T) {
	for _, tool := range buildToolDefinitions() {
		if tool.Name != "criar_evento" && tool.Name != "criar_evento_outro_usuario" {
			continue
		}
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal schema: %v", tool.Name, err)
		}
		var schema struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatalf("%s: parse schema: %v", tool.Name, err)
		}
		desc := schema.Properties["title"].Description
		for _, want := range []string{"apelido", "NUNCA exija nome completo", "salva como X"} {
			if !strings.Contains(desc, want) {
				t.Errorf("%s title.description faltando %q — o schema é o ponto routing-proof que atinge Sonnet E DeepSeek", tool.Name, want)
			}
		}
	}
}

// Negativo cross-engine: nenhum artefato que alimenta os LLMs pode instruir a
// exigir nome completo/real/sobrenome como precondição de salvar.
func TestNoPromptOrSchemaRequestsFullName(t *testing.T) {
	artifacts := map[string]string{
		"operacional": buildSystemPromptStableOperational("X"),
		"companion":   buildCompanionCore("X"),
	}
	for _, tool := range buildToolDefinitions() {
		if raw, err := json.Marshal(tool.InputSchema); err == nil {
			artifacts["schema:"+tool.Name] = string(raw)
		}
	}
	for name, text := range artifacts {
		lower := strings.ToLower(text)
		for _, banned := range []string{"peça o nome completo", "pergunte o nome completo", "exija o nome completo", "precisa do sobrenome"} {
			if strings.Contains(lower, banned) {
				t.Errorf("%s contém instrução de super-pergunta: %q", name, banned)
			}
		}
	}
}

// O branch de aniversário tem que devolver o MESMO envelope OK_CRIADO|display=
// do caminho normal — senão a REGRA DE CITAÇÃO e o guard I4 não têm âncora e o
// modelo pode freehandear a data relativa (B2) no cenário-bandeira do B1.
func TestBirthdayResultIsCitationAnchored(t *testing.T) {
	brt := BRT()
	created := &CalendarEvent{
		Title:     "Aniversário Pupuzinho",
		Start:     time.Date(2026, 6, 7, 0, 0, 0, 0, brt), // 7 de junho — data passada é normal
		End:       time.Date(2026, 6, 8, 0, 0, 0, 0, brt),
		EventType: "birthday",
	}
	now := time.Date(2026, 6, 10, 0, 41, 0, 0, brt)
	out := birthdayCreatedResult(created, now)

	display, ok := strings.CutPrefix(out, "OK_CRIADO|display=")
	if !ok {
		t.Fatalf("aniversário deveria usar o envelope OK_CRIADO|display=, got %q", out)
	}
	if !strings.Contains(display, "Aniversário Pupuzinho") || !strings.Contains(display, "07/06") {
		t.Errorf("display deveria conter título (apelido) e data, got %q", display)
	}
	// E o guard I4 consegue ancorar nele: resposta sem o display viola.
	if v := detectI4DisplayCitation("Salvei o aniversário!", []string{out}); len(v) != 1 {
		t.Fatalf("I4 deveria detectar display ausente, got %d violações", len(v))
	}
	if v := detectI4DisplayCitation(display+"\n\nSalvei! 🎂", []string{out}); len(v) != 0 {
		t.Fatalf("display citado verbatim não pode violar, got %d", len(v))
	}
}

// Documenta POR QUE aniversário precisa rotear pra is_birthday: evento
// não-aniversário com data explícita no passado é erro do resolver — viraria
// MAIS perguntas ao usuário (a falha secundária que o prompt evita).
func TestExplicitPastDateNonBirthdayErrors(t *testing.T) {
	loc := BRT()
	now := time.Date(2026, 6, 10, 10, 0, 0, 0, loc)
	_, err := ResolveEventDate(ResolveInput{
		Source: DateSourceExplicit, ExplicitDate: "2026-06-07", Time: "10:00", Now: now, Loc: loc,
	})
	if err == nil || !strings.Contains(err.Error(), "data explicita no passado") {
		t.Fatalf("data passada não-aniversário deveria errar no resolver, got %v", err)
	}
}
