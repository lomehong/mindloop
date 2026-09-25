import { Link, useNavigate, useParams } from "react-router";

import { ActivityBadge } from "~/components/activity-badge";
import { MindlogSearch } from "~/components/mindlog-search";
import type { SearchHit } from "~/lib/types";
import { cn } from "~/lib/utils";

const TABS = [
  { key: "timeline", label: "时间线", path: "" },
  { key: "recap", label: "摘要", path: "/recap" },
  { key: "mindlog", label: "思维日志", path: "/mindlog" },
  { key: "mindlog2", label: "思维日志 v2", path: "/mindlog2" },
  { key: "thinkers", label: "思考者", path: "/thinkers" },
  { key: "health", label: "健康", path: "/health" },
  { key: "usage", label: "用量", path: "/usage" },
  { key: "chat", label: "对话", path: "/chat" },
  { key: "memories", label: "记忆", path: "/memories" },
  { key: "skills", label: "技能", path: "/skills" },
  { key: "config", label: "配置", path: "/config" },
] as const;

/** Header row shared by the identity sub-pages: breadcrumb + tab links +
 * mind-log search. The search is standard chrome on every tab; pages that
 * render steps locally (timeline, mind log) pass `search` to jump in place,
 * everywhere else a hit navigates to the mind log's step deeplink. */
export function IdentityTabs({
  identityId,
  live,
  active,
  name,
  actions,
  search,
}: {
  identityId: string;
  live: boolean;
  active: (typeof TABS)[number]["key"];
  name?: string;
  /** Page-specific controls rendered at the far right, after the search. */
  actions?: React.ReactNode;
  /** Page-local search behavior; omit for the default jump-to-mindlog. */
  search?: { windowStart: number; onJump: (hit: SearchHit) => void };
}) {
  useParams(); // keep router context
  const navigate = useNavigate();
  const base = `/i/${encodeURIComponent(identityId)}`;
  const displayName = name ?? identityId.split("~").pop() ?? identityId;
  const searchProps = search ?? {
    windowStart: 0,
    onJump: (hit: SearchHit) =>
      navigate(`${base}/mindlog?step=${encodeURIComponent(hit.step_id)}`),
  };

  return (
    <div className="sticky top-12 z-40 mb-4 flex flex-wrap items-center gap-3 border-b bg-background/95 py-2 backdrop-blur">
      <Link to="/" className="text-sm text-muted-foreground hover:underline">
        identities
      </Link>
      <span className="text-muted-foreground">/</span>
      <h1 className="font-mono text-lg font-semibold">{displayName}</h1>
      <ActivityBadge identityId={identityId} live={live} />
      <nav className="ml-auto flex max-w-full items-center gap-1 overflow-x-auto rounded-lg border p-0.5 max-sm:ml-0 max-sm:w-full">
        {TABS.map((tab) => (
          <Link
            key={tab.key}
            to={`${base}${tab.path}`}
            aria-current={tab.key === active ? "page" : undefined}
            className={cn(
              "shrink-0 rounded-md px-2.5 py-1 text-xs transition-colors",
              tab.key === active
                ? "bg-primary/10 font-medium text-primary"
                : "text-muted-foreground hover:bg-accent hover:text-foreground"
            )}
          >
            {tab.label}
          </Link>
        ))}
      </nav>
      <MindlogSearch
        identityId={identityId}
        windowStart={searchProps.windowStart}
        onJump={searchProps.onJump}
      />
      {actions}
    </div>
  );
}
