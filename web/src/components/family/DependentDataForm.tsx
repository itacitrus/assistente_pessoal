"use client";

import * as React from "react";
import { useRouter } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { PhoneInput } from "@/components/forms/PhoneInput";
import { ResendWelcomeButton } from "@/components/family/ResendWelcomeButton";
import { ApiError } from "@/lib/api";
import { updateDependent } from "@/lib/api/family";
import { isValidPhoneBR, normalizePhoneE164BR } from "@/lib/masks";

export interface DependentDataFormProps {
  dependentId: number;
  initialName: string;
  /** Telefone vindo do backend em E.164 ("55" + DDD + número). */
  initialPhoneE164: string;
}

/** Converte E.164 BR ("5561..." ) para os dígitos locais (DDD + número) que o
 * PhoneInput manipula. Tira o prefixo "55" só quando o número tem o tamanho
 * de um telefone nacional com código do país. */
function e164ToLocal(e164: string): string {
  const digits = e164.replace(/\D+/g, "");
  if (digits.startsWith("55") && digits.length >= 12) {
    return digits.slice(2);
  }
  return digits;
}

export function DependentDataForm({
  dependentId,
  initialName,
  initialPhoneE164,
}: DependentDataFormProps) {
  const router = useRouter();
  const [name, setName] = React.useState(initialName);
  const [phone, setPhone] = React.useState(() => e164ToLocal(initialPhoneE164));
  const [saving, setSaving] = React.useState(false);
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);
  const [saved, setSaved] = React.useState(false);

  const initialLocal = e164ToLocal(initialPhoneE164);
  const dirty = name.trim() !== initialName.trim() || phone !== initialLocal;
  const canSave =
    dirty && name.trim().length >= 2 && isValidPhoneBR(phone) && !saving;

  async function handleSave() {
    setSaving(true);
    setErrorMsg(null);
    setSaved(false);
    try {
      const e164 = normalizePhoneE164BR(phone);
      if (!e164) {
        setSaving(false);
        setErrorMsg("Telefone inválido. Use DDD + número.");
        return;
      }
      await updateDependent(dependentId, { name: name.trim(), phone: e164 });
      setSaved(true);
      setSaving(false);
      router.refresh();
    } catch (err) {
      setSaving(false);
      setErrorMsg(
        err instanceof ApiError
          ? err.message
          : "Não consegui salvar agora. Tente novamente.",
      );
    }
  }

  return (
    <div className="space-y-4 rounded-lg border bg-card p-5 shadow-warm">
      <div>
        <h2 className="font-display text-lg font-semibold tracking-tight">
          Dados do dependente
        </h2>
        <p className="text-sm text-muted-foreground">
          O telefone é como o Zello fala com a pessoa no WhatsApp — confira se
          está certo.
        </p>
      </div>

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <div className="space-y-2">
          <Label htmlFor="dep-name">Nome</Label>
          <Input
            id="dep-name"
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setSaved(false);
            }}
          />
        </div>
        <div className="space-y-2">
          <Label htmlFor="dep-phone">Telefone (com DDD)</Label>
          <PhoneInput
            id="dep-phone"
            value={phone}
            onChange={(d) => {
              setPhone(d);
              setSaved(false);
            }}
          />
        </div>
      </div>

      {errorMsg ? (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      ) : null}
      {saved ? (
        <Alert className="border-[--zello-emerald]/30 bg-[--zello-emerald]/5">
          <AlertDescription className="text-[--zello-emerald-deep]">
            Dados atualizados. ✓
          </AlertDescription>
        </Alert>
      ) : null}

      <div className="flex flex-wrap items-center gap-3 border-t border-border/70 pt-4">
        <Button type="button" onClick={handleSave} disabled={!canSave}>
          {saving ? "Salvando..." : "Salvar dados"}
        </Button>
        <ResendWelcomeButton
          dependentId={dependentId}
          dependentName={name || initialName}
        />
      </div>
    </div>
  );
}
