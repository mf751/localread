import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { api } from "../api";
import AudioPlayer from "../components/AudioPlayer";
import type { Chapter, Novel, WordTiming } from "../types";

interface Token {
  text: string;
  wordIndex?: number;
}

// buildParagraphs splits chapter text into paragraphs of tokens. Words carry a
// global index matching the order the backend uses for word timings, so a
// timing can be matched to its rendered span by index.
function buildParagraphs(content: string): Token[][] {
  const paras: Token[][] = [];
  let cur: Token[] = [];
  let wi = 0;
  for (const piece of content.split(/(\s+)/)) {
    if (!piece) continue;
    if (/^\s+$/.test(piece)) {
      const newlines = (piece.match(/\n/g) || []).length;
      if (newlines >= 2) {
        if (cur.length) {
          paras.push(cur);
          cur = [];
        }
      } else {
        cur.push({ text: piece });
      }
    } else {
      cur.push({ text: piece, wordIndex: wi++ });
    }
  }
  if (cur.length) paras.push(cur);
  return paras;
}

// findWord returns the index of the last real word that has started at or
// before t. Words the model did not speak are stored as zero-length markers;
// they are skipped so the previous word stays highlighted during a pause.
function findWord(timings: WordTiming[], t: number): number {
  let lo = 0;
  let hi = timings.length - 1;
  let ans = -1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    if (timings[mid].start <= t) {
      ans = mid;
      lo = mid + 1;
    } else {
      hi = mid - 1;
    }
  }
  while (ans > 0 && timings[ans].end <= timings[ans].start) {
    ans--;
  }
  return ans;
}

