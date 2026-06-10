package main

import (
	"context"
	"fmt"
	"log"
)

type Orchestrator struct {
	agent         *Agent
	transcription *TranscriptionClient
	db            *DB
}

type ImageAttachment struct {
	Data []byte
	Mime string
}

func NewOrchestrator(agent *Agent, transcription *TranscriptionClient, db *DB) *Orchestrator {
	return &Orchestrator{agent: agent, transcription: transcription, db: db}
}

// ProcessUnknown handles messages from non-registered numbers. Runs the
// acquisition ("sales") agent: it introduces Zello, answers questions, guides
// toward signup, and provisions the account when the person confirms interest.
// pushName eh o nome do perfil WhatsApp — palpite inicial de nome confirmado
// no momento do cadastro.
func (o *Orchestrator) ProcessUnknown(ctx context.Context, senderPhone, pushName, message string) (string, error) {
	response, err := o.agent.RunSalesAgent(ctx, senderPhone, pushName, message)
	if err != nil {
		return "", fmt.Errorf("agent sales: %w", err)
	}
	return response, nil
}

// persistedUserContent é a string EXATA que Process grava em
// conversation_history para um turno do usuário. Compartilhada com
// Agent.Run/runCompanion (dropCurrentTurnFromHistory): como Process persiste
// ANTES de rodar o agente, a última row do histórico é o próprio turno atual
// e precisa ser reconhecida byte a byte para não entrar duplicada no contexto.
func persistedUserContent(message string, imageCount int) string {
	if message != "" {
		return message
	}
	if imageCount > 1 {
		return fmt.Sprintf("[%d imagens enviadas]", imageCount)
	}
	if imageCount == 1 {
		return "[imagem enviada]"
	}
	return ""
}

func (o *Orchestrator) Process(ctx context.Context, user *User, message string, images []ImageAttachment) (string, error) {
	// Save user message to history (persist-antes-de-Run: o snippet de
	// auditoria de handleCriarEvento lê o histórico e depende disso).
	if content := persistedUserContent(message, len(images)); content != "" {
		o.db.AddConversationMessage(user.ID, "user", content)
	}

	// Run agent
	response, err := o.agent.Run(ctx, user, message, images)
	if err != nil {
		log.Printf("[%s] Agent error: %v", user.Name, err)
		return "", fmt.Errorf("agent: %w", err)
	}

	return response, nil
}
