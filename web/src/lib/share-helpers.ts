// Helpers shared across the share and login flows. They centralize WeChat in-app browser
// detection (which crashes on navigator.share with files), inviter binding via localStorage,
// and a defensive native-share wrapper that never throws synchronously.

const PENDING_INVITE_KEY = "df.pending_invite_code";
const PENDING_INVITE_SOURCE_KEY = "df.pending_invite_source";

/** Returns true when running inside the WeChat in-app browser. */
export function isWeChatBrowser() {
  if (typeof navigator === "undefined") {
    return false;
  }
  return /MicroMessenger/i.test(navigator.userAgent);
}

/** Returns true for the WeChat in-app browser variants known to crash on navigator.share. */
export function shouldDeferNativeShare() {
  if (typeof navigator === "undefined") {
    return false;
  }
  const ua = navigator.userAgent;
  // QQ in-app browser, Douyin in-app browser and WeChat all produce hard crashes when
  // navigator.share is called with files. We treat them as "must use long-press" hosts.
  return /MicroMessenger|QQ\/|MQQBrowser|aweme|TouTiao/i.test(ua);
}

/** Persist an invite code so it can be applied at signup time. Source is logged for debug. */
export function rememberPendingInviteCode(code: string | null | undefined, source = "unknown") {
  if (typeof window === "undefined") {
    return;
  }
  const trimmed = (code ?? "").trim();
  if (!trimmed) {
    return;
  }
  try {
    window.localStorage.setItem(PENDING_INVITE_KEY, trimmed);
    window.localStorage.setItem(PENDING_INVITE_SOURCE_KEY, source);
  } catch {
    // localStorage may be disabled (private browsing); silently degrade.
  }
}

/** Retrieve the most recent pending invite code, falling back to "" when none. */
export function getPendingInviteCode(): string {
  if (typeof window === "undefined") {
    return "";
  }
  try {
    return (window.localStorage.getItem(PENDING_INVITE_KEY) ?? "").trim();
  } catch {
    return "";
  }
}

/** Clear the persisted invite code, e.g. once registration succeeded. */
export function clearPendingInviteCode() {
  if (typeof window === "undefined") {
    return;
  }
  try {
    window.localStorage.removeItem(PENDING_INVITE_KEY);
    window.localStorage.removeItem(PENDING_INVITE_SOURCE_KEY);
  } catch {
    // ignore
  }
}

/** Resolve the invite code for a fresh social login attempt: explicit > url > pending. */
export function resolveInviteCodeForLogin(explicit?: string | null) {
  if (explicit && explicit.trim()) {
    return explicit.trim();
  }
  if (typeof window !== "undefined") {
    const params = new URLSearchParams(window.location.search);
    const fromQuery = (params.get("invite") || params.get("code") || params.get("ref") || "").trim();
    if (fromQuery) {
      return fromQuery;
    }
  }
  return getPendingInviteCode();
}

export type SafeShareInput = {
  url: string;
  title?: string;
  text?: string;
  files?: File[];
};

export type SafeShareResult =
  | { mode: "wechat-hint"; url: string }
  | { mode: "native"; url: string }
  | { mode: "clipboard"; url: string }
  | { mode: "aborted"; url: string };

/**
 * shareSafely picks the best share strategy for the current environment without throwing.
 *
 *  1. WeChat / QQ / Douyin in-app browsers always return wechat-hint so callers can render a
 *     "long press to share" tip instead of crashing the WebView.
 *  2. Browsers with navigator.canShare(files) get the full file share.
 *  3. Browsers that support url-only sharing fall back to that.
 *  4. Otherwise the URL is copied to the clipboard.
 */
export async function shareSafely(input: SafeShareInput): Promise<SafeShareResult> {
  const url = input.url || "";
  if (shouldDeferNativeShare()) {
    return { mode: "wechat-hint", url };
  }
  if (typeof navigator === "undefined" || typeof navigator.share !== "function") {
    await copyText(url);
    return { mode: "clipboard", url };
  }
  const baseData = { title: input.title, text: input.text, url } as ShareData;
  if (input.files && input.files.length > 0) {
    const fileData = { ...baseData, files: input.files } as ShareData;
    if (canShare(fileData)) {
      try {
        await navigator.share(fileData);
        return { mode: "native", url };
      } catch (error) {
        if (isAbortedShare(error)) {
          return { mode: "aborted", url };
        }
        // fall through to URL-only share
      }
    }
  }
  if (canShare(baseData)) {
    try {
      await navigator.share(baseData);
      return { mode: "native", url };
    } catch (error) {
      if (isAbortedShare(error)) {
        return { mode: "aborted", url };
      }
    }
  }
  await copyText(url);
  return { mode: "clipboard", url };
}

function canShare(data: ShareData) {
  if (typeof navigator === "undefined" || typeof navigator.share !== "function") {
    return false;
  }
  if (typeof navigator.canShare !== "function") {
    return true;
  }
  try {
    return navigator.canShare(data);
  } catch {
    return false;
  }
}

function isAbortedShare(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}

async function copyText(text: string) {
  if (!text) {
    return;
  }
  if (typeof navigator !== "undefined" && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      // fall through to legacy fallback
    }
  }
  if (typeof document === "undefined") {
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  try {
    document.execCommand("copy");
  } finally {
    textarea.remove();
  }
}
