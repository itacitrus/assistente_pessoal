"use client";

import * as React from "react";
import Link from "next/link";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { PhoneInput } from "@/components/forms/PhoneInput";
import { ApiError } from "@/lib/api";
import { requestLoginLink } from "@/lib/api/auth";
import { isValidPhoneBR } from "@/lib/masks";

type Status = "idle" | "submitting" | "success" | "error";

/**
 * NOTA: nao existe self-signup pelo painel. Usuarios sao criados pelo bot
 * (whatsmeow) na primeira mensagem ao WhatsApp. Esta tela so dispara o magic
 * link para quem ja existe — backend espera somente { phone } e responde
 * 200 opaco mesmo quando o numero nao esta cadastrado, para evitar
 * enumeracao.
 *
 * O texto da pagina deixa isso explicito; mantemos o componente para
 * preservar a rota /signup que ja foi divulgada externamente.
 */
export function SignupForm() {
  const [phone, setPhone] = React.useState("");
  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const canSubmit = isValidPhoneBR(phone) && status !== "submitting";

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStatus("submitting");
    setErrorMsg(null);
    try {
      await requestLoginLink({ phone });
      setStatus("success");
    } catch (err) {
      setStatus("error");
      if (err instanceof ApiError) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg(
          "Nao consegui enviar o link agora. Tente novamente em alguns segundos.",
        );
      }
    }
  }

  if (status === "success") {
    return (
      <Alert variant="success">
        <AlertDescription>
          Pronto! Se este numero ja esta cadastrado, voce vai receber um link
          no WhatsApp em alguns segundos. Vale por 15 minutos. Se nada chegar,
          envie qualquer mensagem para o Zello no WhatsApp para criar a
          conta.
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6" noValidate>
      <p className="text-sm text-muted-foreground">
        Para criar a conta, mande qualquer mensagem para o Zello no WhatsApp.
        Depois volte aqui para fazer login com seu numero.
      </p>

      <div className="space-y-2">
        <Label htmlFor="phone">Telefone (com DDD)</Label>
        <PhoneInput id="phone" value={phone} onChange={setPhone} required />
      </div>

      {status === "error" && errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <Button type="submit" disabled={!canSubmit} className="w-full">
        {status === "submitting" ? "Enviando..." : "Enviar link"}
      </Button>

      <p className="text-center text-sm text-muted-foreground">
        Ja tem conta?{" "}
        <Link href="/login" className="font-medium text-foreground underline">
          Fazer login
        </Link>
      </p>
    </form>
  );
}
