import webConfig from "@/constants/common-env";
import { getStoredSessionToken } from "@/store/auth";

const MANAGED_IMAGE_PREFIXES = ["/images/", "/image-thumbnails/"] as const;

function isAbsoluteURL(value: string) {
  return /^[a-z][a-z\d+.-]*:/i.test(value) || value.startsWith("//");
}

function browserBaseURL() {
  if (typeof window === "undefined") {
    return "http://localhost/";
  }
  return window.location.href;
}

function apiBaseURL() {
  const value = String(webConfig.apiUrl || "").trim();
  return value ? `${value.replace(/\/$/, "")}/` : "";
}

function trustedImageOrigins() {
  const origins = new Set<string>();
  if (typeof window !== "undefined") {
    origins.add(window.location.origin);
  }
  const apiBase = apiBaseURL();
  if (apiBase) {
    try {
      origins.add(new URL(apiBase).origin);
    } catch {
      // Ignore invalid runtime config and fall back to current-origin requests.
    }
  }
  return origins;
}

function isManagedImagePath(pathname: string) {
  return MANAGED_IMAGE_PREFIXES.some((prefix) => pathname.startsWith(prefix));
}

export function resolveImageRequestURL(src: string) {
  const value = String(src || "").trim();
  if (!value) {
    return "";
  }

  const browserBase = browserBaseURL();
  const apiBase = apiBaseURL();
  if (!isAbsoluteURL(value) && value.startsWith("/") && apiBase) {
    const relativeCandidate = new URL(value, apiBase);
    if (isManagedImagePath(relativeCandidate.pathname)) {
      return relativeCandidate.toString();
    }
  }

  const candidate = new URL(value, browserBase);
  if (apiBase && isManagedImagePath(candidate.pathname)) {
    return new URL(`${candidate.pathname}${candidate.search}`, apiBase).toString();
  }
  if (isManagedImagePath(candidate.pathname)) {
    return new URL(`${candidate.pathname}${candidate.search}`, browserBase).toString();
  }

  return candidate.toString();
}

export function isManagedImageURL(src: string) {
  try {
    return isManagedImagePath(new URL(resolveImageRequestURL(src)).pathname);
  } catch {
    return false;
  }
}

function canAttachStoredSessionToken(src: string) {
  try {
    const url = new URL(resolveImageRequestURL(src));
    return isManagedImagePath(url.pathname) && trustedImageOrigins().has(url.origin);
  } catch {
    return false;
  }
}

export function shouldUseAuthenticatedImageFallback(src: string) {
  const value = String(src || "").trim();
  return Boolean(value) && !value.startsWith("data:") && !value.startsWith("blob:") && isManagedImageURL(value);
}

export async function fetchAuthenticatedImageBlob(src: string, signal?: AbortSignal) {
  const requestURL = resolveImageRequestURL(src);
  const headers: Record<string, string> = {};
  const canAttachToken = canAttachStoredSessionToken(src);
  if (canAttachToken) {
    const token = await getStoredSessionToken();
    if (token) {
      headers.Authorization = `Bearer ${token}`;
    }
  }
  const managedImage = isManagedImageURL(src);

  const response = await fetch(requestURL, {
    headers,
    signal,
    credentials: headers.Authorization ? "omit" : managedImage ? "include" : "same-origin",
  });
  if (!response.ok) {
    throw new Error(`读取图片失败 (${response.status})`);
  }
  return response.blob();
}
