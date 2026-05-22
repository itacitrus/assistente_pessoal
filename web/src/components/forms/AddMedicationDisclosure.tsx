"use client";

import * as React from "react";
import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import {
  MedicationForm,
  type MedicationTarget,
} from "@/components/forms/MedicationForm";

export interface AddMedicationDisclosureProps {
  target: MedicationTarget;
}

/**
 * Botão "Cadastrar remédio" que revela o formulário só ao ser clicado — o form
 * fica recolhido por padrão (em vez de sempre aberto abaixo da lista). Após
 * salvar com sucesso (MedicationForm chama `onDone`) o form recolhe sozinho.
 */
export function AddMedicationDisclosure({
  target,
}: AddMedicationDisclosureProps) {
  const [open, setOpen] = React.useState(false);

  if (!open) {
    return (
      <Button type="button" onClick={() => setOpen(true)}>
        <Plus className="h-4 w-4" aria-hidden />
        Cadastrar remédio
      </Button>
    );
  }

  return (
    <Card className="shadow-warm">
      <CardHeader className="flex flex-row items-start justify-between gap-3">
        <div className="space-y-1.5">
          <CardTitle className="text-lg">Adicionar remédio</CardTitle>
          <CardDescription>
            Nome, dose, horários, frequência e por quanto tempo o Zello deve
            lembrar.
          </CardDescription>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => setOpen(false)}
          className="shrink-0"
        >
          Cancelar
        </Button>
      </CardHeader>
      <CardContent>
        <MedicationForm target={target} onDone={() => setOpen(false)} />
      </CardContent>
    </Card>
  );
}
