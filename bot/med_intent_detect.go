package main

import "regexp"

// =========================================================================
// Detector deterministico de "tomei" (salvaguarda independente do LLM)
// =========================================================================
//
// PROBLEMA QUE RESOLVE: a confirmacao de dose ("tomei") so vira registro se o
// modelo do companheiro (DeepSeek em prod) emitir o tool_use marcar_remedio_tomado.
// Quando o modelo narra "Anotado" SEM chamar a tool, a dose continua 'pending', o
// motor de escalacao cutuca de novo, e o idoso ve "o agente ignorou meu tomei".
//
// Este detector NAO substitui o LLM — ele eh a rede de seguranca: roda DEPOIS do
// turno do LLM e, so quando ha confirmacao INEQUIVOCA E o LLM nao agiu, persiste a
// tomada pelos mesmos helpers de marcar_remedio_tomado (idempotentes). Filosofia
// "acreditar no usuario": se ele disse que tomou, a dose tem que ser registrada.
//
// PRINCIPIO NEGACAO-PRIMEIRO: o maior risco eh marcar como tomada uma dose que o
// idoso disse que vai tomar ("vou tomar mais tarde") ou que NAO tomou ("ainda nao
// tomei"). Por isso qualquer sinal de adiamento/negacao DERRUBA o veredito de
// tomada — preferimos o falso-negativo (nao agir, deixar o fluxo normal) ao
// falso-positivo (marcar tomada indevida).

// TakenIntent eh o veredito do detector sobre a fala do idoso.
type TakenIntent int

const (
	// IntentNone: a fala nao indica tomada (nem adiamento claro) — salvaguarda
	// fica inerte.
	IntentNone TakenIntent = iota
	// IntentTaken: afirmacao inequivoca de que JA tomou/aplicou.
	IntentTaken
	// IntentDeferred: vai tomar depois OU disse que ainda nao tomou — NUNCA
	// marca como tomada.
	IntentDeferred
)

// deferralRe casa adiamento ("vou tomar", "mais tarde") e negacao explicita de
// tomada ("ainda nao tomei", "nao tomei", "esqueci"). Avaliado ANTES de takenRe;
// qualquer match aqui veta a tomada. Strings sem acento (comparadas contra
// foldAccentsLower): "nao" cobre "não", "ja" cobre "já".
var deferralRe = regexp.MustCompile(`\b(` +
	`vou tomar|vou aplicar|vou passar|vou beber|` +
	`ainda vou|ainda nao|inda nao|` +
	`nao tomei|nem tomei|nao apliquei|` +
	`nao tomo|nao vou tomar|` +
	`mais tarde|daqui a pouco|daqui ha pouco|daqui pouco|` +
	`logo mais|mais pra frente|deixa pra depois|depois eu tomo|` +
	`esqueci de tomar|esqueci o|esqueci a|` +
	`to indo tomar|vou la tomar|ja ja tomo|ja ja eu tomo` +
	`)\b`)

// socialTakenRe casa "tomei X" onde X eh claramente social (cafe, banho, sol...)
// — derruba o falso-positivo de "tomei" generico quando o objeto nao eh remedio.
// So atua quando NAO ha termo forte de remedio na fala (o caller cuida disso via
// medContextActive antes de chamar; aqui eh defesa extra).
var socialTakenRe = regexp.MustCompile(`\btomei\s+(um |uma |o |a )?(` +
	`cafe|cafezinho|banho|sol|agua|suco|cha|chimarrao|mate|` +
	`cerveja|vinho|leite|sorvete|refrigerante|refri|drink|chopp|whisky|cachaca|` +
	`susto|tombo|cuidado|jeito|nota|conta|decisao|partido` +
	`)\b`)

// takenRe casa afirmacoes de tomada/aplicacao ja consumadas. Cobre comprimido,
// liquido e topico (pomada). "ja tomei as 7h" casa via "tomei". Inclui confirmacoes
// TERSAS comuns de idoso em resposta a lembrete ("pronto", "feito", "ja foi") — sao
// seguras porque a salvaguarda so age quando ha dose pendente/recente em aberto
// (reconcileTakenDeterministic vira no-op sem nada a reconciliar). Adiamento/negacao
// tem precedencia (deferralRe roda antes): "pronto, vou tomar" -> Deferred.
var takenRe = regexp.MustCompile(`\b(` +
	`tomei|tomadas|tomados|tomada|tomado|` +
	`apliquei|aplicado|aplicada|apliquei a|passei a pomada|passei o creme|` +
	`ja bebi|bebi o|bebi a|engoli|` +
	`acabei de tomar|acabei de aplicar|acabei de beber|` +
	`pronto|prontinho|ja foi|ja era|feito|ja fiz` +
	`)\b`)

// classifyTakenIntent decide, de forma puramente deterministica, se a fala do
// idoso eh uma confirmacao de tomada. Negacao/adiamento tem precedencia. A
// comparacao usa foldAccentsLower (minusculo + sem acento), entao os padroes
// acima sao escritos sem acento.
//
// IMPORTANTE: este detector eh CONSERVADOR de proposito. Ele so deve disparar a
// salvaguarda quando ha confirmacao clara; na duvida retorna IntentNone e deixa
// o fluxo normal (LLM) decidir. Nunca deve transformar "vou tomar" em tomada.
func classifyTakenIntent(message string) TakenIntent {
	low := foldAccentsLower(message)
	if deferralRe.MatchString(low) {
		return IntentDeferred
	}
	if !takenRe.MatchString(low) {
		return IntentNone
	}
	// Tem termo de tomada. So derruba se for "tomei <objeto social>" E o unico
	// gatilho for o "tomei" generico (sem aplicar/pomada/engoli etc.).
	if socialTakenRe.MatchString(low) && !hasNonSocialTakenSignal(low) {
		return IntentNone
	}
	return IntentTaken
}

// hasNonSocialTakenSignal retorna true se ha sinal de tomada de remedio alem do
// "tomei" generico (que pode ser social). Aplicacao topica, "engoli", "comprimido"
// etc. nunca sao sociais.
func hasNonSocialTakenSignal(low string) bool {
	return regexp.MustCompile(`\b(apliquei|aplicado|aplicada|pomada|creme|engoli|comprimido|capsula|remedio|dose|gota|injec)\b`).MatchString(low)
}
