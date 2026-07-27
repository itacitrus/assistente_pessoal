import { fetchApi } from "@/lib/api";

/**
 * Pareamento do WhatsApp (área admin). O bot dirige o pareamento; estas rotas
 * retornam 403 para quem não for admin (allowlist ADMIN_PHONES no backend).
 * O material de pareamento (código de 8 dígitos e QR) é sensível — nunca exibir
 * fora da área admin.
 */

export type PairingState =
  | "idle"
  | "starting"
  | "waiting"
  | "paired"
  | "error";

export interface PairingStatus {
  status: PairingState;
  method?: "phone" | "qr";
  pair_code?: string;
  qr_png_base64?: string;
  expires_at?: string;
  connected_as?: string;
  error?: string;
}

/** GET /api/v1/admin/pairing/status — o painel faz polling deste endpoint. */
export async function getPairingStatus(
  cookieHeader?: string,
): Promise<PairingStatus> {
  return fetchApi<PairingStatus>("/api/v1/admin/pairing/status", {
    method: "GET",
    cookie: cookieHeader,
  });
}

/**
 * POST /api/v1/admin/pairing/start — inicia o pareamento.
 * `method="qr"` gera QR; `method="phone"` gera código de 8 dígitos para o
 * número informado (dígitos, com DDI).
 */
export async function startPairing(
  method: "qr" | "phone",
  phone?: string,
): Promise<PairingStatus> {
  return fetchApi<PairingStatus>("/api/v1/admin/pairing/start", {
    method: "POST",
    json: { method, phone: phone ?? "" },
  });
}

/** POST /api/v1/admin/pairing/reset — desloga e volta ao modo pareamento. */
export async function resetPairing(): Promise<PairingStatus> {
  return fetchApi<PairingStatus>("/api/v1/admin/pairing/reset", {
    method: "POST",
  });
}
