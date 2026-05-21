"use client";

import * as React from "react";
import { CalendarPlus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ApiError } from "@/lib/api";
import { sendDependentGoogleConnect } from "@/lib/api/family";

export interface ConnectDependentGoogleButtonProps {
  dependentId: number;
  dependentName: string;
  /** Se ja conectado, o botao vira "Reenviar link" e usa visual discreto. */
  connected: boolean;
}

/**
 * Botao na pagina do dependente que faz o Zello enviar, no WhatsApp do PROPRIO
 * dependente, o link de conexao com o Google Calendar. O guardiao dispara, mas
 * quem autoriza eh o dependente no aparelho dele — garantindo que a conta
 * Google conectada seja a da pessoa certa. Estado inline (sem toast global),
 * espelhando o ResendWelcomeButton.
 */
export function ConnectDependentGoogleButton({
  dependentId,
  dependentName,
  connected,
}: ConnectDependentGoogleButtonProps) {
  const [status, setStatus] = React.useState<
    "idle" | "sending" | "sent" | "error"
  >("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const firstName = dependentName.split(" ")[0] || dependentName;

  async function handleClick() {
    setStatus("sending");
    setErrorMsg(null);
    try {
      await sendDependentGoogleConnect(dependentId);
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
          Link de conexão enviado para {firstName} no WhatsApp. Quando{" "}
          {firstName} autorizar, a agenda fica conectada. ✓
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <div className="space-y-2">
      <Button
        type="button"
        variant={connected ? "outline" : "default"}
        onClick={handleClick}
        disabled={status === "sending"}
      >
        <CalendarPlus className="h-4 w-4" aria-hidden />
        {status === "sending"
          ? "Enviando..."
          : connected
            ? "Reenviar link da agenda"
            : "Conectar Google Agenda"}
      </Button>
      {status === "error" && errorMsg ? (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
