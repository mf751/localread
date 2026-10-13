import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api } from "../api";
import type { Chapter, Novel, Progress } from "../types";

export default function NovelPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [novel, setNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [progress, setProgress] = useState<Progress | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    let cancelled = false;
    Promise.all([api.getNovel(id), api.getProgress(id)])
      .then(([detail, prog]) => {
        if (cancelled) return;
        setNovel(detail.novel);
        setChapters(detail.chapters);
        setProgress(prog);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "failed to load"));
    return () => {
      cancelled = true;
    };
  }, [id]);

  if (error) return <div className="page"><p className="error">{error}</p></div>;
  if (!novel) return <div className="page muted">loading…</div>;

  const resumeChapter = progress
    ? chapters.find((c) => c.id === progress.chapterId)
    : chapters[0];

  return (
    <div className="page">
      <div className="row between">
        <Link to="/" className="ghost">
          ← Library
        </Link>
        <button
          className="danger small"
          onClick={async () => {
            if (!confirm(`Delete "${novel.title}" and all its audio?`)) return;
            await api.deleteNovel(novel.id);
            navigate("/");
          }}
        >
          Delete
        </button>
      </div>

      <div className="card novel-header">
        <h1>{novel.title}</h1>
        <p className="muted">{novel.author || "Unknown author"}</p>
        <p className="small">
          {novel.chapterCount} chapters · source: {novel.source}
        </p>
        {novel.description && <p className="muted small">{novel.description}</p>}
        {resumeChapter && (
          <Link
            className="primary as-button"
            to={`/novels/${novel.id}/read/${resumeChapter.id}`}
          >
            {progress ? `Resume: ${progress.chapterTitle}` : "Start reading"}
          </Link>
        )}
      </div>

      <h2>Chapters</h2>
      <ol className="chapter-list">
        {chapters.map((c) => {
          const active = progress?.chapterId === c.id;
          return (
            <li key={c.id} className={active ? "active" : ""}>
              <Link to={`/novels/${novel.id}/read/${c.id}`}>
                <span className="chapter-title">{c.title}</span>
                <span className="muted tiny">{c.wordCount} words</span>
                {active && <span className="badge ok">current</span>}
              </Link>
            </li>
          );
        })}
      </ol>
    </div>
  );
}
