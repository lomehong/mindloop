// Sender identity for the phone chat (/talk). Names live in the `pwa-*`
// namespace: the Slack outbound bridge only forwards `slack-*` recipients,
// so pwa conversations can never leak into Slack.

const NAME_KEY = "shellm-pwa-name";
const LAST_IDENTITY_KEY = "shellm-pwa-last-identity";

export function sanitizePwaName(raw: string): string {
  return raw.toLowerCase().replace(/[^a-z0-9-]/g, "");
}

export function getPwaName(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(NAME_KEY);
}

export function setPwaName(name: string): void {
  window.localStorage.setItem(NAME_KEY, name);
}

export function pwaSender(name: string): string {
  return `pwa-${name}`;
}

export function getLastIdentity(): string | null {
  if (typeof window === "undefined") return null;
  return window.localStorage.getItem(LAST_IDENTITY_KEY);
}

export function setLastIdentity(identityId: string): void {
  window.localStorage.setItem(LAST_IDENTITY_KEY, identityId);
}
