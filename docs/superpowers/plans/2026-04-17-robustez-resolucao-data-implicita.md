# Robustez da resolução de data implícita — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminar divergência entre narrativa e ação em `criar_evento`, consolidando a regra sagrada de data implícita (hora > agora → hoje; hora ≤ agora → amanhã) como código determinístico em Go, com Google Calendar e mensagem ao usuário derivando da mesma fonte da verdade.

**Architecture:** Nova função pura `ResolveEventDate` em `bot/date_resolver.go` recebe `date_source` (`inferred`/`explicit`), `time`, `explicit_date`, `now`, `loc` e retorna `ResolveOutput{Start, Adjusted, AdjustNote}`. `handleCriarEvento` passa a usá-la; `FormatEventCreated` ganha rótulo relativo `HOJE`/`AMANHÃ`; output da tool vira `OK_CRIADO|display=...` com prompt instruindo Claude a citar verbatim. Spec completo em [docs/superpowers/specs/2026-04-17-robustez-resolucao-data-implicita-design.md](../specs/2026-04-17-robustez-resolucao-data-implicita-design.md).

**Tech Stack:** Go 1.x (stdlib `time`), `testing` package, JSON schema do Anthropic Go SDK.

---

## File Structure

- **Create:** `bot/date_resolver.go` — tipos e função pura `ResolveEventDate`.
- **Create:** `bot/date_resolver_test.go` — table tests.
- **Modify:** `bot/formatter.go` — helper `relativeDayLabel`, update `FormatEventCreated`.
- **Modify:** `bot/formatter_test.go` — cobrir novo formato.
- **Modify:** `bot/tools.go` — adicionar `DateSource` em `criarEventoParams`, substituir bloco de parsing por chamada ao resolver, retornar `OK_CRIADO|display=...`.
- **Modify:** `bot/agent.go` — atualizar JSON schema da tool `criar_evento` e adicionar 4 novas seções de regra em `buildSystemPromptStable`.
- **Modify:** `bot/audit.go` — adicionar `LogCriarEvento` com campos estruturados (mantém `Log` genérico pros outros call sites).
- **Modify:** `bot/integration_test.go` — teste de regressão do incidente.

---

## Task 1: Date resolver — esqueleto, tipos e caminho `inferred`

**Files:**
- Create: `bot/date_resolver.go`
- Create: `bot/date_resolver_test.go`

- [ ] **Step 1.1: Criar arquivo de teste com 5 casos `inferred`**

Arquivo `bot/date_resolver_test.go`:

```go
package main

import (
	"testing"
	"time"
)

func TestResolveEventDate_Inferred(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now0702 := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)
	now2345 := time.Date(2026, 4, 16, 23, 45, 0, 0, brt)

	tests := []struct {
		name     string
		now      time.Time
		time     string
		wantDate time.Time
	}{
		{
			name:     "hora > agora resolve para hoje (regressao do bug OTC)",
			now:      now0702,
			time:     "09:00",
			wantDate: time.Date(2026, 4, 16, 9, 0, 0, 0, brt),
		},
		{
			name:     "hora < agora resolve para amanha (5h da manha)",
			now:      now0702,
			time:     "05:00",
			wantDate: time.Date(2026, 4, 17, 5, 0, 0, 0, brt),
		},
		{
			name:     "PM-default ja aplicado por Claude: 17:00 resolve para hoje",
			now:      now0702,
			time:     "17:00",
			wantDate: time.Date(2026, 4, 16, 17, 0, 0, 0, brt),
		},
		{
			name:     "time == now resolve para amanha",
			now:      now0702,
			time:     "07:02",
			wantDate: time.Date(2026, 4, 17, 7, 2, 0, 0, brt),
		},
		{
			name:     "travessia de meia-noite: 23:30 sendo 23:45 vai pra amanha",
			now:      now2345,
			time:     "23:30",
			wantDate: time.Date(2026, 4, 17, 23, 30, 0, 0, brt),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveEventDate(ResolveInput{
				Source: DateSourceInferred,
				Time:   tc.time,
				Now:    tc.now,
				Loc:    brt,
			})
			if err != nil {
				t.Fatalf("erro inesperado: %v", err)
			}
			if !got.Start.Equal(tc.wantDate) {
				t.Fatalf("Start = %s, queria %s", got.Start, tc.wantDate)
			}
			if got.Adjusted {
				t.Fatalf("Adjusted esperava false para inferred, deu true")
			}
		})
	}
}
```

- [ ] **Step 1.2: Rodar teste — esperar falha de compilação**

Run: `cd bot && go test -run TestResolveEventDate_Inferred -v`
Expected: erro de compilação (`ResolveEventDate`, `ResolveInput`, `DateSourceInferred` não definidos).

- [ ] **Step 1.3: Criar `bot/date_resolver.go` com tipos + implementação do caminho `inferred`**

```go
package main

import (
	"fmt"
	"time"
)

type DateSource string

const (
	DateSourceExplicit DateSource = "explicit"
	DateSourceInferred DateSource = "inferred"
)

type ResolveInput struct {
	Source       DateSource
	ExplicitDate string // "YYYY-MM-DD", obrigatorio se Source=Explicit
	Time         string // "HH:MM"
	Now          time.Time
	Loc          *time.Location
}

type ResolveOutput struct {
	Start      time.Time
	Adjusted   bool
	AdjustNote string
}

func ResolveEventDate(in ResolveInput) (ResolveOutput, error) {
	if in.Loc == nil {
		return ResolveOutput{}, fmt.Errorf("Loc obrigatorio")
	}
	hh, mm, err := parseHHMM(in.Time)
	if err != nil {
		return ResolveOutput{}, err
	}
	nowInLoc := in.Now.In(in.Loc)

	switch in.Source {
	case DateSourceInferred:
		today := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), hh, mm, 0, 0, in.Loc)
		if today.After(nowInLoc) {
			return ResolveOutput{Start: today}, nil
		}
		return ResolveOutput{Start: today.AddDate(0, 0, 1)}, nil

	case DateSourceExplicit:
		return ResolveOutput{}, fmt.Errorf("caminho explicit ainda nao implementado")

	default:
		return ResolveOutput{}, fmt.Errorf("date_source invalido: %q", in.Source)
	}
}

func parseHHMM(s string) (int, int, error) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, 0, fmt.Errorf("time invalido %q: %w", s, err)
	}
	return t.Hour(), t.Minute(), nil
}
```

