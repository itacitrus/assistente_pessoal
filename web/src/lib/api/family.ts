import { fetchApi } from "@/lib/api";
import { normalizePhoneE164BR } from "@/lib/masks";
import type {
  CreateDependentBody,
  DependentEntry,
  DependentStatus,
  DependentStatusRaw,
  DependentTimeline,
  DependentTimelineRaw,
  FamilyLink,
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
    snapshots: raw.snapshots.map(normalizeSnapshot),
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
    snapshots: raw.snapshots.map(normalizeSnapshot),
  };
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
