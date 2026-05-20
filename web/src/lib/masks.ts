/**
 * Helpers de mascara para formularios brasileiros.
 *
 * Regra de ouro: a mascara e camada de apresentacao. Persistencia (request
 * para o backend, estado interno do form) sempre usa apenas digitos
 * (`onlyDigits`). Conversao acontece nos componentes wrapper (`PhoneInput`,
 * `CepInput`).
 */

export const onlyDigits = (s: string): string => s.replace(/\D+/g, "");

/**
 * Mascara progressiva para telefone brasileiro.
 * Aceita 10 digitos (fixo) ou 11 digitos (celular).
 *
 *   "11"           -> "(11"
 *   "1199"         -> "(11) 99"
 *   "11999998888"  -> "(11) 99999-8888"
 *   "1133334444"   -> "(11) 3333-4444"
 *
 * Idempotente: aceitar a propria saida produz o mesmo resultado.
 */
export function maskPhone(input: string): string {
  const d = onlyDigits(input).slice(0, 11);
  if (d.length === 0) return "";
  if (d.length <= 2) return `(${d}`;
  if (d.length <= 6) return `(${d.slice(0, 2)}) ${d.slice(2)}`;
  if (d.length <= 10) return `(${d.slice(0, 2)}) ${d.slice(2, 6)}-${d.slice(6)}`;
  return `(${d.slice(0, 2)}) ${d.slice(2, 7)}-${d.slice(7)}`;
}

/**
 * Mascara de CEP: XXXXX-XXX.
 *
 *   "12345"     -> "12345"
 *   "12345678"  -> "12345-678"
 */
export function maskCep(input: string): string {
  const d = onlyDigits(input).slice(0, 8);
  if (d.length <= 5) return d;
  return `${d.slice(0, 5)}-${d.slice(5)}`;
}

/**
 * Mascara de CPF: XXX.XXX.XXX-XX.
 */
export function maskCpf(input: string): string {
  const d = onlyDigits(input).slice(0, 11);
  if (d.length <= 3) return d;
  if (d.length <= 6) return `${d.slice(0, 3)}.${d.slice(3)}`;
  if (d.length <= 9) return `${d.slice(0, 3)}.${d.slice(3, 6)}.${d.slice(6)}`;
  return `${d.slice(0, 3)}.${d.slice(3, 6)}.${d.slice(6, 9)}-${d.slice(9)}`;
}

/**
 * Normaliza para o formato 55DDDNUMERO (12 ou 13 digitos) que o whatsmeow
 * usa. Aceita entrada com mascara, com ou sem prefixo 55. Retorna null se
 * o numero for invalido.
 */
export function normalizePhoneE164BR(input: string): string | null {
  const d = onlyDigits(input);
  if (d.length === 11 || d.length === 10) return `55${d}`;
  if ((d.length === 13 || d.length === 12) && d.startsWith("55")) return d;
  return null;
}

export function isValidPhoneBR(input: string): boolean {
  return normalizePhoneE164BR(input) !== null;
}

export function isValidCepBR(input: string): boolean {
  return onlyDigits(input).length === 8;
}
