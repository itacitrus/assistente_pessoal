import { ApiError, fetchApi } from "@/lib/api";
import type {
  ActivityResponse,
  AgendaEventsResponse,
  AgendaResponse,
  CreateMedicationBody,
  DrugSearchResponse,
  InsightsResponse,
  IntakesResponse,
  MedicationItem,
  MedicationsResponse,
  PersonFactBody,
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
 * GET /api/v1/me/agenda/events?from=YYYY-MM-DD&to=YYYY-MM-DD
 *
 * Eventos do Google Calendar no intervalo [from, to) — usado pela visão de
 * calendário mensal navegável. Intervalo máximo de 62 dias (validado no
 * backend). `from`/`to` no formato AAAA-MM-DD.
 */
export async function getMyAgendaEvents(
  from: string,
  to: string,
  cookieHeader?: string,
): Promise<AgendaEventsResponse> {
  const qs = new URLSearchParams({ from, to }).toString();
  return fetchApi<AgendaEventsResponse>(`/api/v1/me/agenda/events?${qs}`, {
    method: "GET",
    cookie: cookieHeader,
  });
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
 * GET /api/v1/me/medications
 * Lista os remédios do próprio titular. `schedule` já vem como texto humano;
 * `ends_at` presente indica tratamento temporário. Normaliza array nil -> [].
 */
export async function getMyMedications(
  cookieHeader?: string,
): Promise<MedicationsResponse> {
  const res = await fetchApi<MedicationsResponse>("/api/v1/me/medications", {
    method: "GET",
    cookie: cookieHeader,
  });
  return { medications: res.medications ?? [] };
}

/**
 * GET /api/v1/me/drugs/search?q=&limit=
 * Autocomplete do cadastro: resolve o termo (com correção de grafia/fonética)
 * contra o catálogo ANVISA/CMED. `q` com menos de 2 chars devolve lista vazia.
 */
export async function searchDrugs(
  q: string,
  limit = 8,
  signal?: AbortSignal,
): Promise<DrugSearchResponse> {
  const trimmed = q.trim();
  if (trimmed.length < 2) return { matches: [] };
  const qs = new URLSearchParams({ q: trimmed, limit: String(limit) });
  const res = await fetchApi<DrugSearchResponse>(
    `/api/v1/me/drugs/search?${qs}`,
    { method: "GET", signal },
  );
  return { matches: res.matches ?? [] };
}

/**
 * POST /api/v1/me/medications
 * Cadastra um remédio do próprio titular. Mesmo body do dependente (inclui a
 * duração opcional). Devolve 201 com o MedicationItem criado.
 */
export async function createMyMedication(
  body: CreateMedicationBody,
): Promise<MedicationItem> {
  return fetchApi<MedicationItem>("/api/v1/me/medications", {
    method: "POST",
    json: body,
  });
}

/**
 * PATCH /api/v1/me/medications/{id}
 * Edita (replace) um remédio do próprio titular. Devolve o MedicationItem.
 */
export async function updateMyMedication(
  id: number,
  body: CreateMedicationBody,
): Promise<MedicationItem> {
  return fetchApi<MedicationItem>(`/api/v1/me/medications/${id}`, {
    method: "PATCH",
    json: body,
  });
}

/**
 * DELETE /api/v1/me/medications/{id}
 * Remove (soft-delete) um remédio do próprio titular. Devolve `{ ok: true }`.
 */
export async function deleteMyMedication(
  id: number,
): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>(`/api/v1/me/medications/${id}`, {
    method: "DELETE",
  });
}

/**
 * GET /api/v1/me/intakes
 * Histórico de tomadas do próprio titular nos últimos `days` dias (default 14,
 * teto 90). `medicationId` filtra um único remédio. Normaliza array nil -> [].
 */
export async function getMyIntakes(
  opts: { days?: number; medicationId?: number; cookieHeader?: string } = {},
): Promise<IntakesResponse> {
  const qs = new URLSearchParams();
  if (opts.days) qs.set("days", String(opts.days));
  if (opts.medicationId) qs.set("medication_id", String(opts.medicationId));
  const suffix = qs.toString() ? `?${qs.toString()}` : "";
  const res = await fetchApi<IntakesResponse>(`/api/v1/me/intakes${suffix}`, {
    method: "GET",
    cookie: opts.cookieHeader,
  });
  return { intakes: res.intakes ?? [], days: res.days };
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
 * POST /api/v1/me/people
 * Cadastra uma pessoa/relacao na vida do usuario (vira memoria que o Zello
 * passa a conhecer). 409 se ja existir alguem com o mesmo nome no mesmo tipo.
 */
export async function createPersonFact(
  body: PersonFactBody,
): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>("/api/v1/me/people", {
    method: "POST",
    json: body,
  });
}

/**
 * PATCH /api/v1/me/people
 * Edita uma pessoa/relacao existente. `original_category` + `original_key`
 * identificam a entrada (mesmo que o nome/tipo mudem).
 */
export async function updatePersonFact(
  body: PersonFactBody,
): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>("/api/v1/me/people", {
    method: "PATCH",
    json: body,
  });
}

/**
 * DELETE /api/v1/me/people?category=&key=
 * Remove uma pessoa/relacao. Identidade vai na query (keys arbitrarias com
 * unicode/espacos nao cabem bem em path param).
 */
export async function deletePersonFact(
  category: string,
  key: string,
): Promise<{ ok: boolean }> {
  const qs = new URLSearchParams({ category, key }).toString();
  return fetchApi<{ ok: boolean }>(`/api/v1/me/people?${qs}`, {
    method: "DELETE",
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
