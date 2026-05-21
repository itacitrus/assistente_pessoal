"use client";

import * as React from "react";
import { MessageCircleHeart } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ApiError } from "@/lib/api";
import { resendDependentWelcome } from "@/lib/api/family";

export interface ResendWelcomeButtonProps {
  dependentId: number;
  dependentName: string;
}

/**
 * Botao "Reenviar boas-vindas" na pagina do dependente. Dispara o POST
 * /family/dependents/{id}/welcome — util quando o envio na criacao falhou ou
 * a pessoa foi cadastrada antes de a feature existir. Estado de envio/sucesso/
 * erro inline (sem dependencia de toast global).
 */
export function ResendWelcomeButton({
  dependentId,
  dependentName,
}: ResendWelcomeButtonProps) {
  const [status, setStatus] = React.useState<
    "idle" | "sending" | "sent" | "error"
  >("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const firstName = dependentName.split(" ")[0] || dependentName;

  async function handleClick() {
    setStatus("sending");
    setErrorMsg(null);
    try {
      await resendDependentWelcome(dependentId);
      setStatus("sent");
    } catch (err) {
      setStatus("error");
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui enviar agora. Tente novamente em instantes.",
      );
    }
  }

  if (status === "sent") {
    return (
      <Alert className="border-[--zello-emerald]/30 bg-[--zello-emerald]/5">
        <AlertDescription className="text-[--zello-emerald-deep]">
          Mensagem de boas-vindas enviada para {firstName} no WhatsApp. ✓
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <div className="space-y-2">
      <Button
        type="button"
        variant="outline"
        onClick={handleClick}
        disabled={status === "sending"}
      >
        <MessageCircleHeart className="h-4 w-4" aria-hidden />
        {status === "sending" ? "Enviando..." : "Reenviar boas-vindas"}
      </Button>
      {status === "error" && errorMsg ? (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
