import { useQuery } from "@tanstack/react-query";
import { ChevronRight } from "lucide-react";
import { useEffect, useState } from "react";
import { useNavigate, useSearchParams } from "react-router";

import { CredentialControl } from "~/components/credential-control";
import { QueryErrorBanner } from "~/components/query-error-banner";
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
  return [{ title: "mindloop · 对话" }];
}

function relativeTime(iso: string | null): string {
  if (!iso) return "";
  const seconds = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 60) return "刚刚";
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes} 分钟前`;
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return `${hours} 小时前`;
  return `${Math.floor(hours / 24)} 天前`;
}

function NamePrompt({ onDone }: { onDone: (name: string) => void }) {
  const [draft, setDraft] = useState("");
  const name = sanitizePwaName(draft);
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-4 px-8">
      <img src="/icons/icon-192.png" alt="" className="h-16 w-16 rounded-2xl" />
      <h1 className="text-lg font-semibold">你是谁？</h1>
      <p className="text-center text-sm text-muted-foreground">
        你发的消息会以这个名字署名，身份由此知道在和谁说话。
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
          placeholder="你的名字"
          autoCapitalize="none"
          autoCorrect="off"
          className="h-10 flex-1"
        />
        <Button type="submit" disabled={!name}>
          开始
        </Button>
      </form>
      {name && (
        <p className="font-mono text-xs text-muted-foreground">
          你将以 pwa-{name} 的身份出现
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

  const { data: identities, isLoading, error, refetch } = useQuery({
    queryKey: ["identities"],
    queryFn: fetchIdentities,
    refetchInterval: 10000,
    enabled: !!name,
  });

  if (!name) {
    return (
      <>
        <div className="px-4 pt-[env(safe-area-inset-top)]"><CredentialControl /></div>
        <NamePrompt
          onDone={(picked) => {
            setPwaName(picked);
            setName(picked);
          }}
        />
      </>
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-lg flex-1 flex-col px-4 pt-[env(safe-area-inset-top)]">
      <CredentialControl />
      <header className="flex items-center justify-between py-4">
        <h1 className="text-lg font-semibold">选择对话对象…</h1>
        <button
          className="font-mono text-xs text-muted-foreground"
          title="更换名字"
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
      ) : error ? (
        <QueryErrorBanner error={error} onRetry={() => void refetch()} />
      ) : !identities || identities.length === 0 ? (
        <p className="py-16 text-center text-sm text-muted-foreground">
          这台服务器上还没有身份。
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
                    ? "清醒中"
                    : "睡眠中——不会回复"}
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