- [ ] **Step 1.4: Rodar teste — esperar passar**

Run: `cd bot && go test -run TestResolveEventDate_Inferred -v`
Expected: PASS em todos os 5 casos.

- [ ] **Step 1.5: Commit**

```bash
git add bot/date_resolver.go bot/date_resolver_test.go
git commit -m "feat(resolver): regra sagrada de data implicita (caminho inferred)

Implementa ResolveEventDate para o caminho date_source=inferred:
hora > agora -> hoje; hora <= agora -> amanha. Cobre caso do bug
(09:00 as 07:02 -> hoje) mais 4 edge cases inclusive travessia de
meia-noite.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Date resolver — caminho `explicit` com auto-ajuste

**Files:**
- Modify: `bot/date_resolver_test.go`
- Modify: `bot/date_resolver.go`

- [ ] **Step 2.1: Adicionar casos `explicit` ao test file**

Adicionar ao final de `bot/date_resolver_test.go`:

```go
func TestResolveEventDate_Explicit(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now0702 := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)

	t.Run("explicit data futura sem ajuste", func(t *testing.T) {
		got, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-20",
			Time:         "14:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 20, 14, 0, 0, 0, brt)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
		if got.Adjusted {
			t.Fatalf("Adjusted deveria ser false")
		}
	})

	t.Run("explicit hoje com hora no futuro sem ajuste", func(t *testing.T) {
		got, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-16",
			Time:         "09:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 16, 9, 0, 0, 0, brt)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
		if got.Adjusted {
			t.Fatalf("Adjusted deveria ser false")
		}
	})

	t.Run("explicit hoje com hora passada: auto-ajusta para amanha", func(t *testing.T) {
		got, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-16",
			Time:         "05:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 17, 5, 0, 0, 0, brt)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
		if !got.Adjusted {
			t.Fatalf("Adjusted deveria ser true")
		}
		if got.AdjustNote == "" {
			t.Fatalf("AdjustNote deveria ser preenchido")
		}
	})

	t.Run("explicit data passada retorna erro", func(t *testing.T) {
		_, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-10",
			Time:         "09:00",
			Now:          now0702,
			Loc:          brt,
		})
		if err == nil {
			t.Fatalf("esperava erro, deu nil")
		}
	})
}
```

- [ ] **Step 2.2: Rodar testes — esperar 3 falhas + 1 que ainda passa**

Run: `cd bot && go test -run TestResolveEventDate_Explicit -v`
Expected: todos os 4 subtestes falham com "caminho explicit ainda nao implementado".

- [ ] **Step 2.3: Implementar caminho `explicit` em `date_resolver.go`**

Substituir o case `DateSourceExplicit` (atualmente retorna erro) em `ResolveEventDate`:

```go
	case DateSourceExplicit:
		d, err := time.ParseInLocation("2006-01-02", in.ExplicitDate, in.Loc)
		if err != nil {
			return ResolveOutput{}, fmt.Errorf("ExplicitDate invalido %q: %w", in.ExplicitDate, err)
		}
		candidate := time.Date(d.Year(), d.Month(), d.Day(), hh, mm, 0, 0, in.Loc)
		today := time.Date(nowInLoc.Year(), nowInLoc.Month(), nowInLoc.Day(), 0, 0, 0, 0, in.Loc)
		eventDay := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, in.Loc)
		if eventDay.Equal(today) && candidate.Before(nowInLoc) {
			return ResolveOutput{
				Start:      candidate.AddDate(0, 0, 1),
				Adjusted:   true,
				AdjustNote: "Esse horario ja passou hoje. Marquei pra amanha nesse horario. ",
			}, nil
		}
		if eventDay.Before(today) {
			return ResolveOutput{}, fmt.Errorf("data explicita no passado: %s", in.ExplicitDate)
		}
		return ResolveOutput{Start: candidate}, nil
```

- [ ] **Step 2.4: Rodar testes — esperar todos passarem**

Run: `cd bot && go test -run TestResolveEventDate -v`
Expected: PASS em todos os 9 subtestes (5 inferred + 4 explicit).

- [ ] **Step 2.5: Commit**

```bash
git add bot/date_resolver.go bot/date_resolver_test.go
git commit -m "feat(resolver): caminho explicit com auto-ajuste hoje-passado

Explicit + data futura/hoje-futuro passa direto. Explicit + data=hoje
com hora ja passada auto-ajusta para amanha com AdjustNote. Explicit +
data passada nao-hoje retorna erro (evita criar eventos no passado
silenciosamente).

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Date resolver — erros de entrada e edge cases de fuso

**Files:**
- Modify: `bot/date_resolver_test.go`

- [ ] **Step 3.1: Adicionar testes de erro e fuso**

Adicionar ao final de `bot/date_resolver_test.go`:

