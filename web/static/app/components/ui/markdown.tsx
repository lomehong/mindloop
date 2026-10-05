import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";

import { cn } from "~/lib/utils";

export function Markdown({
  children,
  className,
  proseClassName,
}: {
  children: string;
  className?: string;
  /** prose 内层附加类（如记忆正文的衬线声部）。 */
  proseClassName?: string;
}) {
  return (
    <div className={cn("border bg-card p-4 text-sm", className)}>
      <div
        className={cn(
          "prose prose-sm dark:prose-invert max-w-none",
          proseClassName
        )}
      >
        <ReactMarkdown remarkPlugins={[remarkGfm]}>{children}</ReactMarkdown>
      </div>
    </div>
  );
}
