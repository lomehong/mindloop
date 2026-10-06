import { useEffect, useRef, useState } from "react";

import { phaseFor } from "~/lib/pet-state";
import { usePet } from "~/lib/use-pet";

import { CreatureSvg } from "./creature";
import { PetBubble, PetMenu } from "./pet-menu";

// 停靠形态（docs/designs/pet.md）：仪表盘右卡顶部的活体宠物。
// 与浮窗共用 lib/use-pet 信号层——同一后端、同一只生物、同一套表达
// （心情色/表情/苗/粒子/气泡/未读点）；没有窗体行为（拖动/穿透/位置
// 记忆/隐藏都不存在），互动收窄为点击出快捷菜单。
// 身份页停靠固定在页面身份（无身份切换，摘要行由宿主保留）；工作台
// 停靠自选身份（与浮窗同键共享选择，菜单含身份切换）。

export function PetDock({
  fixedIdentityId,
  identitySwitchable = false,
  children,
}: {
  /** 固定身份（身份页停靠 = 页面身份）；不传 = 自选（与浮窗共享选择）。 */
  fixedIdentityId?: string;
  identitySwitchable?: boolean;
  /** 宿主自带的名字/摘要列（身份摘要卡已有自己的行）；不传渲染默认列。 */
  children?: React.ReactNode;
}) {
  const pet = usePet(fixedIdentityId !== undefined ? { fixedIdentityId } : {});
  const [menuOpen, setMenuOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // 菜单外的点击 / Escape 收菜单（与浮窗同惯例）。
  useEffect(() => {
    if (!menuOpen) return;
    const onDown = (e: PointerEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setMenuOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setMenuOpen(false);
    };
    document.addEventListener("pointerdown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("pointerdown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [menuOpen]);

  const name = pet.identityName || pet.identityId || "—";

  return (
    <div ref={rootRef} className="pet-dock" data-testid="pet-dock" data-mood={pet.mood}>
      {pet.bubble && <PetBubble text={pet.bubble.text} kind={pet.bubble.kind} />}
      {menuOpen && (
        <PetMenu
          variant="dock"
          identities={pet.identities}
          identityId={pet.identityId}
          onChooseIdentity={pet.chooseIdentity}
          identitySwitchable={identitySwitchable}
          dnd={pet.dnd}
          dndActive={pet.dndActive}
          onToggleDnd={pet.toggleDnd}
          unreadCount={pet.unreadCount}
          onViewUnread={pet.viewUnread}
          chatDraft={pet.chatDraft}
          onChatDraftChange={pet.setChatDraft}
          onSend={pet.send}
          sending={pet.sending}
          chatErr={pet.chatErr}
          onPoke={pet.poke}
          poking={pet.poking}
          pokeNote={pet.pokeNote}
          authNeeded={pet.authNeeded}
          note={null}
        />
      )}
      <div className="flex items-center gap-3">
        <div className="relative flex-none">
          {pet.unreadCount > 0 && (
            <span
              className="pet-unread-dot pet-dock-dot"
              data-testid="pet-dock-unread"
              title={`勿扰中攒了 ${pet.unreadCount} 条未读`}
            />
          )}
          <CreatureSvg
            size={64}
            mood={pet.mood}
            busy={pet.busy}
            speaking={pet.mood === "speaking"}
            reaction={pet.reaction}
            reactionSeq={pet.reactionSeq}
            phase={phaseFor(pet.identityId ?? "")}
            label={`${name} 的生命体`}
            onClick={() => setMenuOpen((v) => !v)}
            testId="pet-dock-orb"
          />
        </div>
        {children ?? (
          <div className="min-w-0">
            <div className="truncate text-[13.5px] font-semibold">{name}</div>
            <div className="text-[11px] text-muted-foreground">{pet.summaryText}</div>
          </div>
        )}
      </div>
    </div>
  );
}
