import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import { useAuth } from "../auth";
import type { AccountResponse } from "../types";

export default function AccountPage() {
  const { user, refresh } = useAuth();
  const [data, setData] = useState<AccountResponse | null>(null);
  const [displayName, setDisplayName] = useState("");
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  const load = () =>
    api
      .account()
      .then((d) => {
        setData(d);
        setDisplayName(d.user.displayName);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "failed to load"));

  useEffect(() => {
    load();
  }, []);

  const saveProfile = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setMessage("");
    try {
      await api.updateAccount(displayName);
      await refresh();
      setMessage("Profile updated.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "update failed");
    }
  };

  const savePassword = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setMessage("");
    try {
      await api.changePassword(current, next);
      setCurrent("");
      setNext("");
      setMessage("Password changed.");
    } catch (err) {
      setError(err instanceof Error ? err.message : "update failed");
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <h1>Account</h1>
      </div>

      {error && <p className="error">{error}</p>}
      {message && <p className="ok small">{message}</p>}

      {data && (
        <div className="stat-row">
          {Object.entries(data.stats).map(([k, v]) => (
            <div key={k} className="card stat">
              <div className="stat-num">{v}</div>
              <div className="muted small">{k}</div>
            </div>
          ))}
        </div>
      )}

      <div className="card">
        <h2>Profile</h2>
        <p className="muted small">
          Signed in as <strong>{user?.username}</strong>
          {user?.createdAt && ` · joined ${new Date(user.createdAt).toLocaleDateString()}`}
        </p>
        <form className="stack" onSubmit={saveProfile}>
          <label>
            Display name
            <input
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
            />
          </label>
          <button className="primary" type="submit">
            Save profile
          </button>
        </form>
      </div>

      <div className="card">
        <h2>Change password</h2>
        <form className="stack" onSubmit={savePassword}>
          <label>
            Current password
            <input
              type="password"
              value={current}
              onChange={(e) => setCurrent(e.target.value)}
              required
            />
          </label>
          <label>
            New password
            <input
              type="password"
              value={next}
              onChange={(e) => setNext(e.target.value)}
              required
            />
          </label>
          <button className="primary" type="submit">
            Change password
          </button>
        </form>
      </div>

      {data && (
        <div className="card">
          <h2>Voice model</h2>
          <p className="small">
            <span className={`badge ${data.tts.reachable ? "ok" : "warn"}`}>
              {data.tts.reachable ? "online" : "offline"}
            </span>{" "}
            <strong>{data.tts.provider || "default"}</strong> ·{" "}
            <code>{data.tts.model}</code> at <code>{data.tts.baseUrl}</code>
          </p>
          <p className="muted tiny">
            Manage the models available for speech on the <Link to="/tts">TTS</Link>{" "}
            page. Models that return word timings get word-by-word highlighting in
            the reader.
          </p>
        </div>
      )}
    </div>
  );
}
