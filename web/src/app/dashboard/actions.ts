"use server";

import { redirect } from "next/navigation";
import { cookies } from "next/headers";

import { fetchApi } from "@/lib/api";
import { SESSION_COOKIE_NAME } from "@/lib/server-cookie";

/**
 * Server action de logout: chama o backend para invalidar a sessao,
 * remove o cookie local e redireciona para a landing.
 */
export async function logoutAction(): Promise<void> {
  const c = cookies().get(SESSION_COOKIE_NAME);
  const cookieHeader = c ? `${c.name}=${c.value}` : undefined;
  try {
    await fetchApi<{ ok: true }>("/api/v1/auth/logout", {
      method: "POST",
      cookie: cookieHeader,
    });
  } catch {
    // mesmo se o backend falhar, removemos o cookie local — o usuario
    // sai do painel local e pode tentar de novo
  }
  cookies().set(SESSION_COOKIE_NAME, "", {
    path: "/",
    maxAge: 0,
    httpOnly: true,
    sameSite: "strict",
  });
  redirect("/");
}