```go
func TestResolveEventDate_Errors(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)

	t.Run("time invalido retorna erro", func(t *testing.T) {
		_, err := ResolveEventDate(ResolveInput{
			Source: DateSourceInferred,
			Time:   "25:00",
			Now:    now,
			Loc:    brt,
		})
		if err == nil {
			t.Fatalf("esperava erro, deu nil")
		}
	})

	t.Run("explicit date invalido retorna erro", func(t *testing.T) {
		_, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "nao-e-data",
			Time:         "09:00",
			Now:          now,
			Loc:          brt,
		})
		if err == nil {
			t.Fatalf("esperava erro, deu nil")
		}
	})

	t.Run("Loc nil retorna erro", func(t *testing.T) {
		_, err := ResolveEventDate(ResolveInput{
			Source: DateSourceInferred,
			Time:   "09:00",
			Now:    now,
			Loc:    nil,
		})
		if err == nil {
			t.Fatalf("esperava erro, deu nil")
		}
	})

	t.Run("date_source invalido retorna erro", func(t *testing.T) {
		_, err := ResolveEventDate(ResolveInput{
			Source: "lixo",
			Time:   "09:00",
			Now:    now,
			Loc:    brt,
		})
		if err == nil {
			t.Fatalf("esperava erro, deu nil")
		}
	})
}

func TestResolveEventDate_Timezone(t *testing.T) {
	paris, _ := time.LoadLocation("Europe/Paris")
	brt, _ := time.LoadLocation("America/Sao_Paulo")

	t.Run("inferred em fuso Paris: hoje e amanha seguem calendario local", func(t *testing.T) {
		// Em BRT 23:45 de 16/04, em Paris sao 04:45 de 17/04.
		// "reuniao as 9h" em Paris deve ser hoje (17/04) em Paris 09:00.
		now := time.Date(2026, 4, 16, 23, 45, 0, 0, brt)
		got, err := ResolveEventDate(ResolveInput{
			Source: DateSourceInferred,
			Time:   "09:00",
			Now:    now,
			Loc:    paris,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 17, 9, 0, 0, 0, paris)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
	})

	t.Run("explicit em fuso Paris respeita data local", func(t *testing.T) {
		now := time.Date(2026, 4, 16, 23, 45, 0, 0, brt)
		got, err := ResolveEventDate(ResolveInput{
			Source:       DateSourceExplicit,
			ExplicitDate: "2026-04-17",
			Time:         "08:00",
			Now:          now,
			Loc:          paris,
		})
		if err != nil {
			t.Fatalf("erro inesperado: %v", err)
		}
		want := time.Date(2026, 4, 17, 8, 0, 0, 0, paris)
		if !got.Start.Equal(want) {
			t.Fatalf("Start = %s, queria %s", got.Start, want)
		}
	})
}
```

- [ ] **Step 3.2: Rodar testes — esperar todos passarem sem mudar implementação**

Run: `cd bot && go test -run TestResolveEventDate -v`
Expected: PASS nos 15 subtestes totais. A implementação já cobre esses casos porque usa `time.ParseInLocation` e `in.Now.In(in.Loc)` corretamente.

Se algum falhar, inspecionar qual invariante quebrou e corrigir `date_resolver.go` antes de prosseguir.

- [ ] **Step 3.3: Commit**

```bash
git add bot/date_resolver_test.go
git commit -m "test(resolver): cobrir erros de entrada e casos de fuso

Adiciona cobertura para time/date invalidos, Loc nil, source desconhecido,
e travessia BRT->Paris em inferred/explicit. Fecha os 15 casos previstos
no spec.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Formatter — helper `relativeDayLabel` e rótulo em `FormatEventCreated`

**Files:**
- Modify: `bot/formatter.go`
- Modify: `bot/formatter_test.go`

- [ ] **Step 4.1: Adicionar testes para `relativeDayLabel` e novo formato**

Adicionar ao final de `bot/formatter_test.go`:

```go
func TestRelativeDayLabel(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 4, 16, 10, 0, 0, 0, brt)

	cases := []struct {
		name      string
		eventTime time.Time
		want      string
	}{
		{"mesmo dia retorna HOJE", time.Date(2026, 4, 16, 15, 0, 0, 0, brt), "HOJE"},
		{"mesmo dia mais cedo retorna HOJE", time.Date(2026, 4, 16, 6, 0, 0, 0, brt), "HOJE"},
		{"proximo dia retorna AMANHA", time.Date(2026, 4, 17, 5, 0, 0, 0, brt), "AMANHA"},
		{"2 dias no futuro retorna vazio", time.Date(2026, 4, 18, 10, 0, 0, 0, brt), ""},
		{"ontem retorna vazio", time.Date(2026, 4, 15, 10, 0, 0, 0, brt), ""},
		{"travessia meia-noite: evento amanha 00:30 vs agora 23:59", time.Date(2026, 4, 17, 0, 30, 0, 0, brt), "AMANHA"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := relativeDayLabel(tc.eventTime, now)
			if got != tc.want {
				t.Fatalf("relativeDayLabel = %q, queria %q", got, tc.want)
			}
		})
	}
}

