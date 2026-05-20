# Fase 1 — Modelagem de usuários e família

**Data:** 2026-05-09
**Status:** Pronto pra implementar
**Predecessor:** `00-overview.md` (contrato arquitetural)
**Sucessor:** `02-ui-cadastro.md`, `03-medicamentos.md`, `04-companion.md`, `05-relatorio-alertas.md`

---

## 1. Objetivo e não-objetivos

### Objetivo

Criar a fundação de schema, tipos Go e helpers de banco que sustentam o conceito
de **tipo de usuário** (`comum` | `idoso` | `responsavel`) e **vínculo familiar**
(`family_links`) sem alterar nenhum comportamento atual do bot. Toda a Fase 1 é
exclusivamente backend/dados — não toca em system prompt, em tools de Claude,
nem em scheduler. Ao mergear esta fase, qualquer caminho de código existente
deve seguir funcionando idêntico (todos os usuários atuais ficam com `type =
'comum'` e `last_user_message_at IS NULL`).

### Não-objetivos

- **Não** alterar persona/system prompt (Fase 4).
- **Não** criar tools `registrar_familia` / `status_dependente` (Fases 3 e 5).
- **Não** mexer em `calendar_permissions`. `family_links` é tabela paralela.
- **Não** unificar permissões de calendário com permissões familiares: são
  abstrações diferentes (peer-to-peer agenda vs. tutela com preferências de
  notificação).
- **Não** adicionar UI nem endpoints REST (Fase 2).
- **Não** implementar soft-delete em `family_links` — remoção é hard delete
  (justificativa em §9).

---

## 2. DDL completo

### 2.1 Bloco SQL canônico

Este é o estado final desejado do schema relevante após esta fase. As migrations
em §3 chegam neste estado de forma idempotente (incluindo bancos de produção
existentes). O bloco abaixo serve como referência de verdade — não é executado
diretamente.

```sql
-- Coluna nova em users: tipo (comum | idoso | responsavel).
-- Default 'comum' garante que todos os usuários existentes mantenham
-- comportamento atual sem backfill explicito.
ALTER TABLE users
    ADD COLUMN type TEXT NOT NULL DEFAULT 'comum'
    CHECK (type IN ('comum', 'idoso', 'responsavel'));

-- Coluna nova em users: timestamp da ultima mensagem RECEBIDA do usuario
-- (nao do bot). Usada na Fase 4 (`checkInactivity`).
-- NULL = ainda nao recebemos nenhuma mensagem do usuario nesta versao.
ALTER TABLE users
    ADD COLUMN last_user_message_at DATETIME;

-- Vinculo familiar guardian -> dependent.
-- guardian_id eh o responsavel (recebe alertas).
-- dependent_id eh quem esta sob cuidado (tipicamente type='idoso',
-- mas o schema NAO impoe — flexibilidade pra futuros casos: criancas,
-- pessoas com deficiencia, etc).
-- Notify flags: granularidade por canal de alerta. Default true em todos.
CREATE TABLE family_links (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    guardian_id                 INTEGER NOT NULL REFERENCES users(id),
    dependent_id                INTEGER NOT NULL REFERENCES users(id),
    relationship                TEXT NOT NULL DEFAULT '',
    notify_on_medication_miss   INTEGER NOT NULL DEFAULT 1,
    notify_on_inactivity        INTEGER NOT NULL DEFAULT 1,
    notify_on_severe_signal     INTEGER NOT NULL DEFAULT 1,
    created_at                  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(guardian_id, dependent_id),
    CHECK (guardian_id != dependent_id)
);

-- Indices: lookups frequentes no scheduler (Fase 3/4) sao por guardian
-- (quem alertar) e por dependent (quem esta sendo monitorado).
CREATE INDEX idx_family_links_guardian   ON family_links(guardian_id);
CREATE INDEX idx_family_links_dependent  ON family_links(dependent_id);

-- Indice em users.type pra lookups frequentes do scheduler em Fase 3/4
-- ("liste todos type='idoso' ativos").
CREATE INDEX idx_users_type ON users(type);
```

### 2.2 Notas de design SQL

- **Booleans como INTEGER (0/1):** padrão do projeto (ver `users.is_active`).
  SQLite não tem BOOLEAN nativo. `Scan` em Go usa `bool` direto sem cast porque
  `modernc.org/sqlite` faz a conversão.
- **`relationship TEXT NOT NULL DEFAULT ''`:** campo livre. Sem CHECK em
  vocabulário fechado — usuário pode digitar "filha", "esposa", "mãe", "neto",
  "amiga". Útil pra UX no relatório ("seu pai não tomou remédio") sem amarrar
  taxonomia agora.
- **CHECK `guardian_id != dependent_id`:** previne self-link a nível de banco
  (defense in depth — também validamos em Go).
- **`UNIQUE(guardian_id, dependent_id)`:** mesmo guardian não pode vincular o
  mesmo dependente duas vezes. Mas: A → B e B → A são linhas distintas e ambas
  permitidas (relação não é simétrica e a UI da Fase 2 deixa explícita a
  direção).
- **Sem `ON DELETE CASCADE`:** o projeto não usa cascade em FKs (ver tabelas
  existentes). Mantemos consistência. Se um usuário for removido no futuro
  (cenário não previsto hoje), uma rotina dedicada de cleanup vai cuidar dos
  vínculos.
- **Sem coluna `updated_at` em `family_links`:** o único campo mutável são as
  flags de notificação, e Fase 1 já loga toda mudança via audit. Se virar
  necessário no futuro, é um additive migration.

---

## 3. Migrations idempotentes em Go

A função `migrate()` em `bot/db.go:65` segue dois padrões:

1. **Schema base** dentro de uma string SQL com `CREATE TABLE IF NOT EXISTS` —
   roda em qualquer banco, incluindo vazio.
2. **Migrations aditivas** num `[]string`, com tratamento de erro
   `duplicate column` ignorado pra idempotência (`bot/db.go:171`).

Esta fase adiciona:

- 2 ALTERs em `users` (vão na lista aditiva — bancos antigos não têm essas
  colunas).
- 1 CREATE TABLE pra `family_links` (vai no schema base, com `IF NOT EXISTS`).
- 3 CREATE INDEX (vão no schema base, com `IF NOT EXISTS`).

### 3.1 Patch em `bot/db.go`

#### 3.1.1 Adicionar `family_links` e índices ao schema base

No bloco `schema := \`...\`` em `migrate()` (logo após o bloco
`user_travel_periods` que termina na linha 152), inserir:

```go
CREATE TABLE IF NOT EXISTS family_links (
    id                        INTEGER PRIMARY KEY AUTOINCREMENT,
    guardian_id               INTEGER NOT NULL REFERENCES users(id),
    dependent_id              INTEGER NOT NULL REFERENCES users(id),
    relationship              TEXT NOT NULL DEFAULT '',
    notify_on_medication_miss INTEGER NOT NULL DEFAULT 1,
    notify_on_inactivity      INTEGER NOT NULL DEFAULT 1,
    notify_on_severe_signal   INTEGER NOT NULL DEFAULT 1,
    created_at                DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(guardian_id, dependent_id),
    CHECK (guardian_id != dependent_id)
);
CREATE INDEX IF NOT EXISTS idx_family_links_guardian   ON family_links(guardian_id);
CREATE INDEX IF NOT EXISTS idx_family_links_dependent  ON family_links(dependent_id);
CREATE INDEX IF NOT EXISTS idx_users_type              ON users(type);
```

> **Por quê o índice em `users(type)` está no schema base e não numa migration
> aditiva?** `CREATE INDEX IF NOT EXISTS` é idempotente nativamente; só precisa
> rodar depois que a coluna existir. Como as ALTERs aditivas são executadas
> ANTES desse bloco no fluxo atual? **Não são** — o `db.conn.Exec(schema)` roda
> primeiro (linha 154) e o loop aditivo depois (linha 170). Então em banco
> antigo, na primeira inicialização pós-deploy, o `CREATE INDEX ON users(type)`
> falharia porque a coluna ainda não existe.
>
> **Solução:** inverter — criar o índice **depois** das migrations aditivas, num
> segundo bloco SQL. Detalhe na §3.1.3.

