"use client";

import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import {
  canAccessPath,
  getDefaultRouteForSession,
  type AuthRole,
  type StoredAuthSession,
} from "@/store/auth";
import { getCachedAuthSession, getVerifiedAuthSession } from "@/lib/session";

type UseAuthGuardResult = {
  isCheckingAuth: boolean;
  session: StoredAuthSession | null;
};

export function useAuthGuard(allowedRoles?: AuthRole[], requiredPath?: string): UseAuthGuardResult {
  const navigate = useNavigate();
  const location = useLocation();
  const [session, setSession] = useState<StoredAuthSession | null>(() => getCachedAuthSession() ?? null);
  const [isCheckingAuth, setIsCheckingAuth] = useState(() => getCachedAuthSession() === undefined);
  const allowedRolesKey = (allowedRoles || []).join(",");

  useEffect(() => {
    let active = true;

    const load = async () => {
      const roleList = allowedRolesKey ? (allowedRolesKey.split(",") as AuthRole[]) : [];
      const storedSession = await getVerifiedAuthSession();
      if (!active) {
        return;
      }

      if (!storedSession) {
        setSession(null);
        setIsCheckingAuth(false);
        const redirectTo = `${location.pathname}${location.search}`;
        navigate(`/login?redirect=${encodeURIComponent(redirectTo)}`, { replace: true });
        return;
      }

      if (roleList.length > 0 && !roleList.includes(storedSession.role)) {
        setSession(storedSession);
        setIsCheckingAuth(false);
        navigate(getDefaultRouteForSession(storedSession), { replace: true });
        return;
      }

      if (requiredPath && !canAccessPath(storedSession, requiredPath)) {
        setSession(storedSession);
        setIsCheckingAuth(false);
        navigate(getDefaultRouteForSession(storedSession), { replace: true });
        return;
      }

      setSession(storedSession);
      setIsCheckingAuth(false);
    };

    void load();
    return () => {
      active = false;
    };
  }, [allowedRolesKey, location.pathname, location.search, navigate, requiredPath]);

  return { isCheckingAuth, session };
}

export function useRedirectIfAuthenticated() {
  const navigate = useNavigate();
  const location = useLocation();
  const [isCheckingAuth, setIsCheckingAuth] = useState(() => getCachedAuthSession() !== null);

  useEffect(() => {
    let active = true;

    const load = async () => {
      const storedSession = await getVerifiedAuthSession();
      if (!active) {
        return;
      }

      if (storedSession) {
        const redirectTo = new URLSearchParams(location.search).get("redirect") || "";
        navigate(redirectTo.startsWith("/") ? redirectTo : getDefaultRouteForSession(storedSession), { replace: true });
        return;
      }

      setIsCheckingAuth(false);
    };

    void load();
    return () => {
      active = false;
    };
  }, [location.search, navigate]);

  return { isCheckingAuth };
}
