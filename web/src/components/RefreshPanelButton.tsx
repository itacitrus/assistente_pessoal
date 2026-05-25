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
function RefreshPanelButton({ action }: { action: () => Promise<unknown> }) {
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
      ) : null}
    </div>
  );
}

/** Botão de atualizar os Insights do titular (1x/dia). */
export function InsightsRefreshButton() {
  return <RefreshPanelButton action={() => refreshMyInsights()} />;
}

/** Botão de atualizar o relatório de um dependente (1x/dia). */
export function DependentRefreshButton({
  dependentId,
}: {
  dependentId: number;
}) {
  return <RefreshPanelButton action={() => refreshDependent(dependentId)} />;
}
