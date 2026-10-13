import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";
import type { Novel, Progress } from "../types";

export default function LibraryPage() {
  const [novels, setNovels] = useState<Novel[]>([]);
  const [progress, setProgress] = useState<Record<string, Progress>>({});
  const [query, setQuery] = useState("");
  const [error, setError] = useState("");
  const [showUpload, setShowUpload] = useState(false);

  const load = useCallback(async (q: string) => {
    setError("");
    try {
      const [list, prog] = await Promise.all([
        api.listNovels(q),
        api.listProgress(),
      ]);
      setNovels(list);
      setProgress(Object.fromEntries(prog.map((p) => [p.novelId, p])));
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load library");
    }
  }, []);

  useEffect(() => {
    const t = setTimeout(() => load(query), 200);
    return () => clearTimeout(t);
  }, [query, load]);

  return (
    <div className="page">
      <div className="page-head">
        <h1>Library</h1>
        <div className="row">
          <input
            className="search"
            placeholder="Search your novels…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
          <button className="primary" onClick={() => setShowUpload((v) => !v)}>
            {showUpload ? "Close" : "Upload / Import"}
          </button>
        </div>
      </div>

      {showUpload && (
        <UploadPanel
          onImported={() => {
            setShowUpload(false);
            load(query);
          }}
        />
      )}

      {error && <p className="error">{error}</p>}

      {novels.length === 0 ? (
        <div className="empty card">
          <p>No novels yet.</p>
          <p className="muted small">
            Upload a <code>.txt</code> or <code>.epub</code>, or discover one from
            the <Link to="/catalog">Discover</Link> page.
          </p>
        </div>
      ) : (
        <div className="grid">
          {novels.map((n) => {
            const p = progress[n.id];
            return (
              <Link key={n.id} to={`/novels/${n.id}`} className="card novel-card">
                <div className="novel-title">{n.title}</div>
                <div className="muted small">{n.author || "Unknown author"}</div>
                <div className="muted small">
                  {n.chapterCount} chapters · {n.source}
                </div>
                {p ? (
                  <div className="progress-line">
                    <div className="progress-bar">
                      <span
                        style={{
                          width: `${Math.round(
                            ((p.chapterIndex + p.position) /
                              Math.max(n.chapterCount, 1)) *
                              100,
                          )}%`,
                        }}
                      />
                    </div>
                    <span className="muted tiny">
                      {p.chapterTitle} · {Math.round(p.position * 100)}%
                    </span>
                  </div>
                ) : (
                  <span className="muted tiny">Not started</span>
                )}
              </Link>
            );
          })}
        </div>
      )}
    </div>
  );
}

function UploadPanel({ onImported }: { onImported: () => void }) {
  const [tab, setTab] = useState<"file" | "text">("file");
  const [title, setTitle] = useState("");
  const [author, setAuthor] = useState("");
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const fileRef = useRef<HTMLInputElement>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (tab === "file") {
        const f = fileRef.current?.files?.[0];
        if (!f) throw new Error("choose a file first");
        await api.uploadFile(f, title, author);
      } else {
        if (!text.trim()) throw new Error("paste some text first");
        await api.importText(title || "Pasted text", author, text);
      }
      onImported();
    } catch (err) {
      setError(err instanceof Error ? err.message : "import failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <form className="card upload-panel" onSubmit={submit}>
      <div className="tabs">
        <button
          type="button"
          className={tab === "file" ? "tab active" : "tab"}
          onClick={() => setTab("file")}
        >
          Upload file
        </button>
        <button
          type="button"
          className={tab === "text" ? "tab active" : "tab"}
          onClick={() => setTab("text")}
        >
          Paste text
        </button>
      </div>

      <label>
        Title <span className="muted tiny">(optional)</span>
        <input value={title} onChange={(e) => setTitle(e.target.value)} />
      </label>
      <label>
        Author <span className="muted tiny">(optional)</span>
        <input value={author} onChange={(e) => setAuthor(e.target.value)} />
      </label>

      {tab === "file" ? (
        <label>
          File (.txt or .epub)
          <input ref={fileRef} type="file" accept=".txt,.text,.md,.epub" />
        </label>
      ) : (
        <label>
          Text
          <textarea
            rows={8}
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder="Paste a novel or chapter text…"
          />
        </label>
      )}

      {error && <p className="error small">{error}</p>}
      <button className="primary" disabled={busy} type="submit">
        {busy ? "Importing…" : "Import"}
      </button>
    </form>
  );
}