export default function ReaderPage() {
  const { id = "", chapterId = "" } = useParams();
  const navigate = useNavigate();
  const [novel, setNovel] = useState<Novel | null>(null);
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [chapter, setChapter] = useState<Chapter | null>(null);
  const [error, setError] = useState("");
  const [pct, setPct] = useState(0);

  const lastSaved = useRef(0);
  const finished = useRef(false);
  const restoreTo = useRef<number | null>(null);

  // Word-level highlighting while listening.
  const timingsRef = useRef<WordTiming[] | null>(null);
  const [activeWord, setActiveWord] = useState(-1);
  const proseRef = useRef<HTMLDivElement | null>(null);
  const [playing, setPlaying] = useState(false);

  const handleTimings = useCallback((timings: WordTiming[] | null) => {
    timingsRef.current = timings;
    setActiveWord(-1);
  }, []);

  const handleTime = useCallback((t: number) => {
    const timings = timingsRef.current;
    const idx = timings && timings.length ? findWord(timings, t) : -1;
    setActiveWord((prev) => (prev === idx ? prev : idx));
  }, []);

  const handlePlaying = useCallback((p: boolean) => setPlaying(p), []);

  // While listening, keep the highlighted word in view. We only nudge the page
  // when the word drifts out of a comfortable band, so it scrolls in steps
  // rather than jittering on every word.
  useEffect(() => {
    if (!playing || activeWord < 0 || !proseRef.current) return;
    const el = proseRef.current.querySelector<HTMLElement>(".word.active");
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const band = window.innerHeight * 0.15;
    if (rect.top < band || rect.bottom > window.innerHeight - band) {
      const top = window.scrollY + rect.top - window.innerHeight * 0.35;
      window.scrollTo({ top, behavior: "smooth" });
    }
  }, [activeWord, playing]);

  const paragraphs = useMemo(
    () => buildParagraphs(chapter?.content ?? ""),
    [chapter?.content],
  );

  // Load novel + chapter list once.
  useEffect(() => {
    let cancelled = false;
    api
      .getNovel(id)
      .then((d) => {
        if (cancelled) return;
        setNovel(d.novel);
        setChapters(d.chapters);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "failed to load"));
    return () => {
      cancelled = true;
    };
  }, [id]);

  // Load the chapter and restore reading position.
  useEffect(() => {
    let cancelled = false;
    finished.current = false;
    setChapter(null);
    (async () => {
      try {
        const [c, prog] = await Promise.all([
          api.getChapter(chapterId),
          api.getProgress(id),
        ]);
        if (cancelled) return;
        setChapter(c);
        restoreTo.current =
          prog && prog.chapterId === chapterId ? prog.position : 0;
        api.addHistory(id, chapterId, "open", c.title).catch(() => {});
      } catch (e) {
        setError(e instanceof Error ? e.message : "failed to load chapter");
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [chapterId, id]);

  // Restore scroll after the chapter renders.
  useEffect(() => {
    if (!chapter || restoreTo.current == null) return;
    const pos = restoreTo.current;
    if (pos > 0) {
      requestAnimationFrame(() => {
        const max = document.body.scrollHeight - window.innerHeight;
        window.scrollTo({ top: pos * max });
      });
    } else {
      window.scrollTo({ top: 0 });
    }
    restoreTo.current = null;
  }, [chapter]);

  const save = useCallback(
    (position: number, force = false) => {
      const now = Date.now();
      if (!force && now - lastSaved.current < 3000) return;
      lastSaved.current = now;
      api.saveProgress(id, chapterId, position).catch(() => {});
    },
    [id, chapterId],
  );

  // Track scroll progress; save periodically, mark finished at the end.
  useEffect(() => {
    if (!chapter) return;
    const onScroll = () => {
      const max = document.body.scrollHeight - window.innerHeight;
      const position = max <= 0 ? 1 : Math.min(1, Math.max(0, window.scrollY / max));
      setPct(position);
      save(position);
      if (position >= 0.98 && !finished.current) {
        finished.current = true;
        save(1, true);
        api.addHistory(id, chapterId, "finish", chapter.title).catch(() => {});
      }
    };
    window.addEventListener("scroll", onScroll, { passive: true });
    onScroll();
    return () => {
      window.removeEventListener("scroll", onScroll);
      // Persist the latest position when leaving.
      const max = document.body.scrollHeight - window.innerHeight;
      const position = max <= 0 ? 1 : Math.min(1, Math.max(0, window.scrollY / max));
      save(position, true);
    };
  }, [chapter, id, chapterId, save]);

  if (error) return <div className="page"><p className="error">{error}</p></div>;
  if (!chapter || !novel) return <div className="page muted">loading…</div>;

  const idx = chapters.findIndex((c) => c.id === chapterId);
  const prev = idx > 0 ? chapters[idx - 1] : null;
  const next = idx >= 0 && idx < chapters.length - 1 ? chapters[idx + 1] : null;

  return (
    <div className="reader">
      <div className="reader-bar">
        <Link to={`/novels/${novel.id}`} className="ghost small">
          ← {novel.title}
        </Link>
        <div className="reader-progress">
          <div className="progress-bar">
            <span style={{ width: `${Math.round(pct * 100)}%` }} />
          </div>
          <span className="muted tiny">{Math.round(pct * 100)}%</span>
        </div>
      </div>

      <article className="reader-body">
        <h1>{chapter.title}</h1>
        <AudioPlayer
          chapterId={chapter.id}
          onTimings={handleTimings}
          onTime={handleTime}
          onPlaying={handlePlaying}
        />
        <div className="prose" ref={proseRef}>
          {paragraphs.map((para, i) => (
            <p key={i}>
              {para.map((tok, j) =>
                tok.wordIndex === undefined ? (
                  <span key={j}>{tok.text}</span>
                ) : (
                  <span
                    key={j}
                    className={`word${
                      tok.wordIndex === activeWord ? " active" : ""
                    }`}
                  >
                    {tok.text}
                  </span>
                ),
              )}
            </p>
          ))}
        </div>
      </article>

      <div className="reader-nav">
        <button
          className="ghost"
          disabled={!prev}
          onClick={() => prev && navigate(`/novels/${id}/read/${prev.id}`)}
        >
          ← Previous
        </button>
        <button
          className="ghost"
          disabled={!next}
          onClick={() => next && navigate(`/novels/${id}/read/${next.id}`)}
        >
          Next →
        </button>
      </div>
    </div>
  );
}
