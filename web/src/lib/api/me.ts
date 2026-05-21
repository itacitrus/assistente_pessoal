import { ApiError, fetchApi } from "@/lib/api";
import type { AgendaResponse, InsightsResponse } from "@/types/api";

/**
 * GET /api/v1/me/agenda
 *
 * Devolve a agenda futura (Google Calendar) e o feed de atividade recente do
 * usuario logado. Em server component, passe o cookie original via
 * `cookieHeader`.
 *
 * Falha graciosamente em 401/403: devolve um envelope vazio e desconectado,
 * para que uma falha na agenda nao derrube o dashboard. Outros erros borbulham.
 */
export async function getMyAgenda(
  cookieHeader?: string,
): Promise<AgendaResponse> {
  try {
    return await fetchApi<AgendaResponse>("/api/v1/me/agenda", {
      method: "GET",
      cookie: cookieHeader,
    });
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
      return { google_connected: false, upcoming: [], recent_activity: [] };
    }
    throw err;
  }
}

/**
 * GET /api/v1/me/insights?days=30
 *
 * Devolve a sintese por IA do padrao de uso do usuario. `days` controla a
 * janela de analise (default 30).
 *
 * Falha graciosamente em 401/403: devolve `available: false`, fazendo a UI
 * cair no estado calmo de "ainda aprendendo". Outros erros borbulham.
 */
export async function getMyInsights(
  cookieHeader?: string,
  days = 30,
): Promise<InsightsResponse> {
  try {
    return await fetchApi<InsightsResponse>(
      `/api/v1/me/insights?days=${days}`,
      { method: "GET", cookie: cookieHeader },
    );
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
      return {
        generated_at: new Date().toISOString(),
        period_days: days,
        available: false,
        summary: "",
        insights: [],
      };
    }
    throw err;
  }
}
