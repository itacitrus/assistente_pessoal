"use client";

import * as React from "react";
import { useRouter } from "next/navigation";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { PhoneInput } from "@/components/forms/PhoneInput";
import { TimezoneSelect } from "@/components/forms/TimezoneSelect";
import { ApiError } from "@/lib/api";
import { createDependent } from "@/lib/api/family";
import { isValidPhoneBR } from "@/lib/masks";
import { DEFAULT_TIMEZONE } from "@/lib/timezones";

type Status = "idle" | "submitting" | "error";

export function DependentForm() {
  const router = useRouter();
  const [name, setName] = React.useState("");
  const [phone, setPhone] = React.useState("");
  const [relationship, setRelationship] = React.useState("");
  const [timezone, setTimezone] = React.useState<string>(DEFAULT_TIMEZONE);
  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  const canSubmit =
    name.trim().length >= 2 &&
    isValidPhoneBR(phone) &&
    relationship.trim().length >= 2 &&
    status !== "submitting";

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (!canSubmit) return;
    setStatus("submitting");
    setErrorMsg(null);
    try {
      const res = await createDependent({
        name: name.trim(),
        phone,
        relationship: relationship.trim(),
        timezone,
      });
      router.push(`/dashboard/family/${res.user.id}`);
    } catch (err) {
      setStatus("error");
      if (err instanceof ApiError) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg(
          "Nao consegui cadastrar agora. Tente novamente em alguns segundos.",
        );
      }
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-6" noValidate>
      <div className="space-y-2">
        <Label htmlFor="dep-name">Nome</Label>
        <Input
          id="dep-name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          placeholder="Vovo Joana"
          autoComplete="name"
          required
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="dep-phone">Telefone (com DDD)</Label>
        <PhoneInput
          id="dep-phone"
          value={phone}
          onChange={setPhone}
          required
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="dep-relationship">Relacao</Label>
        <Input
          id="dep-relationship"
          value={relationship}
          onChange={(e) => setRelationship(e.target.value)}
          placeholder="filha, filho, esposa, mae..."
          required
        />
      </div>

      <div className="space-y-2">
        <Label htmlFor="dep-tz">Fuso horario</Label>
        <TimezoneSelect id="dep-tz" value={timezone} onChange={setTimezone} />
      </div>

      {status === "error" && errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      <Button type="submit" disabled={!canSubmit} className="w-full">
        {status === "submitting" ? "Cadastrando..." : "Cadastrar"}
      </Button>
    </form>
  );
}
