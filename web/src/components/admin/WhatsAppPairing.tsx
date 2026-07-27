"use client";

import * as React from "react";
import {
  CheckCircle2,
  Loader2,
  QrCode,
  RefreshCw,
  Smartphone,
  Unplug,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ApiError } from "@/lib/api";
import {
  getPairingStatus,
  resetPairing,
  startPairing,
  type PairingStatus,
} from "@/lib/api/pairing";
import { maskPhone } from "@/lib/masks";

const POLL_MS = 3000;

function connectedDisplay(jid: string): string {
  const digits = jid.replace(/\D/g, "");
  const local = digits.startsWith("55") && digits.length > 11 ? digits.slice(2) : digits;
  return maskPhone(local);
}

/** Formata o código de 8 chars como "ABCD-1234" para leitura mais fácil. */
function formatPairCode(code: string): string {
  return code.length === 8 ? `${code.slice(0, 4)}-${code.slice(4)}` : code;
}

export interface WhatsAppPairingProps {
  initial: PairingStatus | null;
}

/**
 * UI de pareamento do WhatsApp. Gera código de 8 dígitos ou QR e faz polling do
 * status até parear. O material de pareamento é sensível: só admin acessa (gate
 * real no backend).
 */
export function WhatsAppPairing({ initial }: WhatsAppPairingProps) {
  const [status, setStatus] = React.useState<PairingStatus | null>(initial);
  const [phone, setPhone] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const state = status?.status ?? "idle";
  const polling = state === "waiting" || state === "starting";

  // Polling enquanto o pareamento está em andamento. Captura rotações de QR e a
  // transição para "paired".
  React.useEffect(() => {
    if (!polling) return;
    let cancelled = false;
    const id = setInterval(async () => {
      try {
        const s = await getPairingStatus();
        if (!cancelled) setStatus(s);
      } catch {
        // Falha transitória: mantém o último status; próximo tick tenta de novo.
      }
    }, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(id);
    };
  }, [polling]);

  async function handleStart(method: "qr" | "phone") {
    setErrorMsg(null);
    if (method === "phone" && phone.replace(/\D/g, "").length < 10) {
      setErrorMsg("Informe o número do WhatsApp do bot com DDI (ex: 55 61 99999-9999).");
      return;
    }
    setBusy(true);
    try {
      const s = await startPairing(method, phone.replace(/\D/g, ""));
      setStatus(s);
    } catch (err) {
      setErrorMsg(
        err instanceof ApiError ? err.message : "Não foi possível iniciar o pareamento.",
      );
    } finally {
      setBusy(false);
    }
  }

  async function handleReset() {
    setErrorMsg(null);
    setBusy(true);
    try {
      const s = await resetPairing();
      setStatus(s);
    } catch (err) {
      setErrorMsg(
        err instanceof ApiError ? err.message : "Não foi possível desconectar.",
      );
    } finally {
      setBusy(false);
    }
  }

  const connectedAs = status?.connected_as;
  const isConnected = state === "idle" && !!connectedAs;
  const justPaired = state === "paired";

  return (
    <div className="space-y-6">
      {/* Estado da conexão */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-lg">
            {isConnected || justPaired ? (
              <>
                <CheckCircle2 className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
                Conectado
              </>
            ) : (
              <>
                <Unplug className="h-5 w-5 text-muted-foreground" aria-hidden />
                Desconectado
              </>
            )}
          </CardTitle>
          <CardDescription>
            {justPaired
              ? "Pareado com sucesso. O bot já está operando nesta conta."
              : isConnected
                ? `O bot está conectado como ${connectedDisplay(connectedAs!)}.`
                : "O bot não está vinculado a nenhuma conta do WhatsApp. Gere um código ou QR abaixo."}
          </CardDescription>
        </CardHeader>
        {(isConnected || justPaired) && (
          <CardContent>
            <Button
              variant="outline"
              onClick={handleReset}
              disabled={busy}
              className="gap-2"
            >
              {busy ? (
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
              ) : (
                <RefreshCw className="h-4 w-4" aria-hidden />
              )}
              Desconectar e parear de novo
            </Button>
          </CardContent>
        )}
      </Card>

      {(errorMsg || (state === "error" && status?.error)) && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg ?? status?.error}</AlertDescription>
        </Alert>
      )}

      {/* Pareamento em andamento: código + QR lado a lado */}
      {polling && (
        <div className="grid gap-6 sm:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-lg">
                <Smartphone className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
                Código de 8 dígitos
              </CardTitle>
              <CardDescription>
                No celular: WhatsApp → Aparelhos conectados → Conectar um
                aparelho → <strong>Conectar com número de telefone</strong>.
              </CardDescription>
            </CardHeader>
            <CardContent>
              {status?.pair_code ? (
                <div
                  className="select-all text-center font-mono text-3xl font-bold tracking-[0.3em] text-foreground"
                  aria-label="Código de pareamento"
                >
                  {formatPairCode(status.pair_code)}
                </div>
              ) : status?.method === "phone" ? (
                <div className="flex items-center gap-2 text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
                  Gerando código…
                </div>
              ) : (
                <p className="text-sm text-muted-foreground">
                  Prefira o código? Informe o número e clique em “Gerar código”.
                </p>
              )}
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-lg">
                <QrCode className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
                QR code
              </CardTitle>
              <CardDescription>
                No celular: WhatsApp → Aparelhos conectados → Conectar um
                aparelho e escaneie.
              </CardDescription>
            </CardHeader>
            <CardContent>
              {status?.qr_png_base64 ? (
                // eslint-disable-next-line @next/next/no-img-element
                <img
                  src={status.qr_png_base64}
                  alt="QR code de pareamento do WhatsApp"
                  className="mx-auto h-56 w-56 rounded-md bg-white p-2"
                />
              ) : (
                <div className="flex h-56 items-center justify-center text-muted-foreground">
                  <Loader2 className="h-5 w-5 animate-spin" aria-hidden />
                </div>
              )}
            </CardContent>
          </Card>
        </div>
      )}

      {/* Iniciar pareamento — só quando não há sessão em andamento nem conexão
          ativa (para conectado, o caminho é "Desconectar e parear de novo"). */}
      {!polling && !isConnected && !justPaired && (
        <Card>
          <CardHeader>
            <CardTitle className="text-lg">Gerar pareamento</CardTitle>
            <CardDescription>
              Escolha o método. O código e o QR expiram em poucos minutos — se
              expirar, gere de novo.
            </CardDescription>
          </CardHeader>
          <CardContent className="space-y-4">
            <div className="space-y-2">
              <Label htmlFor="bot-phone">Número do WhatsApp do bot (com DDI)</Label>
              <div className="flex flex-col gap-3 sm:flex-row">
                <Input
                  id="bot-phone"
                  inputMode="numeric"
                  placeholder="55 61 99999-9999"
                  value={phone}
                  onChange={(e) => setPhone(e.target.value)}
                  className="sm:max-w-xs"
                />
                <Button
                  onClick={() => handleStart("phone")}
                  disabled={busy}
                  className="gap-2"
                >
                  {busy ? (
                    <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
                  ) : (
                    <Smartphone className="h-4 w-4" aria-hidden />
                  )}
                  Gerar código
                </Button>
              </div>
            </div>

            <div className="flex items-center gap-3 pt-2">
              <div className="h-px flex-1 bg-border" />
              <span className="text-xs uppercase tracking-wide text-muted-foreground">
                ou
              </span>
              <div className="h-px flex-1 bg-border" />
            </div>

            <Button
              variant="outline"
              onClick={() => handleStart("qr")}
              disabled={busy}
              className="gap-2"
            >
              {busy ? (
                <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
              ) : (
                <QrCode className="h-4 w-4" aria-hidden />
              )}
              Gerar QR code
            </Button>
          </CardContent>
        </Card>
      )}
    </div>
  );
}
