import Link from "next/link";

import { LoginForm } from "@/components/forms/LoginForm";
import { AuthShell } from "@/components/site/AuthShell";

export const metadata = {
  title: "Entrar — Zello",
};

export default function LoginPage() {
  return (
    <AuthShell
      title="Bem-vindo de volta"
      subtitle="Entre para acompanhar o cuidado de quem voce ama."
    >
      <LoginForm />
      <p className="mt-6 text-center text-sm text-muted-foreground">
        Ainda nao tem conta?{" "}
        <Link href="/signup" className="font-medium text-primary hover:underline">
          Criar conta
        </Link>
      </p>
    </AuthShell>
  );
}