#### 3.1.2 Adicionar ALTER TABLEs aditivos

Estender o slice `additive` em `migrate()` (atualmente linha 160):

```go
additive := []string{
    // ... statements existentes (calendar_event_id, reauth_notified_at) ...

    // type discrimina persona do usuario. Default 'comum' preserva
    // comportamento atual pra todos os usuarios pre-existentes.
    // CHECK constraint feito via DEFAULT + validacao em Go (ver SetUserType).
    // SQLite NAO permite ADD COLUMN com CHECK referenciando a propria coluna
    // de forma confiavel em todas as versoes; aplicamos CHECK no banco novo
    // via CREATE TABLE no schema base de bancos limpos seria ideal, mas
    // como users ja existe, o CHECK fica em Go.
    `ALTER TABLE users ADD COLUMN type TEXT NOT NULL DEFAULT 'comum'`,

    // last_user_message_at registra o timestamp da ultima mensagem
    // RECEBIDA do usuario (nao do bot). Usado na Fase 4 (checkInactivity).
    // NULL = nunca recebemos mensagem do usuario nesta versao do bot.
    `ALTER TABLE users ADD COLUMN last_user_message_at DATETIME`,
}
```

> **Sobre o CHECK constraint na coluna `type`:** SQLite >= 3.25 suporta CHECK
> em ALTER TABLE ADD COLUMN, e `modernc.org/sqlite` embarca SQLite recente.
> Mesmo assim, dependemos do banco de prod — adotar a estratégia conservadora
> de validar em Go (em `SetUserType`) e em qualquer outro caller via
> `ValidateUserType`. **Não** colocamos o CHECK no SQL pra evitar comportamento
> diferente entre banco recém-criado (que receberia o CHECK no schema base) e
> banco migrado (que receberia ALTER sem CHECK). Consistência > defense in
> depth aqui.

#### 3.1.3 Bloco pós-aditivo (índice em coluna nova)

Logo após o loop `for _, stmt := range additive { ... }` em `migrate()`,
adicionar:

```go
// Indices que dependem de colunas adicionadas via migracao aditiva.
// Rodam DEPOIS do loop additive pra garantir que a coluna existe.
postAdditive := `
    CREATE INDEX IF NOT EXISTS idx_users_type ON users(type);
`
if _, err := db.conn.Exec(postAdditive); err != nil {
    return fmt.Errorf("post-additive migration: %w", err)
}
```

E **remover** o `CREATE INDEX IF NOT EXISTS idx_users_type ON users(type);` do
bloco `schema` em §3.1.1 — fica só no `postAdditive`.

#### 3.1.4 Migrate final, ordenado

Estado final do método `migrate()` (recorte do trecho relevante, mostrando a
ordem das três fases):

```go
func (db *DB) migrate() error {
    schema := `
        CREATE TABLE IF NOT EXISTS users (...);  // existente, sem mudanca
        CREATE TABLE IF NOT EXISTS pending_confirmations (...);
        CREATE TABLE IF NOT EXISTS sent_reminders (...);
        CREATE TABLE IF NOT EXISTS action_log (...);
        CREATE TABLE IF NOT EXISTS calendar_permissions (...);
        CREATE TABLE IF NOT EXISTS pending_permission_requests (...);
        CREATE TABLE IF NOT EXISTS user_memories (...);
        CREATE TABLE IF NOT EXISTS conversation_history (...);
        CREATE TABLE IF NOT EXISTS user_travel_periods (...);
        CREATE INDEX IF NOT EXISTS idx_user_travel_periods_user_date ...;

        -- NOVO em Fase 1:
        CREATE TABLE IF NOT EXISTS family_links (
            id                        INTEGER PRIMARY KEY AUTOINCREMENT,
            guardian_id               INTEGER NOT NULL REFERENCES users(id),
            dependent_id              INTEGER NOT NULL REFERENCES users(id),
            relationship              TEXT NOT NULL DEFAULT '',
            notify_on_medication_miss INTEGER NOT NULL DEFAULT 1,
            notify_on_inactivity      INTEGER NOT NULL DEFAULT 1,
            notify_on_severe_signal   INTEGER NOT NULL DEFAULT 1,
            created_at                DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            UNIQUE(guardian_id, dependent_id),
            CHECK (guardian_id != dependent_id)
        );
        CREATE INDEX IF NOT EXISTS idx_family_links_guardian  ON family_links(guardian_id);
        CREATE INDEX IF NOT EXISTS idx_family_links_dependent ON family_links(dependent_id);
    `
    if _, err := db.conn.Exec(schema); err != nil {
        return err
    }

    additive := []string{
        `ALTER TABLE user_travel_periods ADD COLUMN calendar_event_id TEXT NOT NULL DEFAULT ''`,
        `ALTER TABLE users ADD COLUMN reauth_notified_at DATETIME`,
        // NOVO em Fase 1:
        `ALTER TABLE users ADD COLUMN type TEXT NOT NULL DEFAULT 'comum'`,
        `ALTER TABLE users ADD COLUMN last_user_message_at DATETIME`,
    }
    for _, stmt := range additive {
        if _, err := db.conn.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
            return fmt.Errorf("additive migration %q: %w", stmt, err)
        }
    }

    // NOVO em Fase 1: indices que dependem de colunas aditivas.
    postAdditive := `CREATE INDEX IF NOT EXISTS idx_users_type ON users(type);`
    if _, err := db.conn.Exec(postAdditive); err != nil {
        return fmt.Errorf("post-additive migration: %w", err)
    }
    return nil
}
```

### 3.2 Idempotência verificada

- Banco vazio (primeiro deploy): `schema` cria tudo, `additive` falha em
  `duplicate column` (já criado por `schema`?) — **não**, `users` tem só as
  colunas legadas no `schema`, então as ALTERs rodam limpas.
  > **Verificar:** as colunas `type` e `last_user_message_at` **não** estão na
  > definição de `users` dentro do `schema` (`bot/db.go:67-80`). Permanecem
  > apenas no slice `additive`. Isso garante que banco novo passa pelas mesmas
  > ALTERs que banco antigo — caminho único.
- Banco migrado uma vez: `schema` é no-op (`IF NOT EXISTS`), `additive` retorna
  erro `duplicate column` que é silenciado, `postAdditive` é no-op.
- Banco a meio do caminho (improvável, mas possível): cada statement é
  individualmente idempotente. Sem ordem implícita problemática.

---

## 4. Tipos Go novos

Local recomendado: novo arquivo `bot/family.go` (mantém `db.go` focado no que
já tem). Os tipos abaixo são `package main` (todo o bot é um único pacote
hoje).

