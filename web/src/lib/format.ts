/**
 * Helpers de formatacao pt-BR para datas, horas e tempo relativo.
 *
 * Toda formatacao usa o locale `pt-BR`. As funcoes sao defensivas: entradas
 * invalidas (string vazia, ISO malformada) devolvem string vazia em vez de
 * lancar, para nunca derrubar a renderizacao de um card.
 */

function parse(iso: string | null | undefined): Date | null {
  if (!iso) return null;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? null : d;
}

/**
 * Formata um instante como "qui, 22 mai · 14h00" (pt-BR). Para eventos de dia
 * inteiro (`allDay`), omite a hora: "qui, 22 mai".
 */
export function formatEventWhen(iso: string, allDay = false): string {
  const d = parse(iso);
  if (!d) return "";

  const day = new Intl.DateTimeFormat("pt-BR", {
    weekday: "short",
    day: "2-digit",
    month: "short",
  })
    .format(d)
    .replace(/\.,/, ",") // "qui., 22 de mai." -> normaliza pontuacao
    .replace(/\sde\s/g, " ")
    .replace(/\.$/, "");

  if (allDay) return capitalizeFirst(day);

  const time = formatTime(d);
  return `${capitalizeFirst(day)} · ${time}`;
}

/** Formata so a hora como "14h00" (pt-BR, 24h). */
export function formatTime(input: string | Date): string {
  const d = input instanceof Date ? input : parse(input);
  if (!d) return "";
  const hh = String(d.getHours()).padStart(2, "0");
  const mm = String(d.getMinutes()).padStart(2, "0");
  return `${hh}h${mm}`;
}

/**
 * Tempo relativo curto e caloroso em pt-BR a partir de um instante passado:
 * "agora", "há 2 min", "há 3h", "ontem", "há 4 dias". Para datas muito
 * antigas (> ~30 dias), cai para a data absoluta "22 mai".
 */
export function formatRelativeTime(iso: string, now: Date = new Date()): string {
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
  })
    .format(d)
    .replace(/\sde\s/g, " ")
    .replace(/\.$/, "");
}

/** Saudacao por periodo do dia (pt-BR), baseada na hora local. */
export function greetingForHour(date: Date = new Date()): string {
  const h = date.getHours();
  if (h < 12) return "Bom dia";
  if (h < 18) return "Boa tarde";
  return "Boa noite";
}

function capitalizeFirst(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}
