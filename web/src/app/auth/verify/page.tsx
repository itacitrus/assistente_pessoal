"use client";

import * as React from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Wordmark } from "@/components/brand/Logo";
import { ApiError } from "@/lib/api";
import { verifyToken } from "@/lib/api/auth";

type Status = "loading" | "success" | "missing" | "expired" | "used" | "error";

export default function VerifyPage() {
  return (
    <div className="relative flex min-h-screen flex-col overflow-hidden">
      <div aria-hidden className="pointer-events-none absolute inset-0 -z-10">
        <div className="absolute -left-32 -top-24 h-[26rem] w-[26rem] rounded-full bg-[--zello-emerald]/12 blur-3xl" />
        <div className="absolute -right-24 bottom-0 h-80 w-80 rounded-full bg-[--zello-amber]/20 blur-3xl" />
        <div className="absolute inset-0 bg-noise opacity-40 mix-blend-multiply" />
      </div>
      <header className="border-b border-border/70 bg-[--zello-cream]/70 backdrop-blur-md">
        <div className="container flex h-16 items-center">
          <Link
            href="/"
            className="rounded-md ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
          >
            <Wordmark />
          </Link>
        </div>
      </header>
      <main className="flex flex-1 items-center justify-center p-4">
        <div className="w-full max-w-md space-y-6 text-center">
          <h1 className="text-2xl font-semibold tracking-tight">
            Validando seu link
          </h1>
          <React.Suspense fallback={<LoadingState />}>
            <VerifyInner />
          </React.Suspense>
        </div>
      </main>
    </div>
  );
}

function LoadingState() {
  return <p className="text-muted-foreground">Um instante...</p>;
}

function VerifyInner() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const token = searchParams.get("token");
  const [status, setStatus] = React.useState<Status>(
    token ? "loading" : "missing",
  );
  const [message, setMessage] = React.useState<string | null>(null);

  React.useEffect(() => {
    if (!token) return;
    let cancelled = false;
    (async () => {
      try {
        await verifyToken(token);
        if (cancelled) return;
        setStatus("success");
        setTimeout(() => router.replace("/dashboard"), 400);
      } catch (err) {
        if (cancelled) return;
        if (err instanceof ApiError) {
          if (err.code === "token_expired") {
            setStatus("expired");
          } else if (err.code === "already_used") {
            setStatus("used");
          } else {
            setStatus("error");
            setMessage(err.message);
          }
        } else {
          setStatus("error");
          setMessage("Falha de rede ao validar o link.");
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [token, router]);

  return (
    <div className="space-y-4">
      {status === "loading" && <LoadingState />}

      {status === "missing" && (
        <Alert variant="destructive">
          <AlertDescription>
            O link parece incompleto. Volte para o login e peça um novo.
          </AlertDescription>
        </Alert>
      )}

      {status === "expired" && (
        <Alert variant="destructive">
          <AlertDescription>
            Esse link expirou. Peça um novo no login — eles valem 15 minutos.
          </AlertDescription>
        </Alert>
      )}

      {status === "used" && (
        <Alert variant="destructive">
          <AlertDescription>
            Esse link já foi usado. Por segurança, peça um novo no login.
          </AlertDescription>
        </Alert>
      )}

      {status === "error" && (
        <Alert variant="destructive">
          <AlertDescription>
            {message ?? "Não consegui validar agora. Tente novamente."}
          </AlertDescription>
        </Alert>
      )}

      {status === "success" && (
        <Alert variant="success">
          <AlertDescription>
            Tudo certo. Levando você ao painel...
          </AlertDescription>
        </Alert>
      )}

      {status !== "loading" && status !== "success" && (
        <Button asChild>
          <Link href="/login">Voltar ao login</Link>
        </Button>
      )}
    </div>
  );
}
