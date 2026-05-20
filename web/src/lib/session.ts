"use client";

import { verifySession, type LoginResponse } from "@/lib/api";
import {
  clearStoredAuthSession,
  getStoredAuthSession,
  setStoredAuthSession,
  type StoredAuthSession,
} from "@/store/auth";

let cachedAuthSession: StoredAuthSession | null | undefined;
let verifyAuthSessionPromise: Promise<StoredAuthSession | null> | null = null;
let authSessionVersion = 0;
export const AUTH_SESSION_CHANGE_EVENT = "chatgpt2api:auth-session-change";

export function authSessionFromLoginResponse(data: LoginResponse, key: string): StoredAuthSession {
  return {
    key: data.token || key,
    role: data.role,
    roleId: data.role_id,
    roleName: data.role_name,
    subjectId: data.subject_id,
    name: data.name,
    provider: data.provider,
    menuPaths: data.menu_paths || [],
    apiPermissions: data.api_permissions || [],
    menus: data.menus || [],
    imageQuotaTotal: data.image_quota_total ?? null,
    imageQuotaUsed: Math.max(0, Number(data.image_quota_used ?? 0) || 0),
    imageQuotaRemaining: data.image_quota_remaining ?? null,
  };
}

function emitAuthSessionChange() {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(AUTH_SESSION_CHANGE_EVENT));
  }
}

export function getCachedAuthSession() {
  return cachedAuthSession;
}

export async function getVerifiedAuthSession(): Promise<StoredAuthSession | null> {
  if (cachedAuthSession !== undefined) {
    return cachedAuthSession;
  }

  const verifyStartedAtVersion = authSessionVersion;
  verifyAuthSessionPromise ??= verifyStoredAuthSession();
  try {
    const verifiedSession = await verifyAuthSessionPromise;
    if (verifyStartedAtVersion === authSessionVersion) {
      cachedAuthSession = verifiedSession;
      if (verifiedSession) {
        await setStoredAuthSession(verifiedSession);
      } else {
        await clearStoredAuthSession();
      }
      return verifiedSession;
    }
    return cachedAuthSession ?? null;
  } finally {
    if (verifyStartedAtVersion === authSessionVersion) {
      verifyAuthSessionPromise = null;
    }
  }
}

export async function setVerifiedAuthSession(session: StoredAuthSession) {
  authSessionVersion += 1;
  cachedAuthSession = session;
  verifyAuthSessionPromise = null;
  await setStoredAuthSession(session);
  emitAuthSessionChange();
}

export async function clearVerifiedAuthSession() {
  authSessionVersion += 1;
  cachedAuthSession = null;
  verifyAuthSessionPromise = null;
  await clearStoredAuthSession();
  emitAuthSessionChange();
}

async function verifyStoredAuthSession(): Promise<StoredAuthSession | null> {
  const storedSession = await getStoredAuthSession();
  if (!storedSession) {
    return null;
  }

  try {
    const data = await verifySession(storedSession.key);
    return authSessionFromLoginResponse(data, storedSession.key);
  } catch {
    return null;
  }
}
