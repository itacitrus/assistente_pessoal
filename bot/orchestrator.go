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

// ProcessUnknown handles messages from non-registered users.
// Acts like a polite messenger — answers briefly, clarifies doubts about
// a delivered message, but doesn't engage in long conversations or take requests.
func (o *Orchestrator) ProcessUnknown(ctx context.Context, senderPhone, message string) (string, error) {
	response, err := o.agent.RunForUnknown(ctx, senderPhone, message)
	if err != nil {
		return "", fmt.Errorf("agent unknown: %w", err)
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

