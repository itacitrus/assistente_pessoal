import { ApiError, fetchApi } from "@/lib/api";
import type {
  ActivityResponse,
  AgendaResponse,
  InsightsResponse,
  ProfileFacts,
} from "@/types/api";

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

/**
 * GET /api/v1/me/activity?limit=100
 *
 * Devolve o historico completo de atividade relevante do usuario (o backend
 * ja filtra os eventos que valem a pena mostrar). `limit` limita a quantidade.
 *
 * Falha graciosamente em 401/403: devolve lista vazia, para que uma falha aqui
 * nao derrube a pagina de historico. Outros erros borbulham.
 */
export async function getMyActivity(
  cookieHeader?: string,
  limit = 100,
): Promise<ActivityResponse> {
  try {
    const res = await fetchApi<ActivityResponse>(
      `/api/v1/me/activity?limit=${limit}`,
      { method: "GET", cookie: cookieHeader },
    );
    // Backend Go pode serializar slice nil como `null` — normaliza.
    return { items: res.items ?? [] };
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
      return { items: [] };
    }
    throw err;
  }
}

/**
 * POST /api/v1/me/google/connect-url
 *
 * Pede ao backend a URL de consentimento do Google Calendar para o proprio
 * usuario logado (com um state opaco de uso unico ja embutido). O caller
 * redireciona o navegador pra essa URL — ao autorizar, o callback OAuth grava
 * as credenciais. POST porque emite um token de uso unico (efeito colateral).
 */
export async function getGoogleConnectUrl(): Promise<{ url: string }> {
  return fetchApi<{ url: string }>("/api/v1/me/google/connect-url", {
    method: "POST",
  });
}

/**
 * GET /api/v1/me/profile-facts
 *
 * Devolve os fatos que o Zello aprendeu sobre o usuario: pessoas na vida dele
 * (relacoes + pessoas citadas) e viagens conhecidas.
 *
 * Falha graciosamente em 401/403: devolve `available: false` com listas
 * vazias, fazendo a UI cair no estado calmo de "ainda aprendendo". Tambem
 * normaliza qualquer array nil vindo do backend para [].
 */
export async function getProfileFacts(
  cookieHeader?: string,
): Promise<ProfileFacts> {
  try {
    const res = await fetchApi<ProfileFacts>("/api/v1/me/profile-facts", {
      method: "GET",
      cookie: cookieHeader,
    });
    return {
      available: res.available ?? false,
      relations: res.relations ?? [],
      people: res.people ?? [],
      trips: res.trips ?? [],
    };
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
      return { available: false, relations: [], people: [], trips: [] };
    }
    throw err;
  }
}
