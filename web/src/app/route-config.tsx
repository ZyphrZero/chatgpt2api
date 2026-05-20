/* eslint-disable react/only-export-components -- This file is a route registry, not a hot-reloaded component module. */
import { lazy, type ReactNode } from "react";

const AccountsPage = lazy(() => import("@/app/accounts/page"));
const AdminLoginPage = lazy(() => import("@/app/admin-login/page"));
const LinuxDoCallbackPage = lazy(() => import("@/app/auth/linuxdo/callback/page"));
const SocialCallbackPage = lazy(() => import("@/app/auth/social/callback/page"));
const ImagePage = lazy(() => import("@/app/image/page"));
const ImageManagerPage = lazy(() => import("@/app/image-manager/page"));
const InvitationPage = lazy(() => import("@/app/invitation/page"));
const InvitePage = lazy(() => import("@/app/invite/page"));
const EcommerceAgentPage = lazy(() => import("@/app/ecommerce-agent/page"));
const HomePage = lazy(() => import("@/app/page"));
const LoginPage = lazy(() => import("@/app/login/page"));
const LogsPage = lazy(() => import("@/app/logs/page"));
const ProfilePage = lazy(() => import("@/app/profile/page"));
const RBACPage = lazy(() => import("@/app/rbac/page"));
const RegisterPage = lazy(() => import("@/app/register/page"));
const SettingsPage = lazy(() => import("@/app/settings/page"));
const SharePage = lazy(() => import("@/app/share/page"));
const SubscriptionPage = lazy(() => import("@/app/subscription/page"));
const UsersPage = lazy(() => import("@/app/users/page"));

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
  { path: "/subscription", element: <SubscriptionPage />, requiredPath: "/subscription" },
  { path: "/invite", element: <InvitePage />, requiredPath: "/invite" },
  { path: "/users", element: <UsersPage />, requiredPath: "/users" },
  { path: "/profile", element: <ProfilePage />, requiredPath: "/profile" },
  { path: "/rbac", element: <RBACPage />, requiredPath: "/rbac" },
  { path: "/logs", element: <LogsPage />, requiredPath: "/logs" },
  { path: "/settings", element: <SettingsPage />, requiredPath: "/settings" },
  { path: "/image", element: <ImagePage />, requiredPath: "/image" },
  { path: "*", element: <HomePage /> },
];
