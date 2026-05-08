import type { ReactNode } from "react";

import AccountsPage from "@/app/accounts/page";
import AdminLoginPage from "@/app/admin-login/page";
import LinuxDoCallbackPage from "@/app/auth/linuxdo/callback/page";
import SocialCallbackPage from "@/app/auth/social/callback/page";
import ImagePage from "@/app/image/page";
import ImageManagerPage from "@/app/image-manager/page";
import InvitationPage from "@/app/invitation/page";
import InvitePage from "@/app/invite/page";
import EcommerceAgentPage from "@/app/ecommerce-agent/page";
import HomePage from "@/app/page";
import LoginPage from "@/app/login/page";
import LogsPage from "@/app/logs/page";
import ProfilePage from "@/app/profile/page";
import RBACPage from "@/app/rbac/page";
import RegisterPage from "@/app/register/page";
import SettingsPage from "@/app/settings/page";
import SharePage from "@/app/share/page";
import UsersPage from "@/app/users/page";

export type AppRouteConfig = {
  path: string;
  element: ReactNode;
  requiredPath?: string;
};

export const appRoutes: AppRouteConfig[] = [
  { path: "/", element: <HomePage /> },
  { path: "/login", element: <LoginPage /> },
  { path: "/admin-login", element: <AdminLoginPage /> },
  { path: "/auth/linuxdo/callback", element: <LinuxDoCallbackPage /> },
  { path: "/auth/social/callback", element: <SocialCallbackPage /> },
  { path: "/invitation", element: <InvitationPage /> },
  { path: "/share", element: <SharePage /> },
  { path: "/ecommerce-agent", element: <EcommerceAgentPage /> },
  { path: "/accounts", element: <AccountsPage />, requiredPath: "/accounts" },
  { path: "/register", element: <RegisterPage />, requiredPath: "/register" },
  { path: "/image-manager", element: <ImageManagerPage />, requiredPath: "/image-manager" },
  { path: "/invite", element: <InvitePage />, requiredPath: "/invite" },
  { path: "/users", element: <UsersPage />, requiredPath: "/users" },
  { path: "/profile", element: <ProfilePage />, requiredPath: "/profile" },
  { path: "/rbac", element: <RBACPage />, requiredPath: "/rbac" },
  { path: "/logs", element: <LogsPage />, requiredPath: "/logs" },
  { path: "/settings", element: <SettingsPage />, requiredPath: "/settings" },
  { path: "/image", element: <ImagePage />, requiredPath: "/image" },
  { path: "*", element: <HomePage /> },
];
