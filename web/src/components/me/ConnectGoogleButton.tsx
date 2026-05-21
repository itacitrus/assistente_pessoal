"use client";

import * as React from "react";
import { CalendarPlus } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ApiError } from "@/lib/api";
import { getGoogleConnectUrl } from "@/lib/api/me";

export interface ConnectGoogleButtonProps {
  /** Se ja conectado, o botao vira "Reconectar" e usa visual discreto. */
  connected: boolean;
}

/**
 * Botao que conecta a agenda Google do PROPRIO titular. Pede a URL de
 * consentimento ao backend e redireciona o navegador atual — o titular ja
 * esta logado na conta Google dele aqui, entao autoriza na mesma sessao.
 * Mantemos `redirecting` ate a navegacao acontecer pra evitar duplo clique.
 */
export function ConnectGoogleButton({ connected }: ConnectGoogleButtonProps) {
  const [redirecting, setRedirecting] = React.useState(false);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  async function handleClick() {
    setRedirecting(true);
    setErrorMsg(null);
    try {
      const { url } = await getGoogleConnectUrl();
      window.location.href = url;
    } catch (err) {
      setRedirecting(false);
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui gerar o link agora. Tente novamente em instantes.",
      );
    }
  }

  return (
    <div className="space-y-2">
      <Button
        type="button"
        variant={connected ? "outline" : "default"}
        onClick={handleClick}
        disabled={redirecting}
      >
        <CalendarPlus className="h-4 w-4" aria-hidden />
        {redirecting
          ? "Abrindo o Google..."
          : connected
            ? "Reconectar Google Agenda"
            : "Conectar Google Agenda"}
      </Button>
      {errorMsg ? (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}
    </div>
  );
}
