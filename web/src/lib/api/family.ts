import { fetchApi } from "@/lib/api";
import { normalizePhoneE164BR } from "@/lib/masks";
import type {
  CreateDependentBody,
  CreateMedicationBody,
  DependentEntry,
  DependentStatus,
  DependentStatusRaw,
  DependentTimeline,
  DependentTimelineRaw,
  FamilyLink,
  MedicationItem,
  MedicationsResponse,
  SnapshotPoint,
  SnapshotPointRaw,
  UpdateDependentBody,
  UpdateLinkNotifyBody,
  User,
} from "@/types/api";

export interface CreateDependentArgs {
  name: string;
  phone: string;
  /** Backend espera `relationship` (nao `relation`). */
  relationship: string;
  timezone?: string;
}

/**
 * POST /api/v1/family/dependents
 * Backend espera `{ name, phone, relationship, timezone? }`.
 */
export async function createDependent(
  args: CreateDependentArgs,
): Promise<{ user: User; link: FamilyLink }> {
  const e164 = normalizePhoneE164BR(args.phone);
  if (!e164) {
    throw new Error("invalid_phone_local");
  }
  const body: CreateDependentBody = {
    name: args.name,
    phone: e164,
    relationship: args.relationship,
    ...(args.timezone ? { timezone: args.timezone } : {}),
  };
  return fetchApi<{ user: User; link: FamilyLink }>(
    "/api/v1/family/dependents",
    { method: "POST", json: body },
  );
}

/** GET /api/v1/family/dependents */
export async function listDependents(
  cookieHeader?: string,
): Promise<{ dependents: DependentEntry[] }> {
  return fetchApi<{ dependents: DependentEntry[] }>(
    "/api/v1/family/dependents",
    { method: "GET", cookie: cookieHeader },
  );
}

/** PATCH /api/v1/family/dependents/{id} */
export async function updateDependent(
  id: number,
  body: UpdateDependentBody,
): Promise<User> {
  return fetchApi<User>(`/api/v1/family/dependents/${id}`, {
    method: "PATCH",
    json: body,
  });
}

/** PATCH /api/v1/family/links/{id}/notify */
export async function updateLinkNotifications(
  linkId: number,
  body: UpdateLinkNotifyBody,
): Promise<FamilyLink> {
  return fetchApi<FamilyLink>(`/api/v1/family/links/${linkId}/notify`, {
    method: "PATCH",
    json: body,
  });
}

/**
 * GET /api/v1/family/dependents/{id}/status
 * Endpoint da Fase 5; devolve sintese psicologica + aderencia + alertas.
 *
 * Normalizacao: snapshots com `0` em humor/energia/sociabilidade/autocuidado/
 * confidence representam "sem dado" no backend; convertemos para `null` aqui
 * para preservar o ergonomico no frontend (escala 1..5 vs ausencia).
 */
export async function getDependentStatus(
  id: number,
  opts: { days?: number; cookieHeader?: string } = {},
): Promise<DependentStatus> {
  const qs = opts.days ? `?days=${opts.days}` : "";
  const raw = await fetchApi<DependentStatusRaw>(
    `/api/v1/family/dependents/${id}/status${qs}`,
    { method: "GET", cookie: opts.cookieHeader },
  );
  return {
    ...raw,
    // Backend Go pode mandar slice nil como `null` — guarda antes do .map.
    alerts_open: raw.alerts_open ?? [],
    snapshots: (raw.snapshots ?? []).map(normalizeSnapshot),
  };
}

/**
 * GET /api/v1/family/dependents/{id}/timeline
 * Snapshots psicologicos agregados por dia para o grafico de evolucao.
 *
 * Mesma normalizacao 0->null aplicada ao /status.
 */
export async function getDependentTimeline(
  id: number,
  opts: { days?: number; cookieHeader?: string } = {},
): Promise<DependentTimeline> {
  const qs = opts.days ? `?days=${opts.days}` : "";
  const raw = await fetchApi<DependentTimelineRaw>(
    `/api/v1/family/dependents/${id}/timeline${qs}`,
    { method: "GET", cookie: opts.cookieHeader },
  );
  return {
    ...raw,
    snapshots: (raw.snapshots ?? []).map(normalizeSnapshot),
  };
}

/**
 * GET /api/v1/family/dependents/{id}/medications
 * Lista os remedios cadastrados para o dependente. `schedule` ja vem como
 * texto humano pronto para exibicao.
 *
 * Normaliza array nil vindo do backend para [] (defensivo contra crash no
 * .map do frontend).
 */
export async function getDependentMedications(
  id: number,
  cookieHeader?: string,
): Promise<MedicationsResponse> {
  const res = await fetchApi<MedicationsResponse>(
    `/api/v1/family/dependents/${id}/medications`,
    { method: "GET", cookie: cookieHeader },
  );
  return { medications: res.medications ?? [] };
}

/**
 * POST /api/v1/family/dependents/{id}/medications
 * Cadastra um novo remedio. Devolve 201 com o MedicationItem criado.
 *
 * O caller (form client) e responsavel por validar 1-6 horarios "HH:MM" e por
 * exigir `days` quando frequency="weekly".
 */
export async function createDependentMedication(
  id: number,
  body: CreateMedicationBody,
): Promise<MedicationItem> {
  return fetchApi<MedicationItem>(
    `/api/v1/family/dependents/${id}/medications`,
    { method: "POST", json: body },
  );
}

/**
 * DELETE /api/v1/family/dependents/{id}/medications/{medId}
 * Remove um remedio cadastrado. Devolve `{ ok: true }`.
 */
export async function deleteDependentMedication(
  id: number,
  medId: number,
): Promise<{ ok: boolean }> {
  return fetchApi<{ ok: boolean }>(
    `/api/v1/family/dependents/${id}/medications/${medId}`,
    { method: "DELETE" },
  );
}

/**
 * Converte 0 -> null nos scores psicologicos. Backend serializa 0 quando o
 * sinal nao existe naquele dia (vide Fase 5 §3); manter 0 no frontend
 * confunde com a escala 1..5.
 *
 * `confidence` segue a mesma regra: 0 = "sem confianca registrada", null no TS.
 */
export function normalizeSnapshot(p: SnapshotPointRaw): SnapshotPoint {
  return {
    date: p.date,
    humor: p.humor === 0 ? null : p.humor,
    energia: p.energia === 0 ? null : p.energia,
    sociabilidade: p.sociabilidade === 0 ? null : p.sociabilidade,
    autocuidado: p.autocuidado === 0 ? null : p.autocuidado,
    confidence: p.confidence === 0 ? null : p.confidence,
  };
}
