import Link from "next/link";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import type { DependentEntry } from "@/types/api";

export interface DependentListProps {
  dependents: DependentEntry[];
}

export function DependentList({ dependents }: DependentListProps) {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
      {(dependents ?? []).map((d) => (
        <DependentCard key={d.user.id} entry={d} />
      ))}
      <Card className="flex flex-col items-center justify-center border-dashed bg-muted/30 p-6 text-center">
        <CardTitle className="text-lg">Adicionar pessoa</CardTitle>
        <CardDescription className="mt-2 text-sm">
          Cadastre alguém que você cuida.
        </CardDescription>
        <Button asChild className="mt-4" variant="outline">
          <Link href="/dashboard/family/new">Adicionar</Link>
        </Button>
      </Card>
    </div>
  );
}

function DependentCard({ entry }: { entry: DependentEntry }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-lg">{entry.user.name}</CardTitle>
        {entry.link.relationship && (
          <CardDescription className="text-sm capitalize">
            {entry.link.relationship}
          </CardDescription>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-2">
        <Button asChild variant="default">
          <Link href={`/dashboard/family/${entry.user.id}`}>
            Ver detalhes
          </Link>
        </Button>
        <Button asChild variant="outline">
          <Link href={`/dashboard/family/${entry.user.id}/evolucao`}>
            Ver evolução
          </Link>
        </Button>
        <Button asChild variant="ghost">
          <Link href={`/dashboard/family/${entry.user.id}/preferences`}>
            Notificações
          </Link>
        </Button>
      </CardContent>
    </Card>
  );
}
