"use client";

import * as React from "react";

import { Input } from "@/components/ui/input";
import { isValidCepBR, maskCep, onlyDigits } from "@/lib/masks";
import { parseCepLookup, type CepLookupResult } from "@/lib/viacep";

export interface CepInputProps
  extends Omit<
    React.InputHTMLAttributes<HTMLInputElement>,
    "value" | "onChange"
  > {
  /** Digitos puros (max 8). */
  value: string;
  onChange: (digits: string) => void;
  /**
   * Disparado quando o ViaCEP devolve um endereco valido. O caller deve
   * preencher logradouro/bairro/cidade/uf APENAS se os campos destino
   * estiverem vazios — regra global do produto.
   */
  onLookup?: (result: CepLookupResult) => void;
  invalidMessage?: string;
}

/**
 * Input controlado para CEP brasileiro com autocomplete via ViaCEP.
 *
 * Disparada UMA vez por CEP completo (8 digitos), com `AbortController`
 * cancelando requests pendentes se o usuario continuar digitando. Resultado
 * e propagado via `onLookup` para o form pai decidir como aplicar.
 */
export const CepInput = React.forwardRef<HTMLInputElement, CepInputProps>(
  function CepInput(
    { value, onChange, onLookup, invalidMessage, id, ...rest },
    ref,
  ) {
    const [loading, setLoading] = React.useState(false);
    const [lookupError, setLookupError] = React.useState<string | null>(null);
    const lastLookedUp = React.useRef<string>("");
    const onLookupRef = React.useRef(onLookup);

    React.useEffect(() => {
      onLookupRef.current = onLookup;
    }, [onLookup]);

    React.useEffect(() => {
      if (!isValidCepBR(value)) {
        if (value.length < 8) lastLookedUp.current = "";
        return;
      }
      if (lastLookedUp.current === value) return;
      const ctrl = new AbortController();
      lastLookedUp.current = value;
      setLoading(true);
      setLookupError(null);
      parseCepLookup(value, ctrl.signal)
        .then((res) => {
          if (!res) {
            setLookupError("CEP não encontrado.");
            return;
          }
          onLookupRef.current?.(res);
        })
        .catch((e: unknown) => {
          if ((e as { name?: string })?.name !== "AbortError") {
            setLookupError("Falha ao buscar CEP. Tente de novo.");
          }
        })
        .finally(() => setLoading(false));
      return () => ctrl.abort();
    }, [value]);

    const display = maskCep(value);
    const showInvalid = value.length > 0 && value.length < 8;

    return (
      <div className="flex flex-col gap-1">
        <Input
          ref={ref}
          id={id}
          inputMode="numeric"
          autoComplete="postal-code"
          placeholder="00000-000"
          value={display}
          onChange={(e) => onChange(onlyDigits(e.target.value).slice(0, 8))}
          {...rest}
        />
        {loading && (
          <span className="text-sm text-muted-foreground">
            Buscando endereço...
          </span>
        )}
        {lookupError && (
          <span className="text-sm text-red-600">{lookupError}</span>
        )}
        {showInvalid && !loading && !lookupError && (
          <span className="text-sm text-muted-foreground">
            {invalidMessage ?? "Continue digitando — 8 números."}
          </span>
        )}
      </div>
    );
  },
);