```go
package main

import (
    "fmt"
    "time"
)

// UserType discrimina a persona do usuario. Persona dinamica via system
// prompt switch (ver overview D1). Tipos NAO sao mutuamente exclusivos no
// nivel conceitual — um responsavel tambem tem agenda propria — mas a coluna
// users.type carrega a persona PRIMARIA pra fins de prompt e scheduler.
// Pra responsabilidade familiar, usar family_links (tabela paralela).
type UserType string

const (
    UserTypeComum       UserType = "comum"
    UserTypeIdoso       UserType = "idoso"
    UserTypeResponsavel UserType = "responsavel"
)

// allUserTypes eh a lista canonica de tipos validos. Mantido em sync com
// o CHECK constraint conceitual e com ValidateUserType.
var allUserTypes = []UserType{
    UserTypeComum,
    UserTypeIdoso,
    UserTypeResponsavel,
}

// IsValid retorna true se ut for um dos tipos reconhecidos.
func (ut UserType) IsValid() bool {
    for _, v := range allUserTypes {
        if v == ut {
            return true
        }
    }
    return false
}

// ValidateUserType retorna erro descritivo se t nao for um tipo conhecido.
// Usar antes de qualquer write em users.type.
func ValidateUserType(t UserType) error {
    if !t.IsValid() {
        return fmt.Errorf("invalid user type %q (valid: comum, idoso, responsavel)", t)
    }
    return nil
}

// FamilyNotifyPrefs encapsula as flags de notificacao de um vinculo familiar.
// Default semantico (zero-value!): todas false. CUIDADO: o default DO BANCO
// eh true — quem usa este struct para WRITE tem que setar explicitamente.
// Pra construir com defaults do banco, use DefaultFamilyNotifyPrefs().
type FamilyNotifyPrefs struct {
    OnMedicationMiss bool `json:"on_medication_miss"`
    OnInactivity     bool `json:"on_inactivity"`
    OnSevereSignal   bool `json:"on_severe_signal"`
}

// DefaultFamilyNotifyPrefs retorna prefs com todos os canais ligados,
// igualando o DEFAULT do schema.
func DefaultFamilyNotifyPrefs() FamilyNotifyPrefs {
    return FamilyNotifyPrefs{
        OnMedicationMiss: true,
        OnInactivity:     true,
        OnSevereSignal:   true,
    }
}

// FamilyLink representa um vinculo guardian -> dependent.
// Quando retornado por GetDependents/GetGuardians, o campo Other carrega o
// usuario do "outro lado" do vinculo (o dependente, ou o guardiao, conforme
// a query). Isso evita o caller fazer N+1 lookups por nome/telefone.
type FamilyLink struct {
    ID            int64             `json:"id"`
    GuardianID    int64             `json:"guardian_id"`
    DependentID   int64             `json:"dependent_id"`
    Relationship  string            `json:"relationship"`
    Notify        FamilyNotifyPrefs `json:"notify"`
    CreatedAt     time.Time         `json:"created_at"`

    // Other eh o usuario do outro lado do vinculo, populado pelos getters
    // de listagem (GetDependents/GetGuardians). Nao populado por
    // IsGuardianOf, LinkFamily etc. Pode ser nil em contextos onde nao foi
    // hidratado.
    Other *User `json:"other,omitempty"`
}
```

### 4.1 Sentinel errors

Adicionar a `bot/db.go` (junto dos `Err...` existentes em linhas 13-16) ou
declarar em `bot/family.go`. Recomendado: `bot/family.go` pra coesão.

```go
// Em bot/family.go:
var (
    // ErrFamilyLinkNotFound indica que o par (guardian, dependent) nao existe.
    ErrFamilyLinkNotFound = errors.New("family link not found")

    // ErrFamilyLinkSelfLink eh retornado quando guardian_id == dependent_id.
    ErrFamilyLinkSelfLink = errors.New("family link cannot be self-referential")

    // ErrFamilyLinkDuplicate indica violacao do UNIQUE(guardian_id, dependent_id).
    ErrFamilyLinkDuplicate = errors.New("family link already exists")

    // ErrFamilyLinkUserNotFound indica que guardian_id ou dependent_id
    // nao referencia um user existente (defesa em profundidade — SQLite nao
    // valida FKs por default).
    ErrFamilyLinkUserNotFound = errors.New("family link references non-existent user")
)
```

> **Nota sobre FKs no SQLite:** o projeto não habilita `PRAGMA foreign_keys =
> ON` (verificável em `bot/db.go:48`). Logo, FKs declaradas (`REFERENCES
> users(id)`) **não são enforced** pelo banco. `LinkFamily` valida em Go via
> `db.GetUserByID` antes de inserir. Habilitar `foreign_keys = ON` é uma
> mudança maior (pode quebrar comportamento existente em outras tabelas) — fora
> de escopo desta fase.

---

## 5. Assinaturas exatas dos helpers

Todas as funções abaixo são métodos de `*DB` (mesmo padrão dos helpers
existentes em `bot/db.go`), declarados em `bot/family.go`.

### 5.1 `SetUserType`

```go
// SetUserType atualiza users.type apos validar o valor. Retorna
// ErrUserNotFound se o id nao existe.
func (db *DB) SetUserType(userID int64, t UserType) error
```

**Implementação:**

```go
func (db *DB) SetUserType(userID int64, t UserType) error {
    if err := ValidateUserType(t); err != nil {
        return err
    }
    res, err := db.conn.Exec(`UPDATE users SET type = ? WHERE id = ?`, string(t), userID)
    if err != nil {
        return fmt.Errorf("set user type: %w", err)
    }
    n, err := res.RowsAffected()
    if err != nil {
        return err
    }
    if n == 0 {
        return ErrUserNotFound
    }
    return nil
}
```

**Exemplo:**

```go
if err := db.SetUserType(user.ID, UserTypeIdoso); err != nil {
    return fmt.Errorf("marcar usuario como idoso: %w", err)
}
```

### 5.2 `MarkUserMessageReceived`

```go
// MarkUserMessageReceived registra o timestamp da ultima mensagem RECEBIDA
// do usuario (chamada do handler de WhatsApp). Usado por checkInactivity
// na Fase 4. Sempre persiste em UTC.
func (db *DB) MarkUserMessageReceived(userID int64, ts time.Time) error
```

**Implementação:**

```go
func (db *DB) MarkUserMessageReceived(userID int64, ts time.Time) error {
    res, err := db.conn.Exec(
        `UPDATE users SET last_user_message_at = ? WHERE id = ?`,
        ts.UTC(), userID,
    )
    if err != nil {
        return fmt.Errorf("mark user message received: %w", err)
    }
    n, err := res.RowsAffected()
    if err != nil {
        return err
    }
    if n == 0 {
        return ErrUserNotFound
    }
    return nil
}
```

**Exemplo:**

```go
// Em handler.go, no inicio de cada handler de mensagem do usuario:
_ = db.MarkUserMessageReceived(user.ID, time.Now())
```

> **Por que não bloquear erro aqui?** O caller é hot path do handler de
> WhatsApp; falhar uma mensagem inteira porque um update de timestamp falhou é
> fragilidade desnecessária. Erros são logados pelo caller (Fase 4 implementa o
> wiring; nesta fase só entregamos o helper).

### 5.3 `LinkFamily`

```go
// LinkFamily cria um vinculo guardian -> dependent. Retorna:
//   - ErrFamilyLinkSelfLink     se guardianID == dependentID
//   - ErrFamilyLinkUserNotFound se algum dos ids nao existe
//   - ErrFamilyLinkDuplicate    se ja existe vinculo com mesmo par
// O relationship eh livre ("filha", "esposa", etc); pode ser "" se desconhecido.
// As prefs de notificacao iniciam em DefaultFamilyNotifyPrefs() (todas true).
func (db *DB) LinkFamily(guardianID, dependentID int64, relationship string) (*FamilyLink, error)
```

**Implementação:**

