"use client";

import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { AlertCircle, LoaderCircle } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { verifySession } from "@/lib/api";
import { authSessionFromLoginResponse, clearVerifiedAuthSession, setVerifiedAuthSession } from "@/lib/session";
import { clearPendingInviteCode } from "@/lib/share-helpers";
import { getDefaultRouteForSession, type StoredAuthSession } from "@/store/auth";

function fragmentParams() {
  const hash = typeof window === "undefined" ? "" : window.location.hash.replace(/^#/, "");
  return new URLSearchParams(hash);
}

function sanitizeRedirectPath(path: string | null | undefined) {
  if (!path || !path.startsWith("/") || path.startsWith("//") || path.includes("://") || path.includes("\n") || path.includes("\r")) {
    return "";
  }
  return path;
}

function stringParam(params: URLSearchParams, key: string) {
  return String(params.get(key) || "").trim();
}

function sessionFromFragment(params: URLSearchParams, key: string): StoredAuthSession | null {
  const role = stringParam(params, "role");
  if (role !== "admin" && role !== "user") {
    return null;
  }
  const subjectId = stringParam(params, "subject_id");
  if (!key || !subjectId) {
    return null;
  }
  return {
    key,
    role,
    roleId: stringParam(params, "role_id"),
    roleName: stringParam(params, "role_name"),
    subjectId,
    name: stringParam(params, "name") || "第三方用户",
    provider: stringParam(params, "provider"),
    menuPaths: [],
    apiPermissions: [],
    menus: [],
    imageQuotaTotal: null,
    imageQuotaUsed: 0,
    imageQuotaRemaining: null,
  };
}

export default function SocialCallbackPage() {
  const navigate = useNavigate();
  const [errorMessage, setErrorMessage] = useState("");

  useEffect(() => {
    let active = true;
    const finishLogin = async () => {
      const params = fragmentParams();
      const error = params.get("error");
      if (error) {
        await clearVerifiedAuthSession();
        if (active) {
          setErrorMessage(params.get("error_description") || params.get("error_message") || error);
        }
        return;
      }

      const key = params.get("key") || "";

      try {
        const fastSession = sessionFromFragment(params, key);
        let session = fastSession;
        if (fastSession) {
          await setVerifiedAuthSession(fastSession);
        } else {
          const data = await verifySession(key);
          session = authSessionFromLoginResponse(data, key);
          await setVerifiedAuthSession(session);
        }
        clearPendingInviteCode();
        toast.success("登录成功");
        if (!session) {
          throw new Error("聚合登录会话无效");
        }
        const redirect = sanitizeRedirectPath(params.get("redirect")) || getDefaultRouteForSession(session);
        navigate(redirect, { replace: true });
        if (fastSession) {
          void verifySession(key)
            .then((data) => setVerifiedAuthSession(authSessionFromLoginResponse(data, key)))
            .catch(() => undefined);
        }
      } catch (error) {
        await clearVerifiedAuthSession();
        if (active) {
          setErrorMessage(error instanceof Error ? error.message : "聚合登录失败");
        }
      }
    };
    void finishLogin();
    return () => {
      active = false;
    };
  }, [navigate]);

  return (
    <div className="grid min-h-[calc(100vh-1rem)] w-full place-items-center px-4 py-6">
      <Card className="w-full max-w-md rounded-[24px]">
        <CardContent className="flex flex-col items-center gap-5 p-8 text-center">
          {errorMessage ? (
            <>
              <div className="flex size-12 items-center justify-center rounded-[16px] bg-rose-50 text-rose-600">
                <AlertCircle className="size-5" />
              </div>
              <div className="space-y-2">
                <h1 className="text-xl font-semibold">聚合登录失败</h1>
                <p className="break-words text-sm leading-6 text-stone-500">{errorMessage}</p>
              </div>
              <Button className="h-10 rounded-xl px-5" onClick={() => navigate("/login", { replace: true })}>
                返回登录
              </Button>
            </>
          ) : (
            <>
              <LoaderCircle className="size-6 animate-spin text-stone-400" />
              <div className="space-y-2">
                <h1 className="text-xl font-semibold">正在完成登录</h1>
                <p className="text-sm text-stone-500">请稍候。</p>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
