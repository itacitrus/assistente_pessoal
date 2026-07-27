"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PhoneInput } from "@/components/forms/PhoneInput";
import { ApiError } from "@/lib/api";
import { adminLogin, requestLoginLink } from "@/lib/api/auth";
import { isValidPhoneBR } from "@/lib/masks";

type Status = "idle" | "submitting" | "success" | "error";

export function LoginForm() {
  const router = useRouter();
  const [adminMode, setAdminMode] = React.useState(false);
  const [phone, setPhone] = React.useState("");
  const [code, setCode] = React.useState("");
  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const phoneOK = isValidPhoneBR(phone);
  const codeOK = code.replace(/\D/g, "").length === 6;
  const canSubmit =
    status !== "submitting" && phoneOK && (adminMode ? codeOK : true);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStatus("submitting");
    setErrorMsg(null);
    try {
      if (adminMode) {
        await adminLogin(phone, code);
        // Sucesso: cookie setado pelo backend — vai pro painel.
        router.push("/dashboard");
        router.refresh();
        return;
      }
      await requestLoginLink({ phone });
      setStatus("success");
    } catch (err) {
      setStatus("error");
      if (adminMode) {
        // Erro genérico de propósito (não revela se o número é admin).
        setErrorMsg(
          err instanceof ApiError && err.status === 429
            ? err.message
            : "Telefone ou código inválidos.",
        );
      } else if (err instanceof ApiError) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg(
          "Não consegui enviar o link agora. Tente novamente em alguns segundos.",
        );
      }
    }
  }

  if (status === "success" && !adminMode) {
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
        {adminMode
          ? "Acesso do administrador com código do app autenticador."
          : "Vamos te mandar um link de acesso pelo WhatsApp."}
      </p>

      <div className="space-y-2">
        <Label htmlFor="phone">Telefone</Label>
        <PhoneInput id="phone" value={phone} onChange={setPhone} required />
      </div>

      {adminMode && (
        <div className="space-y-2">
          <Label htmlFor="totp">Código do app (6 dígitos)</Label>
          <Input
            id="totp"
            inputMode="numeric"
            autoComplete="one-time-code"
            placeholder="000000"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            className="tracking-[0.4em]"
          />
        </div>
      )}

      {status === "error" && errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <Button type="submit" disabled={!canSubmit} className="w-full">
        {status === "submitting"
          ? adminMode
            ? "Entrando..."
            : "Enviando..."
          : adminMode
            ? "Entrar"
            : "Enviar link"}
      </Button>

      <div className="space-y-2 text-center text-sm">
        {!adminMode && (
          <p className="text-muted-foreground">
            Ainda não tem conta?{" "}
            <Link
              href="/signup"
              className="font-medium text-foreground underline"
            >
              Criar conta
            </Link>
          </p>
        )}
        <button
          type="button"
          onClick={() => {
            setAdminMode((v) => !v);
            setStatus("idle");
            setErrorMsg(null);
            setCode("");
          }}
          className="text-muted-foreground underline-offset-4 hover:underline"
        >
          {adminMode
            ? "Voltar ao acesso normal"
            : "Acesso do administrador"}
        </button>
      </div>
    </form>
  );
}
