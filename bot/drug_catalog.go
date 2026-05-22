package main

import (
	"fmt"
	"sort"
	"strings"
)

// =========================================================================
// Catalogo de medicamentos: resolucao fuzzy/fonetica (Fase 3)
// =========================================================================
//
// A tabela drug_catalog (fonte: Lista CMED/ANVISA) eh populada FORA do runtime
// pelo script scripts/ingest_drug_catalog.py. Aqui o bot apenas LE e resolve o
// que o usuario digitou/falou (possivelmente errado) para apresentacoes reais.
//
// Estrategia (uma passada sobre o catalogo por consulta):
//   1. match exato no nome normalizado          -> confianca 1.00
//   2. query eh prefixo do nome (digitacao parcial) -> 0.90
//   3. nome contem a query (substring, q>=4)     -> 0.80
//   4. similaridade de Levenshtein + bonus fonetico -> ate ~0.75
// O nome comercial e o principio ativo sao ambos pontuados; vale o maior.
//
// Por que scan completo e nao indice fonetico: ~9k apresentacoes x Levenshtein
// de strings curtas eh ~1M ops triviais (poucos ms). Com o debounce do
// autocomplete isso eh barato, e garante recuperar erros que um bucket fonetico
// imperfeito perderia (ex: "buscopam" -> "buscopan", distancia 1). Se algum dia
// o volume crescer, da pra adicionar bucket por phonName sem mudar a interface.

// DrugMatch eh um candidato resolvido, pronto pra UI/LLM. Confidence in (0,1].
type DrugMatch struct {
	ID               int64   `json:"id"`
	CommercialName   string  `json:"commercial_name"`
	ActiveIngredient string  `json:"active_ingredient"`
	Concentration    string  `json:"concentration"`
	Presentation     string  `json:"presentation"`
	ProductType      string  `json:"product_type"`
	Tarja            string  `json:"tarja"`
	Confidence       float64 `json:"confidence"`
}

// drugEntry eh uma linha do catalogo com as chaves de match pre-computadas.
type drugEntry struct {
	id               int64
	commercialName   string
	activeIngredient string
	concentration    string
	presentation     string
	productType      string
	tarja            string

	normName       string // normalizeName(commercialName)
	normIngredient string // normalizeName(activeIngredient)
	phonName       string // metaphonePTBR(commercialName)
	phonIngredient string // metaphonePTBR(activeIngredient)
}

// loadDrugIndex le o catalogo inteiro em memoria, pre-computando normalizacoes.
// Retorna slice vazio (nao erro) quando a tabela existe mas esta vazia — o
// catalogo eh populado por um script externo e pode ainda nao ter rodado.
func (db *DB) loadDrugIndex() ([]drugEntry, error) {
	rows, err := db.conn.Query(`
		SELECT id, commercial_name, active_ingredient, concentration,
		       presentation, product_type, tarja, norm_name, norm_ingredient
		FROM drug_catalog`)
	if err != nil {
		return nil, fmt.Errorf("query drug_catalog: %w", err)
	}
	defer rows.Close()

	var out []drugEntry
	for rows.Next() {
		var e drugEntry
		if err := rows.Scan(&e.id, &e.commercialName, &e.activeIngredient,
			&e.concentration, &e.presentation, &e.productType, &e.tarja,
			&e.normName, &e.normIngredient); err != nil {
			return nil, fmt.Errorf("scan drug_catalog: %w", err)
		}
		e.phonName = metaphonePTBR(e.commercialName)
		e.phonIngredient = metaphonePTBR(e.activeIngredient)
		out = append(out, e)
	}
	return out, rows.Err()
}

// drugIndex devolve o indice em memoria, construindo-o na primeira chamada.
// Cacheia apenas quando ha linhas: se a tabela estiver vazia (ingestao ainda
// nao rodou), uma chamada futura tenta de novo — barato e evita "travar" vazio.
// (Re)ingestao com o bot em execucao so reflete apos restart, o que acontece em
// todo deploy; documentado de proposito para nao complicar com invalidacao.
func (db *DB) drugIndex() ([]drugEntry, error) {
	db.drugMu.RLock()
	idx := db.drugIdx
	db.drugMu.RUnlock()
	if idx != nil {
		return idx, nil
	}

	db.drugMu.Lock()
	defer db.drugMu.Unlock()
	if db.drugIdx != nil { // outra goroutine construiu enquanto esperavamos
		return db.drugIdx, nil
	}
	loaded, err := db.loadDrugIndex()
	if err != nil {
		return nil, err
	}
	if len(loaded) > 0 {
		db.drugIdx = loaded
	}
	return loaded, nil
}

