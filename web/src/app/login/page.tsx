import { redirect } from "next/navigation";

import { getMe } from "@/lib/api/auth";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import { LoginForm } from "@/components/forms/LoginForm";
import { AuthShell } from "@/components/site/AuthShell";

export const metadata = {
  title: "Entrar — Zello",
};

// Lemos o cookie da sessao a cada request — nada de cache estatico aqui.
export const dynamic = "force-dynamic";

export default async function LoginPage() {
  // Se ja existe sessao valida, "Entrar" nao deve reabrir o formulario de
  // magic link — manda direto pro painel. A sessao persiste 30d (cookie
  // httpOnly), entao quem ja autenticou so volta pra ca apos logout explicito
  // ou expiracao. A validacao bate em GET /api/v1/me reaproveitando o cookie.
  const cookieHeader = getSessionCookieHeader();
  let authenticated = false;
  if (cookieHeader) {
    try {
      await getMe(cookieHeader);
      authenticated = true;
    } catch {
      // Cookie ausente/invalido/expirado, ou backend indisponivel: caimos no
      // formulario de login (degradacao graciosa — nunca travamos o acesso).
    }
  }
  // redirect() lanca NEXT_REDIRECT; fica FORA do try pra nao ser engolido.
  if (authenticated) {
    redirect("/dashboard");
  }

  return (
    <AuthShell
      title="Bem-vindo de volta"
      subtitle="Entre para acompanhar o cuidado de quem você ama."
    >
      <LoginForm />
    </AuthShell>
  );
}
