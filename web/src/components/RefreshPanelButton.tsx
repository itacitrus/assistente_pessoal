"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { RefreshCw } from "lucide-react";

import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { refreshMyInsights } from "@/lib/api/me";
import { refreshDependent } from "@/lib/api/family";
import { cn } from "@/lib/utils";

/**
 * Botão "Atualizar" genérico do painel. Roda a `action` (regeneração de IA no
 * servidor, síncrona) e dá router.refresh() para re-buscar os dados frescos.
 * O limite de 1x/dia é imposto no backend — ao estourar, a API responde 429 e
 * mostramos a mensagem ("já atualizou hoje"). Os wrappers abaixo fixam a action
 * (server components não podem passar funções como prop).
 */
function RefreshPanelButton({
  action,
  lastUpdated,
}: {
  action: () => Promise<unknown>;
  lastUpdated?: string | null;
}) {
  const router = useRouter();
  const [status, setStatus] = React.useState<"idle" | "loading" | "error">(
    "idle",
  );
  const [msg, setMsg] = React.useState<string | null>(null);

  async function onClick() {
    setStatus("loading");
    setMsg(null);
    try {
      await action();
      router.refresh();
      setStatus("idle");
    } catch (err) {
      setStatus("error");
      setMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui atualizar agora. Tente novamente em instantes.",
      );
    }
  }

  const updatedLabel = formatUpdatedAt(lastUpdated);

  return (
    <div className="flex flex-col items-end gap-1">
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={onClick}
        disabled={status === "loading"}
      >
        <RefreshCw
          className={cn("h-4 w-4", status === "loading" && "animate-spin")}
          aria-hidden
        />
        {status === "loading" ? "Atualizando..." : "Atualizar"}
      </Button>
      {status === "error" && msg ? (
        <p className="max-w-[16rem] text-right text-xs text-muted-foreground">
          {msg}
        </p>
      ) : updatedLabel ? (
        <p className="text-right text-xs text-muted-foreground">
          Atualizado {updatedLabel}
        </p>
      ) : null}
    </div>
  );
}

/**
 * formatUpdatedAt formata um timestamp ISO (momento, em UTC) para "em DD/MM
 * às HH:mm" no fuso local. Diferente de data de calendário, aqui a conversão
 * de fuso é correta (é um instante). Retorna "" se ausente/ inválido.
 */
function formatUpdatedAt(iso?: string | null): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const data = d.toLocaleDateString("pt-BR", { day: "2-digit", month: "2-digit" });
  const hora = d.toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });
  return `em ${data} às ${hora}`;
}

/** Botão de atualizar os Insights do titular (1x/dia). */
export function InsightsRefreshButton({
  lastUpdated,
}: {
  lastUpdated?: string | null;
}) {
  return (
    <RefreshPanelButton action={() => refreshMyInsights()} lastUpdated={lastUpdated} />
  );
}

/** Botão de atualizar o relatório de um dependente (1x/dia). */
export function DependentRefreshButton({
  dependentId,
  lastUpdated,
}: {
  dependentId: number;
  lastUpdated?: string | null;
}) {
  return (
    <RefreshPanelButton
      action={() => refreshDependent(dependentId)}
      lastUpdated={lastUpdated}
    />
  );
}