```go
func (db *DB) LinkFamily(guardianID, dependentID int64, relationship string) (*FamilyLink, error) {
    if guardianID == dependentID {
        return nil, ErrFamilyLinkSelfLink
    }
    // Defesa em profundidade — SQLite nao enforca FK por default.
    if _, err := db.GetUserByID(guardianID); err != nil {
        if errors.Is(err, ErrUserNotFound) {
            return nil, ErrFamilyLinkUserNotFound
        }
        return nil, fmt.Errorf("validate guardian: %w", err)
    }
    if _, err := db.GetUserByID(dependentID); err != nil {
        if errors.Is(err, ErrUserNotFound) {
            return nil, ErrFamilyLinkUserNotFound
        }
        return nil, fmt.Errorf("validate dependent: %w", err)
    }

    prefs := DefaultFamilyNotifyPrefs()
    res, err := db.conn.Exec(
        `INSERT INTO family_links
            (guardian_id, dependent_id, relationship,
             notify_on_medication_miss, notify_on_inactivity, notify_on_severe_signal)
         VALUES (?, ?, ?, ?, ?, ?)`,
        guardianID, dependentID, relationship,
        prefs.OnMedicationMiss, prefs.OnInactivity, prefs.OnSevereSignal,
    )
    if err != nil {
        // Detecta UNIQUE violation por substring — modernc.org/sqlite
        // nao expoe codigos estaveis.
        if strings.Contains(err.Error(), "UNIQUE") {
            return nil, ErrFamilyLinkDuplicate
        }
        if strings.Contains(err.Error(), "CHECK") {
            // Caso raro: CHECK constraint pegou self-link que escapou da
            // validacao em Go (improvavel, mas defense in depth).
            return nil, ErrFamilyLinkSelfLink
        }
        return nil, fmt.Errorf("insert family link: %w", err)
    }
    id, _ := res.LastInsertId()
    return &FamilyLink{
        ID:           id,
        GuardianID:   guardianID,
        DependentID:  dependentID,
        Relationship: relationship,
        Notify:       prefs,
        CreatedAt:    time.Now().UTC(),
    }, nil
}
```

**Exemplo:**

```go
link, err := db.LinkFamily(maria.ID, joao.ID, "filha")
switch {
case errors.Is(err, ErrFamilyLinkDuplicate):
    return "Voce ja eh responsavel por esse usuario.", nil
case errors.Is(err, ErrFamilyLinkSelfLink):
    return "Voce nao pode se cadastrar como seu proprio responsavel.", nil
case err != nil:
    return "", err
}
log.Printf("link criado id=%d", link.ID)
```

### 5.4 `UnlinkFamily`

```go
// UnlinkFamily remove o vinculo (guardian, dependent). Retorna
// ErrFamilyLinkNotFound se nao existia.
func (db *DB) UnlinkFamily(guardianID, dependentID int64) error
```

**Implementação:**

```go
func (db *DB) UnlinkFamily(guardianID, dependentID int64) error {
    res, err := db.conn.Exec(
        `DELETE FROM family_links WHERE guardian_id = ? AND dependent_id = ?`,
        guardianID, dependentID,
    )
    if err != nil {
        return fmt.Errorf("unlink family: %w", err)
    }
    n, err := res.RowsAffected()
    if err != nil {
        return err
    }
    if n == 0 {
        return ErrFamilyLinkNotFound
    }
    return nil
}
```

**Exemplo:**

```go
if err := db.UnlinkFamily(maria.ID, joao.ID); err != nil && !errors.Is(err, ErrFamilyLinkNotFound) {
    return fmt.Errorf("remover vinculo: %w", err)
}
```

### 5.5 `GetDependents`

```go
// GetDependents retorna todos os vinculos onde guardianID eh o responsavel,
// com o User do dependente preenchido em FamilyLink.Other. Ordenado por
// nome do dependente (case-insensitive) pra UX previsivel.
func (db *DB) GetDependents(guardianID int64) ([]FamilyLink, error)
```

**Implementação:**

```go
func (db *DB) GetDependents(guardianID int64) ([]FamilyLink, error) {
    rows, err := db.conn.Query(
        `SELECT fl.id, fl.guardian_id, fl.dependent_id, fl.relationship,
                fl.notify_on_medication_miss, fl.notify_on_inactivity, fl.notify_on_severe_signal,
                fl.created_at,
                u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
                u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
                u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at
         FROM family_links fl
         JOIN users u ON u.id = fl.dependent_id
         WHERE fl.guardian_id = ?
         ORDER BY LOWER(u.name) ASC`, guardianID,
    )
    if err != nil {
        return nil, fmt.Errorf("query dependents: %w", err)
    }
    defer rows.Close()

    var links []FamilyLink
    for rows.Next() {
        var fl FamilyLink
        var u User
        if err := rows.Scan(
            &fl.ID, &fl.GuardianID, &fl.DependentID, &fl.Relationship,
            &fl.Notify.OnMedicationMiss, &fl.Notify.OnInactivity, &fl.Notify.OnSevereSignal,
            &fl.CreatedAt,
            &u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
            &u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
            &u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
        ); err != nil {
            return nil, err
        }
        fl.Other = &u
        links = append(links, fl)
    }
    return links, rows.Err()
}
```

**Exemplo:**

```go
deps, err := db.GetDependents(responsavel.ID)
if err != nil { return err }
for _, link := range deps {
    fmt.Printf("%s (%s): med=%v inat=%v sig=%v\n",
        link.Other.Name, link.Relationship,
        link.Notify.OnMedicationMiss,
        link.Notify.OnInactivity,
        link.Notify.OnSevereSignal)
}
```

### 5.6 `GetGuardians`

```go
// GetGuardians retorna todos os vinculos onde dependentID eh o cuidado,
// com o User do guardiao preenchido em FamilyLink.Other. Ordenado por
// nome do guardiao.
func (db *DB) GetGuardians(dependentID int64) ([]FamilyLink, error)
```

**Implementação:** simétrica a `GetDependents`, trocando `WHERE
fl.guardian_id` por `WHERE fl.dependent_id` e o JOIN por `JOIN users u ON u.id
= fl.guardian_id`. Cabeçalho:

```go
func (db *DB) GetGuardians(dependentID int64) ([]FamilyLink, error) {
    rows, err := db.conn.Query(
        `SELECT fl.id, fl.guardian_id, fl.dependent_id, fl.relationship,
                fl.notify_on_medication_miss, fl.notify_on_inactivity, fl.notify_on_severe_signal,
                fl.created_at,
                u.id, u.phone_number, u.name, u.google_calendar_id, u.google_credentials,
                u.daily_summary_time, u.weekly_summary_day, u.weekly_summary_time,
                u.reminder_before, u.auto_confirm_timeout, u.is_active, u.created_at
         FROM family_links fl
         JOIN users u ON u.id = fl.guardian_id
         WHERE fl.dependent_id = ?
         ORDER BY LOWER(u.name) ASC`, dependentID,
    )
    if err != nil {
        return nil, fmt.Errorf("query guardians: %w", err)
    }
    defer rows.Close()

    var links []FamilyLink
    for rows.Next() {
        var fl FamilyLink
        var u User
        if err := rows.Scan(
            &fl.ID, &fl.GuardianID, &fl.DependentID, &fl.Relationship,
            &fl.Notify.OnMedicationMiss, &fl.Notify.OnInactivity, &fl.Notify.OnSevereSignal,
            &fl.CreatedAt,
            &u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
            &u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
            &u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
        ); err != nil {
            return nil, err
        }
        fl.Other = &u
        links = append(links, fl)
    }
    return links, rows.Err()
}
```

### 5.7 `IsGuardianOf`

```go
// IsGuardianOf retorna true se existe vinculo (guardianID -> dependentID).
// Equivalente conceitual ao CanScheduleFor de PermissionManager, mas em
// dimensao familiar e nao de calendario.
func (db *DB) IsGuardianOf(guardianID, dependentID int64) (bool, error)
```

**Implementação:**

```go
func (db *DB) IsGuardianOf(guardianID, dependentID int64) (bool, error) {
    var n int
    err := db.conn.QueryRow(
        `SELECT COUNT(*) FROM family_links WHERE guardian_id = ? AND dependent_id = ?`,
        guardianID, dependentID,
    ).Scan(&n)
    if err != nil {
        return false, fmt.Errorf("is guardian of: %w", err)
    }
    return n > 0, nil
}
```

**Exemplo:**

```go
ok, err := db.IsGuardianOf(quemPergunta.ID, sobreQuem.ID)
if err != nil { return err }
if !ok {
    return "Voce nao tem permissao pra ver isso.", nil
}
```

### 5.8 `UpdateNotifyPreferences`

```go
// UpdateNotifyPreferences sobrescreve as flags de notificacao de um vinculo.
// Retorna ErrFamilyLinkNotFound se linkID nao existe.
func (db *DB) UpdateNotifyPreferences(linkID int64, prefs FamilyNotifyPrefs) error
```

