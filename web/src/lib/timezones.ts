/**
 * Lista de fusos horarios brasileiros relevantes para o produto.
 *
 * Mantida em portugues, com indicacao do offset GMT e exemplos de cidades.
 * Default em formularios: `America/Sao_Paulo`.
 */

export const BR_TIMEZONES = [
  { id: "America/Sao_Paulo", label: "Brasilia (GMT-3) — SP, RJ, MG, ..." },
  { id: "America/Bahia", label: "Bahia (GMT-3)" },
  { id: "America/Fortaleza", label: "Fortaleza (GMT-3)" },
  { id: "America/Recife", label: "Recife (GMT-3)" },
  { id: "America/Belem", label: "Belem (GMT-3)" },
  { id: "America/Manaus", label: "Manaus (GMT-4)" },
  { id: "America/Cuiaba", label: "Cuiaba (GMT-4)" },
  { id: "America/Campo_Grande", label: "Campo Grande (GMT-4)" },
  { id: "America/Porto_Velho", label: "Porto Velho (GMT-4)" },
  { id: "America/Boa_Vista", label: "Boa Vista (GMT-4)" },
  { id: "America/Rio_Branco", label: "Rio Branco (GMT-5)" },
  { id: "America/Eirunepe", label: "Eirunepe (GMT-5)" },
  { id: "America/Noronha", label: "Fernando de Noronha (GMT-2)" },
] as const;

export type BRTimezoneId = (typeof BR_TIMEZONES)[number]["id"];

export const DEFAULT_TIMEZONE: BRTimezoneId = "America/Sao_Paulo";

export function isValidBRTimezone(tz: string): tz is BRTimezoneId {
  return BR_TIMEZONES.some((t) => t.id === tz);
}
