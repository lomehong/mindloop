import { cn } from "~/lib/utils";

/** 统一加载语言（docs/designs/ui-language.md §8）：灰条脉冲替代页面级
 * 转圈——骨架条/骨架卡按页面轮廓摆放，数值处用同宽灰条占位。 */
export function Skeleton({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="skeleton"
      className={cn("animate-pulse rounded-md bg-muted", className)}
      {...props}
    />
  );
}