**Implementação:**

```go
func (db *DB) UpdateNotifyPreferences(linkID int64, prefs FamilyNotifyPrefs) error {
    res, err := db.conn.Exec(
        `UPDATE family_links
            SET notify_on_medication_miss = ?,
                notify_on_inactivity      = ?,
                notify_on_severe_signal   = ?
          WHERE id = ?`,
        prefs.OnMedicationMiss, prefs.OnInactivity, prefs.OnSevereSignal, linkID,
    )
    if err != nil {
        return fmt.Errorf("update notify preferences: %w", err)
    }
    n, err := res.RowsAffected()
    if err != nil {
        return err
    }
    if n == 0 {
        return ErrFamilyLinkNotFound
    }
    return nil
}
```

**Exemplo:**

```go
err := db.UpdateNotifyPreferences(link.ID, FamilyNotifyPrefs{
    OnMedicationMiss: true,
    OnInactivity:     false, // responsavel desativou esse canal
    OnSevereSignal:   true,
})
```

### 5.9 Pequenas alterações em helpers existentes

Pra que `User.Type` e `User.LastUserMessageAt` sejam preenchidos pelos getters
existentes, **estender o struct e os 4 SELECTs** que carregam `User`:

#### 5.9.1 Estender o struct `User` em `bot/db.go:18`

```go
type User struct {
    ID                  int64
    PhoneNumber         string
    Name                string
    GoogleCalendarID    string
    GoogleCredentials   string
    DailySummaryTime    string
    WeeklySummaryDay    string
    WeeklySummaryTime   string
    ReminderBefore      string
    AutoConfirmTimeout  string
    IsActive            bool
    CreatedAt           time.Time

    // NOVO em Fase 1:
    Type                 UserType
    LastUserMessageAt    *time.Time // nil se nunca recebemos mensagem
}
```

#### 5.9.2 Atualizar SELECTs

Atualizar nos 4 lugares: `GetUserByPhone` (db.go:336), `ListActiveUsers`
(db.go:353), `GetUserByName` (db.go:479), `GetUserByID` (db.go:496) — adicionar
`type, last_user_message_at` ao final do `SELECT` e do `Scan`. Padrão:

```go
err := db.conn.QueryRow(
    `SELECT id, phone_number, name, google_calendar_id, google_credentials,
     daily_summary_time, weekly_summary_day, weekly_summary_time,
     reminder_before, auto_confirm_timeout, is_active, created_at,
     type, last_user_message_at
     FROM users WHERE phone_number = ?`, phone,
).Scan(&u.ID, &u.PhoneNumber, &u.Name, &u.GoogleCalendarID, &u.GoogleCredentials,
    &u.DailySummaryTime, &u.WeeklySummaryDay, &u.WeeklySummaryTime,
    &u.ReminderBefore, &u.AutoConfirmTimeout, &u.IsActive, &u.CreatedAt,
    &u.Type, &u.LastUserMessageAt)
```

> **Nota sobre `LastUserMessageAt`:** o tipo `*time.Time` faz scan funcionar
> com `NULL` direto via `database/sql` quando se usa `sql.NullTime` adapter.
> Mas o ponteiro nu não. Padrão correto:

```go
var lastMsg sql.NullTime
err := row.Scan(..., &u.Type, &lastMsg)
if lastMsg.Valid {
    t := lastMsg.Time
    u.LastUserMessageAt = &t
}
```

Refatorar essa lógica num helper interno em `family.go`:

```go
// scanUserExtras lida com colunas opcionais (NULL) que vivem em users
// alem do conjunto basico. Mantem o codigo dos getters limpo.
func scanUserExtras(u *User, ut sql.NullString, lastMsg sql.NullTime) {
    if ut.Valid && ut.String != "" {
        u.Type = UserType(ut.String)
    } else {
        u.Type = UserTypeComum
    }
    if lastMsg.Valid {
        t := lastMsg.Time
        u.LastUserMessageAt = &t
    }
}
```

E em cada SELECT:

```go
var ut sql.NullString
var lastMsg sql.NullTime
err := row.Scan(..., &ut, &lastMsg)
// trata err
scanUserExtras(u, ut, lastMsg)
```

> Por que `sql.NullString` se a coluna é `NOT NULL DEFAULT 'comum'`? Porque
> **antes** da migração rodar pela primeira vez (cenário de race em deploy
> paralelo), um SELECT pode ver a coluna como ausente. Defesa em profundidade.
> Em prática `ut.Valid` será sempre true após primeiro `migrate()`.

#### 5.9.3 Atualizar `ListTargetsFor` em `permissions.go`

Mesmo padrão — adicionar as 2 colunas novas ao SELECT em `permissions.go:43-67`
(usado por `pm.ListTargetsFor`).

---

## 6. Auditoria

### 6.1 Novas ações

Estender o map `actionLabelsPT` em `bot/audit.go:56`:

```go
var actionLabelsPT = map[string]string{
    // ... entradas existentes ...
    "family_link_created":          "Cadastrou familiar",
    "family_link_removed":          "Removeu familiar",
    "family_notify_prefs_updated":  "Atualizou alertas de familiar",
    "user_type_changed":            "Mudou tipo de usuario",
}
```

### 6.2 Helpers de log

Adicionar em `bot/audit.go` (logo após `LogCriarEvento` em audit.go:88):

```go
// LogFamilyLinkCreated registra a criacao de um vinculo familiar.
// userID = ator (quem solicitou a criacao); pode ser o guardian ou um admin.
// Em geral, sera o guardian; mas mantemos generico pra futuro fluxo de
// admin/CS criando vinculos manualmente.
func (a *AuditLog) LogFamilyLinkCreated(userID, guardianID, dependentID int64, relationship string) error {
    details := fmt.Sprintf(
        "guardian_id=%d|dependent_id=%d|relationship=%s",
        guardianID, dependentID, relationship,
    )
    return a.Log(userID, "family_link_created", "", details)
}

// LogFamilyLinkRemoved registra a remocao de um vinculo familiar.
func (a *AuditLog) LogFamilyLinkRemoved(userID, guardianID, dependentID int64) error {
    details := fmt.Sprintf("guardian_id=%d|dependent_id=%d", guardianID, dependentID)
    return a.Log(userID, "family_link_removed", "", details)
}

// LogFamilyNotifyPrefsUpdated registra mudanca de preferencias de notificacao
// de um vinculo familiar.
func (a *AuditLog) LogFamilyNotifyPrefsUpdated(userID, linkID int64, before, after FamilyNotifyPrefs) error {
    details := fmt.Sprintf(
        "link_id=%d|before=med:%t,inat:%t,sig:%t|after=med:%t,inat:%t,sig:%t",
        linkID,
        before.OnMedicationMiss, before.OnInactivity, before.OnSevereSignal,
        after.OnMedicationMiss, after.OnInactivity, after.OnSevereSignal,
    )
    return a.Log(userID, "family_notify_prefs_updated", "", details)
}

// LogUserTypeChanged registra mudanca de tipo de usuario.
// userID = ator (em geral, o proprio user; em fluxo admin pode ser outro).
// targetUserID = quem teve o tipo mudado.
func (a *AuditLog) LogUserTypeChanged(userID, targetUserID int64, before, after UserType) error {
    details := fmt.Sprintf("target_user_id=%d|before=%s|after=%s", targetUserID, before, after)
    return a.Log(userID, "user_type_changed", "", details)
}
```

### 6.3 Payload `details` — formato pipe-separated

Mantemos o padrão existente (`LogCriarEvento` usa pipe-separated key=value).
Cada novo helper segue o mesmo formato pra consistência de parsing/grep em
prod.

Resumo dos payloads:

