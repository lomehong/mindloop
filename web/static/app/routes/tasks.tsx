// 任务页（/talk/:identityId/tasks）：PWA 外壳 + 共享任务板——任务
// 事实与命令都走 TasksBoard（与桌面身份「任务」tab 同一实现）；本页
// 只提供手机壳（返回对话、身份名、安全区）。

import { ChevronLeft, ListTodo } from "lucide-react";
import { Link, useParams } from "react-router";

import { TasksBoard } from "~/components/tasks-board";

export function meta({ params }: { params: { identityId?: string } }) {
  return [
    {
      title: params.identityId ? `${params.identityId} · 任务` : "mindloop · 任务",
    },
  ];
}

export default function TasksPage() {
  const { identityId = "" } = useParams();
  const identityName = identityId.split("~").pop();

  return (
    <div className="min-h-dvh">
      <header className="sticky top-0 z-10 flex items-center gap-2 border-b bg-background px-2 pb-2 pt-[calc(env(safe-area-inset-top)+0.5rem)]">
        <Link
          to={`/talk/${encodeURIComponent(identityId)}`}
          className="flex h-9 w-9 items-center justify-center rounded-full active:bg-accent"
          aria-label="返回对话"
        >
          <ChevronLeft className="size-5" />
        </Link>
        <ListTodo className="size-4 text-muted-foreground" aria-hidden />
        <span className="font-medium">任务</span>
        <span className="min-w-0 truncate text-sm text-muted-foreground">
          {identityName}
        </span>
      </header>
      <div className="space-y-2 px-3 py-3 pb-[calc(env(safe-area-inset-bottom)+0.75rem)]">
        <TasksBoard
          identityId={identityId}
          emptyHint="还没有任务。回到对话，把要它做的事用「交给 Agent 执行」发出去。"
        />
      </div>
    </div>
  );
}
