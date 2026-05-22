import { fetchApi } from "@/lib/api";
import type { User } from "@/types/api";

/**
 * Chamadas da area de admin do painel. O privilegio vive no backend
 * (allowlist ADMIN_PHONES) — estas rotas retornam 403 pra quem nao for admin.
 * A impersonacao ("ver como") fica gravada na sessao do admin no servidor;
 * por isso `start`/`stop` nao precisam carregar estado no cliente — basta
 * navegar/recarregar que o SSR ja resolve o usuario efetivo.
 */

export interface AdminUsersResponse {
  users: User[];
}

/**
 * GET /api/v1/admin/users?q=<busca>
 * Lista usuarios por nome ou telefone. Query vazia retorna os mais recentes.
 * Em server component, passe o cookie via `cookieHeader`.
 */
export async function searchUsers(
  query: string,
  cookieHeader?: string,
): Promise<AdminUsersResponse> {
  const qs = query.trim() ? `?q=${encodeURIComponent(query.trim())}` : "";
  return fetchApi<AdminUsersResponse>(`/api/v1/admin/users${qs}`, {
    method: "GET",
    cookie: cookieHeader,
  });
}

/**
 * POST /api/v1/admin/impersonate — liga a visao "ver como" o usuario alvo.
 * Cliente: sem cookieHeader (o navegador envia o cookie automaticamente).
 */
export async function startImpersonation(userId: number): Promise<User> {
  return fetchApi<User>("/api/v1/admin/impersonate", {
    method: "POST",
    json: { user_id: userId },
  });
}

/** DELETE /api/v1/admin/impersonate — sai da visao e volta a ser o admin. */
export async function stopImpersonation(): Promise<{ ok: true }> {
  return fetchApi<{ ok: true }>("/api/v1/admin/impersonate", {
    method: "DELETE",
  });
}
