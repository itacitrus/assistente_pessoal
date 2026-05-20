"use client";

import * as React from "react";

import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { BR_TIMEZONES } from "@/lib/timezones";

export interface TimezoneSelectProps {
  value: string;
  onChange: (tz: string) => void;
  id?: string;
  disabled?: boolean;
}

export function TimezoneSelect({
  value,
  onChange,
  id,
  disabled,
}: TimezoneSelectProps) {
  return (
    <Select value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger id={id}>
        <SelectValue placeholder="Escolha um fuso" />
      </SelectTrigger>
      <SelectContent>
        {BR_TIMEZONES.map((tz) => (
          <SelectItem key={tz.id} value={tz.id}>
            {tz.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}
