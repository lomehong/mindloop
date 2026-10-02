export function slackSourceUrl(value: unknown): string | null {
  if (typeof value !== "string" || value.length > 2048) return null;
  try {
    const url = new URL(value);
    const host = url.hostname.toLowerCase();
    if (
      url.protocol === "https:" &&
      (host === "slack.com" || host.endsWith(".slack.com")) &&
      url.pathname.startsWith("/archives/")
    ) {
      return url.href;
    }
  } catch {
    return null;
  }
  return null;
}

export function slackConversationUrl(name: unknown): string | null {
  if (typeof name !== "string") return null;
  // String.prototype.match 而非 RegExp.exec：语义相同（无 g 标志），
  // 只是取频道号的纯字符串解析。
  const match = name.match(/^slack-[A-Za-z0-9]+-([A-Za-z0-9]+)(?:-[0-9.]+)?$/);
  if (!match) return null;
  return `https://slack.com/app_redirect?channel=${encodeURIComponent(match[1])}`;
}