// minDrugConfidence eh o piso para um candidato entrar no resultado. Abaixo
// disso o match eh ruido (palavras sem parentesco real).
const minDrugConfidence = 0.5

// ResolveDrug devolve ate `limit` apresentacoes mais provaveis para `query`,
// ordenadas por confianca desc. query vazia ou catalogo vazio -> nil, nil.
func (db *DB) ResolveDrug(query string, limit int) ([]DrugMatch, error) {
	qNorm := normalizeName(query)
	if qNorm == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	entries, err := db.drugIndex()
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	qPhon := metaphonePTBR(query)

	type scored struct {
		e     *drugEntry
		score float64
	}
	var hits []scored
	for i := range entries {
		e := &entries[i]
		s := scoreField(qNorm, qPhon, e.normName, e.phonName)
		// Principio ativo pesa um tico menos para que, em empate, o nome
		// comercial ganhe (eh o que o usuario costuma digitar).
		if is := scoreField(qNorm, qPhon, e.normIngredient, e.phonIngredient) * 0.97; is > s {
			s = is
		}
		if s >= minDrugConfidence {
			hits = append(hits, scored{e, s})
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}

	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		// Desempate: nome mais curto (mais proximo do que foi digitado) e
		// ordem alfabetica, para um resultado estavel/deterministico.
		if len(hits[i].e.normName) != len(hits[j].e.normName) {
			return len(hits[i].e.normName) < len(hits[j].e.normName)
		}
		return hits[i].e.normName < hits[j].e.normName
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]DrugMatch, len(hits))
	for i, h := range hits {
		out[i] = DrugMatch{
			ID:               h.e.id,
			CommercialName:   h.e.commercialName,
			ActiveIngredient: h.e.activeIngredient,
			Concentration:    h.e.concentration,
			Presentation:     h.e.presentation,
			ProductType:      h.e.productType,
			Tarja:            h.e.tarja,
			Confidence:       round2(h.score),
		}
	}
	return out, nil
}

// scoreField pontua a query contra UM campo (nome ou principio ativo). Nomes
// no catalogo costumam ter varias palavras ("Losartana Potassica", "Dipirona
// Monoidratada") enquanto o usuario digita so o termo principal — entao
// pontuamos contra a string inteira E contra cada palavra, valendo o maior.
// Isso faz "losartna" casar com o token "losartana" sem que o " potassica"
// infle a distancia de edicao.
func scoreField(qNorm, qPhon, tNorm, tPhon string) float64 {
	if tNorm == "" {
		return 0
	}
	best := scoreOne(qNorm, qPhon, tNorm, tPhon)
	if best >= 1.0 {
		return best
	}
	if strings.ContainsRune(tNorm, ' ') {
		for _, tok := range strings.Fields(tNorm) {
			if len(tok) < 3 { // ignora conectivos ("de", "da") como alvo
				continue
			}
			if s := scoreOne(qNorm, qPhon, tok, metaphonePTBR(tok)); s > best {
				best = s
			}
		}
	}
	return best
}

// scoreOne pontua a query contra uma unica string-alvo ja normalizada.
func scoreOne(qNorm, qPhon, tNorm, tPhon string) float64 {
	if tNorm == qNorm {
		return 1.0
	}
	if strings.HasPrefix(tNorm, qNorm) {
		return 0.9
	}
	if len(qNorm) >= 4 && strings.Contains(tNorm, qNorm) {
		return 0.8
	}
	// Camada fuzzy: similaridade de edicao, com piso fonetico para homofonos.
	d := levenshtein(qNorm, tNorm)
	maxLen := len(qNorm)
	if len(tNorm) > maxLen {
		maxLen = len(tNorm)
	}
	if maxLen == 0 {
		return 0
	}
	sim := 1 - float64(d)/float64(maxLen)
	if qPhon != "" && qPhon == tPhon && sim < 0.72 {
		sim = 0.72 // mesmo som -> trata como forte mesmo se a grafia divergir
	}
	if sim < 0.6 {
		return 0
	}
	// Mapeia sim[0.6..1.0] -> ~[0.6..0.78], sempre abaixo do tier de substring.
	return 0.45 + sim*0.33
}

// levenshtein eh a distancia de edicao classica (insercao/remocao/substituicao),
// com uma unica linha de DP. Opera sobre runes para nao quebrar em multibyte
// (apos normalizeName as strings sao ASCII, mas runes mantem correto de toda forma).
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr := prev[0]
		prev[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := prev[j-1] + 1
			sub := curr + cost
			next := del
			if ins < next {
				next = ins
			}
			if sub < next {
				next = sub
			}
			curr = prev[j]
			prev[j] = next
		}
	}
	return prev[lb]
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

