import { useEffect, useState } from "react";
import type { ReactNode } from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useAuth } from "../auth";
import { useTheme } from "../theme";

type IconName =
  | "library"
  | "discover"
  | "history"
  | "tts"
  | "account"
  | "sun"
  | "moon"
  | "logout"
  | "chevron-left"
  | "chevron-right";

const ICONS: Record<IconName, ReactNode> = {
  library: (
    <>
      <path d="M2 4.5h6.5A3.5 3.5 0 0 1 12 8v11.5a3 3 0 0 0-3-3H2z" />
      <path d="M22 4.5h-6.5A3.5 3.5 0 0 0 12 8v11.5a3 3 0 0 1 3-3h7z" />
    </>
  ),
  discover: (
    <>
      <circle cx="12" cy="12" r="9.2" />
      <path d="m15.6 8.4-2.1 5.1-5.1 2.1 2.1-5.1z" />
    </>
  ),
  history: (
    <>
      <path d="M3.2 12a8.8 8.8 0 1 0 2.6-6.2" />
      <path d="M3 4.5V9h4.5" />
      <path d="M12 7.6V12l3.1 1.9" />
    </>
  ),
  tts: (
    <>
      <path d="M4 9.5v5" />
      <path d="M8 6.5v11" />
      <path d="M12 4v16" />
      <path d="M16 6.5v11" />
      <path d="M20 9.5v5" />
    </>
  ),
  account: (
    <>
      <circle cx="12" cy="8.2" r="3.8" />
      <path d="M4.5 20.2a7.5 7.5 0 0 1 15 0" />
    </>
  ),
  sun: (
    <>
      <circle cx="12" cy="12" r="4" />
      <path d="M12 2.6v2.1M12 19.3v2.1M4.8 4.8l1.5 1.5M17.7 17.7l1.5 1.5M2.6 12h2.1M19.3 12h2.1M4.8 19.2l1.5-1.5M17.7 6.3l1.5-1.5" />
    </>
  ),
  moon: <path d="M20.5 13.3A8.5 8.5 0 1 1 10.7 3.5a7 7 0 0 0 9.8 9.8z" />,
  logout: (
    <>
      <path d="M14.5 3.5H18a2.5 2.5 0 0 1 2.5 2.5v12a2.5 2.5 0 0 1-2.5 2.5h-3.5" />
      <path d="M9.5 16.5 14 12 9.5 7.5" />
      <path d="M14 12H3.5" />
    </>
  ),
  "chevron-left": <path d="M15 6l-6 6 6 6" />,
  "chevron-right": <path d="M9 6l6 6-6 6" />,
};

function Icon({ name, className }: { name: IconName; className?: string }) {
  return (
    <svg
      className={className ?? "icon"}
      viewBox="0 0 24 24"
      width="18"
      height="18"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.7"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      {ICONS[name]}
    </svg>
  );
}

const NAV: { to: string; label: string; icon: IconName; end?: boolean }[] = [
  { to: "/", label: "Library", icon: "library", end: true },
  { to: "/catalog", label: "Discover", icon: "discover" },
  { to: "/history", label: "History", icon: "history" },
  { to: "/tts", label: "TTS", icon: "tts" },
  { to: "/account", label: "Account", icon: "account" },
];

const COLLAPSE_KEY = "localread-sidebar-collapsed";

export default function Layout() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  const { theme, setTheme } = useTheme();
  const location = useLocation();

  const [collapsed, setCollapsed] = useState(() => {
    try {
      return localStorage.getItem(COLLAPSE_KEY) === "1";
    } catch {
      return false;
    }
  });
  useEffect(() => {
    try {
      localStorage.setItem(COLLAPSE_KEY, collapsed ? "1" : "0");
    } catch {
      // ignore storage failures
    }
  }, [collapsed]);

  // The reader gets the sidebar too, but a full-bleed content column so the
  // prose can centre at its own reading width.
  const isReader = /^\/novels\/[^/]+\/read\//.test(location.pathname);

  const name = user?.displayName || user?.username || "";
  const initial = name.trim().slice(0, 1).toUpperCase() || "?";

  return (
    <div className="app">
      <aside className={`sidebar${collapsed ? " collapsed" : ""}`}>
        <div className="side-top">
          <div className="side-brand" onClick={() => navigate("/")}>
            <span className="logo-mark">◈</span>
            <span className="brand-text">localread</span>
          </div>
          <button
            type="button"
            className="icon-btn collapse-btn"
            onClick={() => setCollapsed((v) => !v)}
            title={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            aria-expanded={!collapsed}
          >
            <Icon name={collapsed ? "chevron-right" : "chevron-left"} />
          </button>
        </div>

        <nav className="side-nav">
          <span className="side-label">Menu</span>
          {NAV.map((item) => (
            <NavLink key={item.to} to={item.to} end={item.end} className="side-link">
              <Icon name={item.icon} />
              <span>{item.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="side-foot">
          <div className="theme-toggle" role="group" aria-label="Colour theme">
            <button
              type="button"
              className={theme === "light" ? "active" : ""}
              onClick={() => setTheme("light")}
              aria-pressed={theme === "light"}
              title="Light theme"
            >
              <Icon name="sun" />
              <span>Light</span>
            </button>
            <button
              type="button"
              className={theme === "dark" ? "active" : ""}
              onClick={() => setTheme("dark")}
              aria-pressed={theme === "dark"}
              title="Dark theme"
            >
              <Icon name="moon" />
              <span>Dark</span>
            </button>
          </div>

          <div className="side-user">
            <span className="avatar">{initial}</span>
            <span className="side-user-name">{name}</span>
            <button
              className="icon-btn"
              title="Sign out"
              aria-label="Sign out"
              onClick={async () => {
                await logout();
                navigate("/login");
              }}
            >
              <Icon name="logout" />
            </button>
          </div>
        </div>
      </aside>

      <main className={`content${isReader ? " reader-content" : ""}`}>
        <Outlet />
      </main>
    </div>
  );
}
