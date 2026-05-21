import Link from "next/link";

import { SignupForm } from "@/components/forms/SignupForm";
import { AuthShell } from "@/components/site/AuthShell";

export const metadata = {
  title: "Criar conta — Zello",
};

export default function SignupPage() {
  return (
    <AuthShell
      title="Crie sua conta"
      subtitle="Em poucos minutos o Zello comeca a cuidar de quem voce ama."
    >
      <SignupForm />
      <p className="mt-6 text-center text-sm text-muted-foreground">
        Ja tem conta?{" "}
        <Link href="/login" className="font-medium text-primary hover:underline">
          Entrar
        </Link>
      </p>
    </AuthShell>
  );
}