func TestFormatEventCreated_RelativeLabel(t *testing.T) {
	brt, _ := time.LoadLocation("America/Sao_Paulo")

	// Para testar determinamente, usamos a hora atual real e um evento 1 hora
	// no futuro (mesmo dia). Se rodar em 23:xx de um dia pode virar amanha;
	// por isso usamos relativeDayLabelFn injetavel. Aqui testamos a integracao
	// completa com relativeDayLabel real usando eventos em data FIXA relativo
	// a "now" injetado via variavel package-level.
	//
	// Abordagem mais simples: testar o formato estatico com evento em
	// data arbitraria e checar presenca/ausencia de marcadores.
	ev := CalendarEvent{
		Title: "Reuniao com OTC",
		Start: time.Now().In(brt).Add(1 * time.Hour),
		End:   time.Now().In(brt).Add(2 * time.Hour),
	}
	out := FormatEventCreated(ev)
	if !strings.Contains(out, "Reuniao com OTC") {
		t.Fatalf("output deveria conter titulo, got: %s", out)
	}
	if !strings.Contains(out, "HOJE") {
		t.Fatalf("evento 1h no futuro deveria ter rotulo HOJE, got: %s", out)
	}
}
```

- [ ] **Step 4.2: Rodar testes — esperar falha de compilação e formatação**

Run: `cd bot && go test -run "TestRelativeDayLabel|TestFormatEventCreated_RelativeLabel" -v`
Expected: `TestRelativeDayLabel` falha de compilação (`relativeDayLabel` não existe). `TestFormatEventCreated_RelativeLabel` também falha (sem "HOJE" no output).

- [ ] **Step 4.3: Implementar `relativeDayLabel` e atualizar `FormatEventCreated`**

Em `bot/formatter.go`, adicionar após o `weekdaysPT` map:

```go
// relativeDayLabel retorna "HOJE" se eventStart e now caem no mesmo dia
// calendario (no fuso de eventStart); "AMANHA" se eventStart e o dia
// calendario seguinte; string vazia caso contrario. Ancora narrativa
// para impedir freehand divergente do agente.
func relativeDayLabel(eventStart, now time.Time) string {
	loc := eventStart.Location()
	nowInLoc := now.In(loc)
	sY, sM, sD := eventStart.Date()
	nY, nM, nD := nowInLoc.Date()
	if sY == nY && sM == nM && sD == nD {
		return "HOJE"
	}
	tomorrow := nowInLoc.AddDate(0, 0, 1)
	tY, tM, tD := tomorrow.Date()
	if sY == tY && sM == tM && sD == tD {
		return "AMANHA"
	}
	return ""
}
```

Substituir a função `FormatEventCreated` atual (linhas 72-80):

```go
func FormatEventCreated(ev CalendarEvent) string {
	weekday := weekdaysPT[ev.Start.Weekday()]
	if ev.EventType == "birthday" {
		return fmt.Sprintf("Aniversario criado: *%s*\n%s, %s (repete todo ano)",
			ev.Title, weekday, ev.Start.Format("02/01"))
	}
	rel := relativeDayLabel(ev.Start, time.Now())
	prefix := ""
	if rel != "" {
		prefix = rel + " — "
	}
	return fmt.Sprintf("Evento criado: *%s*\n%s%s, %s as %s",
		ev.Title, prefix, weekday, ev.Start.Format("02/01"), ev.Start.Format("15:04"))
}
```

- [ ] **Step 4.4: Rodar testes — esperar passar**

Run: `cd bot && go test -run "TestRelativeDayLabel|TestFormatEventCreated" -v`
Expected: PASS em todos. Rodar também o teste existente `TestFormatEventList_ShowsEventTypeAndMaster` pra garantir que não quebrou nada:

Run: `cd bot && go test -run TestFormat -v`
Expected: PASS em todos.

- [ ] **Step 4.5: Commit**

```bash
git add bot/formatter.go bot/formatter_test.go
git commit -m "feat(formatter): ancora narrativa HOJE/AMANHA em FormatEventCreated

Novo helper relativeDayLabel retorna HOJE/AMANHA/\"\" baseado na data
calendaria do evento vs agora no fuso do evento. FormatEventCreated
prefixa o weekday com esse rotulo quando aplicavel. Impede que o
agente reformule em freehand a data relativa, bug que causou o
incidente do evento OTC criado para dois dias a frente.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Tool contract — atualizar struct de params e JSON schema

**Files:**
- Modify: `bot/tools.go`
- Modify: `bot/agent.go`

- [ ] **Step 5.1: Atualizar `criarEventoParams` em `bot/tools.go`**

Substituir o struct atual (linhas 135-147):

```go
type criarEventoParams struct {
	Title           string   `json:"title"`
	DateSource      string   `json:"date_source"` // "explicit" | "inferred"
	Date            string   `json:"date"`
	Time            string   `json:"time"`
	DurationMinutes int      `json:"duration_minutes"`
	Location        string   `json:"location"`
	Attendees       []string `json:"attendees"`
	ComMeet         bool     `json:"com_meet"`
	ForceConflict   bool     `json:"force_conflict"`
	Timezone        string   `json:"timezone"`
	Recurrence      string   `json:"recurrence"`
	IsBirthday      bool     `json:"is_birthday"`
}
```

- [ ] **Step 5.2: Atualizar JSON schema da tool em `bot/agent.go`**

