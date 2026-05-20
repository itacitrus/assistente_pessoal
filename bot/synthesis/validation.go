package synthesis

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// quoteRegex matcha provavel citacao literal: texto entre aspas retas ou
// curvas com >= 6 caracteres. NAO matcha apostrofo solto (false-positives
// em "ja nao" etc). Usado em todas as validacoes de output (writer e report).
//
// Construcao defensiva: cobre "...", "...", "..." (curvas) e mistos.
var quoteRegex = regexp.MustCompile("[\"“][^\"“”]{6,}[\"”]")

// clinicalTerms eh a lista de palavras que disparam rejeicao automatica.
// O writer e o report SAO PROIBIDOS de diagnosticar — emitem observacao
// abstrata, nao rotulo clinico. Forma com e sem acento pra cobrir saidas
// inconsistentes do modelo.
var clinicalTerms = []string{
	"depressao", "depressão",
	"ansiedade clinica", "ansiedade clínica",
	"transtorno",
	"sindrome", "síndrome",
	"demencia", "demência",
	"alzheimer",
	"patologia",
	"diagnostico", "diagnóstico",
}

// fofocaKeywords lista padroes textuais que sinalizam que o item eh fofoca
// social, NAO saude/seguranca. Match com padding de espaco pra evitar
// false-positive em substrings (ex: "religiao" dentro de "religiao_da_filha").
//
// O writer ja eh instruido a filtrar via prompt; isso eh defesa em
// profundidade (algumas saidas escapam o filtro semantico do modelo).
var fofocaKeywords = []string{
	" brigou ", " brigaram ", " discutiu ",
	" fofoca ", " novela ",
	" presidente ", " politica ", " política ",
	" religiao ", " religião ",
	" futebol ", " corinthians ", " flamengo ", " palmeiras ",
}

// ValidateSnapshotOutput aplica o contrato do writer:
//
//   - Scores em [0,5].
//   - Confidence em [1,5].
//   - Lengths max em humor_nuance, sinais_observados, eventos_dia.
//   - SEM aspas literais (privacidade).
//   - SEM termo clinico (diagnostico).
//   - SEM keyword de fofoca (privacidade + escopo).
//   - Se SafetyAlertNeeded != nil: severity e category devem estar nos
//     enums; reason nao vazio; sem citacao em reason+recommended.
func ValidateSnapshotOutput(o SnapshotOutput) error {
	if err := validateScore("humor_score", o.HumorScore); err != nil {
		return err
	}
	if err := validateScore("energia_score", o.EnergiaScore); err != nil {
		return err
	}
	if err := validateScore("sociabilidade_score", o.SociabilidadeScore); err != nil {
		return err
	}
	if err := validateScore("autocuidado_score", o.AutocuidadoScore); err != nil {
		return err
	}
	if o.Confidence < 1 || o.Confidence > 5 {
		return fmt.Errorf("confidence fora do range 1-5: %d", o.Confidence)
	}
	if len(o.HumorNuance) > 100 {
		return fmt.Errorf("humor_nuance excede 100 ch: %d", len(o.HumorNuance))
	}
	if len(o.SinaisObservados) > 5 {
		return fmt.Errorf("sinais_observados excede 5: %d", len(o.SinaisObservados))
	}
	if len(o.EventosDia) > 5 {
		return fmt.Errorf("eventos_dia excede 5: %d", len(o.EventosDia))
	}
	for _, s := range o.SinaisObservados {
		if len(s) > 100 {
			return fmt.Errorf("sinal_observado excede 100 ch: %q", truncate(s, 50))
		}
	}
	for _, e := range o.EventosDia {
		if len(e) > 100 {
			return fmt.Errorf("evento_dia excede 100 ch: %q", truncate(e, 50))
		}
	}

	// Filtros de privacidade + clinico + fofoca aplicados ao agregado de
	// todo texto livre. Padding de espacos garante match de substring com
	// boundary defensivo.
	all := o.HumorNuance + " " +
		strings.Join(o.SinaisObservados, " ") + " " +
		strings.Join(o.EventosDia, " ")
	if quoteRegex.MatchString(all) {
		return errors.New("output contains literal-looking quotation (privacy violation)")
	}
	lower := " " + strings.ToLower(all) + " "
	for _, term := range clinicalTerms {
		if strings.Contains(lower, term) {
			return fmt.Errorf("output contains clinical term: %q", term)
		}
	}
	for _, kw := range fofocaKeywords {
		if strings.Contains(lower, kw) {
			return fmt.Errorf("output contains fofoca/off-topic keyword: %q", strings.TrimSpace(kw))
		}
	}

	if o.SafetyAlertNeeded != nil {
		sa := o.SafetyAlertNeeded
		if !validSeverity[sa.Severity] {
			return fmt.Errorf("safety_alert_needed.severity invalido: %q", sa.Severity)
		}
		if !validCategories[sa.Category] {
			return fmt.Errorf("safety_alert_needed.category invalido: %q (use medico_fisico|psicologico|violencia|negligencia|outros)", sa.Category)
		}
		if strings.TrimSpace(sa.Reason) == "" {
			return errors.New("safety_alert_needed.reason vazio")
		}
		if quoteRegex.MatchString(sa.Reason + " " + sa.Recommended) {
			return errors.New("safety_alert_needed contem citacao literal")
		}
	}
	return nil
}