// =========================================================================
// Normalizacao e fonetica PT-BR
// =========================================================================

// foldAccentsLower deixa minusculo e remove acentos do portugues, mapeando para
// o ASCII base. ç -> c (consistente com a normalizacao do ingest em Python, que
// usa NFKD + remocao de combining). Para a fonetica, ç vira 's' ANTES de chamar
// isto (ver metaphonePTBR).
func foldAccentsLower(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'á', 'à', 'â', 'ã', 'ä', 'å':
			b.WriteRune('a')
		case 'é', 'è', 'ê', 'ë':
			b.WriteRune('e')
		case 'í', 'ì', 'î', 'ï':
			b.WriteRune('i')
		case 'ó', 'ò', 'ô', 'õ', 'ö':
			b.WriteRune('o')
		case 'ú', 'ù', 'û', 'ü':
			b.WriteRune('u')
		case 'ç':
			b.WriteRune('c')
		case 'ñ':
			b.WriteRune('n')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normalizeName espelha scripts/ingest_drug_catalog.py:normalize — minusculo,
// sem acento, somente [a-z0-9 ], espacos colapsados. PRECISA bater com o Python
// para que o match exato/substring contra norm_name funcione.
func normalizeName(s string) string {
	folded := foldAccentsLower(s)
	var b strings.Builder
	b.Grow(len(folded))
	prevSpace := false
	for _, r := range folded {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			b.WriteRune(r)
			prevSpace = false
		} else {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// metaphonePTBR produz um codigo fonetico aproximado para o portugues: aplica
// substituicoes de som (digrafos, c/g brandos, s/z, etc), funde nasais m/n,
// remove vogais (exceto a inicial) e colapsa repeticoes. Nao eh um algoritmo
// canonico — eh um "esqueleto consonantal" tunado pro PT-BR, usado como reforco
// de confianca para homofonos no ResolveDrug.
func metaphonePTBR(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// ç soa 's' (precisa vir antes do fold, que mandaria pra 'c'/'k').
	s = strings.ReplaceAll(s, "ç", "s")
	s = foldAccentsLower(s)

	// Mantem so letras (junta tudo; espacos viram nada para o esqueleto).
	var letters []rune
	for _, r := range s {
		if r >= 'a' && r <= 'z' {
			letters = append(letters, r)
		}
	}
	if len(letters) == 0 {
		return ""
	}

	var out []rune
	n := len(letters)
	for i := 0; i < n; i++ {
		c := letters[i]
		var next rune
		if i+1 < n {
			next = letters[i+1]
		}
		switch c {
		case 'h':
			// 'h' eh mudo isolado; digrafos com h sao tratados na consoante anterior.
			continue
		case 'l':
			if next == 'h' { // lh -> som de 'l' palatal; aproximamos por 'l'
				out = append(out, 'l')
				i++
				continue
			}
			out = append(out, 'l')
		case 'n':
			if next == 'h' { // nh -> nasal palatal ~ 'n'
				out = append(out, 'n')
				i++
				continue
			}
			out = append(out, 'n')
		case 'm':
			out = append(out, 'n') // funde nasais: "buscopam" ~ "buscopan"
		case 'c':
			switch {
			case next == 'h': // ch -> som de 'x' (chiado) no PT-BR
				out = append(out, 'x')
				i++
			case next == 'e' || next == 'i': // c brando -> 's'
				out = append(out, 's')
			default: // c duro -> 'k'
				out = append(out, 'k')
			}
		case 'g':
			if next == 'e' || next == 'i' { // g brando -> 'j'
				out = append(out, 'j')
			} else {
				out = append(out, 'g')
			}
		case 'q':
			out = append(out, 'k') // qu -> k (o 'u' mudo cai como vogal)
		case 'z':
			out = append(out, 's')
		case 's':
			out = append(out, 's')
		case 'x':
			out = append(out, 'x')
		case 'w':
			out = append(out, 'v')
		case 'y':
			out = append(out, 'i')
		case 'p':
			if next == 'h' { // ph -> f
				out = append(out, 'f')
				i++
				continue
			}
			out = append(out, 'p')
		case 'k':
			out = append(out, 'k')
		case 'a', 'e', 'i', 'o', 'u':
			if len(out) == 0 { // mantem so a vogal inicial
				out = append(out, c)
			}
		default:
			out = append(out, c)
		}
	}

	// Colapsa consoantes repetidas consecutivas (rr->r, ss->s, etc).
	var dedup []rune
	for i, r := range out {
		if i > 0 && r == out[i-1] {
			continue
		}
		dedup = append(dedup, r)
	}
	return string(dedup)
}
