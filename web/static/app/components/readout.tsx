import { cn } from "~/lib/utils";

/** 读数格：readout 条里的单格（标签 mono 小字 + 等宽大数字）。 */
export function Ro({
  label,
  value,
  unit,
  className,
}: {
  label: string;
  value: string;
  unit?: string;
  className?: string;
}) {
  return (
    <div className="min-w-[7.5rem] flex-1 border-r border-line px-4 py-2.5 last:border-r-0">
      <div className="mb-1 font-mono text-[10.5px] tracking-[0.1em] text-faint">
        {label}
      </div>
      <div
        className={cn(
          "font-mono text-[21px] font-medium leading-none tracking-[-0.02em] tabular-nums",
          className
        )}
      >
        {value}
        {unit && (
          <span className="ml-1 text-[11px] font-normal text-muted-foreground">
            {unit}
          </span>
        )}
      </div>
    </div>
  );
}

/** 读数条：等分格横向排列，换行时保持格线。 */
export function Readout({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex flex-wrap overflow-hidden rounded-xl border border-line bg-card">
      {children}
    </div>
  );
}
