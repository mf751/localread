import type {
  AccountResponse,
  AudioStatus,
  CatalogEntry,
  Chapter,
  HistoryEvent,
  Novel,
  Progress,
  TTSProvider,
  TTSProviderInput,
  TTSProviderTest,
  User,
  WordTiming,
} from "./types";

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const { headers: initHeaders, ...rest } = init;
  const headers = new Headers(initHeaders);

  // Only default to JSON when we actually send JSON. FormData bodies must keep
  // the browser-generated multipart boundary, so never set Content-Type for
  // them (setting application/json here was what broke epub uploads).
  const isFormData =
    typeof FormData !== "undefined" && rest.body instanceof FormData;
  if (rest.body && !isFormData && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }

  const res = await fetch(`/api${path}`, {
    credentials: "same-origin",
    ...rest,
    headers,
  });
  const text = await res.text();
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) {
    throw new Error(data?.error || `request failed (${res.status})`);
  }
  return data as T;
}

export const api = {
  // auth
  me: () => request<User>("/auth/me"),
  login: (username: string, password: string) =>
    request<User>("/auth/login", {
      method: "POST",
      body: JSON.stringify({ username, password }),
    }),
  register: (username: string, password: string, displayName: string) =>
    request<User>("/auth/register", {
      method: "POST",
      body: JSON.stringify({ username, password, displayName }),
    }),
  logout: () => request<{ ok: boolean }>("/auth/logout", { method: "POST" }),

  // library
  listNovels: (q = "") =>
    request<Novel[]>(`/novels${q ? `?q=${encodeURIComponent(q)}` : ""}`),
  getNovel: (id: string) =>
    request<{ novel: Novel; chapters: Chapter[] }>(`/novels/${id}`),
  deleteNovel: (id: string) =>
    request<{ ok: boolean }>(`/novels/${id}`, { method: "DELETE" }),
  getChapter: (id: string) => request<Chapter>(`/chapters/${id}`),

  importText: (title: string, author: string, text: string) =>
    request<Novel>("/novels/import", {
      method: "POST",
      body: JSON.stringify({ title, author, text }),
    }),

  uploadFile: (file: File, title = "", author = "") => {
    const form = new FormData();
    form.append("file", file);
    if (title) form.append("title", title);
    if (author) form.append("author", author);
    return request<Novel>("/novels/import", { method: "POST", body: form });
  },

  // catalog
  catalogSearch: (q: string) =>
    request<{ entries: CatalogEntry[]; sources: string[]; errors: string[] }>(
      `/catalog/search?q=${encodeURIComponent(q)}`,
    ),
  catalogInstall: (entry: CatalogEntry) =>
    request<Novel>("/catalog/install", {
      method: "POST",
      body: JSON.stringify({
        url: entry.downloadUrl,
        title: entry.title,
        author: entry.author,
      }),
    }),

  // audio / tts
  audioStatus: (chapterId: string) =>
    request<AudioStatus>(`/chapters/${chapterId}/audio/status`),
  generateAudio: (chapterId: string) =>
    request<AudioStatus>(`/chapters/${chapterId}/audio`, { method: "POST" }),
  deleteAudio: (chapterId: string) =>
    request<{ ok: boolean }>(`/chapters/${chapterId}/audio`, {
      method: "DELETE",
    }),
  audioUrl: (chapterId: string) => `/api/chapters/${chapterId}/audio`,
  audioTimings: (chapterId: string) =>
    request<{ timings: WordTiming[] | null }>(
      `/chapters/${chapterId}/audio/timings`,
    ),

  // tts models (providers)
  listTTSProviders: () =>
    request<{ providers: TTSProvider[]; activeId: number }>("/tts/providers"),
  createTTSProvider: (p: TTSProviderInput) =>
    request<TTSProviderTest>("/tts/providers", {
      method: "POST",
      body: JSON.stringify(p),
    }),
  updateTTSProvider: (id: number, p: TTSProviderInput) =>
    request<TTSProviderTest>(`/tts/providers/${id}`, {
      method: "PUT",
      body: JSON.stringify(p),
    }),
  deleteTTSProvider: (id: number) =>
    request<{ ok: boolean }>(`/tts/providers/${id}`, { method: "DELETE" }),
  activateTTSProvider: (id: number) =>
    request<{ ok: boolean }>(`/tts/providers/${id}/activate`, {
      method: "POST",
    }),
  testTTSProvider: (id: number) =>
    request<TTSProviderTest>(`/tts/providers/${id}/test`, { method: "POST" }),

  // progress & history
  listProgress: () => request<Progress[]>("/progress"),
  getProgress: (novelId: string) =>
    request<Progress | null>(`/progress/${novelId}`),
  saveProgress: (novelId: string, chapterId: string, position: number) =>
    request<Progress>(`/progress/${novelId}`, {
      method: "PUT",
      body: JSON.stringify({ chapterId, position }),
    }),
  listHistory: (limit = 100) =>
    request<HistoryEvent[]>(`/history?limit=${limit}`),
  addHistory: (novelId: string, chapterId: string, event: string, detail = "") =>
    request<{ ok: boolean }>("/history", {
      method: "POST",
      body: JSON.stringify({ novelId, chapterId, event, detail }),
    }),

  // account
  account: () => request<AccountResponse>("/account"),
  updateAccount: (displayName: string) =>
    request<User>("/account", {
      method: "PUT",
      body: JSON.stringify({ displayName }),
    }),
  changePassword: (currentPassword: string, newPassword: string) =>
    request<{ ok: boolean }>("/account/password", {
      method: "PUT",
      body: JSON.stringify({ currentPassword, newPassword }),
    }),
};
