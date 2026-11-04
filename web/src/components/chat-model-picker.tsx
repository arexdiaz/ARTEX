"use client";

import * as React from "react";

import Link from "next/link";

import {
  Brain,
  Check,
  ChevronDown,
  ChevronRight,
  Loader2,
  RefreshCw,
  Search,
  Settings2,
  Sparkles,
  X,
} from "lucide-react";
import { toast } from "sonner";

import { Checkbox } from "@/components/ui/checkbox";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { getLocalStorageValue, setLocalStorageValue } from "@/lib/local-storage.client";
import type { LLMProfile } from "@/lib/types";
import { cn } from "@/lib/utils";

export interface ChatModelChoice {
  profileId: number | null;
  model: string;
  effort: string | null;
}

const visibilityKey = "artex.chat.hidden-models";
const visibilityEvent = "artex:model-visibility";
const modelKey = (profile: string, model: string) => JSON.stringify([profile, model]);
const catalogCache = new Map<string, { models: string[]; fetched: number }>();
const catalogRequests = new Map<string, Promise<string[]>>();
const efforts = [
  { value: null, label: "Profile default" },
  { value: "", label: "Model default" },
  { value: "off", label: "Off" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
  { value: "xhigh", label: "Extra high" },
  { value: "max", label: "Max" },
] as const;
const modelLabel = (model: string) => model.split("/").at(-1) || model;

async function fetchModels(profile: LLMProfile, force: boolean): Promise<string[]> {
  if (profile.model.trim()) return [profile.model.trim()];
  const key = JSON.stringify([profile.id, profile.format, profile.base_url, profile.proxy, profile.api_key_hint]);
  const cached = catalogCache.get(key);
  if (!force && cached && Date.now() - cached.fetched < 300_000) return cached.models;
  const existing = catalogRequests.get(key);
  if (existing) return existing;
  const request = api
    .fetchLLMModels(profile.format, profile.base_url ?? "", "", profile.proxy ?? "", Number(profile.id))
    .then((result) => {
      if (!result.ok) throw new Error(result.error || "Could not discover models");
      const models = [...new Set((result.models || []).filter((id) => typeof id === "string" && id.trim()))].sort();
      catalogCache.set(key, { models, fetched: Date.now() });
      return models;
    })
    .finally(() => catalogRequests.delete(key));
  catalogRequests.set(key, request);
  return request;
}

export function ChatModelPicker({
  profiles,
  value,
  onChange,
  disabled = false,
  defaultProfileId,
  rightSlot,
}: {
  profiles: LLMProfile[];
  value: ChatModelChoice;
  onChange: (choice: ChatModelChoice) => void | Promise<void>;
  disabled?: boolean;
  defaultProfileId?: number | null;
  rightSlot?: React.ReactNode;
}) {
  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [models, setModels] = React.useState<Record<string, string[]>>({});
  const [errors, setErrors] = React.useState<Record<string, string>>({});
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [hidden, setHidden] = React.useState<Set<string>>(() => new Set());
  const [collapsed, setCollapsed] = React.useState<Set<string>>(() => new Set());
  const [reasoning, setReasoning] = React.useState<{ profile: LLMProfile; model: string } | null>(null);
  const generation = React.useRef(0);
  const searchRef = React.useRef<HTMLInputElement>(null);
  const listRef = React.useRef<HTMLDivElement>(null);
  const defaultProfile = profiles.find((p) =>
    defaultProfileId != null ? Number(p.id) === defaultProfileId : p.is_default,
  );
  const currentProfile =
    value.profileId != null ? profiles.find((p) => Number(p.id) === value.profileId) : defaultProfile;
  const currentModel = value.model || currentProfile?.model || "";
  const effort = value.effort ?? currentProfile?.reasoning_effort ?? "";
  const effortLabel = efforts.find((option) => option.value === effort)?.label || effort;

  React.useEffect(() => {
    function restore() {
      try {
        const stored: unknown = JSON.parse(getLocalStorageValue(visibilityKey) ?? "[]");
        setHidden(
          new Set(Array.isArray(stored) ? stored.filter((item): item is string => typeof item === "string") : []),
        );
      } catch {
        setHidden(new Set());
      }
    }
    restore();
    window.addEventListener("storage", restore);
    window.addEventListener(visibilityEvent, restore);
    return () => {
      window.removeEventListener("storage", restore);
      window.removeEventListener(visibilityEvent, restore);
    };
  }, []);

  const load = React.useCallback(
    async (force = false) => {
      const seq = ++generation.current;
      setLoading(true);
      const results = await Promise.allSettled(profiles.map((p) => fetchModels(p, force)));
      if (seq !== generation.current) return;
      const next: Record<string, string[]> = {};
      const failures: Record<string, string> = {};
      results.forEach((result, index) => {
        const id = profiles[index].id;
        if (result.status === "fulfilled") next[id] = result.value;
        else failures[id] = result.reason instanceof Error ? result.reason.message : "Could not discover models";
      });
      setModels(next);
      setErrors(failures);
      setLoading(false);
    },
    [profiles],
  );

  React.useEffect(() => {
    if (open) void load();
    return () => {
      generation.current++;
    };
  }, [open, load]);

  function show(next: boolean) {
    setOpen(next);
    if (next) {
      setEditing(false);
      setReasoning(null);
      setQuery("");
    }
  }

  function saveHidden(next: Set<string>) {
    setHidden(next);
    setLocalStorageValue(visibilityKey, JSON.stringify([...next]));
    window.dispatchEvent(new Event(visibilityEvent));
  }

  async function choose(choice: ChatModelChoice) {
    if (saving || disabled) return;
    setSaving(true);
    try {
      await onChange(choice);
      show(false);
    } catch (error) {
      toast.error(`Failed to switch model: ${(error as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  function toggleGroup(id: string) {
    setCollapsed((previous) => {
      const next = new Set(previous);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  }

  const filtered = profiles.map((profile) => {
    const available = models[profile.id] ?? (profile.model ? [profile.model] : []);
    const matching = available.filter(
      (model) =>
        (editing || !hidden.has(modelKey(profile.id, model))) &&
        `${profile.name} ${model}`.toLowerCase().includes(query.toLowerCase().trim()),
    );
    return { profile, available, matching };
  });
  const anyMatches = filtered.some((group) => group.matching.length > 0);
  const shownModels = Object.entries(models).flatMap(([id, entries]) =>
    entries.filter((model) => !hidden.has(modelKey(id, model))),
  );

  return (
    <div className="flex min-w-0 shrink-0 items-center gap-1 px-2 pb-1 text-xs">
      <Popover open={open} onOpenChange={show}>
        <PopoverTrigger asChild>
          <button
            type="button"
            disabled={disabled || saving}
            className="flex min-w-0 items-center gap-1 rounded px-1 py-1 hover:bg-accent disabled:opacity-40"
            title={currentProfile ? `${currentProfile.name} · ${currentModel || "Select a model"}` : "Select a model"}
          >
            <Sparkles className="size-3 shrink-0 text-muted-foreground" />
            <span className="truncate">{currentModel ? modelLabel(currentModel) : "Select a model"}</span>
            <ChevronDown className="size-3 shrink-0" />
          </button>
        </PopoverTrigger>
        <PopoverContent
          side="top"
          align="start"
          sideOffset={68}
          className={cn(
            "w-80 max-w-[calc(100vw-2rem)] gap-0 overflow-hidden p-0",
            reasoning && !editing && "w-[540px]",
          )}
          aria-label={editing ? "Edit visible models" : "Choose a model"}
          onEscapeKeyDown={(event) => {
            if (editing || reasoning) {
              event.preventDefault();
              setEditing(false);
              setReasoning(null);
              setQuery("");
            }
          }}
        >
          <div className="flex max-h-[var(--radix-popover-content-available-height)] flex-col overflow-y-auto sm:flex-row">
            <div className="flex min-w-0 flex-1 flex-col">
              {editing && (
                <div className="flex items-center justify-between px-3 pt-2">
                  <h2 className="text-xs font-semibold">Models</h2>
                  <button
                    type="button"
                    className="rounded p-1 hover:bg-accent"
                    aria-label="Done editing models"
                    onClick={() => {
                      setEditing(false);
                      setQuery("");
                    }}
                  >
                    <X className="size-3.5" />
                  </button>
                </div>
              )}
              <div className="flex items-center gap-2 border-b px-3 py-2.5">
                <Search className="size-3.5 shrink-0 text-muted-foreground" />
                <input
                  ref={searchRef}
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="Search models…"
                  aria-label={editing ? "Search models to show or hide" : "Search models or providers"}
                  className="min-w-0 flex-1 bg-transparent text-xs outline-none"
                  onKeyDown={(event) => {
                    if (event.key === "ArrowDown") {
                      event.preventDefault();
                      listRef.current?.querySelector<HTMLButtonElement>("button")?.focus();
                    }
                  }}
                />
              </div>
              {!editing && (
                <div className="flex items-center justify-between border-b px-3 py-1.5">
                  <span className="text-xs font-medium">Models</span>
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      onClick={() => {
                        setEditing(true);
                        setReasoning(null);
                        setQuery("");
                      }}
                      className="flex items-center gap-1 rounded px-1 py-1 text-[11px] text-muted-foreground hover:bg-accent"
                    >
                      <Settings2 className="size-3" />
                      Edit models
                    </button>
                    <button
                      type="button"
                      aria-label="Refresh models"
                      disabled={loading}
                      onClick={() => void load(true)}
                      className="rounded p-1 hover:bg-accent"
                    >
                      <RefreshCw className={cn("size-3", loading && "animate-spin")} />
                    </button>
                  </div>
                </div>
              )}
              <div
                ref={listRef}
                role="group"
                aria-label={editing ? "Model visibility" : "Available models"}
                className="max-h-[min(420px,55svh)] overflow-y-auto overscroll-contain p-1"
                onKeyDown={(event) => {
                  if (event.key !== "ArrowDown" && event.key !== "ArrowUp") return;
                  const buttons = [...event.currentTarget.querySelectorAll<HTMLButtonElement>("button:not(:disabled)")];
                  const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
                  if (index < 0) return;
                  event.preventDefault();
                  buttons[(index + (event.key === "ArrowDown" ? 1 : -1) + buttons.length) % buttons.length]?.focus();
                }}
              >
                {!editing &&
                  !query &&
                  defaultProfile?.model &&
                  !hidden.has(modelKey(defaultProfile.id, defaultProfile.model)) && (
                    <button
                      type="button"
                      disabled={saving}
                      className="flex w-full items-center justify-between rounded px-2 py-2 text-left text-xs hover:bg-accent"
                      onClick={() => void choose({ profileId: null, model: "", effort: null })}
                    >
                      Default ({defaultProfile.name}){value.profileId == null && <Check className="size-3" />}
                    </button>
                  )}
                {filtered.map(({ profile, available, matching }) => {
                  if (!matching.length && !errors[profile.id]) return null;
                  const expanded = !collapsed.has(profile.id) || !!query;
                  const enabled = available.filter((model) => !hidden.has(modelKey(profile.id, model))).length;
                  const allEnabled = available.length > 0 && enabled === available.length;
                  const checked = enabled === 0 ? false : allEnabled || "indeterminate";
                  return (
                    <div key={profile.id} className="pb-1">
                      <div className="flex items-center gap-2 px-2">
                        <button
                          type="button"
                          onClick={() => toggleGroup(profile.id)}
                          aria-expanded={expanded}
                          className="flex min-w-0 flex-1 items-center gap-1 py-2 text-left text-[10px] font-semibold uppercase text-muted-foreground"
                        >
                          {expanded ? (
                            <ChevronDown className="size-3 shrink-0" />
                          ) : (
                            <ChevronRight className="size-3 shrink-0" />
                          )}
                          <span className="truncate">{profile.name}</span>
                        </button>
                        {editing && (
                          <Checkbox
                            disabled={!available.length}
                            checked={checked}
                            aria-label={`Show all models from ${profile.name}`}
                            className="data-[state=checked]:border-emerald-600 data-[state=checked]:bg-emerald-600 data-[state=indeterminate]:border-emerald-600 data-[state=indeterminate]:bg-emerald-600 data-[state=indeterminate]:text-white"
                            onCheckedChange={(checked) => {
                              const next = new Set(hidden);
                              for (const model of available) {
                                checked
                                  ? next.delete(modelKey(profile.id, model))
                                  : next.add(modelKey(profile.id, model));
                              }
                              saveHidden(next);
                            }}
                          />
                        )}
                      </div>
                      {errors[profile.id] && (
                        <p className="px-3 pb-2 text-[11px] text-destructive">{errors[profile.id]}</p>
                      )}
                      {expanded &&
                        matching.map((model) => {
                          const selected = currentProfile?.id === profile.id && currentModel === model;
                          return editing ? (
                            <div
                              key={model}
                              className="flex min-h-8 items-center justify-between gap-3 rounded px-3 hover:bg-accent"
                            >
                              <span className="truncate text-xs" title={model}>
                                {modelLabel(model)}
                              </span>
                              <Switch
                                size="sm"
                                checked={!hidden.has(modelKey(profile.id, model))}
                                aria-label={`Show ${model} from ${profile.name}`}
                                className="data-[state=checked]:bg-emerald-600"
                                onCheckedChange={(checked) => {
                                  const next = new Set(hidden);
                                  checked
                                    ? next.delete(modelKey(profile.id, model))
                                    : next.add(modelKey(profile.id, model));
                                  saveHidden(next);
                                }}
                              />
                            </div>
                          ) : (
                            <div
                              key={model}
                              className={cn("flex items-center rounded px-2 hover:bg-accent", selected && "bg-accent")}
                            >
                              <button
                                type="button"
                                disabled={saving}
                                className="flex min-w-0 flex-1 items-center gap-2 py-2 text-left text-xs"
                                title={model}
                                aria-pressed={selected}
                                onClick={() => void choose({ profileId: Number(profile.id), model, effort: null })}
                              >
                                <span className="min-w-0 flex-1 truncate">{modelLabel(model)}</span>
                                {selected && <Check className="size-3 shrink-0" />}
                              </button>
                              <button
                                type="button"
                                className="rounded p-1 text-muted-foreground hover:bg-background"
                                aria-label={`Choose reasoning for ${model} from ${profile.name}`}
                                onClick={() => setReasoning({ profile, model })}
                              >
                                <ChevronRight className="size-3" />
                              </button>
                            </div>
                          );
                        })}
                    </div>
                  );
                })}
                {loading && (
                  <div className="flex items-center justify-center gap-2 py-4 text-xs text-muted-foreground">
                    <Loader2 className="size-3 animate-spin" />
                    Discovering models…
                  </div>
                )}
                {!loading && !anyMatches && (
                  <div className="px-3 py-5 text-center text-xs text-muted-foreground">
                    {query ? "No matching models" : "No models shown"}
                    {!editing && (
                      <button
                        type="button"
                        className="mt-2 block w-full text-primary"
                        onClick={() => {
                          setEditing(true);
                          setQuery("");
                        }}
                      >
                        Edit models
                      </button>
                    )}
                  </div>
                )}
              </div>
              <div className="flex items-center justify-between gap-2 border-t px-3 py-2 text-[10px] text-muted-foreground">
                <Link href="/system/llm" className="hover:text-foreground">
                  Manage providers
                </Link>
                {editing ? (
                  <button type="button" onClick={() => saveHidden(new Set())} className="hover:text-foreground">
                    Reset to defaults
                  </button>
                ) : (
                  <span>{shownModels.length ? "Auto-discovery" : "Select models to show"}</span>
                )}
              </div>
            </div>
            {reasoning && !editing && (
              <div className="w-full shrink-0 border-t p-2 sm:w-52 sm:border-t-0 sm:border-l">
                <div className="flex items-center justify-between p-1 text-xs font-medium">
                  <span className="flex items-center gap-1">
                    <Brain className="size-3" />
                    Reasoning
                  </span>
                  <button
                    type="button"
                    aria-label="Close reasoning"
                    onClick={() => setReasoning(null)}
                    className="rounded p-1 hover:bg-accent"
                  >
                    <X className="size-3" />
                  </button>
                </div>
                <p className="truncate px-1 pb-2 text-[10px] text-muted-foreground" title={reasoning.model}>
                  {modelLabel(reasoning.model)}
                </p>
                {efforts.map((option) => (
                  <button
                    key={option.value ?? "inherit"}
                    type="button"
                    disabled={saving}
                    className="flex w-full items-center justify-between rounded px-2 py-1.5 text-left text-xs hover:bg-accent"
                    onClick={() =>
                      void choose({
                        profileId: Number(reasoning.profile.id),
                        model: reasoning.model,
                        effort: option.value,
                      })
                    }
                  >
                    {option.label}
                    {value.profileId === Number(reasoning.profile.id) &&
                      value.model === reasoning.model &&
                      value.effort === option.value && <Check className="size-3" />}
                  </button>
                ))}
                <p className="px-1 pt-3 text-[10px] text-muted-foreground">
                  Available levels depend on the model and provider.
                </p>
              </div>
            )}
          </div>
        </PopoverContent>
      </Popover>
      {currentProfile && currentModel && (
        <>
          <span className="text-border">|</span>
          <button
            type="button"
            disabled={disabled || saving}
            className="flex shrink-0 items-center gap-1 rounded px-1 py-1 text-muted-foreground hover:bg-accent disabled:opacity-40"
            aria-label="Choose reasoning effort"
            onClick={() => {
              show(true);
              setReasoning({ profile: currentProfile, model: currentModel });
            }}
          >
            <Brain className="size-3" />
            {effortLabel}
            <ChevronDown className="size-3" />
          </button>
        </>
      )}
      {rightSlot}
    </div>
  );
}