| Ação                           | Campos no `details`                                                                                                       |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------- |
| `family_link_created`          | `guardian_id=<id>` ` \| ` `dependent_id=<id>` ` \| ` `relationship=<str>`                                                  |
| `family_link_removed`          | `guardian_id=<id>` ` \| ` `dependent_id=<id>`                                                                              |
| `family_notify_prefs_updated`  | `link_id=<id>` ` \| ` `before=med:<bool>,inat:<bool>,sig:<bool>` ` \| ` `after=med:<bool>,inat:<bool>,sig:<bool>`           |
| `user_type_changed`            | `target_user_id=<id>` ` \| ` `before=<type>` ` \| ` `after=<type>`                                                          |

Os helpers de DB **não chamam** o audit diretamente — ficam puros. Os callers
de mais alto nível (Fase 2 endpoints, Fase 3 tools) é que combinam DB + audit.
Esta fase entrega só os helpers.

---

## 7. Casos de teste

Arquivo: `bot/family_test.go`. Padrão: `setupTestDB(t)` (db_test.go:8) cria
SQLite in-memory com schema migrado.

### 7.1 Tabela de testes

| Nome do teste                                          | Caminho     | O que valida                                                                                                                      |
| ------------------------------------------------------ | ----------- | --------------------------------------------------------------------------------------------------------------------------------- |
| `TestUserTypeDefaultIsComum`                           | feliz       | Usuario criado via `CreateUser` sem campo `Type` retorna `UserTypeComum` em `GetUserByPhone`/`GetUserByID`.                       |
| `TestSetUserType_HappyPath`                            | feliz       | Mudar de `comum` -> `idoso` persiste e reflete em getters.                                                                        |
| `TestSetUserType_InvalidType`                          | edge        | `SetUserType(id, "qualquer")` retorna erro de validacao; banco permanece inalterado.                                               |
| `TestSetUserType_UserNotFound`                         | edge        | `SetUserType(99999, UserTypeIdoso)` retorna `ErrUserNotFound`.                                                                    |
| `TestMarkUserMessageReceived_HappyPath`                | feliz       | Apos chamada, `LastUserMessageAt` do user retorna timestamp em UTC com tolerancia de 1s.                                          |
| `TestMarkUserMessageReceived_NilBeforeFirst`           | feliz       | Antes da primeira chamada, `LastUserMessageAt == nil`.                                                                            |
| `TestMarkUserMessageReceived_UserNotFound`             | edge        | Retorna `ErrUserNotFound` pra id inexistente.                                                                                     |
| `TestLinkFamily_HappyPath`                             | feliz       | Cria vinculo, retorna `*FamilyLink` com IDs, `Notify` em defaults (todos true), `CreatedAt` recente.                              |
| `TestLinkFamily_SelfLinkBlocked`                       | edge        | `LinkFamily(alice.ID, alice.ID, "")` retorna `ErrFamilyLinkSelfLink`. Nenhuma row inserida.                                       |
| `TestLinkFamily_DuplicateBlocked`                      | edge        | Segundo `LinkFamily` com mesmo (guardian, dependent) retorna `ErrFamilyLinkDuplicate`.                                            |
| `TestLinkFamily_BothDirectionsAllowed`                 | edge        | `Link(A,B)` e `Link(B,A)` ambos sucedem (sao linhas distintas).                                                                   |
| `TestLinkFamily_GuardianMissing`                       | edge        | `LinkFamily(99999, dep.ID, "")` retorna `ErrFamilyLinkUserNotFound`.                                                              |
| `TestLinkFamily_DependentMissing`                      | edge        | `LinkFamily(guard.ID, 99999, "")` retorna `ErrFamilyLinkUserNotFound`.                                                            |
| `TestLinkFamily_EmptyRelationshipAllowed`              | feliz       | `LinkFamily(g, d, "")` sucede; `Relationship == ""` no retorno.                                                                   |
| `TestUnlinkFamily_HappyPath`                           | feliz       | Apos `LinkFamily` + `UnlinkFamily`, `IsGuardianOf` retorna false.                                                                  |
| `TestUnlinkFamily_NotFound`                            | edge        | `UnlinkFamily` em par inexistente retorna `ErrFamilyLinkNotFound`.                                                                |
| `TestGetDependents_Empty`                              | feliz       | Guardian sem vinculos retorna slice vazia (nil ou len 0), sem erro.                                                               |
| `TestGetDependents_OrderedByName`                      | feliz       | 3 dependentes (Carla, ana, Bruno). Resultado vem ordenado case-insensitive: ana, Bruno, Carla.                                    |
| `TestGetDependents_HydratesOther`                      | feliz       | Cada `FamilyLink.Other != nil` e tem `PhoneNumber` correto.                                                                       |
| `TestGetDependents_NotifyFlagsRoundTrip`               | feliz       | `LinkFamily` -> `UpdateNotifyPreferences` -> `GetDependents` retorna prefs atualizadas.                                           |
| `TestGetGuardians_HappyPath`                           | feliz       | Dependente com 2 guardioes retorna ambos com `Other` populado.                                                                    |
| `TestIsGuardianOf_TrueAfterLink`                       | feliz       | Apos `LinkFamily(A, B)`, `IsGuardianOf(A, B)` == true.                                                                            |
| `TestIsGuardianOf_FalseBeforeLink`                     | feliz       | Antes de qualquer vinculo, retorna false sem erro.                                                                                |
| `TestIsGuardianOf_DirectionalAsymmetry`                | edge        | `Link(A, B)` -> `IsGuardianOf(A, B) == true`, `IsGuardianOf(B, A) == false`.                                                      |
| `TestUpdateNotifyPreferences_HappyPath`                | feliz       | Update persiste e `GetDependents` reflete.                                                                                        |
| `TestUpdateNotifyPreferences_NotFound`                 | edge        | `UpdateNotifyPreferences(99999, ...)` retorna `ErrFamilyLinkNotFound`.                                                            |
| `TestUpdateNotifyPreferences_AllFalse`                 | feliz       | Persistir todas as 3 flags como false e ler de volta.                                                                             |
| `TestMigration_NewColumnsAreNullableOrDefaulted`       | regressao   | Em DB recem-migrado, `users.type == 'comum'` pra todos os users criados antes; `last_user_message_at` eh NULL.                   |
| `TestMigration_Idempotent`                             | regressao   | Chamar `db.migrate()` 3x consecutivas nao gera erro nem duplica indices.                                                          |
| `TestAuditLogFamilyLinkCreated`                        | feliz       | `LogFamilyLinkCreated` insere row em `action_log` com action correta e details parseaveis.                                        |
| `TestAuditLogUserTypeChanged_DetailsContainBeforeAfter`| feliz       | Details contem `before=comum|after=idoso`.                                                                                        |

### 7.2 Esqueleto canônico (referência de estilo)

Mantém a mesma estética do projeto (table-driven quando aplicável, mas curto e
direto pra casos simples — ver `permissions_test.go` que **não** usa
table-driven e ainda assim eh padrao no projeto). Exemplos:

