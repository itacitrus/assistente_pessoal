import { fetchApi } from "@/lib/api";
import { normalizePhoneE164BR } from "@/lib/masks";
import type { RequestLinkBody, User, VerifyTokenBody } from "@/types/api";

export interface RequestLinkArgs {
  phone: string; // pode vir com mascara
}

/**
 * POST /api/v1/auth/request-link
 *
 * Backend aceita SOMENTE `{ phone }`. Resposta sempre 200 `{ ok: true }`,
 * mesmo se o phone nao existe (resposta opaca para evitar enumeracao). Em
 * caso de invalido (regex) ou rate limit retorna 400/429 — ApiError borbulha.
 *
 * NOTA: nao existe self-signup via API. Usuarios sao criados pelo bot
 * (whatsmeow) na primeira mensagem; o painel so faz login de quem ja existe.
 */
export async function requestLoginLink(
  args: RequestLinkArgs,
): Promise<{ ok: true }> {
  const e164 = normalizePhoneE164BR(args.phone);
  if (!e164) {
    throw new Error("invalid_phone_local");
  }
  const body: RequestLinkBody = { phone: e164 };
  return fetchApi<{ ok: true }>("/api/v1/auth/request-link", {
    method: "POST",
    json: body,
  });
}

/**
 * POST /api/v1/auth/verify
 * Em sucesso, o backend devolve `{ user }` e seta o cookie de sessao.
 */
export async function verifyToken(token: string): Promise<{ user: User }> {
  const body: VerifyTokenBody = { token };
  return fetchApi<{ user: User }>("/api/v1/auth/verify", {
    method: "POST",
    json: body,
  });
}

/** POST /api/v1/auth/logout */
export async function logout(): Promise<{ ok: true }> {
  return fetchApi<{ ok: true }>("/api/v1/auth/logout", { method: "POST" });
}

/**
 * GET /api/v1/me
 * Em server component, passe o cookie original via `cookieHeader`.
 */
export async function getMe(cookieHeader?: string): Promise<User> {
  return fetchApi<User>("/api/v1/me", {
    method: "GET",
    cookie: cookieHeader,
  });
}
