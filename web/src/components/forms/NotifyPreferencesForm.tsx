"use client";

import * as React from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { ApiError } from "@/lib/api";
import { updateLinkNotifications } from "@/lib/api/family";
import type { FamilyLink, UpdateLinkNotifyBody } from "@/types/api";

export interface NotifyPreferencesFormProps {
  link: FamilyLink;
}

type Status = "idle" | "saving" | "saved" | "error";

export function NotifyPreferencesForm({ link }: NotifyPreferencesFormProps) {
  const [medication, setMedication] = React.useState(
    link.notify.on_medication_miss,
  );
  const [inactivity, setInactivity] = React.useState(link.notify.on_inactivity);
  const [severeSignal, setSevereSignal] = React.useState(
    link.notify.on_severe_signal,
  );

  const [status, setStatus] = React.useState<Status>("idle");
  const [errorMsg, setErrorMsg] = React.useState<string | null>(null);

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setStatus("saving");
    setErrorMsg(null);
    const body: UpdateLinkNotifyBody = {
      on_medication_miss: medication,
      on_inactivity: inactivity,
      on_severe_signal: severeSignal,
    };
    try {
      await updateLinkNotifications(link.id, body);
      setStatus("saved");
    } catch (err) {
      setStatus("error");
      if (err instanceof ApiError) {
        setErrorMsg(err.message);
      } else {
        setErrorMsg("Nao consegui salvar agora. Tente novamente.");
      }
    }
  }

  return (
    <form onSubmit={handleSubmit} className="space-y-4">
      <ToggleRow
        id="notify-medication"
        label="Avisar quando uma dose nao for tomada"
        checked={medication}
        onChange={setMedication}
      />
      <ToggleRow
        id="notify-inactivity"
        label="Avisar se ele(a) ficar muito tempo sem responder"
        checked={inactivity}
        onChange={setInactivity}
      />
      <ToggleRow
        id="notify-severe"
        label="Avisar quando o Zello identificar um sinal preocupante"
        checked={severeSignal}
        onChange={setSevereSignal}
      />

      {status === "error" && errorMsg && (
        <Alert variant="destructive">
          <AlertDescription>{errorMsg}</AlertDescription>
        </Alert>
      )}

      {status === "saved" && (
        <Alert variant="success">
          <AlertDescription>Preferencias salvas.</AlertDescription>
        </Alert>
      )}

      <Button type="submit" disabled={status === "saving"}>
        {status === "saving" ? "Salvando..." : "Salvar"}
      </Button>
    </form>
  );
}

function ToggleRow({
  id,
  label,
  checked,
  onChange,
}: {
  id: string;
  label: string;
  checked: boolean;
  onChange: (next: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-4 rounded-md border p-3">
      <Label htmlFor={id} className="cursor-pointer text-base font-normal">
        {label}
      </Label>
      <Switch id={id} checked={checked} onCheckedChange={onChange} />
    </div>
  );
}