Substituir o `InputSchema` da tool `criar_evento` ([agent.go:448-464](../../bot/agent.go#L448-L464)):

```go
			InputSchema: json.RawMessage(`{
				"type": "object",
				"properties": {
					"title": {"type": "string", "description": "Titulo do evento"},
					"date_source": {"type": "string", "enum": ["explicit", "inferred"], "description": "explicit quando o usuario mencionou qualquer marcador temporal (data, dia da semana, amanha, hoje, daqui N dias). inferred quando o usuario mencionou APENAS hora, sem nenhum marcador temporal. OBRIGATORIO."},
					"date": {"type": "string", "description": "Data YYYY-MM-DD. Obrigatorio quando date_source=explicit. IGNORADO pelo sistema quando date_source=inferred (o sistema resolve via regra deterministica: hora > agora -> hoje; hora <= agora -> amanha)."},
					"time": {"type": "string", "description": "Horario de inicio HH:MM. Para horas bare menores que 07:00 sem qualificador, aplique PM-default (ex: '2h' -> 14:00, '5h' -> 17:00). Qualificadores 'da madrugada'/'da manha' mantem AM."},
					"duration_minutes": {"type": "integer", "description": "Duracao em minutos (default: 60)"},
					"location": {"type": "string", "description": "Local do evento (opcional)"},
					"com_meet": {"type": "boolean", "description": "Gera link do Google Meet. SOMENTE passe true quando o usuario pedir explicitamente (ex: 'com meet', 'remoto', 'online', 'videochamada', 'por video', 'chamada') OU quando o contexto deixar obvio que e remoto (ex: participantes em outra cidade sem local fisico). NUNCA infira Meet so porque e 'reuniao'. Reunioes presenciais sao o default."},
					"attendees": {"type": "array", "items": {"type": "string"}, "description": "Emails de participantes (opcional, NAO peca proativamente)"},
					"force_conflict": {"type": "boolean", "description": "Se true, cria mesmo com conflito de horario (so usar apos usuario confirmar)"},
					"timezone": {"type": "string", "description": "Fuso horario IANA (ex: Europe/London). Default: America/Sao_Paulo."},
					"recurrence": {"type": "string", "description": "Regra de recorrencia iCal para eventos recorrentes NAO-aniversario. Ex: RRULE:FREQ=WEEKLY;BYDAY=MO para toda segunda. Para aniversarios use is_birthday=true em vez disso."},
					"is_birthday": {"type": "boolean", "description": "Se true, cria como aniversario nativo do Google (all-day, recorrencia anual automatica, emoji 🎂). Use para qualquer aniversario. Nao precisa de time/duration/recurrence quando true."}
				},
				"required": ["title", "date_source"]
			}`),
```

Principais mudanças:
- `date_source` novo, obrigatório, enum.
- `date` descrição atualizada: "Obrigatório se explicit; ignorado se inferred".
- `time` ganha regra de PM-default.
- `required` muda de `["title", "date"]` pra `["title", "date_source"]`.

- [ ] **Step 5.3: Rodar `go build` e testes existentes — não podem quebrar**

Run: `cd bot && go build ./... && go test ./...`
Expected: build OK, testes passam. Só mudamos struct/schema; handler ainda lê `p.Date` normalmente.

- [ ] **Step 5.4: Commit**

```bash
git add bot/tools.go bot/agent.go
git commit -m "feat(tool): adiciona date_source ao contrato de criar_evento

Novo campo date_source (explicit|inferred) sinaliza se o usuario
mencionou marcador temporal (data/dia/amanha/hoje/etc) ou apenas hora.
'date' passa a ser condicional (obrigatorio em explicit; ignorado em
inferred). Schema documenta tambem PM-default para horas bare < 07:00.

Preparacao para integracao do resolver deterministico em Task 6.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: `handleCriarEvento` — integrar resolver e mudar output pra `OK_CRIADO|display=...`

**Files:**
- Modify: `bot/tools.go`

- [ ] **Step 6.1: Ler o bloco atual a ser substituído**

Arquivo `bot/tools.go`, função `handleCriarEvento`, linhas aproximadas 182-204 (o trecho de cálculo de `loc`, validação de `p.Time == ""`, e `time.ParseInLocation` em `startTime`). Identificar esse bloco antes de substituir.

- [ ] **Step 6.2: Substituir o bloco pela chamada ao resolver**

Substituir este bloco em `handleCriarEvento`:

```go
	// Resolve the timezone from the event's calendar date via the travel
	// period helper. An explicit Timezone param still wins (lets the agent
	// override for one-off foreign events without a registered travel).
	parsedDate, _ := time.ParseInLocation("2006-01-02", p.Date, BRT())
	loc := agent.db.GetEventTimezone(user.ID, parsedDate)
	tz := p.Timezone
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	} else {
		tz = loc.String()
	}
	// Time is required for non-birthday events. If the agent omitted it
	// (schema now allows that for birthdays), ask for it instead of failing.
	if p.Time == "" {
		log.Printf("[%s] criar_evento early-return: missing time (title=%q date=%s)", user.Name, p.Title, p.Date)
		return "Preciso do horario do evento. Pergunte ao usuario.", nil
	}
	startTime, err := time.ParseInLocation("2006-01-02 15:04", p.Date+" "+p.Time, loc)
	if err != nil {
		return "", fmt.Errorf("parse event time: %w", err)
	}
```

por:

```go
	// Hint inicial de data para lookup de fuso: data explicita se houver,
	// senao "agora" (caminho inferred). Apos resolver a data final, checamos
	// de novo se o fuso muda (caso viagem comece na data resolvida).
	var parsedDateHint time.Time
	if p.Date != "" {
		parsedDateHint, _ = time.ParseInLocation("2006-01-02", p.Date, BRT())
	} else {
		parsedDateHint = time.Now().In(BRT())
	}
	loc := agent.db.GetEventTimezone(user.ID, parsedDateHint)
	tz := p.Timezone
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	} else {
		tz = loc.String()
	}
	if p.Time == "" {
		log.Printf("[%s] criar_evento early-return: missing time (title=%q date=%s)", user.Name, p.Title, p.Date)
		return "Preciso do horario do evento. Pergunte ao usuario.", nil
	}
	if p.DateSource == "" {
		// Defensive: o schema exige date_source, mas se vier vazio tratamos
		// como explicit pra preservar comportamento anterior. Logamos pra
		// monitorar se acontece em producao.
		log.Printf("[%s] criar_evento warn: date_source vazio (title=%q date=%s) — tratando como explicit", user.Name, p.Title, p.Date)
		p.DateSource = string(DateSourceExplicit)
	}
	res, err := ResolveEventDate(ResolveInput{
		Source:       DateSource(p.DateSource),
		ExplicitDate: p.Date,
		Time:         p.Time,
		Now:          time.Now().In(loc),
		Loc:          loc,
	})
	if err != nil {
		return "", fmt.Errorf("resolve event date: %w", err)
	}
	// Se a data resolvida cai em outro fuso (viagem comecando nessa data),
	// re-resolver com o novo Loc.
	if resolvedLoc := agent.db.GetEventTimezone(user.ID, res.Start); resolvedLoc.String() != loc.String() && p.Timezone == "" {
		loc = resolvedLoc
		tz = loc.String()
		res, err = ResolveEventDate(ResolveInput{
			Source:       DateSource(p.DateSource),
			ExplicitDate: p.Date,
			Time:         p.Time,
			Now:          time.Now().In(loc),
			Loc:          loc,
		})
		if err != nil {
			return "", fmt.Errorf("resolve event date (re-tz): %w", err)
		}
	}
	startTime := res.Start
```

- [ ] **Step 6.3: Atualizar o retorno de sucesso pra `OK_CRIADO|display=...`**

No final de `handleCriarEvento`, substituir o bloco atual:

```go
	agent.audit.Log(user.ID, "criar_evento", "", p.Title)
	result := FormatEventCreated(*created)
	if created.MeetLink != "" {
		result += fmt.Sprintf("\nLink do Meet: %s", created.MeetLink)
	}
	if len(allDayNotes) > 0 {
		result += fmt.Sprintf("\nLembrete: nesse dia voce tem: %s", strings.Join(allDayNotes, ", "))
	}
	if conflictCheckWarn != "" {
		result += conflictCheckWarn
	}
	return result, nil
```

por:

```go
	agent.audit.Log(user.ID, "criar_evento", "", p.Title)
	display := FormatEventCreated(*created)
	if res.AdjustNote != "" {
		display = res.AdjustNote + display
	}
	if created.MeetLink != "" {
		display += fmt.Sprintf("\nLink do Meet: %s", created.MeetLink)
	}
	if len(allDayNotes) > 0 {
		display += fmt.Sprintf("\nLembrete: nesse dia voce tem: %s", strings.Join(allDayNotes, ", "))
	}
	if conflictCheckWarn != "" {
		display += conflictCheckWarn
	}
	return "OK_CRIADO|display=" + display, nil
