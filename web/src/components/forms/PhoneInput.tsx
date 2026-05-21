"use client";

import * as React from "react";

import { Input } from "@/components/ui/input";
import { isValidPhoneBR, maskPhone, onlyDigits } from "@/lib/masks";

export interface PhoneInputProps
  extends Omit<
    React.InputHTMLAttributes<HTMLInputElement>,
    "value" | "onChange"
  > {
  /** Sempre digitos puros (sem mascara). */
  value: string;
  /** Emite digitos puros (sem mascara). Backend recebe E.164 do caller. */
  onChange: (digits: string) => void;
  invalidMessage?: string;
}

/**
 * Input controlado para telefone brasileiro.
 *
 * Aplica mascara visualmente (`(11) 99999-8888`), mas o componente trabalha
 * com digitos puros — `value` e `onChange` nao envolvem caracteres de
 * mascara. Persistencia/normalizacao para E.164 (`55...`) acontece no caller
 * (`api/auth.requestLoginLink`, `api/family.createDependent`).
 */
export const PhoneInput = React.forwardRef<HTMLInputElement, PhoneInputProps>(
  function PhoneInput(
    { value, onChange, invalidMessage, onBlur, id, ...rest },
    ref,
  ) {
    const [touched, setTouched] = React.useState(false);
    const display = maskPhone(value);
    const showError = touched && value.length > 0 && !isValidPhoneBR(value);

    const errorId = id ? `${id}-error` : undefined;

    return (
      <div className="flex flex-col gap-1">
        <Input
          ref={ref}
          id={id}
          type="tel"
          inputMode="numeric"
          autoComplete="tel-national"
          placeholder="(11) 99999-8888"
          value={display}
          onChange={(e) => onChange(onlyDigits(e.target.value).slice(0, 11))}
          onBlur={(e) => {
            setTouched(true);
            onBlur?.(e);
          }}
          aria-invalid={showError || undefined}
          aria-describedby={showError ? errorId : undefined}
          {...rest}
        />
        {showError && (
          <span id={errorId} className="text-sm text-red-600">
            {invalidMessage ?? "Telefone inválido. Use DDD + número."}
          </span>
        )}
      </div>
    );
  },
);
