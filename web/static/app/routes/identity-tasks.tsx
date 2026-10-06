// 身份「任务」tab（桌面）：显式委托的任务面——与 PWA 任务页共享
// TasksBoard；页头动作是同一枚「新的任务」对话框（预选本身份）。

import { Plus } from "lucide-react";
import { parseAsString, useQueryState } from "nuqs";
import { useState } from "react";
import { useParams } from "react-router";

import { NewTaskDialog } from "~/components/new-task-dialog";
import { TasksBoard } from "~/components/tasks-board";
import { Button } from "~/components/ui/button";

export function meta({ params }: { params: { identityId?: string } }) {
  return [
    {
      title: params.identityId ? `${params.identityId} · 任务` : "mindloop · 任务",
    },
  ];
}

export default function IdentityTasks() {
  const { identityId = "" } = useParams();
  const [open, setOpen] = useState(false);
  // ?task= 深链：对话流的任务消息/工作卡入口定位到该任务卡。
  const [focusTask] = useQueryState("task", parseAsString.withDefault(""));
  return (
    <div className="mx-auto w-full max-w-3xl">
      <div className="mb-4 flex items-center gap-3">
        <p className="text-xs text-muted-foreground">
          显式委托：交给它做的事在这里排队、执行、留底。
        </p>
        <span className="flex-1" />
        <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
          <Plus className="size-3" />
          新的任务
        </Button>
      </div>
      <TasksBoard
        identityId={identityId}
        emptyHint="还没有任务。点「新的任务」把要做的事交给它。"
        focusTaskId={focusTask || undefined}
      />
      <NewTaskDialog
        open={open}
        onOpenChange={setOpen}
        defaultIdentityId={identityId}
      />
    </div>
  );
}
