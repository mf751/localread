import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { HistoryEvent } from "../types";

const labels: Record<string, string> = {
  open: "opened",
  finish: "finished",
  listen: "listened to",
  import: "imported",
  note: "noted",
};

function when(iso: string): string {
  const d = new Date(iso);
  const diff = (Date.now() - d.getTime()) / 1000;
  if (diff < 60) return "just now";
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`;
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`;
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)}d ago`;
  return d.toLocaleDateString();
}

export default function HistoryPage() {
  const [events, setEvents] = useState<HistoryEvent[]>([]);
  const [error, setError] = useState("");

  useEffect(() => {
    api
      .listHistory(200)
      .then(setEvents)
      .catch((e) => setError(e instanceof Error ? e.message : "failed to load"));
  }, []);

  return (
    <div className="page">
      <div className="page-head">
        <h1>History</h1>
      </div>
      {error && <p className="error">{error}</p>}
      {events.length === 0 ? (
        <div className="empty card">No activity yet.</div>
      ) : (
        <ul className="history-list">
          {events.map((e) => (
            <li key={e.id} className="card history-item">
              <span className={`dot ${e.event}`} />
              <div className="grow">
                <div>
                  You {labels[e.event] ?? e.event}{" "}
                  <Link to={`/novels/${e.novelId}`}>{e.novelTitle}</Link>
                  {e.chapterTitle && (
                    <>
                      {" — "}
                      <span className="muted">{e.chapterTitle}</span>
                    </>
                  )}
                </div>
                {e.detail && <div className="muted tiny">{e.detail}</div>}
              </div>
              <span className="muted tiny">{when(e.createdAt)}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
