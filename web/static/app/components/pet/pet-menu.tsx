import type { AnnouncementKind, PetDndConfig } from "~/lib/pet-state";
import type { Identity } from "~/lib/types";

// 宠物的语音气泡：播报（announcer 出队）或流式回复尾部。
export function PetBubble({
  text,
  kind,
}: {
  text: string;
  kind: AnnouncementKind | "stream";
}) {
  return (
    <div className="pet-bubble" data-testid="pet-bubble" data-kind={kind}>
      <span className="pet-bubble-kind" data-kind={kind}>
        {BUBBLE_KIND_LABEL[kind] ?? ""}
      </span>
      <span>{text}</span>
      {kind === "stream" && <span className="pet-caret">▍</span>}
    </div>
  );
}

const BUBBLE_KIND_LABEL: Partial<Record<AnnouncementKind | "stream", string>> = {
  alert: "警情",
  error: "错误",
  task: "任务",
  approval: "审批",
  budget: "预算",
  breaker: "熔断",
  digest: "你好",
  info: "说",
};

// 快捷菜单：说话 / 戳醒 / 勿扰 / 身份 / 未读 / 仪表盘 / 隐藏。
// 纯展示 + 回调；动作与状态在 pet-app.tsx。
export function PetMenu({
  identities,
  identityId,
  onChooseIdentity,
  dnd,
  dndActive,
  onToggleDnd,
  unreadCount,
  onViewUnread,
  chatDraft,
  onChatDraftChange,
  onSend,
  sending,
  chatErr,
  onPoke,
  poking,
  pokeNote,
  onOpenDashboard,
  onHide,
  authNeeded,
  note,
}: {
  identities: Identity[];
  identityId: string | null;
  onChooseIdentity: (id: string) => void;
  dnd: PetDndConfig;
  dndActive: boolean;
  onToggleDnd: () => void;
  unreadCount: number;
  onViewUnread: () => void;
  chatDraft: string;
  onChatDraftChange: (v: string) => void;
  onSend: () => void;
  sending: boolean;
  chatErr: string | null;
  onPoke: () => void;
  poking: boolean;
  pokeNote: string | null;
  onOpenDashboard: () => void;
  onHide: () => void;
  authNeeded: boolean;
  note: string | null;
}) {
  const canAct = identityId !== null;
  return (
    <div className="pet-menu" data-testid="pet-menu" data-pet-solid>
      <div className="pet-menu-row pet-menu-chat">
        <input
          data-testid="pet-chat-input"
          className="pet-input"
          placeholder={canAct ? "说点什么，回车发送…" : "未连接身份"}
          value={chatDraft}
          disabled={!canAct}
          onChange={(e) => onChatDraftChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing) onSend();
          }}
        />
        <button
          type="button"
          className="pet-btn pet-btn-solid"
          data-testid="pet-send"
          disabled={!canAct || sending || chatDraft.trim() === ""}
          onClick={onSend}
        >
          发送
        </button>
      </div>

      <div className="pet-menu-row">
        <button
          type="button"
          className="pet-btn"
          data-testid="pet-poke"
          disabled={!canAct || poking}
          onClick={onPoke}
        >
          戳醒
          <span className="pet-menu-hint">消耗自发档预算</span>
        </button>
        <button type="button" className="pet-btn" data-testid="pet-dnd" onClick={onToggleDnd}>
          勿扰 {dnd.enabled ? "开" : "关"}
          <span className="pet-menu-hint">
            {dndActive && !dnd.enabled
              ? "夜间段进行中"
              : `夜间 ${String(dnd.nightStart).padStart(2, "0")}:00–${String(dnd.nightEnd).padStart(2, "0")}:00`}
          </span>
        </button>
      </div>

      {unreadCount > 0 && (
        <button type="button" className="pet-btn" data-testid="pet-unread" onClick={onViewUnread}>
          未读播报 {unreadCount} 条 · 查看
        </button>
      )}

      {identities.length > 1 && (
        <label className="pet-menu-row pet-menu-identity">
          身份
          <select
            data-testid="pet-identity"
            className="pet-input"
            value={identityId ?? ""}
            onChange={(e) => onChooseIdentity(e.target.value)}
          >
            {identities.map((it) => (
              <option key={it.id} value={it.id}>
                {it.name}
              </option>
            ))}
          </select>
        </label>
      )}

      {authNeeded && <TokenRow />}

      <div className="pet-menu-row">
        <button type="button" className="pet-btn" data-testid="pet-open-dashboard" onClick={onOpenDashboard}>
          打开仪表盘
        </button>
        <button type="button" className="pet-btn" data-testid="pet-hide" onClick={onHide}>
          隐藏
        </button>
      </div>

      {(chatErr || pokeNote || note) && (
        <div className="pet-note">{chatErr ?? pokeNote ?? note}</div>
      )}
    </div>
  );
}

// 非回环 Token 部署：宠物窗体独立于仪表盘 iframe，凭据缺失时在此补录。
function TokenRow() {
  return (
    <form
      className="pet-menu-row pet-menu-chat"
      onSubmit={(e) => {
        e.preventDefault();
        const input = e.currentTarget.elements.namedItem("token") as HTMLInputElement;
        import("~/lib/api").then(({ setWebToken }) => setWebToken(input.value));
      }}
    >
      <input
        data-testid="pet-token"
        name="token"
        type="password"
        className="pet-input"
        placeholder="访问凭据"
        autoComplete="off"
      />
      <button type="submit" className="pet-btn pet-btn-solid">
        保存
      </button>
    </form>
  );
}