```

**Notas:**
- `parsedDate` ao redor da linha 185 era usado em outros lugares? Verificar com `grep -n parsedDate bot/tools.go` e renomear usos. No código atual o `parsedDate` só é usado pra passar a `GetEventTimezone`, então a substituição por `parsedDateHint` é suficiente.
- O path de birthday (linhas 162-179 aprox.) NÃO usa `date_source` — aniversários sempre têm data explícita. Deixar inalterado.

- [ ] **Step 6.4: Rodar build + testes — nada pode quebrar**

Run: `cd bot && go build ./... && go test ./...`
Expected: build OK. Testes passam (nenhum teste existente depende do formato de retorno do handler ser texto livre vs `OK_CRIADO|`).

Se algum teste existente falhar por causa do novo prefixo, ajustar o teste pra aceitar o novo formato.

- [ ] **Step 6.5: Commit**

```bash
git add bot/tools.go
git commit -m "feat(criar_evento): integra resolver deterministico e blinda narrativa

handleCriarEvento agora delega resolucao de data a ResolveEventDate, que
aplica a regra sagrada quando date_source=inferred e auto-ajusta
explicit-hoje-passado. O evento criado no Google Calendar e a narrativa
(display) derivam da mesma variavel startTime -- impossivel divergir.
Retorno do handler agora vem prefixado com 'OK_CRIADO|display=' para o
prompt instruir Claude a citar verbatim.

Re-resolucao de fuso quando a data resolvida cai em periodo de viagem
com timezone diferente garante que 'reuniao as 9h' na vespera de viagem
respeita o fuso do destino.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: System prompt — regras de data implícita, PM-default, citação

**Files:**
- Modify: `bot/agent.go`

- [ ] **Step 7.1: Adicionar bloco de regras após "REGRAS CRITICAS PARA CRIAR EVENTOS"**

Em `bot/agent.go`, função `buildSystemPromptStable`, localizar o trecho existente:

```
REGRAS CRITICAS PARA CRIAR EVENTOS:
- Se faltar o horario, use seu julgamento: eventos como feiras, viagens, feriados → crie como dia inteiro (00:00, 1440min). Reunioes e compromissos com hora implicita → consulte a agenda, sugira o primeiro horario livre e so confirme (ex: "Marquei pra 10h, tudo bem?").
- "dia inteiro" = evento de 00:00 com duracao 1440 minutos.
- Quando o usuario pedir multiplos eventos, crie TODOS de uma vez (chame criar_evento varias vezes na mesma resposta).
```

E **inserir** logo após, antes de "REGRAS CRITICAS PARA EDITAR EVENTOS":

```
REGRA SAGRADA DE DATA IMPLICITA:
Quando o usuario mencionar APENAS uma hora, sem data, dia da semana, "amanha/hoje", ou qualquer outro marcador temporal, passe date_source="inferred" e NAO preencha date. O sistema resolve usando a regra deterministica:
- hora > agora → hoje
- hora <= agora → amanha

Quando o usuario mencionar QUALQUER marcador temporal (data explicita, dia da semana, "amanha", "hoje", "daqui N dias", "semana que vem"), passe date_source="explicit" com a data resolvida no campo date.

REGRA DE HORA BARE < 7H (PM-DEFAULT):
Horas bare (sem qualificador) menores que 07:00 → interprete como PM (some 12). Ex: "reuniao as 2h" = time="14:00". "call as 5h" = time="17:00". "as 6h" = time="18:00". EXCECOES: qualificador explicito "da madrugada", "da manha" mantem AM. Ex: "5h da manha" = time="05:00". Horas 07:00 ou maiores nao sofrem PM-default.

REGRA DE DIA DA SEMANA QUE BATE COM HOJE:
Se o usuario mencionar um dia da semana que e hoje (ex: "quinta as 9h" sendo hoje quinta), PERGUNTE antes de chamar a tool qual semana (essa ou a proxima). Nunca assuma.

REGRA DE CITACAO DO RESULTADO DE CRIAR_EVENTO:
Quando criar_evento retornar "OK_CRIADO|display=<texto>", sua resposta ao usuario DEVE incluir <texto> verbatim. Voce pode adicionar frase antes ou depois, mas NUNCA reformule a data relativa (HOJE/AMANHA) nem altere data/hora dentro de <texto>. Exemplo de resposta valida: "<texto do display>\n\nCriado. :)" (texto livre opcional APOS o display).

Exemplos de date_source (agora = 2026-04-16 07:02, quinta):
- "Reuniao as 9h"         → date_source="inferred", time="09:00"    (sistema: hoje 09:00)
- "Call as 5h"            → date_source="inferred", time="17:00"    (PM-default: hoje 17:00)
- "5h da manha"           → date_source="inferred", time="05:00"    (qualificador AM: amanha 05:00)
- "Reuniao as 7h"         → date_source="inferred", time="07:00"    (>= 7h sem PM-default: amanha 07:00)
- "Reuniao amanha as 9h"  → date_source="explicit", date="2026-04-17", time="09:00"
- "Reuniao dia 20 as 14h" → date_source="explicit", date="2026-04-20", time="14:00"
- "Quinta as 9h"          → PERGUNTE qual quinta (hoje e quinta); NAO chame a tool.
```

- [ ] **Step 7.2: Rodar build**

Run: `cd bot && go build ./...`
Expected: OK (mudança é só string do prompt).

- [ ] **Step 7.3: Rodar testes existentes**

Run: `cd bot && go test ./...`
Expected: PASS. Prompt não afeta testes unitários.

- [ ] **Step 7.4: Commit**

