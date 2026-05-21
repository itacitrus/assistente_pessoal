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

export function LoginForm() {
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
          "Não consegui enviar o link agora. Tente novamente em alguns segundos.",
        );
      }
    }
  }

  if (status === "success") {
    return (
      <Alert variant="success">
        <AlertDescription>
          Pronto. Se este número está cadastrado, você vai receber um link no
          WhatsApp em alguns segundos. Vale por 15 minutos.
        </AlertDescription>
      </Alert>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6" noValidate>
      <p className="text-sm text-muted-foreground">
        Vamos te mandar um link de acesso pelo WhatsApp.
      </p>
      <div className="space-y-2">
        <Label htmlFor="phone">Telefone</Label>
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
        Ainda não tem conta?{" "}
        <Link href="/signup" className="font-medium text-foreground underline">
          Criar conta
        </Link>
      </p>
    </form>
  );
}