```go
package main

import (
    "errors"
    "testing"
    "time"
)

// Helper local: cria N usuarios com nomes/telefones convenientes pros testes.
func mkUsers(t *testing.T, db *DB, names ...string) []*User {
    t.Helper()
    users := make([]*User, 0, len(names))
    for i, n := range names {
        u := &User{
            PhoneNumber:       fmt.Sprintf("55119999900%02d", i),
            Name:              n,
            GoogleCalendarID:  "x",
            GoogleCredentials: "x",
        }
        if err := db.CreateUser(u); err != nil {
            t.Fatalf("create user %s: %v", n, err)
        }
        users = append(users, u)
    }
    return users
}

func TestLinkFamily_SelfLinkBlocked(t *testing.T) {
    db := setupTestDB(t)
    users := mkUsers(t, db, "Alice")

    _, err := db.LinkFamily(users[0].ID, users[0].ID, "")
    if !errors.Is(err, ErrFamilyLinkSelfLink) {
        t.Fatalf("expected ErrFamilyLinkSelfLink, got %v", err)
    }

    // Nenhuma row foi inserida.
    deps, err := db.GetDependents(users[0].ID)
    if err != nil {
        t.Fatalf("GetDependents: %v", err)
    }
    if len(deps) != 0 {
        t.Fatalf("expected 0 dependents, got %d", len(deps))
    }
}

func TestLinkFamily_DuplicateBlocked(t *testing.T) {
    db := setupTestDB(t)
    users := mkUsers(t, db, "Maria", "Joao")

    if _, err := db.LinkFamily(users[0].ID, users[1].ID, "filha"); err != nil {
        t.Fatalf("first link: %v", err)
    }
    _, err := db.LinkFamily(users[0].ID, users[1].ID, "filha")
    if !errors.Is(err, ErrFamilyLinkDuplicate) {
        t.Fatalf("expected ErrFamilyLinkDuplicate, got %v", err)
    }
}

func TestGetDependents_OrderedByName(t *testing.T) {
    db := setupTestDB(t)
    users := mkUsers(t, db, "Guardian", "Carla", "ana", "Bruno")
    g := users[0]
    for _, dep := range users[1:] {
        if _, err := db.LinkFamily(g.ID, dep.ID, ""); err != nil {
            t.Fatalf("link %s: %v", dep.Name, err)
        }
    }

    deps, err := db.GetDependents(g.ID)
    if err != nil {
        t.Fatalf("GetDependents: %v", err)
    }
    if len(deps) != 3 {
        t.Fatalf("expected 3 deps, got %d", len(deps))
    }
    want := []string{"ana", "Bruno", "Carla"}
    for i, link := range deps {
        if link.Other == nil {
            t.Fatalf("link[%d].Other is nil", i)
        }
        if link.Other.Name != want[i] {
            t.Fatalf("dep[%d]: expected %q, got %q", i, want[i], link.Other.Name)
        }
    }
}

func TestMarkUserMessageReceived_HappyPath(t *testing.T) {
    db := setupTestDB(t)
    users := mkUsers(t, db, "Test")

    before, err := db.GetUserByID(users[0].ID)
    if err != nil {
        t.Fatalf("get before: %v", err)
    }
    if before.LastUserMessageAt != nil {
        t.Fatalf("expected nil LastUserMessageAt before any mark, got %v", *before.LastUserMessageAt)
    }

    ts := time.Now().UTC().Truncate(time.Second)
    if err := db.MarkUserMessageReceived(users[0].ID, ts); err != nil {
        t.Fatalf("MarkUserMessageReceived: %v", err)
    }

    after, err := db.GetUserByID(users[0].ID)
    if err != nil {
        t.Fatalf("get after: %v", err)
    }
    if after.LastUserMessageAt == nil {
        t.Fatal("expected LastUserMessageAt non-nil after mark")
    }
    diff := ts.Sub(*after.LastUserMessageAt)
    if diff > time.Second || diff < -time.Second {
        t.Fatalf("timestamp drift: stored=%v, expected=~%v", *after.LastUserMessageAt, ts)
    }
}

func TestMigration_Idempotent(t *testing.T) {
    db := setupTestDB(t)
    for i := 0; i < 3; i++ {
        if err := db.migrate(); err != nil {
            t.Fatalf("migrate run %d: %v", i, err)
        }
    }
    // Cria user, garante schema final funciona.
    if err := db.CreateUser(&User{PhoneNumber: "1", Name: "X", GoogleCalendarID: "x", GoogleCredentials: "x"}); err != nil {
        t.Fatalf("create user after re-migrate: %v", err)
    }
}
```

### 7.3 Cobertura mínima esperada

- 100% dos branches em `LinkFamily` (self / dup / FK miss / sucesso).
- 100% dos branches em `SetUserType` (invalid / user not found / sucesso).
- Round-trip completo em `FamilyNotifyPrefs` (criar com defaults → update →
  re-leitura).
- 1 teste de regressão de migration (idempotência).

---

## 8. Plano de implementação granular

Cada item abaixo é **um commit ou PR pequeno** (no estilo já praticado no
repo: ver `git log` recente — `feat(audit): log estruturado de criar_evento`,
`feat(prompt): regras de data implicita`, etc). Sequencial; cada um deixa o
build verde.

| # | Commit                                                         | Arquivos                                                | Critério de aceite                                                                                                  |
| - | -------------------------------------------------------------- | ------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| 1 | `feat(schema): tipo de usuario e last_user_message_at`         | `bot/db.go` (struct User + ALTERs aditivos + getters)   | `go test -v ./bot/...` verde. `setUserType` ainda não existe — mas getters já preenchem `Type` via `scanUserExtras`. |
| 2 | `feat(schema): tabela family_links e indices`                  | `bot/db.go` (CREATE TABLE no schema base + indices)     | `go test -v ./bot/...` verde. Migration idempotente (teste).                                                         |
| 3 | `feat(family): tipos UserType e FamilyNotifyPrefs`             | `bot/family.go` (novo, só tipos + sentinels + helpers)  | Compila; sem novos testes.                                                                                          |
| 4 | `feat(family): helpers SetUserType e MarkUserMessageReceived`  | `bot/family.go`                                          | Testes: `TestSetUserType_*`, `TestMarkUserMessageReceived_*` verdes.                                                |
| 5 | `feat(family): helpers LinkFamily/UnlinkFamily/IsGuardianOf`   | `bot/family.go`                                          | Testes: `TestLinkFamily_*`, `TestUnlinkFamily_*`, `TestIsGuardianOf_*` verdes.                                      |
| 6 | `feat(family): GetDependents/GetGuardians com Other hidratado` | `bot/family.go`                                          | Testes: `TestGetDependents_*`, `TestGetGuardians_*` verdes.                                                         |
| 7 | `feat(family): UpdateNotifyPreferences`                        | `bot/family.go`                                          | Testes: `TestUpdateNotifyPreferences_*` verdes.                                                                     |
| 8 | `feat(audit): acoes de family_link e user_type_changed`        | `bot/audit.go` + `bot/audit_test.go`                     | Map `actionLabelsPT` atualizado; helpers `LogFamilyLink*` e `LogUserTypeChanged` com testes.                        |
| 9 | `chore(family): fechamento de cobertura e regressao`           | `bot/family_test.go`                                     | `TestMigration_Idempotent`, `TestUserTypeDefaultIsComum`, regressão de schema. Coverage report >= 90% em `family.go`. |

> **Sugestão de granularidade:** mergear em PRs de 2-3 commits agrupados —
> (1+2) "schema", (3+4+5+6+7) "helpers de family", (8) "audit", (9) "fechamento".
> Mas commits granulares facilitam revert pontual.

### 8.1 Ordem dos arquivos novos/modificados

- **Novo:** `bot/family.go` (~250 linhas)
- **Novo:** `bot/family_test.go` (~400 linhas)
- **Modificado:** `bot/db.go` (struct User, migrate, 4 getters, scanUserExtras)
- **Modificado:** `bot/audit.go` (map + 4 helpers novos)
- **Modificado:** `bot/permissions.go` (apenas `ListTargetsFor` SELECT)
- **Modificado:** `bot/audit_test.go` (smoke test dos novos helpers de log)

### 8.2 Validação local antes de PR

```bash
cd bot
go vet ./...
go test -v -race ./...
go test -cover ./...
```

Cobertura esperada mínima: 90% em `family.go`, sem regressão em arquivos
preexistentes.

---

## 9. Riscos da fase

### 9.1 Backfill de usuários existentes

**Risco:** usuários já cadastrados em prod (qualquer um com type implícito
"comum") precisam migrar pra novo schema sem virar `UserTypeIdoso` por
acidente.

**Mitigação:**
- ALTER TABLE com `DEFAULT 'comum'` aplicado pelo SQLite a todas as rows
  existentes. Verificável: `SELECT type, COUNT(*) FROM users GROUP BY type;`
  após deploy deve mostrar 100% em `comum`.