// ValidateReportOutput aplica o contrato do report:
//
//   - Tendencia e NivelPreocupacao no enum.
//   - Resumo nao vazio.
//   - Lengths max nos campos de texto.
//   - Sem aspas literais (privacidade).
//   - Sem termo clinico.
//   - Recomendacoes <=3, cada <=200 ch.
func ValidateReportOutput(o ReportOutput) error {
	if !validTendencia[o.Tendencia] {
		return fmt.Errorf("tendencia invalida: %q", o.Tendencia)
	}
	if !validNivel[o.NivelPreocupacao] {
		return fmt.Errorf("nivel_preocupacao invalido: %q", o.NivelPreocupacao)
	}
	if strings.TrimSpace(o.Resumo) == "" {
		return errors.New("resumo vazio")
	}
	if len(o.Resumo) > 500 {
		return fmt.Errorf("resumo excede 500 ch: %d", len(o.Resumo))
	}
	if len(o.Comparacao) > 200 {
		return fmt.Errorf("comparacao excede 200 ch: %d", len(o.Comparacao))
	}
	if len(o.HumorRecente) > 200 {
		return fmt.Errorf("humor_recente excede 200 ch: %d", len(o.HumorRecente))
	}
	if len(o.PontoDeAtencao) > 200 {
		return fmt.Errorf("ponto_de_atencao excede 200 ch: %d", len(o.PontoDeAtencao))
	}
	if len(o.RecomendacoesCarinhosas) > 3 {
		return fmt.Errorf("recomendacoes_carinhosas excede 3: %d", len(o.RecomendacoesCarinhosas))
	}
	for _, r := range o.RecomendacoesCarinhosas {
		if len(r) > 200 {
			return fmt.Errorf("recomendacao excede 200 ch: %q", truncate(r, 50))
		}
	}

	all := o.Comparacao + " " + o.HumorRecente + " " + o.PontoDeAtencao + " " +
		o.Resumo + " " + strings.Join(o.RecomendacoesCarinhosas, " ")
	if quoteRegex.MatchString(all) {
		return errors.New("output contains literal-looking quotation (privacy violation)")
	}
	lower := " " + strings.ToLower(all) + " "
	for _, term := range clinicalTerms {
		if strings.Contains(lower, term) {
			return fmt.Errorf("output contains clinical term: %q", term)
		}
	}
	return nil
}

// validateScore aceita 0 (= NULL no banco) ate 5.
func validateScore(name string, v int) error {
	if v < 0 || v > 5 {
		return fmt.Errorf("%s fora do range 0-5: %d", name, v)
	}
	return nil
}

// stripFences remove markdown fences (```json...```) que o modelo as vezes
// embrulha em volta do JSON. Tolerante a JSON sem fence.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		// Pula a primeira linha (```json ou ```) e a ultima ```.
		idx := strings.Index(s, "\n")
		if idx >= 0 {
			s = s[idx+1:]
		} else {
			// Sem newline (ex: ```json{...}``` em uma linha) — strip prefix.
			s = strings.TrimPrefix(s, "```json")
			s = strings.TrimPrefix(s, "```")
		}
		if i := strings.LastIndex(s, "```"); i >= 0 {
			s = s[:i]
		}
		s = strings.TrimSpace(s)
	}
	return s
}

// truncate corta a string em n caracteres, anexando "..." se cortou.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
