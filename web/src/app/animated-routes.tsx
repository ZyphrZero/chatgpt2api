import { Suspense, type ReactNode } from "react";
import {
  Navigate,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import {
  AnimatePresence,
  motion,
  useReducedMotion,
  type Transition,
  type Variants,
} from "motion/react";

import { appRoutes } from "@/app/route-config";
import { getCachedAuthSession } from "@/lib/session";
import { canAccessPath, getDefaultRouteForSession } from "@/store/auth";

const routeTransition: Transition = {
  duration: 0.2,
  ease: [0.22, 1, 0.36, 1],
};

const reducedRouteTransition: Transition = {
  duration: 0.01,
};

const routeVariants: Variants = {
  initial: {
    opacity: 0,
  },
  animate: {
    opacity: 1,
  },
  exit: {
    opacity: 0,
  },
};

const reducedRouteVariants: Variants = {
  initial: { opacity: 0 },
  animate: { opacity: 1 },
  exit: { opacity: 0 },
};

function PermissionRoute({ requiredPath, children }: { requiredPath?: string; children: ReactNode }) {
  const session = getCachedAuthSession();
  if (!requiredPath) {
    return children;
  }
  if (session === undefined) {
    return children;
  }
  if (!session) {
    return <Navigate to="/login" replace />;
  }
  if (!canAccessPath(session, requiredPath)) {
    return <Navigate to={getDefaultRouteForSession(session)} replace />;
  }
  return children;
}

export function AnimatedRoutes() {
  const location = useLocation();
  const prefersReducedMotion = useReducedMotion();

  return (
    <AnimatePresence mode="wait" initial={false}>
      <motion.div
        key={location.pathname}
        variants={prefersReducedMotion ? reducedRouteVariants : routeVariants}
        initial="initial"
        animate="animate"
        exit="exit"
        transition={prefersReducedMotion ? reducedRouteTransition : routeTransition}
        className="min-w-0"
      >
        <Suspense fallback={<RouteFallback />}>
          <Routes location={location}>
            {appRoutes.map((route) => (
              <Route
                key={route.path}
                path={route.path}
                element={<PermissionRoute requiredPath={route.requiredPath}>{route.element}</PermissionRoute>}
              />
            ))}
          </Routes>
        </Suspense>
      </motion.div>
    </AnimatePresence>
  );
}

function RouteFallback() {
  return <div className="min-h-screen bg-[#f8fbff]" />;
}
