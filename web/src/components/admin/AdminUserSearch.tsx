"use client";

import * as React from "react";
import { useRouter } from "next/navigation";
import { Eye, Loader2, Search, UserRound } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { ApiError } from "@/lib/api";
import { searchUsers, startImpersonation } from "@/lib/api/admin";
import { maskPhone } from "@/lib/masks";
import type { User } from "@/types/api";

/** Exibe o telefone (armazenado como 55DDDNUMERO) em formato BR legivel. */
function displayPhone(phone: string): string {
  const local = phone.startsWith("55") && phone.length > 11 ? phone.slice(2) : phone;
  return maskPhone(local);
}

const TYPE_LABEL: Record<string, string> = {
  comum: "Titular",
  responsavel: "Responsável",
  idoso: "Dependente",
};

export interface AdminUserSearchProps {
  initialUsers: User[];
}

/**
 * Barra de busca + lista de usuarios da area admin. Digitar refaz a busca no
 * backend (debounce 300ms). "Ver painel" liga a impersonacao no servidor e
 * navega pro dashboard — que, no SSR, ja renderiza como o usuario-alvo.
 */
export function AdminUserSearch({ initialUsers }: AdminUserSearchProps) {
  const router = useRouter();
  const [query, setQuery] = React.useState("");
  const [users, setUsers] = React.useState<User[]>(initialUsers);
  const [loading, setLoading] = React.useState(false);
  const [pendingId, setPendingId] = React.useState<number | null>(null);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  // Debounce da busca. Ignora respostas obsoletas via flag de cancelamento.
  React.useEffect(() => {
    let cancelled = false;
    setLoading(true);
    const t = setTimeout(async () => {
      try {
        const res = await searchUsers(query);
        if (!cancelled) setUsers(res.users ?? []);
      } catch {
        if (!cancelled) setUsers([]);
      } finally {
        if (!cancelled) setLoading(false);
      }
    }, 300);
    return () => {
      cancelled = true;
      clearTimeout(t);
    };
  }, [query]);

  async function handleView(user: User) {
    setPendingId(user.id);
    setErrorMsg(null);
    try {
      await startImpersonation(user.id);
      router.push("/dashboard");
      router.refresh();
    } catch (err) {
      setPendingId(null);
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui abrir o painel agora. Tente novamente.",
      );
    }
  }

  return (
    <div className="space-y-5">
      <div className="relative">
        <Search
          className="pointer-events-none absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground"
          aria-hidden
        />
        <Input
          type="search"
          inputMode="search"
          placeholder="Buscar por nome ou telefone…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="pl-9"
          aria-label="Buscar pessoa"
        />
      </div>

      {errorMsg ? (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}

      <div className="rounded-lg border bg-card shadow-warm">
        {loading && users.length === 0 ? (
          <p className="flex items-center gap-2 p-6 text-sm text-muted-foreground">
            <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
            Buscando…
          </p>
        ) : users.length === 0 ? (
          <p className="p-6 text-sm text-muted-foreground">
            Ninguém encontrado para “{query}”.
          </p>
        ) : (
          <ul className="divide-y divide-border/70">
            {users.map((u) => (
              <li
                key={u.id}
                className="flex items-center gap-3 p-4"
              >
                <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-[--zello-emerald]/10 text-[--zello-emerald]">
                  <UserRound className="h-5 w-5" aria-hidden />
                </div>
                <div className="min-w-0 flex-1">
                  <p className="truncate font-medium text-foreground">
                    {u.name || "Sem nome"}
                    {!u.is_active ? (
                      <span className="ml-2 rounded-full bg-muted px-2 py-0.5 text-xs font-normal text-muted-foreground">
                        inativo
                      </span>
                    ) : null}
                  </p>
                  <p className="truncate text-sm text-muted-foreground">
                    {displayPhone(u.phone_number)}
                    {TYPE_LABEL[u.type] ? ` · ${TYPE_LABEL[u.type]}` : ""}
                  </p>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  size="sm"
                  onClick={() => handleView(u)}
                  disabled={pendingId !== null}
                >
                  {pendingId === u.id ? (
                    <Loader2 className="h-4 w-4 animate-spin" aria-hidden />
                  ) : (
                    <Eye className="h-4 w-4" aria-hidden />
                  )}
                  Ver painel
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </div>
  );
}
