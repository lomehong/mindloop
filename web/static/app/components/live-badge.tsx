import { Badge } from "~/components/ui/badge";

export function LiveBadge() {
  return (
    <Badge className="gap-1.5 border border-primary/35 bg-primary/10 text-primary">
      <span className="relative flex h-2 w-2">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-primary opacity-75" />
        <span className="relative inline-flex h-2 w-2 rounded-full bg-primary" />
      </span>
      运行中
    </Badge>
  );
}