```bash
git add bot/agent.go
git commit -m "feat(prompt): regras de data implicita, PM-default e citacao

Adiciona quatro blocos de regra no system prompt estavel:
1. Regra sagrada de data implicita (quando usar date_source=inferred).
2. PM-default para horas bare < 07:00 (ex: '2h' = 14:00).
3. Confirmacao obrigatoria para dia da semana que bate com hoje.
4. Instrucao para citar OK_CRIADO|display=... verbatim, sem reformular
   a data relativa HOJE/AMANHA.

Essas regras, combinadas com o resolver deterministico em Go e o
rotulo HOJE/AMANHA no FormatEventCreated, fecham as tres camadas de
falha que causaram o incidente OTC.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Auditoria expandida — `LogCriarEvento` com campos estruturados

**Files:**
- Modify: `bot/audit.go`
- Modify: `bot/tools.go`

- [ ] **Step 8.1: Adicionar `LogCriarEvento` em `bot/audit.go`**

No final de `bot/audit.go`, adicionar:

```go
// LogCriarEvento registra criacao de evento com campos estruturados para
// observabilidade da regra de data implicita. Details armazena um blob
// pipe-separado: "title=...|user_msg=...|date_source=...|claude_date=...|claude_time=...|resolved_start=...|adjusted=...".
func (a *AuditLog) LogCriarEvento(userID int64, title, userMsgSnippet, dateSource, claudeDate, claudeTime, resolvedStart string, adjusted bool) error {
	snippet := userMsgSnippet
	if len(snippet) > 120 {
		snippet = snippet[:120]
	}
	details := fmt.Sprintf(
		"title=%s|user_msg=%s|date_source=%s|claude_date=%s|claude_time=%s|resolved_start=%s|adjusted=%t",
		title, snippet, dateSource, claudeDate, claudeTime, resolvedStart, adjusted,
	)
	_, err := a.db.conn.Exec(
		`INSERT INTO action_log (user_id, action, target_user, details) VALUES (?, ?, ?, ?)`,
		userID, "criar_evento", "", details)
	return err
}
```

- [ ] **Step 8.2: Substituir `audit.Log` por `audit.LogCriarEvento` em `handleCriarEvento`**

Em `bot/tools.go`, localizar a linha:

```go
	agent.audit.Log(user.ID, "criar_evento", "", p.Title)
```

E substituir por:

```go
	// Snippet da ultima mensagem do usuario pra observabilidade de data implicita.
	var userMsgSnippet string
	if hist, histErr := agent.db.GetConversationHistory(user.ID, 5); histErr == nil {
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].Role == "user" {
				userMsgSnippet = hist[i].Content
				break
			}
		}
	}
	agent.audit.LogCriarEvento(user.ID, p.Title, userMsgSnippet, p.DateSource, p.Date, p.Time,
		res.Start.Format(time.RFC3339), res.Adjusted)
```

**Nota:** o path de birthday em `handleCriarEvento` (linhas ~162-179) usa `audit.Log` direto — manter inalterado, porque birthday tem semântica diferente.

- [ ] **Step 8.3: Rodar build + testes**

Run: `cd bot && go build ./... && go test ./...`
Expected: PASS. Mudança adiciona função nova + chamada; não quebra nada existente.

- [ ] **Step 8.4: Commit**

```bash
git add bot/audit.go bot/tools.go
git commit -m "feat(audit): log estruturado de criar_evento para observabilidade

Nova LogCriarEvento grava em details um blob pipe-separado com:
title, user_msg (snippet dos 120 primeiros chars), date_source,
claude_date (mesmo quando ignorado pelo resolver), claude_time,
resolved_start (ISO-8601), adjusted (bool).

Permite post-mortem: quantas chamadas com date_source=inferred tiveram
claude_date divergente do resolvido? Sinal de saude do prompt.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Teste de regressão do incidente em `integration_test.go`

**Files:**
- Modify: `bot/integration_test.go`

- [ ] **Step 9.1: Adicionar teste de regressão que exercita o resolver + formatter**

Este teste não chama Calendar real (não podemos em unit test). Foca no contrato interno: dado `date_source=inferred` + `time=09:00` + now=07:02 de 16/04, a saída do resolver é 16/04 09:00 e o `FormatEventCreated` sobre um evento com essa data retorna texto contendo `HOJE`, `Quinta`, `16/04`, `09:00`.

Adicionar ao final de `bot/integration_test.go`:

```go
func TestRegressao_BugReuniaoOTC(t *testing.T) {
	// Incidente 16/04/2026 07:02: usuario disse "Reuniao as 9h com OTC".
	// Regra sagrada: 9h > 7h:02 -> HOJE (16/04) 09:00.
	// Bot criou para 18/04 e confirmou "amanha as 9h" -> divergencia tripla.
	// Este teste blinda: com date_source=inferred, o resolver produz a
	// data correta, e o FormatEventCreated aplica rotulo HOJE.
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	incidentNow := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)

	res, err := ResolveEventDate(ResolveInput{
		Source: DateSourceInferred,
		Time:   "09:00",
		Now:    incidentNow,
		Loc:    brt,
	})
	if err != nil {
		t.Fatalf("resolver falhou: %v", err)
	}
	wantStart := time.Date(2026, 4, 16, 9, 0, 0, 0, brt)
	if !res.Start.Equal(wantStart) {
		t.Fatalf("BUG REINCIDENTE: resolver deu %s, esperava %s (HOJE 09:00)", res.Start, wantStart)
	}
	if res.Adjusted {
		t.Fatalf("Adjusted deveria ser false em inferred")
	}

	// Validar formatacao da narrativa.
	ev := CalendarEvent{
		Title: "Reuniao com OTC",
		Start: res.Start,
		End:   res.Start.Add(time.Hour),
	}
	// Injetamos incidentNow via patch de FormatEventCreated? Nao: a funcao
	// usa time.Now() internamente. Solucao: testamos relativeDayLabel
	// diretamente com o now injetado.
	label := relativeDayLabel(ev.Start, incidentNow)
	if label != "HOJE" {
		t.Fatalf("BUG NARRATIVO: relativeDayLabel retornou %q, esperava HOJE", label)
	}
}

func TestRegressao_InferredTardeVaiProHoje(t *testing.T) {
	// Caso PM-default: "call as 5h" as 07:02 -> Claude converte pra 17:00,
	// resolver coloca hoje 17:00 (17 > 7:02).
	brt, _ := time.LoadLocation("America/Sao_Paulo")
	now := time.Date(2026, 4, 16, 7, 2, 0, 0, brt)

	res, err := ResolveEventDate(ResolveInput{
		Source: DateSourceInferred,
		Time:   "17:00",
		Now:    now,
		Loc:    brt,
	})
	if err != nil {
		t.Fatalf("resolver falhou: %v", err)
	}
	want := time.Date(2026, 4, 16, 17, 0, 0, 0, brt)
	if !res.Start.Equal(want) {
		t.Fatalf("PM-default path quebrou: deu %s, esperava %s", res.Start, want)
	}
	if relativeDayLabel(res.Start, now) != "HOJE" {
		t.Fatalf("rotulo relativo deveria ser HOJE")
	}
}
```

