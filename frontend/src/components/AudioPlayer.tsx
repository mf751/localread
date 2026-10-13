import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import type { TTSStatus, WordTiming } from "../types";

interface Props {
  chapterId: string;
  onGenerated?: () => void;
  /** Word timings for the current audio, or null when the model gives none. */
  onTimings?: (timings: WordTiming[] | null) => void;
  /** Reports playback position in seconds so the reader can highlight words. */
  onTime?: (time: number) => void;
  /** Reports whether audio is currently playing (for follow-along scrolling). */
  onPlaying?: (playing: boolean) => void;
}

/**
 * AudioPlayer requests speech for a chapter from the active TTS model. The first
 * play generates and caches the WAV on the backend; later plays stream the
 * cached file (with range support) instantly. When the model returns word
 * timings they are handed to the reader for highlighting.
 */
export default function AudioPlayer({
  chapterId,
  onGenerated,
  onTimings,
  onTime,
  onPlaying,
}: Props) {
  const [ready, setReady] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [tts, setTts] = useState<TTSStatus | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);

  useEffect(() => {
    let cancelled = false;
    setReady(false);
    setError("");
    onTimings?.(null);
    api
      .audioStatus(chapterId)
      .then((s) => {
        if (cancelled) return;
        setReady(s.ready);
        setTts(s.tts);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [chapterId]);

  // Once audio exists, ask the backend for word timings (it may have none).
  useEffect(() => {
    if (!ready) return;
    let cancelled = false;
    api
      .audioTimings(chapterId)
      .then(({ timings }) => {
        if (!cancelled) onTimings?.(timings);
      })
      .catch(() => {
        if (!cancelled) onTimings?.(null);
      });
    return () => {
      cancelled = true;
    };
  }, [ready, chapterId]);

  // Report playback position while the audio is playing (or after a seek).
  useEffect(() => {
    const audio = audioRef.current;
    if (!audio || !ready) return;
    let raf = 0;
    const tick = () => {
      onTime?.(audio.currentTime);
      raf = requestAnimationFrame(tick);
    };
    const start = () => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(tick);
      onPlaying?.(true);
    };
    const stop = () => {
      cancelAnimationFrame(raf);
      raf = 0;
      onTime?.(audio.currentTime);
      onPlaying?.(false);
    };
    // A seek while playing does not fire "play" again, so restart the loop or
    // the highlight (and follow-along) would stop until the next play/pause.
    const seeked = () => {
      if (audio.paused) stop();
      else start();
    };
    audio.addEventListener("play", start);
    audio.addEventListener("seeked", seeked);
    audio.addEventListener("pause", stop);
    audio.addEventListener("ended", stop);
    return () => {
      cancelAnimationFrame(raf);
      onPlaying?.(false);
      audio.removeEventListener("play", start);
      audio.removeEventListener("seeked", seeked);
      audio.removeEventListener("pause", stop);
      audio.removeEventListener("ended", stop);
    };
  }, [ready, onTime, onPlaying]);

  const generate = async () => {
    setBusy(true);
    setError("");
    try {
      const s = await api.generateAudio(chapterId);
      setReady(true);
      setTts(s.tts);
      onGenerated?.();
      // Give the browser a tick to see the new src.
      setTimeout(() => audioRef.current?.play().catch(() => {}), 50);
    } catch (e) {
      setError(e instanceof Error ? e.message : "generation failed");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="audio-player">
      <div className="audio-head">
        <strong>Listen</strong>
        {tts && (
          <span className={`badge ${tts.reachable ? "ok" : "warn"}`}>
            {tts.reachable ? "voice model online" : "voice model offline"} ·{" "}
            {tts.model}
            {tts.timings ? " · word timings" : ""}
          </span>
        )}
      </div>

      {ready ? (
        <audio
          ref={audioRef}
          controls
          preload="none"
          src={`${api.audioUrl(chapterId)}?v=${chapterId}`}
        />
      ) : (
        <button className="primary" onClick={generate} disabled={busy}>
          {busy ? "generating speech…" : "Generate speech"}
        </button>
      )}

      {busy && (
        <p className="muted small">
          Generating locally with {tts?.model ?? "the voice model"}. Long chapters
          are split into chunks and merged — this can take a while.
        </p>
      )}
      {error && <p className="error small">{error}</p>}
      {ready && (
        <button
          className="ghost small"
          onClick={async () => {
            await api.deleteAudio(chapterId);
            onTimings?.(null);
            setReady(false);
          }}
        >
          Regenerate
        </button>
      )}
    </div>
  );
}
