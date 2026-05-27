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

func (o *Orchestrator) Process(ctx context.Context, user *User, message string, images []ImageAttachment) (string, error) {
	// Save user message to history
	if message != "" {
		o.db.AddConversationMessage(user.ID, "user", message)
	} else if len(images) > 0 {
		marker := "[imagem enviada]"
		if len(images) > 1 {
			marker = fmt.Sprintf("[%d imagens enviadas]", len(images))
		}
		o.db.AddConversationMessage(user.ID, "user", marker)
	}

	// Run agent
	response, err := o.agent.Run(ctx, user, message, images)
	if err != nil {
		log.Printf("[%s] Agent error: %v", user.Name, err)
		return "", fmt.Errorf("agent: %w", err)
	}

	return response, nil
}
