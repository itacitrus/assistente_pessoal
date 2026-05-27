package main

import (
	"os"
	"strings"
	"testing"
)

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"Losartana":              "losartana",
		"DIPIRONA SÓDICA":        "dipirona sodica",
		"Ácido Acetilsalicílico": "acido acetilsalicilico",
		"AAS 100mg":              "aas 100mg",
		"  vários   espaços  ":   "varios espacos",
		"Tylenol® (DC)":          "tylenol dc",
		"":                       "",
	}
	for in, want := range cases {
		if got := normalizeName(in); got != want {
			t.Errorf("normalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetaphonePTBR(t *testing.T) {
	// Pares que DEVEM colidir foneticamente (mesmo som, grafia diferente/errada).
	collide := [][2]string{
		{"losartana", "lozartana"}, // s/z
		{"buscopan", "buscopam"},   // nasal final m/n
		{"farmacia", "pharmacia"},  // ph/f
		{"caza", "casa"},           // z/s
		{"quilo", "kilo"},          // qu/k
		{"giro", "jiro"},           // g brando / j
	}
	for _, p := range collide {
		a, b := metaphonePTBR(p[0]), metaphonePTBR(p[1])
		if a != b {
			t.Errorf("metaphonePTBR(%q)=%q != metaphonePTBR(%q)=%q (deveriam colidir)", p[0], a, p[1], b)
		}
	}
	// Pares que NAO devem colidir (sons claramente distintos).
	distinct := [][2]string{
		{"losartana", "sinvastatina"},
		{"omeprazol", "metformina"},
	}
	for _, p := range distinct {
		if metaphonePTBR(p[0]) == metaphonePTBR(p[1]) {
			t.Errorf("metaphonePTBR(%q) e metaphonePTBR(%q) colidiram, nao deveriam", p[0], p[1])
		}
	}
}

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "", 3},
		{"losartana", "losartna", 1},
		{"kitten", "sitting", 3},
		{"buscopan", "buscopam", 1},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// newDrugTestDB cria um DB em memoria com um catalogo minimo controlado, para
// testar o ranking do ResolveDrug sem depender do arquivo CMED real.
func newDrugTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	seed := []struct {
		name, ingredient, conc string
	}{
		{"Losartana Potássica", "LOSARTANA POTASSICA", "50 MG"},
		{"Losartana Potássica", "LOSARTANA POTASSICA", "100 MG"},
		{"Buscopan", "BUTILBROMETO DE ESCOPOLAMINA", "10 MG"},
		{"Neosaldina", "DIPIRONA;ISOMETEPTENO;CAFEINA", "300 MG"},
		{"Rivotril", "CLONAZEPAM", "2 MG"},
		{"Dipirona Monoidratada", "DIPIRONA MONOIDRATADA", "500 MG"},
		{"Sinvastatina", "SINVASTATINA", "20 MG"},
		{"Omeprazol", "OMEPRAZOL", "20 MG"},
		{"Atenolol", "ATENOLOL", "25 MG"},
	}
	for _, s := range seed {
		_, err := db.conn.Exec(
			`INSERT INTO drug_catalog (commercial_name, active_ingredient, concentration, norm_name, norm_ingredient)
			 VALUES (?,?,?,?,?)`,
			s.name, s.ingredient, s.conc, normalizeName(s.name), normalizeName(s.ingredient))
		if err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
	return db
}

func TestResolveDrugTypos(t *testing.T) {
	db := newDrugTestDB(t)

	// Cada caso: query errada -> esperamos esse nome comercial como TOP match.
	cases := map[string]string{
		"losartna":   "Losartana Potássica", // letra faltando
		"lozartana":  "Losartana Potássica", // s->z fonetico
		"buscopam":   "Buscopan",            // nasal final
		"rivotrio":   "Rivotril",            // l->o no fim
		"neusaldina": "Neosaldina",          // e->u
		"dipirona":   "Dipirona Monoidratada",
		"sinvastina": "Sinvastatina", // silaba faltando
		"omeprasol":  "Omeprazol",    // s/z
	}
	for query, wantTop := range cases {
		matches, err := db.ResolveDrug(query, 5)
		if err != nil {
			t.Fatalf("ResolveDrug(%q): %v", query, err)
		}
		if len(matches) == 0 {
			t.Errorf("ResolveDrug(%q): nenhum match (esperava %q no topo)", query, wantTop)
			continue
		}
		if matches[0].CommercialName != wantTop {
			t.Errorf("ResolveDrug(%q): top=%q (conf %.2f), esperava %q. Lista: %v",
				query, matches[0].CommercialName, matches[0].Confidence, wantTop, names(matches))
		}
	}
}

func TestResolveDrugByActiveIngredient(t *testing.T) {
	db := newDrugTestDB(t)
	// Buscar pelo principio ativo deve achar o produto de marca.
	matches, err := db.ResolveDrug("clonazepam", 5)
	if err != nil {
		t.Fatalf("ResolveDrug: %v", err)
	}
	if len(matches) == 0 || matches[0].CommercialName != "Rivotril" {
		t.Errorf("buscar 'clonazepam' deveria achar Rivotril no topo, veio %v", names(matches))
	}
}

func TestResolveDrugPrefixAutocomplete(t *testing.T) {
	db := newDrugTestDB(t)
	// Prefixo curto (digitacao parcial do autocomplete) traz as 2 apresentacoes
	// de Losartana.
	matches, err := db.ResolveDrug("losar", 10)
	if err != nil {
		t.Fatalf("ResolveDrug: %v", err)
	}
	if len(matches) < 2 {
		t.Fatalf("prefixo 'losar' deveria trazer >=2 apresentacoes, veio %d: %v", len(matches), names(matches))
	}
	for _, m := range matches[:2] {
		if m.CommercialName != "Losartana Potássica" {
			t.Errorf("esperava Losartana nas primeiras posicoes, veio %q", m.CommercialName)
		}
	}
}

func TestResolveDrugEmptyAndGarbage(t *testing.T) {
	db := newDrugTestDB(t)
	if m, _ := db.ResolveDrug("", 5); m != nil {
		t.Errorf("query vazia deveria retornar nil, veio %v", names(m))
	}
	if m, _ := db.ResolveDrug("xyzqwk", 5); len(m) != 0 {
		t.Errorf("lixo sem parentesco deveria retornar vazio, veio %v", names(m))
	}
}

func TestResolveDrugEmptyCatalog(t *testing.T) {
	db, err := NewDB(":memory:")
	if err != nil {
		t.Fatalf("NewDB: %v", err)
	}
	defer db.Close()
	// Tabela existe (migrada) mas vazia: sem erro, sem resultados.
	m, err := db.ResolveDrug("losartana", 5)
	if err != nil {
		t.Fatalf("ResolveDrug em catalogo vazio retornou erro: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("catalogo vazio deveria dar nil, veio %v", names(m))
	}
}

// TestResolveDrugRealCatalog eh opt-in: rode com ZELLO_DRUG_DB apontando para um
// SQLite ja populado pelo scripts/ingest_drug_catalog.py. Valida que erros reais
// de digitacao recuperam o remedio certo entre os primeiros candidatos.
func TestResolveDrugRealCatalog(t *testing.T) {
	path := os.Getenv("ZELLO_DRUG_DB")
	if path == "" {
		t.Skip("defina ZELLO_DRUG_DB=<caminho do bot.db populado> para rodar este teste")
	}
	db, err := NewDB(path)
	if err != nil {
		t.Fatalf("NewDB(%q): %v", path, err)
	}
	defer db.Close()

	// query errada -> nome comercial (normalizado) que deve aparecer no top 3.
	cases := map[string]string{
		"losartna":   "losartana",
		"buscopam":   "buscopan",
		"sinvastina": "sinvastatina",
		"omeprasol":  "omeprazol",
		"rivotrio":   "rivotril",
		"dipirona":   "dipirona",
		"metformina": "metformina",
		"puran t4":   "puran",
	}
	for query, wantSub := range cases {
		matches, err := db.ResolveDrug(query, 3)
		if err != nil {
			t.Fatalf("ResolveDrug(%q): %v", query, err)
		}
		found := false
		for _, m := range matches {
			if strings.Contains(normalizeName(m.CommercialName), wantSub) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ResolveDrug(%q): esperava %q no top 3, veio %v", query, wantSub, names(matches))
		}
	}
}

func names(ms []DrugMatch) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.CommercialName
	}
	return out
}
