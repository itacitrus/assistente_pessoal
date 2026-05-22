"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { HeartHandshake, Pencil, Plus, Trash2, Users, X } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { ApiError } from "@/lib/api";
import {
  createPersonFact,
  deletePersonFact,
  updatePersonFact,
} from "@/lib/api/me";
import type {
  PersonFact,
  PersonFactType,
  RelationFact,
} from "@/types/api";

/** Linha editavel normalizada (memoria crua). */
interface EditableRow {
  name: string;
  detail: string;
  type: PersonFactType;
  category: string;
  key: string;
}

/** Vinculo familiar — exibido, mas gerido nas telas de familia. */
interface LockedRow {
  name: string;
  relation: string;
}

function typeFromCategory(category?: string): PersonFactType {
  return category === "relacao" ? "relacao" : "pessoa";
}

function splitRows(relations: RelationFact[], people: PersonFact[]) {
  const editable: EditableRow[] = [];
  const locked: LockedRow[] = [];
  for (const r of relations) {
    if (r.editable && r.category && r.key) {
      editable.push({
        name: r.name,
        detail: r.relation,
        type: typeFromCategory(r.category),
        category: r.category,
        key: r.key,
      });
    } else {
      locked.push({ name: r.name, relation: r.relation });
    }
  }
  for (const p of people) {
    if (p.editable && p.category && p.key) {
      editable.push({
        name: p.name,
        detail: p.detail,
        type: typeFromCategory(p.category),
        category: p.category,
        key: p.key,
      });
    }
  }
  return { editable, locked };
}

export interface PeopleManagerProps {
  relations: RelationFact[];
  people: PersonFact[];
}

