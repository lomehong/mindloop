import { AlertTriangle, RotateCw } from "lucide-react";

import { Button } from "~/components/ui/button";

/** Inline error state for first-screen queries. Pairs with the global
 * QueryCache toast in root.tsx: the toast is the moment-level signal,
 * this banner is the persistent, retryable explanation on the page. */
export function QueryErrorBanner({
  error,
  onRetry,
}: {
  error: unknown;
  onRetry: () => void;
}) {
  const message = error instanceof Error ? error.message : String(error);
  return (
    <div
      role="alert"
      className="flex flex-wrap items-center gap-3 rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-3 text-sm"
    >
      <AlertTriangle className="size-4 shrink-0 text-destructive" />
      <span>
        加载失败：<span className="font-medium">{message}</span>
        <span className="ml-2 text-muted-foreground">
          请确认 mindloop web 服务是否在运行。
        </span>
      </span>
      <div className="ml-auto">
        <Button size="sm" variant="outline" onClick={onRetry}>
          <RotateCw className="size-3" />
          重试
        </Button>
      </div>
    </div>
  );
}
