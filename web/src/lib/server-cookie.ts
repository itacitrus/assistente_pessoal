/**
 * Helper para encaminhar o cookie httpOnly da request original em fetches
 * feitos a partir de server components / route handlers do Next.
 *
 * Observacao: o backend Go le o cookie `assistente_session` via header
 * `Cookie: name=value`.
 */

import { cookies } from "next/headers";

export const SESSION_COOKIE_NAME = "assistente_session";

/**
 * Devolve o cookie atual do usuario formatado como header `Cookie`.
 * Retorna `undefined` quando o cookie nao esta presente.
 */
export function getSessionCookieHeader(): string | undefined {
  const c = cookies().get(SESSION_COOKIE_NAME);
  if (!c) return undefined;
  return `${c.name}=${c.value}`;
}
