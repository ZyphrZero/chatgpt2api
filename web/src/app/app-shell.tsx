import { AnimatedRoutes } from "@/app/animated-routes";
import { TopNav } from "@/components/top-nav";
import { useLocation } from "react-router-dom";

const standalonePaths = new Set([
  "/",
  "/share",
  "/login",
  "/admin-login",
  "/auth/linuxdo/callback",
  "/auth/social/callback",
]);

export function AppShell() {
  const location = useLocation();
  const pathname = location.pathname.replace(/\/+$/, "") || "/";

  if (standalonePaths.has(pathname)) {
    return (
      <main className="min-h-screen bg-[#f8fbff] text-foreground" style={{ minHeight: "100vh", backgroundColor: "#f8fbff" }}>
        <AnimatedRoutes />
      </main>
    );
  }

  return (
    <main className="min-h-screen bg-background text-foreground">
      <div className="mx-auto flex min-h-screen max-w-[1600px] flex-col gap-2 px-2 py-2 sm:px-4 lg:px-5">
        <TopNav />
        <AnimatedRoutes />
      </div>
    </main>
  );
}
