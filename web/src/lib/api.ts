/**
 * Wrapper de fetch para a API REST do bot.
 *
 * - Sempre envia `credentials: 'include'` para o cookie httpOnly de sessao.
 * - Header `Content-Type: application/json` quando body presente.
 * - Parsing estruturado do envelope de erro `{ error: { code, message } }`.
 * - Compativel com server-component (fetch nativo do Next 14) e client.
 */

import type { ApiErrorBody } from "@/types/api";

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }

  /** True quando o erro e 401 (sessao ausente ou expirada). */
  get isUnauthorized(): boolean {
    return this.status === 401;
  }
}

export function getApiBaseUrl(): string {
  const envUrl = process.env.NEXT_PUBLIC_API_BASE_URL;
  if (envUrl && envUrl.length > 0) return envUrl.replace(/\/$/, "");
  return "http://localhost:8080";
}

export interface FetchApiOptions extends Omit<RequestInit, "body"> {
  /** Body sera serializado como JSON. */
  json?: unknown;
  /**
   * Quando true, encaminha o cookie da request original (uso em server
   * components que precisam re-encaminhar a sessao). Espera string ja
   * formatada `name=value`.
   */
  cookie?: string;
}

export async function fetchApi<T>(
  path: string,
  opts: FetchApiOptions = {},
): Promise<T> {
  const { json, cookie, headers, ...rest } = opts;
  const url = path.startsWith("http")
    ? path
    : `${getApiBaseUrl()}${path.startsWith("/") ? path : `/${path}`}`;

  const finalHeaders = new Headers(headers);
  if (json !== undefined) {
    finalHeaders.set("Content-Type", "application/json");
  }
  if (cookie) {
    finalHeaders.set("Cookie", cookie);
  }

  const res = await fetch(url, {
    ...rest,
    headers: finalHeaders,
    credentials: "include",
    body: json !== undefined ? JSON.stringify(json) : undefined,
    cache: "no-store",
  });

  if (res.status === 204) {
    return undefined as T;
  }

  const contentType = res.headers.get("content-type") ?? "";
  const isJson = contentType.includes("application/json");

  if (!res.ok) {
    if (isJson) {
      try {
        const body = (await res.json()) as ApiErrorBody;
        throw new ApiError(
          res.status,
          body.error?.code ?? "unknown_error",
          body.error?.message ?? "Erro inesperado.",
        );
      } catch (e) {
        if (e instanceof ApiError) throw e;
        // fallthrough para erro generico
      }
    }
    throw new ApiError(
      res.status,
      "http_error",
      `Falha HTTP ${res.status}.`,
    );
  }

  if (!isJson) {
    return undefined as T;
  }
  return (await res.json()) as T;
}
