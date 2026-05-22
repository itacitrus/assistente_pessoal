import { redirect } from "next/navigation";
import { ShieldCheck } from "lucide-react";

import { AdminUserSearch } from "@/components/admin/AdminUserSearch";
import { ApiError } from "@/lib/api";
import { getMe } from "@/lib/api/auth";
import { searchUsers } from "@/lib/api/admin";
import { getSessionCookieHeader } from "@/lib/server-cookie";
import type { User } from "@/types/api";

export const dynamic = "force-dynamic";

/**
 * Painel admin — acesso restrito ao operador (allowlist ADMIN_PHONES no
 * backend). Permite buscar qualquer pessoa cadastrada e abrir o painel dela
 * via "ver como". O gate aqui (is_admin) eh espelho do gate real do backend,
 * que retorna 403 independentemente desta checagem de UI.
 */
export default async function AdminPage() {
  const cookieHeader = getSessionCookieHeader();

  const me = await getMe(cookieHeader);
  if (!me.is_admin) {
    redirect("/dashboard");
  }

  let initialUsers: User[] = [];
  try {
    const res = await searchUsers("", cookieHeader);
    initialUsers = res.users ?? [];
  } catch (err) {
    // 403 nao deveria acontecer (ja checamos is_admin), mas falha de rede vira
    // lista vazia — a busca client-side ainda funciona.
    if (!(err instanceof ApiError)) throw err;
  }

  return (
    <div className="space-y-8">
      <section className="animate-rise">
        <div className="flex items-center gap-2">
          <ShieldCheck className="h-5 w-5 text-[--zello-emerald]" aria-hidden />
          <p className="text-sm font-medium text-[--zello-emerald]">
            Área do administrador
          </p>
        </div>
        <h1 className="mt-1 font-display text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">
          Painel de qualquer pessoa
        </h1>
        <p className="mt-3 max-w-prose text-base text-muted-foreground">
          Busque por nome ou telefone e abra o painel da pessoa para ver e
          ajustar a agenda, os remédios e as preferências dela.
        </p>
      </section>

      <AdminUserSearch initialUsers={initialUsers} />
    </div>
  );
}