export function PeopleManager({ relations, people }: PeopleManagerProps) {
  const router = useRouter();
  const { editable, locked } = React.useMemo(
    () => splitRows(relations, people),
    [relations, people],
  );

  // Form: fechado | criando | editando (com identidade original).
  const [mode, setMode] = React.useState<"closed" | "create" | "edit">(
    "closed",
  );
  const [original, setOriginal] = React.useState<{
    category: string;
    key: string;
  } | null>(null);
  const [name, setName] = React.useState("");
  const [type, setType] = React.useState<PersonFactType>("pessoa");
  const [detail, setDetail] = React.useState("");
  const [saving, setSaving] = React.useState(false);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  // Remocao: chave da linha em confirmacao + estado de carregamento.
  const [pendingDelete, setPendingDelete] = React.useState<string | null>(null);
  const [deleting, setDeleting] = React.useState(false);

  function openCreate() {
    setMode("create");
    setOriginal(null);
    setName("");
    setType("pessoa");
    setDetail("");
    setErrorMsg(null);
  }

  function openEdit(row: EditableRow) {
    setMode("edit");
    setOriginal({ category: row.category, key: row.key });
    setName(row.name);
    setType(row.type);
    setDetail(row.detail);
    setErrorMsg(null);
  }

  function closeForm() {
    setMode("closed");
    setErrorMsg(null);
  }

  const canSave = name.trim().length > 0 && !saving;

  async function handleSubmit() {
    if (!canSave) return;
    setSaving(true);
    setErrorMsg(null);
    try {
      if (mode === "edit" && original) {
        await updatePersonFact({
          name: name.trim(),
          detail: detail.trim(),
          type,
          original_category: original.category,
          original_key: original.key,
        });
      } else {
        await createPersonFact({ name: name.trim(), detail: detail.trim(), type });
      }
      setSaving(false);
      setMode("closed");
      router.refresh();
    } catch (err) {
      setSaving(false);
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui salvar agora. Tente novamente.",
      );
    }
  }

  async function handleDelete(row: EditableRow) {
    setDeleting(true);
    try {
      await deletePersonFact(row.category, row.key);
      setDeleting(false);
      setPendingDelete(null);
      router.refresh();
    } catch {
      setDeleting(false);
      setPendingDelete(null);
    }
  }

  const isEmpty = editable.length === 0 && locked.length === 0;

  return (
    <Card className="shadow-warm">
      <CardHeader className="flex flex-row items-start justify-between gap-3 space-y-0">
        <div className="space-y-1.5">
          <CardTitle className="flex items-center gap-2 text-lg">
            <Users className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
            Pessoas na sua vida
          </CardTitle>
          <CardDescription>
            Quem o Zello conhece — você pode adicionar e editar à vontade.
          </CardDescription>
        </div>
        {mode === "closed" ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={openCreate}
            className="shrink-0"
          >
            <Plus className="mr-1.5 h-4 w-4" aria-hidden />
            Adicionar
          </Button>
        ) : null}
      </CardHeader>
      <CardContent className="space-y-4">
        {mode !== "closed" ? (
          <div className="space-y-4 rounded-lg border bg-muted/30 p-4">
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="space-y-2">
                <Label htmlFor="pf-name">Nome</Label>
                <Input
                  id="pf-name"
                  value={name}
                  placeholder="Ex: João Victor"
                  onChange={(e) => setName(e.target.value)}
                  maxLength={80}
                />
              </div>
              <div className="space-y-2">
                <Label htmlFor="pf-type">Tipo</Label>
                <Select
                  value={type}
                  onValueChange={(v) => setType(v as PersonFactType)}
                >
                  <SelectTrigger id="pf-type">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="relacao">
                      Relação (família, pessoas próximas)
                    </SelectItem>
                    <SelectItem value="pessoa">
                      Pessoa (contato, conhecido)
                    </SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="space-y-2">
              <Label htmlFor="pf-detail">Descrição</Label>
              <Textarea
                id="pf-detail"
                value={detail}
                placeholder={
                  type === "relacao"
                    ? "Ex: Pai, mora em Brasília"
                    : "Ex: Colega da Octalab, joaovitor@email.com"
                }
                onChange={(e) => setDetail(e.target.value)}
                maxLength={500}
              />
            </div>

            {errorMsg ? (
              <Alert variant="destructive">
                <AlertDescription>{errorMsg}</AlertDescription>
              </Alert>
            ) : null}

            <div className="flex items-center gap-3">
              <Button type="button" onClick={handleSubmit} disabled={!canSave}>
                {saving
                  ? "Salvando..."
                  : mode === "edit"
                    ? "Salvar"
                    : "Adicionar"}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={closeForm}
                disabled={saving}
              >
                Cancelar
              </Button>
            </div>
          </div>
        ) : null}

        {isEmpty && mode === "closed" ? (
          <p className="py-4 text-center text-sm text-muted-foreground">
            Ainda não há ninguém aqui. Toque em “Adicionar” para o Zello começar
            a conhecer as pessoas da sua vida.
          </p>
        ) : null}

        <ul className="divide-y divide-border/70">
          {locked.map((r, i) => (
            <li
              key={`locked-${r.name}-${i}`}
              className="flex items-start gap-3 py-3 first:pt-0"
            >
              <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
                <HeartHandshake className="h-4 w-4" aria-hidden />
              </div>
              <div className="min-w-0 flex-1">
                <p className="truncate font-medium text-foreground">{r.name}</p>
                {r.relation ? (
                  <p className="text-sm capitalize text-muted-foreground">
                    {r.relation}
                  </p>
                ) : null}
              </div>
              <span className="mt-1 shrink-0 rounded-full bg-muted px-2 py-0.5 text-xs text-muted-foreground">
                Vínculo familiar
              </span>
            </li>
          ))}

          {editable.map((row) => {
            const rowKey = `${row.category} ${row.key}`;
            const confirming = pendingDelete === rowKey;
            return (
              <li
                key={rowKey}
                className="flex items-start gap-3 py-3 first:pt-0"
              >
                <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
                  <HeartHandshake className="h-4 w-4" aria-hidden />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium text-foreground">
                    {row.name}
                  </p>
                  {row.detail ? (
                    <p className="text-sm text-muted-foreground">
                      {row.detail}
                    </p>
                  ) : null}
                </div>
                {confirming ? (
                  <div className="flex shrink-0 items-center gap-1.5">
                    <Button
                      type="button"
                      size="sm"
                      variant="destructive"
                      onClick={() => handleDelete(row)}
                      disabled={deleting}
                    >
                      {deleting ? "Removendo..." : "Remover"}
                    </Button>
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      onClick={() => setPendingDelete(null)}
                      disabled={deleting}
                    >
                      <X className="h-4 w-4" aria-hidden />
                    </Button>
                  </div>
                ) : (
                  <div className="flex shrink-0 items-center gap-0.5">
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      aria-label={`Editar ${row.name}`}
                      onClick={() => openEdit(row)}
                    >
                      <Pencil className="h-4 w-4" aria-hidden />
                    </Button>
                    <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      aria-label={`Remover ${row.name}`}
                      onClick={() => setPendingDelete(rowKey)}
                    >
                      <Trash2 className="h-4 w-4 text-destructive" aria-hidden />
                    </Button>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      </CardContent>
    </Card>
  );
}
