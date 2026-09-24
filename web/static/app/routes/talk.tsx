import { useQuery } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { Button } from "~/components/ui/button";
import { Input } from "~/components/ui/input";
import { LoadingDots } from "~/components/ui/loading-dots";
import { fetchIdentities } from "~/lib/api";
import {
  getLastIdentity,
  getPwaName,
  sanitizePwaName,
  setPwaName,
} from "~/lib/pwa";

export function meta() {
  return [{ title: "Headlong · talk" }];
}

function relativeTime(iso: string | null): string {
  if (!iso) return "";
  const seconds = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 60) return "just now";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
}

function NamePrompt({ onDone }: { onDone: (name: string) => void }) {
  const [draft, setDraft] = useState("");
  const name = sanitizePwaName(draft);
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 px-8">
      <img src="/icons/icon-192.png" alt="" className="h-16 w-16 rounded-2xl" />
      <h1 className="text-lg font-semibold">Who's talking?</h1>
      <p className="text-center text-sm text-muted-foreground">
        Messages you send are signed with this name, so the identity knows who
        it's talking to.
      </p>
      <form
        className="flex w-full max-w-xs items-center gap-2"
        onSubmit={(event) => {
          event.preventDefault();
          if (name) onDone(name);
        }}
      >
        <Input
          autoFocus
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          placeholder="your first name"
          autoCapitalize="none"
          autoCorrect="off"
          className="h-10 flex-1"
        />
        <Button type="submit" disabled={!name}>
          Start
        </Button>
      </form>
      {name && (
        <p className="font-mono text-xs text-muted-foreground">
          you'll appear as pwa-{name}
        </p>
      )}
    </div>
  );
}

export default function TalkHome() {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const [name, setName] = useState<string | null>(getPwaName);
  // ?pick=1 (the back link from a conversation) suppresses the auto-forward
  // to the last-used identity so the picker is actually reachable.
  const picking = searchParams.get("pick") === "1";

  useEffect(() => {
    if (!name || picking) return;
    const last = getLastIdentity();
    if (last) navigate(`/talk/${encodeURIComponent(last)}`, { replace: true });
  }, [name, picking, navigate]);

  const { data: identities, isLoading } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 10000,
    enabled: !!name,
  });

  if (!name) {
    return (
      <NamePrompt
        onDone={(picked) => {
          setPwaName(picked);
          setName(picked);
        }}
      />
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-lg flex-1 flex-col px-4 pt-[env(safe-area-inset-top)]">
      <header className="flex items-center justify-between py-4">
        <h1 className="text-lg font-semibold">Talk to…</h1>
        <button
          className="font-mono text-xs text-muted-foreground"
          title="Change your name"
          onClick={() => {
            setName(null);
          }}
        >
          pwa-{name}
        </button>
      </header>
      {isLoading ? (
        <div className="flex justify-center py-16">
          <LoadingDots />
        </div>
      ) : !identities || identities.length === 0 ? (
        <p className="py-16 text-center text-sm text-muted-foreground">
          No identities found on this server.
        </p>
      ) : (
        <div className="flex flex-col gap-2 pb-8">
          {identities.map((identity) => (
            <button
              key={identity.id}
              className="flex items-center gap-3 rounded-xl border bg-card px-4 py-3 text-left active:bg-accent"
              onClick={() =>
                navigate(`/talk/${encodeURIComponent(identity.id)}`)
              }
            >
              <span className="relative flex h-2.5 w-2.5 shrink-0">
                {identity.live && (
                  <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-green-500 opacity-75" />
                )}
                <span
                  className={`relative inline-flex h-2.5 w-2.5 rounded-full ${
                    identity.live ? "bg-green-500" : "bg-muted-foreground/30"
                  }`}
                />
              </span>
              <span className="flex-1">
                <span className="block font-medium">{identity.name}</span>
                <span className="block text-xs text-muted-foreground">
                  {identity.dispatcher?.running
                    ? "awake"
                    : "asleep — won't answer"}
                  {identity.last_activity_ts
                    ? ` · ${relativeTime(identity.last_activity_ts)}`
                    : ""}
                </span>
              </span>
              <ChevronRight className="size-4 text-muted-foreground" />
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
