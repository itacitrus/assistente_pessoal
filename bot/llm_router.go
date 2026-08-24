package main

import (
	"log"

	"github.com/giovannirambo/assistente_pessoal/bot/llm"
)

// hasHealthSurface responde "este turno PODERIA tocar dado de saude?" — nao
// "tocou?". Nao da pra saber qual tool o modelo vai escolher antes de ele
// escolher, entao o criterio e conservador de proposito.
//
// Roteia por USUARIO e nao por tipo de agente porque o agente operacional
// TAMBEM expoe as tools de medicacao (cadastrar_medicamento,
// extrair_receita_imagem, buscar_aderencia): um filho adulto cadastrando o
// remedio da mae carrega posologia pelo caminho operacional. Rotear por
// agente daria falsa sensacao de separacao — pior que nao separar.
//
// Tres criterios, do mais barato ao mais caro:
//  1. e idoso (o proprio perfil e clinico);
//  2. tem medicacao ativa propria;
//  3. e responsavel por alguem (family_links) — pode consultar adesao,
//     cadastrar remedio e receber alerta de dose perdida do dependente.
func (a *Agent) hasHealthSurface(user *User) bool {
	if user == nil {
		return false
	}
	if user.Type == UserTypeIdoso {
		return true
	}
	if a.db == nil {
		return false
	}
	if meds, err := a.db.ListActiveMedications(user.ID); err == nil && len(meds) > 0 {
		return true
	}
	if deps, err := a.db.GetDependents(user.ID); err == nil && len(deps) > 0 {
		return true
	}
	return false
}

// chatFor devolve o ChatProvider do turno.
//
// Substitui pickChat, que roteava so por user.Type e era codigo morto (nunca
// chamado fora de teste). Fallback: sem provider clinico configurado, o
// usuario com superficie de saude cai no operacional em vez de quebrar o
// turno — degradar e melhor que nao responder a um idoso.
func (a *Agent) chatFor(user *User) llm.ChatProvider {
	if a.hasHealthSurface(user) && a.companionChat != nil {
		return a.companionChat
	}
	return a.chat
}

// engineNameFor da o rotulo de engine usado pelo output-guard, pelo audit e
// pela contabilidade de token. Independente do provider escolhido: descreve
// QUAL agente falou, nao para onde foi a chamada.
func (a *Agent) engineNameFor(user *User) string {
	if user != nil && user.Type == UserTypeIdoso {
		return "companion"
	}
	return "operational"
}

// recordUsage grava o consumo de um turno. Falha de contabilidade so loga —
// nunca derruba o turno do usuario.
func (a *Agent) recordUsage(user *User, engine, provider, model string, u llm.Usage, latencyMS int64) {
	if a.db == nil {
		return
	}
	var uid int64
	if user != nil {
		uid = user.ID
	}
	err := a.db.RecordLLMUsage(LLMUsageRow{
		UserID: uid, Engine: engine, Provider: provider, Model: model,
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
		CacheReadTokens: u.CacheReadTokens, LatencyMS: latencyMS,
	})
	if err != nil {
		log.Printf("llm_usage: %v", err)
	}
}
