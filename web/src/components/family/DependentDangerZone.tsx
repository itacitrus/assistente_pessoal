"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { PauseCircle, PlayCircle, UserMinus } from "lucide-react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import { setDependentActive, unlinkDependent } from "@/lib/api/family";

export interface DependentDangerZoneProps {
  dependentId: number;
  dependentName: string;
  initialActive: boolean;
}

/**
 * Ações de gestão do vínculo com o dependente:
 *  - Desativar/Reativar a conta (pausa lembretes e proatividade; reversível).
 *  - Remover o vínculo (o idoso e seus dados permanecem; some da sua lista).
 * Ambas reversíveis — nada apaga dados.
 */
export function DependentDangerZone({
  dependentId,
  dependentName,
  initialActive,
}: DependentDangerZoneProps) {
  const router = useRouter();
  const [active, setActive] = React.useState(initialActive);
  const [busy, setBusy] = React.useState<"toggle" | "unlink" | null>(null);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const firstName = dependentName.split(" ")[0] || "o dependente";

  async function handleToggleActive() {
    setBusy("toggle");
    setErrorMsg(null);
    try {
      await setDependentActive(dependentId, !active);
      setActive(!active);
      router.refresh();
    } catch (err) {
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui alterar agora. Tente novamente.",
      );
    } finally {
      setBusy(null);
    }
  }

  async function handleUnlink() {
    const ok = window.confirm(
      `Remover ${firstName} da sua lista? Você deixa de acompanhar e de receber avisos. ` +
        `A conta e o histórico de ${firstName} continuam — dá pra vincular de novo depois.`,
    );
    if (!ok) return;
    setBusy("unlink");
    setErrorMsg(null);
    try {
      await unlinkDependent(dependentId);
      router.push("/dashboard");
      router.refresh();
    } catch (err) {
      setBusy(null);
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui remover agora. Tente novamente.",
      );
    }
  }

  return (
    <div className="space-y-4 rounded-lg border bg-card p-5 shadow-warm">
      <div>
        <h2 className="font-display text-lg font-semibold tracking-tight">
          Gerenciar acesso
        </h2>
        <p className="text-sm text-muted-foreground">
          {active
            ? `${firstName} está ativo. Você pode pausar os lembretes ou remover o vínculo — nada disso apaga dados.`
            : `${firstName} está pausado: o Zello não envia lembretes nem puxa conversa. Reative quando quiser.`}
        </p>
      </div>

      {errorMsg ? (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}

      <div className="flex flex-wrap gap-3">
        <Button
          type="button"
          variant="outline"
          onClick={handleToggleActive}
          disabled={busy !== null}
        >
          {active ? (
            <>
              <PauseCircle className="h-4 w-4" aria-hidden />
              {busy === "toggle" ? "Pausando..." : "Pausar conta"}
            </>
          ) : (
            <>
              <PlayCircle className="h-4 w-4" aria-hidden />
              {busy === "toggle" ? "Reativando..." : "Reativar conta"}
            </>
          )}
        </Button>
        <Button
          type="button"
          variant="ghost"
          onClick={handleUnlink}
          disabled={busy !== null}
          className="text-muted-foreground hover:text-destructive"
        >
          <UserMinus className="h-4 w-4" aria-hidden />
          {busy === "unlink" ? "Removendo..." : "Remover vínculo"}
        </Button>
      </div>
    </div>
  );
}
