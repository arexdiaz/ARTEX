"use client";

import * as React from "react";

import { CheckIcon, ChevronDownIcon, Loader2Icon, RefreshCwIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";

// The default is independent of model visibility and never narrows discovery.
export function ProviderModelSelect({
  value,
  onChange,
  loadModels,
  disabled,
  label = "Default model",
}: {
  value: string;
  onChange: (model: string) => void;
  loadModels: () => Promise<string[]>;
  disabled?: boolean;
  label?: string;
}) {
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const [models, setModels] = React.useState<string[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [error, setError] = React.useState("");
  const generation = React.useRef(0);
  React.useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );

  async function refresh() {
    const seq = ++generation.current;
    setLoading(true);
    setError("");
    try {
      const result = await loadModels();
      if (seq === generation.current) setModels(result);
    } catch (e) {
      if (seq === generation.current) setError((e as Error).message);
    } finally {
      if (seq === generation.current) setLoading(false);
    }
  }

  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (next) {
          setQuery("");
          void refresh();
        }
      }}
      modal
    >
      <PopoverTrigger asChild>
        <Button
          type="button"
          variant="outline"
          disabled={disabled}
          aria-label={label}
          className="w-full min-w-0 justify-between gap-2 font-normal"
          title={value || "Choose a default model"}
        >
          <span className="truncate">{value || "Choose a default model"}</span>
          <ChevronDownIcon className="size-3.5 shrink-0 text-muted-foreground" />
        </Button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-[min(26rem,calc(100vw-2rem))] gap-0 p-1">
        <div className="flex gap-1 p-1">
          <Input
            aria-label="Search default models"
            placeholder="Search models"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="h-8"
          />
          <Button
            variant="ghost"
            size="icon"
            aria-label="Refresh default models"
            disabled={loading}
            onClick={() => void refresh()}
          >
            {loading ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
          </Button>
        </div>
        <div
          className="max-h-64 overflow-y-auto overscroll-contain"
          role="listbox"
          aria-label="Available default models"
        >
          {error && (
            <p role="alert" className="p-2 text-destructive text-xs">
              {error}
            </p>
          )}
          {!error && loading && <p className="p-2 text-muted-foreground text-xs">Loading models…</p>}
          {!error &&
            !loading &&
            models
              .filter((model) => model.toLowerCase().includes(query.trim().toLowerCase()))
              .map((model) => (
                <button
                  type="button"
                  role="option"
                  aria-selected={value === model}
                  key={model}
                  className="flex w-full items-center gap-2 rounded-sm px-2 py-2 text-left text-xs hover:bg-accent focus-visible:bg-accent focus-visible:outline-none"
                  onClick={() => {
                    onChange(model);
                    setOpen(false);
                  }}
                >
                  <span className="min-w-0 flex-1 break-all">{model}</span>
                  {value === model && <CheckIcon className="size-3.5 shrink-0" />}
                </button>
              ))}
          {!loading && !error && !models.some((model) => model.toLowerCase().includes(query.trim().toLowerCase())) && (
            <p className="p-2 text-muted-foreground text-xs">No models found</p>
          )}
        </div>
      </PopoverContent>
    </Popover>
  );
}
