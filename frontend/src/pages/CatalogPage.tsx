import { useState } from "react";
import { api } from "../api";
import type { CatalogEntry } from "../types";

export default function CatalogPage() {
  const [query, setQuery] = useState("");
  const [entries, setEntries] = useState<CatalogEntry[]>([]);
  const [sources, setSources] = useState<string[]>([]);
  const [errors, setErrors] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [installing, setInstalling] = useState<string | null>(null);
  const [message, setMessage] = useState("");

  const search = async (e?: React.FormEvent) => {
    e?.preventDefault();
    setBusy(true);
    setMessage("");
    try {
      const res = await api.catalogSearch(query);
      setEntries(res.entries);
      setSources(res.sources);
      setErrors(res.errors);
    } catch (err) {
      setErrors([err instanceof Error ? err.message : "search failed"]);
    } finally {
      setBusy(false);
    }
  };

  const install = async (entry: CatalogEntry) => {
    setInstalling(entry.downloadUrl);
    setMessage("");
    try {
      const n = await api.catalogInstall(entry);
      setMessage(`Installed "${n.title}" (${n.chapterCount} chapters).`);
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "install failed");
    } finally {
      setInstalling(null);
    }
  };

  return (
    <div className="page">
      <div className="page-head">
        <h1>Discover</h1>
      </div>

      <form className="row" onSubmit={search}>
        <input
          className="search"
          placeholder="Search sources and your library…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
        />
        <button className="primary" disabled={busy}>
          {busy ? "Searching…" : "Search"}
        </button>
      </form>

      <p className="muted small">
        Sources: {sources.length ? sources.join(", ") : "local library only"}.
        Configure OPDS feeds with <code>LOCALREAD_OPDS_URLS</code> in the backend
        to search remote catalogs.
      </p>

      {errors.map((e, i) => (
        <p key={i} className="warn small">
          {e}
        </p>
      ))}
      {message && <p className="small">{message}</p>}

      {entries.length > 0 && (
        <div className="grid">
          {entries.map((entry) => (
            <div key={entry.downloadUrl || entry.title} className="card">
              <div className="novel-title">{entry.title}</div>
              <div className="muted small">{entry.author || "Unknown author"}</div>
              {entry.summary && (
                <p className="muted tiny clamp">{entry.summary}</p>
              )}
              <div className="row between">
                <span className="badge">{entry.source}</span>
                {entry.downloadUrl ? (
                  <button
                    className="primary small"
                    disabled={installing === entry.downloadUrl}
                    onClick={() => install(entry)}
                  >
                    {installing === entry.downloadUrl ? "Installing…" : "Install"}
                  </button>
                ) : (
                  <span className="badge ok">installed</span>
                )}
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