- `scanUserExtras` defensivo: mesmo se a coluna vier `NULL` ou ausente em algum
  cenário de race, `u.Type` cai pra `UserTypeComum`.
- Teste de regressão `TestUserTypeDefaultIsComum` blinda contra mudança
  inadvertida do default.

### 9.2 SQLite sem foreign_keys habilitado

**Risco:** `family_links` declara FKs pra `users(id)`, mas SQLite não as
enforce por default. Linha "órfã" pode existir se um user for excluído.

**Mitigação:**
- `LinkFamily` valida via `db.GetUserByID` antes de inserir (defesa em
  profundidade).
- Hoje **nenhum código deleta usuários**. Mesmo se virar feature futura,
  qualquer rotina de deleção fica obrigada a fazer cleanup explícito de
  `family_links` (documentar em `00-overview.md` se virar relevante).
- Não habilitar `PRAGMA foreign_keys = ON` agora — mudança de
  semântica em todo o app, fora de escopo.

### 9.3 SetUserType não notifica caches/sessions vivos

**Risco:** se houver cache de `User` em memória (handler atual carrega
`User` a cada mensagem via `GetUserByPhone`, então sem cache) — verificado:
não há cache. Cenário não se aplica hoje.

**Mitigação ativa:** nenhuma necessária. Se Fase 4 introduzir cache de user
no agent, terá que invalidar em mudança de `type`.

### 9.4 CHECK constraint divergente entre banco novo e migrado

**Risco:** se algum dia adicionarmos o CHECK no schema base (banco novo) sem
adicionar via migration adequada (banco antigo), obtemos comportamento
divergente.

**Mitigação:**
- Decisão explícita nesta fase: CHECK fica em Go, não no SQL. Documentado em
  §3.1.2.
- Adicionar CHECK em produção é uma mudança intencional futura, não acidental.

### 9.5 Hard-delete em UnlinkFamily perde histórico

**Risco:** `UnlinkFamily` é DELETE puro. Se um responsável remover um vínculo
e depois precisar auditar quem foi guardião do idoso na semana passada, o
vínculo já não existe.

**Mitigação:**
- O `audit_log` registra `family_link_created` e `family_link_removed` com
  IDs e timestamps — fonte primária de auditoria histórica.
- Soft-delete (`deleted_at` column) foi considerado e **rejeitado** nesta
  fase: complica todos os SELECTs, viola o YAGNI atual, e não há requisito
  funcional de "consultar vínculos passados". Audit log basta.
- Se um requisito futuro pedir histórico relacional consultável (ex: "mostre
  todos os vínculos que existiam em 2026-Q4"), introduz-se uma tabela
  `family_links_history` em fase posterior — não soft-delete na tabela
  primária.

### 9.6 Mudança no struct User pode quebrar callers

**Risco:** adicionar campos a `User` não quebra **leitores** (campos extras
são ignorados em zero-value), mas pode quebrar **construtores explícitos** do
tipo `User{...}` que usam keyed initialization e algum lint reclame.

**Mitigação:**
- Todo construtor de `User` no codebase usa keyed init (`User{Field: val}`),
  então adicionar campos é compatível por padrão Go.
- Verificar com `grep -rn "User{" bot/` que não há posicional init. Se
  houver, refatorar antes do merge da Fase 1.

### 9.7 Ordem de migration vs. createIndex em coluna nova

**Risco:** se alguém futuramente adicionar um índice em coluna criada por
ALTER TABLE no schema base (em vez de no `postAdditive`), o índice falha em
banco antigo na primeira execução pós-deploy.

**Mitigação:**
- Convenção documentada nesta fase: índice em coluna aditiva vai em
  `postAdditive`. Anotar comentário no código:

```go
// CONVENCAO: indices em colunas adicionadas via additive migration vao
// AQUI, nao no schema base. Garante que a coluna existe antes do CREATE
// INDEX rodar em banco antigo.
postAdditive := `...`
```

### 9.8 Performance do JOIN em GetDependents/GetGuardians

**Risco:** join de `family_links` com `users` sem índice apropriado vira
table-scan em volumes futuros.

**Mitigação:**
- `idx_family_links_guardian` e `idx_family_links_dependent` cobrem o
  WHERE.
- `users(id)` é PK (índice implícito) — JOIN eficiente.
- Volume esperado nos próximos 12 meses: < 10k vínculos. Não há hot path.

---

## 10. Checklist de pronto

Marcar binário (sim/não), sem ambiguidade.

- [ ] DDL — `family_links` criada com colunas, FKs, UNIQUE, CHECK conforme §2.
- [ ] DDL — `users.type` ALTER TABLE aplicado com `DEFAULT 'comum'`.
- [ ] DDL — `users.last_user_message_at` ALTER TABLE aplicado (nullable).
- [ ] DDL — `idx_family_links_guardian`, `idx_family_links_dependent`,
  `idx_users_type` criados.
- [ ] Migration idempotente — `db.migrate()` executável N vezes sem erro.
- [ ] Convenção de `postAdditive` para índice em coluna nova documentada em
  comentário no código.
- [ ] Tipo `UserType` declarado com 3 constantes; `IsValid()` e
  `ValidateUserType()` cobertos por teste.
- [ ] Tipo `FamilyNotifyPrefs` declarado; `DefaultFamilyNotifyPrefs()` retorna
  todas true.
- [ ] Tipo `FamilyLink` declarado com campo `Other *User` (omitempty).
- [ ] Sentinels `ErrFamilyLinkNotFound`, `ErrFamilyLinkSelfLink`,
  `ErrFamilyLinkDuplicate`, `ErrFamilyLinkUserNotFound` declarados.
- [ ] `db.SetUserType` valida tipo, retorna `ErrUserNotFound` se id ausente.
- [ ] `db.MarkUserMessageReceived` salva em UTC, retorna `ErrUserNotFound` se
  id ausente.
- [ ] `db.LinkFamily` bloqueia self-link, duplicate, FK órfã.
- [ ] `db.UnlinkFamily` retorna `ErrFamilyLinkNotFound` em par inexistente.
- [ ] `db.GetDependents` retorna `[]FamilyLink` ordenado por `LOWER(name)` com
  `Other` populado.
- [ ] `db.GetGuardians` análogo a `GetDependents`.
- [ ] `db.IsGuardianOf` retorna bool sem erro pra par inexistente.
- [ ] `db.UpdateNotifyPreferences` retorna `ErrFamilyLinkNotFound` em link
  inexistente.
- [ ] Struct `User` estendido com `Type` (`UserType`) e `LastUserMessageAt`
  (`*time.Time`).
- [ ] `GetUserByPhone`, `GetUserByID`, `GetUserByName`, `ListActiveUsers`,
  `permissions.ListTargetsFor` atualizados pra preencher os campos novos.
- [ ] Helper `scanUserExtras` declarado e reusado pelos getters.
- [ ] `audit.actionLabelsPT` estendido com 4 novas chaves.
- [ ] `AuditLog.LogFamilyLinkCreated`, `LogFamilyLinkRemoved`,
  `LogFamilyNotifyPrefsUpdated`, `LogUserTypeChanged` implementados com payload
  pipe-separado.
- [ ] Suite de testes em `bot/family_test.go` passa todos os casos da §7.1.
- [ ] `go test -race -cover ./...` verde, cobertura `family.go` >= 90%.
- [ ] `go vet ./...` limpo.
- [ ] Nenhuma mudança em comportamento observável: bot processa mensagens
  exatamente como antes (smoke manual: enviar "marcar reuniao amanha 15h" em
  ambiente de staging e validar fluxo completo).
- [ ] Audit log de prod **não** ganha entradas novas espontaneamente — só
  quando código futuro (Fase 2+) chamar os helpers.
- [ ] PR descreve a fase, referencia este documento, e o reviewer confirma
  contrato com `00-overview.md` (especialmente: `family_links` paralelo a
  `calendar_permissions`, persona dinâmica não tocada).
