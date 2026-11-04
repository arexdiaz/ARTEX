"use client";

import * as React from "react";

import { ArrowDownIcon, ArrowUpIcon, XIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
} from "@/components/ui/combobox";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import type { LLMProfile, Task } from "@/lib/types";

interface TaskLLMProfileChainProps {
  profiles: LLMProfile[];
  selections?: Task["llm_selections"];
  value: string[];
  onValueChange: (value: string[]) => void;
  activeProfileId?: string;
  onActiveProfileChange?: (value: string) => void;
  disabled?: boolean;
  inputId?: string;
  portalContainer?: React.RefObject<HTMLElement | null>;
}

function ProfileRoleBadge({ index, currentIndex }: { index: number; currentIndex: number }) {
  if (index === currentIndex) return <Badge variant="default">Current</Badge>;
  if (index < currentIndex) {
    return (
      <Badge variant="outline" title="Profiles before the current cursor are not picked by automatic failover">
        Skipped
      </Badge>
    );
  }
  return <Badge variant="secondary">Fallback {index - currentIndex}</Badge>;
}

export function TaskLLMProfileChain({
  profiles,
  selections,
  value,
  onValueChange,
  activeProfileId,
  onActiveProfileChange,
  disabled = false,
  inputId,
  portalContainer,
}: TaskLLMProfileChainProps) {
  const profilesByID = React.useMemo(() => new Map(profiles.map((profile) => [profile.id, profile])), [profiles]);
  const selectionsByID = React.useMemo(() => new Map((selections ?? []).map((selection) => [String(selection.profile_id), selection])), [selections]);
  const itemIDs = React.useMemo(() => profiles.map((profile) => profile.id), [profiles]);
  const profilesUnavailable = profiles.length === 0;
  const currentProfileID = activeProfileId && value.includes(activeProfileId) ? activeProfileId : value[0];
  const currentIndex = value.indexOf(currentProfileID);
  // api_key_hint is display-only and is empty for valid keys shorter than four
  // characters. The task API remains authoritative for profile validation.

  const profileLabel = React.useCallback(
    (id: string) => {
      const profile = profilesByID.get(id);
      return profile ? `${profile.name} ${profile.model || profile.default_model || "Choose a default model first"}` : `Profile #${id}`;
    },
    [profilesByID],
  );

  const move = (index: number, offset: -1 | 1) => {
    const target = index + offset;
    if (target < 0 || target >= value.length) return;
    const next = [...value];
    [next[index], next[target]] = [next[target], next[index]];
    onValueChange(next);
  };

  const remove = (id: string) => {
    const next = value.filter((profileID) => profileID !== id);
    onValueChange(next);
    if (activeProfileId === id) onActiveProfileChange?.(next[0] ?? "");
  };

  const handleValueChange = (next: string[]) => {
    onValueChange(next);
    if (activeProfileId && !next.includes(activeProfileId)) {
      onActiveProfileChange?.(next[0] ?? "");
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <Combobox
        items={itemIDs}
        itemToStringValue={profileLabel}
        multiple
        value={value}
        onValueChange={handleValueChange}
        disabled={disabled}
      >
        <ComboboxChips>
          <ComboboxValue>
            {value.map((id) => (
              <ComboboxChip key={id}>{profilesByID.get(id)?.name ?? `#${id}`}</ComboboxChip>
            ))}
          </ComboboxValue>
          <ComboboxChipsInput
            id={inputId}
            placeholder={profiles.length > 0 ? "Search and add LLM profiles" : "No LLM profiles available"}
            disabled={disabled ? true : profilesUnavailable}
          />
        </ComboboxChips>
        <ComboboxContent portalContainer={portalContainer}>
          <ComboboxEmpty>No matching LLM profiles</ComboboxEmpty>
          <ComboboxList>
            {(id) => {
              const profile = profilesByID.get(id);
              return (
                <ComboboxItem key={id} value={id} disabled={!profile}>
                  <div className="flex min-w-0 flex-1 flex-col">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate">
                        {profile?.name ?? `Profile #${id}`}
                        {profile?.is_default ? " (active)" : ""}
                      </span>
                    </span>
                    {profile && <span className="truncate text-muted-foreground text-xs">{profile.model || profile.default_model || "Default model required"}</span>}
                  </div>
                </ComboboxItem>
              );
            }}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>

      {value.length === 0 ? (
        <p className="text-muted-foreground text-xs">When no provider is selected, the task saves the system default model and reasoning.</p>
      ) : (
        <div className="flex flex-col divide-y rounded-lg border">
          {value.map((id, index) => {
            const profile = profilesByID.get(id);
            const selection = selectionsByID.get(id);
            return (
              <div
                key={id}
                className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 px-2.5 py-2 sm:flex"
              >
                <span className="w-5 shrink-0 text-center text-muted-foreground text-xs tabular-nums">{index + 1}</span>
                <div className="min-w-0 sm:flex-1">
                  <p className="truncate font-medium text-sm">{profile?.name ?? `Profile #${id}`}</p>
                  <div className="flex min-w-0 items-center gap-2">
                    <p className="truncate text-muted-foreground text-xs">{selection?.model || profile?.model || profile?.default_model || "Default model required"}</p>
                  </div>
                  {selection && <p className="truncate text-muted-foreground text-xs">{selection.thinking_type === "disabled" ? "Reasoning off" : selection.reasoning_effort ? `Reasoning ${selection.reasoning_effort}` : "Model default reasoning"}</p>}
                </div>
                <ProfileRoleBadge index={index} currentIndex={currentIndex} />
                <div className="col-span-2 col-start-2 flex shrink-0 items-center gap-1 justify-self-end sm:col-auto sm:justify-self-auto">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Move profile up"
                    onClick={() => move(index, -1)}
                    disabled={disabled ? true : index === 0}
                  >
                    <ArrowUpIcon data-icon="inline-start" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Move profile down"
                    onClick={() => move(index, 1)}
                    disabled={disabled ? true : index === value.length - 1}
                  >
                    <ArrowDownIcon data-icon="inline-start" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label="Remove profile"
                    onClick={() => remove(id)}
                    disabled={disabled}
                  >
                    <XIcon data-icon="inline-start" />
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {onActiveProfileChange && value.length > 0 && (
        <div className="flex flex-col items-stretch gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
          <span className="font-medium text-sm">Current profile</span>
          <Select
            value={activeProfileId && value.includes(activeProfileId) ? activeProfileId : value[0]}
            onValueChange={onActiveProfileChange}
            disabled={disabled}
          >
            <SelectTrigger size="sm" className="w-full sm:min-w-48 sm:max-w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {value.map((id) => {
                  const profile = profilesByID.get(id);
                  return (
                    <SelectItem key={id} value={id} disabled={!profile}>
                      {profile?.name ?? `Profile #${id}`}
                    </SelectItem>
                  );
                })}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      )}
    </div>
  );
}
