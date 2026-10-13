export interface User {
  id: number;
  username: string;
  displayName: string;
  createdAt: string;
}

export interface Novel {
  id: string;
  title: string;
  author: string;
  description: string;
  source: string;
  sourceUrl: string;
  chapterCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface Chapter {
  id: string;
  novelId: string;
  index: number;
  title: string;
  content?: string;
  wordCount: number;
}

export interface Progress {
  novelId: string;
  novelTitle: string;
  chapterId: string;
  chapterIndex: number;
  chapterTitle: string;
  position: number;
  updatedAt: string;
}

export interface HistoryEvent {
  id: number;
  novelId: string;
  novelTitle: string;
  chapterId: string;
  chapterTitle: string;
  event: string;
  detail: string;
  createdAt: string;
}

export interface CatalogEntry {
  source: string;
  title: string;
  author: string;
  summary: string;
  downloadUrl: string;
  format: string;
}

export interface TTSStatus {
  provider: string;
  baseUrl: string;
  model: string;
  voice: string;
  speed: number;
  timings: boolean;
  reachable: boolean;
}

export interface AccountResponse {
  user: User;
  stats: Record<string, number>;
  tts: TTSStatus;
}

export interface AudioStatus {
  ready: boolean;
  bytes?: number;
  cached?: boolean;
  timings?: boolean;
  tts: TTSStatus;
}

/** One word's playback window, in seconds from the start of the chapter audio. */
export interface WordTiming {
  start: number;
  end: number;
  word: string;
}

/** Whether a model returns word timings, as detected by the backend. */
export type TimingSupport = "yes" | "no" | "unknown";

export interface TTSProvider {
  id: number;
  name: string;
  baseUrl: string;
  model: string;
  voice: string;
  speed: number;
  lang: string;
  hasKey: boolean;
  timings: TimingSupport;
  active: boolean;
  createdAt: string;
}

export interface TTSProviderInput {
  name: string;
  baseUrl: string;
  apiKey: string;
  model: string;
  voice: string;
  speed: number;
  lang: string;
}

export interface TTSProviderTest {
  provider: TTSProvider;
  reachable: boolean;
  timings: boolean;
  /** Why the probe failed, when reachable is false. */
  detail?: string;
  tts?: TTSStatus;
}
