/**
 * Helpers de formatacao pt-BR para datas, horas e tempo relativo.
 *
 * Toda formatacao usa o locale `pt-BR`. As funcoes sao defensivas: entradas
 * invalidas (string vazia, ISO malformada) devolvem string vazia em vez de
 * lancar, para nunca derrubar a renderizacao de um card.
 *
 * FUSO: relogio de parede (hora/dia de um evento) é SEMPRE formatado num
 * timeZone EXPLICITO — nunca no fuso do processo. As paginas do painel sao
 * server components renderizados em UTC; sem timeZone explicito, `getHours()`
 * e `Intl.DateTimeFormat` mostrariam a hora do servidor (UTC), aparecendo +3h
 * para o usuario brasileiro. O default é `DEFAULT_TIMEZONE` (America/Sao_Paulo),
 * o mesmo fuso canonico ja usado em IntakeHistoryList/AgendaCalendar e no
 * backend (BRT). Os instants chegam corretos (RFC3339 com offset); o que se
 * decide aqui é apenas em qual fuso EXIBIR.
 */

import { DEFAULT_TIMEZONE } from "@/lib/timezones";

/** Fuso canonico de exibicao do produto (Brasil). Reexportado para que sites de
 *  formatacao compartilhem uma unica fonte da verdade. */
export const APP_TIME_ZONE: string = DEFAULT_TIMEZONE;

function parse(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

/**
 * Parser tolerante a datas SEM hora ("YYYY-MM-DD", como TripFact.start/end ou
 * SnapshotPoint.date). `new Date("2026-05-30")` vira meia-noite UTC e, ao
 * formatar em America/Sao_Paulo (UTC-3), recuaria para 29/05 — o classico shift
 * de dia. Ancoramos a data-only ao MEIO-DIA UTC: assim o dia do calendario
 * sobrevive a qualquer fuso de UTC-12 a UTC+12. Instants completos (RFC3339)
 * seguem pelo parser normal.
 */
function parseDateOnlyAware(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso.trim());
  if (m) {
    return new Date(Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]), 12, 0, 0));
  }
  return parse(iso);
}

/**
 * Formata um instante como "qui, 22 mai · 14h00" (pt-BR), no fuso indicado. Para
 * eventos de dia inteiro (`allDay`), omite a hora: "qui, 22 mai".
 */
export function formatEventWhen(
  iso: string,
  allDay = false,
  timeZone: string = APP_TIME_ZONE,
): string {
  const d = parse(iso);
  if (!d) return "";

  const day = new Intl.DateTimeFormat("pt-BR", {
    weekday: "short",
    day: "2-digit",
    month: "short",
    timeZone,
  })
    .format(d)
    .replace(/\.,/, ",") // "qui., 22 de mai." -> normaliza pontuacao
    .replace(/\sde\s/g, " ")
    .replace(/\.$/, "");

  if (allDay) return capitalizeFirst(day);

  const time = formatTime(d, timeZone);
  return `${capitalizeFirst(day)} · ${time}`;
}

/** Formata so a hora como "14h00" (pt-BR, 24h) no fuso indicado. */
export function formatTime(
  input: string | Date,
  timeZone: string = APP_TIME_ZONE,
): string {
  const d = input instanceof Date ? input : parse(input);
  if (!d) return "";
  // formatToParts garante o shape "HHhMM" independentemente do separador do
  // locale; usar timeZone explicito evita a hora do processo (UTC no servidor).
  const parts = new Intl.DateTimeFormat("pt-BR", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
    timeZone,
  }).formatToParts(d);
  const hh = parts.find((p) => p.type === "hour")?.value ?? "00";
  const mm = parts.find((p) => p.type === "minute")?.value ?? "00";
  return `${hh}h${mm}`;
}

/**
 * Tempo relativo curto e caloroso em pt-BR a partir de um instante passado:
 * "agora", "há 2 min", "há 3h", "ontem", "há 4 dias". Para datas muito
 * antigas (> ~30 dias), cai para a data absoluta "22 mai".
 *
 * O ramo relativo usa diferenca de instants (epoch ms) — independente de fuso.
 * Apenas o fallback absoluto formata uma data, e o faz no fuso indicado.
 */
export function formatRelativeTime(
  iso: string,
  now: Date = new Date(),
  timeZone: string = APP_TIME_ZONE,
): string {
  const d = parse(iso);
  if (!d) return "";

  const diffMs = now.getTime() - d.getTime();
  const diffSec = Math.round(diffMs / 1000);

  // Futuro ou praticamente agora.
  if (diffSec < 45) return "agora";

  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `há ${diffMin} min`;

  const diffHour = Math.round(diffMin / 60);
  if (diffHour < 24) return `há ${diffHour}h`;

  const diffDay = Math.round(diffHour / 24);
  if (diffDay === 1) return "ontem";
  if (diffDay < 30) return `há ${diffDay} dias`;

  return new Intl.DateTimeFormat("pt-BR", {
    day: "2-digit",
    month: "short",
    timeZone,
  })
    .format(d)
    .replace(/\sde\s/g, " ")
    .replace(/\.$/, "");
}

/** Formata uma data curta como "22 mai 2026" (pt-BR), sem hora, no fuso indicado.
 *  Aceita instants (RFC3339) e datas-only ("YYYY-MM-DD") sem shift de dia. */
export function formatShortDate(
  iso: string,
  timeZone: string = APP_TIME_ZONE,
): string {
  const d = parseDateOnlyAware(iso);
  if (!d) return "";
  return new Intl.DateTimeFormat("pt-BR", {
    day: "2-digit",
    month: "short",
    year: "numeric",
    timeZone,
  })
    .format(d)
    .replace(/\sde\s/g, " ")
    .replace(/\./g, "");
}

/**
 * Formata um periodo de viagem em pt-BR a partir de duas datas ISO:
 * "22 mai 2026 — 30 mai 2026". Quando uma das pontas e invalida, devolve so a
 * valida; quando ambas sao invalidas, devolve string vazia.
 */
export function formatTripPeriod(
  start: string,
  end: string,
  timeZone: string = APP_TIME_ZONE,
): string {
  const from = formatShortDate(start, timeZone);
  const to = formatShortDate(end, timeZone);
  if (from && to) return `${from} — ${to}`;
  return from || to;
}

/** Saudacao por periodo do dia (pt-BR), baseada na hora no fuso indicado. */
export function greetingForHour(
  date: Date = new Date(),
  timeZone: string = APP_TIME_ZONE,
): string {
  // getHours() leria a hora do processo (UTC no servidor); extraimos a hora no
  // fuso de exibicao via Intl. en-US + hour12:false da um numero limpo "0".."23"
  // (alguns engines emitem "24" a meia-noite — normalizamos com % 24).
  const hourStr = new Intl.DateTimeFormat("en-US", {
    hour: "2-digit",
    hour12: false,
    timeZone,
  }).format(date);
  const h = Number.parseInt(hourStr, 10) % 24;
  if (h < 12) return "Bom dia";
  if (h < 18) return "Boa tarde";
  return "Boa noite";
}

function capitalizeFirst(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}
