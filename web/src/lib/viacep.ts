/**
 * Cliente ViaCEP. Resolve endereco a partir de CEP de 8 digitos.
 *
 * Regras importantes:
 * - Caller deve garantir que o CEP tem 8 digitos. Funcao retorna null se
 *   nao for valido (em vez de throw) para simplificar o consumo.
 * - Em uso tipico (formulario), o caller chama somente quando o usuario
 *   completou os 8 digitos e usa um `AbortController` para cancelar
 *   requisicoes em andamento se o usuario digitar de novo.
 * - Resultado nao deve sobrescrever campos ja preenchidos pelo usuario
 *   (regra global do produto).
 */

export interface CepLookupResult {
  cep: string; // 8 digitos
  logradouro: string;
  bairro: string;
  cidade: string;
  uf: string;
}

interface ViaCepRaw {
  cep?: string;
  logradouro?: string;
  bairro?: string;
  localidade?: string;
  uf?: string;
  erro?: boolean;
}

export async function parseCepLookup(
  cep: string,
  signal?: AbortSignal,
): Promise<CepLookupResult | null> {
  const digits = cep.replace(/\D+/g, "");
  if (digits.length !== 8) return null;
  const res = await fetch(`https://viacep.com.br/ws/${digits}/json/`, {
    signal,
  });
  if (!res.ok) return null;
  const data: ViaCepRaw = await res.json();
  if (data.erro) return null;
  return {
    cep: digits,
    logradouro: data.logradouro ?? "",
    bairro: data.bairro ?? "",
    cidade: data.localidade ?? "",
    uf: data.uf ?? "",
  };
}