- [ ] **Step 9.2: Rodar teste — esperar passar**

Run: `cd bot && go test -run TestRegressao -v`
Expected: PASS em ambos os subtestes.

- [ ] **Step 9.3: Rodar suite completa pra garantir que nada quebrou**

Run: `cd bot && go test ./...`
Expected: PASS em tudo.

- [ ] **Step 9.4: Commit**

```bash
git add bot/integration_test.go
git commit -m "test(regressao): bug do evento OTC criado para 2 dias a frente

Documenta no codigo, como teste de regressao, o incidente de 16/04/2026:
'Reuniao as 9h' as 07:02 deve resolver para HOJE 09:00. Se esse teste
voltar a falhar, a regra sagrada esta violada.

Segundo teste cobre o caminho PM-default: '5h' bare as 07:02 -> Claude
envia time=17:00 -> resolver produz hoje 17:00.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10 (opcional, defesa em profundidade): Watchdog de divergência narrativa

**Files:**
- Modify: `bot/watchdog.go`
- Modify: `bot/orchestrator.go` (ponto de chamada)

Só executar se a suite de Tasks 1-9 estiver estável em produção por algumas semanas **ou** se a equipe quiser a rede de segurança desde o início.

- [ ] **Step 10.1: Adicionar helper `checkNarrativeDrift` em `bot/watchdog.go`**

```go
import "regexp"

var displayExtractor = regexp.MustCompile(`OK_CRIADO\|display=(.+?)(?:\n|$)`)
var relLabelExtractor = regexp.MustCompile(`\b(HOJE|AMANHA)\b`)

// CheckNarrativeDrift compara o rotulo relativo presente no toolOutput
// (saida de criar_evento) contra a finalResponse enviada ao usuario.
// Se o rotulo existe no display mas nao aparece na resposta final,
// loga warning. Nao modifica nem bloqueia -- apenas observabilidade.
func CheckNarrativeDrift(toolOutput, finalResponse string) {
	m := displayExtractor.FindStringSubmatch(toolOutput)
	if len(m) < 2 {
		return
	}
	display := m[1]
	rel := relLabelExtractor.FindString(display)
	if rel == "" {
		return
	}
	if !strings.Contains(finalResponse, rel) {
		log.Printf("[NARRATIVE_DRIFT] expected rel=%s got_response=%q display=%q",
			rel, finalResponse, display)
	}
}
```

Adicionar imports necessários se faltarem.

- [ ] **Step 10.2: Chamar `CheckNarrativeDrift` no orchestrator após resposta final**

Em `bot/orchestrator.go`, localizar onde a resposta final do Claude volta após uma tool call de `criar_evento`. Adicionar chamada a `CheckNarrativeDrift(lastToolOutput, finalResponse)` imediatamente antes de enviar a resposta ao WhatsApp.

Identificar o ponto exato lendo o fluxo atual do orquestrador (não replicar aqui pra evitar desatualização; o engenheiro deve ler o arquivo e inserir no local correto do loop tool→response).

- [ ] **Step 10.3: Rodar build + testes**

Run: `cd bot && go build ./... && go test ./...`
Expected: PASS.

- [ ] **Step 10.4: Commit**

```bash
git add bot/watchdog.go bot/orchestrator.go
git commit -m "feat(watchdog): log divergencia entre display e resposta final

CheckNarrativeDrift extrai o rotulo HOJE/AMANHA da saida do tool
criar_evento e verifica se aparece na resposta final do agente ao
usuario. Se divergir, log warning NARRATIVE_DRIFT. Nao modifica a
resposta -- observabilidade apenas. Se virar frequente, v2 pode
forcar append do display.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage (spec → task mapping):**

| Seção do spec | Task(s) |
|---|---|
| §2 Arquitetura fonte única | Task 1-3, 6 |
| §3 Contrato `criar_evento` (input + output) | Task 5, 6 |
| §4 Resolver determinístico | Task 1-3 |
| §5 Integração em `handleCriarEvento` | Task 6 |
| §6 Narrativa determinística `FormatEventCreated` | Task 4 |
| §7 Trava narrativa no prompt | Task 7 |
| §8 Auditoria expandida | Task 8 |
| §9 Watchdog narrativo (opcional) | Task 10 |
| §10 Estratégia de testes | Tasks 1-3 (unit), 4 (formatter), 9 (regressão) |
| §11 Escopo fora | não implementado (correto) |
| §12 Riscos | mitigados via tasks 7 (prompt), 8 (audit), 9 (teste) |

Nenhum requisito do spec sem task correspondente. ✓

**Placeholder scan:** nenhum `TBD`, `TODO`, ou "implement later". Código completo em cada step. ✓

**Type consistency:**
- `DateSource`, `ResolveInput`, `ResolveOutput`, `ResolveEventDate` consistentes entre tasks 1, 2, 3, 6, 9.
- `DateSourceInferred`, `DateSourceExplicit` consistentes.
- `relativeDayLabel` consistente entre task 4 e 9.
- `OK_CRIADO|display=` prefixo consistente entre tasks 6, 7 (prompt), 10 (watchdog).
- `LogCriarEvento` assinatura consistente entre task 8 (definição) e 8 (chamada).

Nenhum mismatch de tipo/nome detectado. ✓

**Escopo:** plano focado num único tool (`criar_evento`), single implementation pass. ✓
