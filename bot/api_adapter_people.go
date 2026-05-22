package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/giovannirambo/assistente_pessoal/bot/api"
)

// =========================================================================
// Adapter: pessoas na vida (CRUD manual) — /api/v1/me/people
// =========================================================================
//
// Curadoria manual do que o Zello "sabe" sobre as pessoas do usuario. Grava na
// MESMA tabela user_memories que o bot alimenta via salvar_memoria, entao o
// que o usuario cadastra aqui o assistente passa a conhecer nas conversas.
//
// Mapa tipo (UI) -> categoria (memoria):
//   "relacao" -> "relacao"        (familia/proximos; renderiza em Relations)
//   "pessoa"  -> "social_context" (contexto social; renderiza em People)
//
// "contato" eh uma categoria legada que o card tambem exibe como Pessoa.
// Ao editar uma entrada "contato" mantida como tipo "pessoa", preservamos a
// categoria original (mesmo "balde") em vez de migrar pra social_context.

// canonicalPersonCategory devolve a categoria canonica de gravacao pro tipo.
func canonicalPersonCategory(t api.PersonFactType) (string, error) {
	switch t {
	case api.PersonFactTypeRelacao:
		return "relacao", nil
	case api.PersonFactTypePessoa:
		return "social_context", nil
	default:
		return "", fmt.Errorf("%w: tipo invalido", api.ErrValidation)
	}
}

// personBucket agrupa categorias por "balde" de exibicao: relacoes vs pessoas.
// Editar dentro do mesmo balde preserva a categoria original.
func personBucket(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "relacao":
		return "relacao"
	case "contato", "social_context":
		return "pessoa"
	default:
		return ""
	}
}

// resolveTargetCategory decide a categoria final ao editar: se o tipo continua
// no balde "pessoa" e a original ja era "contato"/"social_context", preserva a
// original (nao migra contato -> social_context atoa). Caso contrario, usa a
// canonica do tipo.
func resolveTargetCategory(t api.PersonFactType, originalCategory string) (string, error) {
	canonical, err := canonicalPersonCategory(t)
	if err != nil {
		return "", err
	}
	if personBucket(canonical) == personBucket(originalCategory) && originalCategory != "" {
		return originalCategory, nil
	}
	return canonical, nil
}

// CreatePersonFact grava uma nova pessoa/relacao. ErrConflict se ja existir.
func (a *apiAdapter) CreatePersonFact(ctx context.Context, userID int64, in api.PersonFactRequest) error {
	category, err := canonicalPersonCategory(in.Type)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(in.Name)
	value := strings.TrimSpace(in.Detail)

	exists, err := a.db.MemoryExists(userID, category, key)
	if err != nil {
		return err
	}
	if exists {
		return api.ErrConflict
	}
	if err := a.db.SaveMemory(userID, category, key, value); err != nil {
		return err
	}
	if a.audit != nil {
		_ = a.audit.Log(userID, "person_fact_created", key, "category="+category)
	}
	return nil
}

// UpdatePersonFact edita uma pessoa/relacao existente. Quando muda a key
// (nome) ou a category (tipo), remove a antiga e cria a nova atomicamente.
func (a *apiAdapter) UpdatePersonFact(ctx context.Context, userID int64, in api.PersonFactRequest) error {
	origCategory := strings.TrimSpace(in.OriginalCategory)
	origKey := strings.TrimSpace(in.OriginalKey)
	if origCategory == "" || origKey == "" {
		return fmt.Errorf("%w: identificador da entrada ausente", api.ErrValidation)
	}
	if personBucket(origCategory) == "" {
		return fmt.Errorf("%w: categoria nao editavel", api.ErrValidation)
	}

	existsOrig, err := a.db.MemoryExists(userID, origCategory, origKey)
	if err != nil {
		return err
	}
	if !existsOrig {
		return api.ErrNotFound
	}

	newCategory, err := resolveTargetCategory(in.Type, origCategory)
	if err != nil {
		return err
	}
	newKey := strings.TrimSpace(in.Name)
	value := strings.TrimSpace(in.Detail)

	// Mesma identidade: simples upsert do valor.
	if newCategory == origCategory && newKey == origKey {
		if err := a.db.SaveMemory(userID, newCategory, newKey, value); err != nil {
			return err
		}
		if a.audit != nil {
			_ = a.audit.Log(userID, "person_fact_updated", newKey, "category="+newCategory)
		}
		return nil
	}

	// Identidade mudou: o destino nao pode colidir com outra entrada.
	existsTarget, err := a.db.MemoryExists(userID, newCategory, newKey)
	if err != nil {
		return err
	}
	if existsTarget {
		return api.ErrConflict
	}
	if err := a.db.SaveMemory(userID, newCategory, newKey, value); err != nil {
		return err
	}
	if err := a.db.DeleteMemory(userID, origCategory, origKey); err != nil {
		return err
	}
	if a.audit != nil {
		_ = a.audit.Log(userID, "person_fact_updated", newKey,
			fmt.Sprintf("category=%s|renamed_from=%s/%s", newCategory, origCategory, origKey))
	}
	return nil
}

// DeletePersonFact remove a memoria (category, key). So categorias do card.
func (a *apiAdapter) DeletePersonFact(ctx context.Context, userID int64, category, key string) error {
	category = strings.TrimSpace(category)
	key = strings.TrimSpace(key)
	if personBucket(category) == "" {
		return fmt.Errorf("%w: categoria nao editavel", api.ErrValidation)
	}
	if key == "" {
		return fmt.Errorf("%w: chave ausente", api.ErrValidation)
	}
	if err := a.db.DeleteMemory(userID, category, key); err != nil {
		return err
	}
	if a.audit != nil {
		_ = a.audit.Log(userID, "person_fact_deleted", key, "category="+category)
	}
	return nil
}
