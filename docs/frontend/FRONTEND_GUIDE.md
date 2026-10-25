# EthioScript — Complete Frontend Developer Guide

> **Stack:** Next.js 14+ (App Router) · TypeScript · Tailwind CSS · Axios  
> **Backend:** 5 microservices on localhost — no API gateway needed in dev  
> **Auth:** JWT Bearer tokens (access + refresh rotation)

---

## Table of Contents

1. [Project Setup](#1-project-setup)
2. [Environment Variables](#2-environment-variables)
3. [Service URL Map](#3-service-url-map)
4. [API Client Setup](#4-api-client-setup)
5. [Authentication Flow](#5-authentication-flow)
6. [Account Service API](#6-account-service-api)
7. [Document Service API](#7-document-service-api)
8. [Job Orchestrator API](#8-job-orchestrator-api)
9. [AI Chat Service API](#9-ai-chat-service-api)
10. [File Upload Flow](#10-file-upload-flow)
11. [Types Reference](#11-types-reference)
12. [React Hooks](#12-react-hooks)
13. [Token Management](#13-token-management)
14. [Error Handling](#14-error-handling)
15. [Routing Structure](#15-routing-structure)

---

## 1. Project Setup

```bash
# Create Next.js app inside the monorepo
cd apps/web
npx create-next-app@latest . --typescript --tailwind --app --src-dir

# Install API + utility dependencies
npm install axios @tanstack/react-query zustand js-cookie
npm install -D @types/js-cookie
```

---

## 2. Environment Variables

Create `apps/web/.env.local`:

```bash
# ── Backend service URLs (server-side + client-side) ─────────────────────────
NEXT_PUBLIC_ACCOUNT_SERVICE_URL=http://localhost:8000
NEXT_PUBLIC_DOCUMENT_SERVICE_URL=http://localhost:6001
NEXT_PUBLIC_JOB_SERVICE_URL=http://localhost:6002
NEXT_PUBLIC_AI_CHAT_SERVICE_URL=http://localhost:6006

# ── AI Chat shared secret (server-side ONLY — never prefix with NEXT_PUBLIC_) ─
AI_CHAT_SECRET=EthioAISecret2026!

# ── Internal key for document/job services (server-side ONLY) ─────────────────
INTERNAL_API_KEY=EthioInternalKey2026!
```

> **Rule:** Only values the browser needs get `NEXT_PUBLIC_`. Secret keys must never have it.

---

## 3. Service URL Map

| Service | URL | Swagger UI | Purpose |
|---|---|---|---|
| account-service | `http://localhost:8000` | `/docs` | Auth, users, tenants, API keys |
| document-service | `http://localhost:6001` | `/docs` | Documents, assets, results, exports |
| job-orchestrator | `http://localhost:6002` | `/docs` | Job status and cancellation |
| ai-chat | `http://localhost:6006` | `/docs` | Chat, voice, translation, analysis |
| pgadmin | `http://localhost:6100` | — | Database admin |
| grafana | `http://localhost:6201` | — | Monitoring dashboards |
| minio console | `http://localhost:6303` | — | Object storage |
| rabbitmq | `http://localhost:6305` | — | Message broker management |

---

## 4. API Client Setup

### `src/lib/api/client.ts`

```typescript
import axios, { AxiosInstance, InternalAxiosRequestConfig } from "axios";

// ── Token storage ──────────────────────────────────────────────────────────
const ACCESS_TOKEN_KEY  = "ethio_access_token";
const REFRESH_TOKEN_KEY = "ethio_refresh_token";

export const tokenStore = {
  getAccess:     () => localStorage.getItem(ACCESS_TOKEN_KEY) ?? "",
  getRefresh:    () => localStorage.getItem(REFRESH_TOKEN_KEY) ?? "",
  setAccess:     (t: string) => localStorage.setItem(ACCESS_TOKEN_KEY, t),
  setRefresh:    (t: string) => localStorage.setItem(REFRESH_TOKEN_KEY, t),
  clear:         () => {
    localStorage.removeItem(ACCESS_TOKEN_KEY);
    localStorage.removeItem(REFRESH_TOKEN_KEY);
  },
};

// ── Attach Bearer token to every request ─────────────────────────────────
function attachToken(config: InternalAxiosRequestConfig) {
  const token = tokenStore.getAccess();
  if (token) config.headers.Authorization = `Bearer ${token}`;
  return config;
}

// ── Account Service (auth, users, tenants, API keys) ─────────────────────
export const accountApi = axios.create({
  baseURL: process.env.NEXT_PUBLIC_ACCOUNT_SERVICE_URL,
  headers: { "Content-Type": "application/json" },
  timeout: 15_000,
});
accountApi.interceptors.request.use(attachToken);

// ── Document Service ──────────────────────────────────────────────────────
export const documentApi = axios.create({
  baseURL: process.env.NEXT_PUBLIC_DOCUMENT_SERVICE_URL,
  headers: { "Content-Type": "application/json" },
  timeout: 30_000,
});
documentApi.interceptors.request.use(attachToken);

// ── Job Orchestrator ──────────────────────────────────────────────────────
export const jobApi = axios.create({
  baseURL: process.env.NEXT_PUBLIC_JOB_SERVICE_URL,
  headers: { "Content-Type": "application/json" },
  timeout: 15_000,
});
jobApi.interceptors.request.use(attachToken);
```

### Auto refresh interceptor — `src/lib/api/refreshInterceptor.ts`

```typescript
import axios from "axios";
import { accountApi, tokenStore } from "./client";

let refreshing: Promise<string> | null = null;

export function setupRefreshInterceptor() {
  accountApi.interceptors.response.use(
    (res) => res,
    async (error) => {
      const original = error.config;
      if (error.response?.status !== 401 || original._retry) {
        return Promise.reject(error);
      }
      original._retry = true;

      if (!refreshing) {
        refreshing = accountApi
          .post("/auth/refresh", { refresh_token: tokenStore.getRefresh() })
          .then((r) => {
            tokenStore.setAccess(r.data.access_token);
            tokenStore.setRefresh(r.data.refresh_token);
            return r.data.access_token;
          })
          .finally(() => { refreshing = null; });
      }

      const newToken = await refreshing;
      original.headers.Authorization = `Bearer ${newToken}`;
      return accountApi(original);
    }
  );
}
```

---

## 5. Authentication Flow

### Register

```typescript
// src/lib/api/auth.ts
import { accountApi, tokenStore } from "./client";
import type { TokenResponse, RegisterRequest, LoginRequest } from "../types";

export async function register(data: RegisterRequest): Promise<TokenResponse> {
  const res = await accountApi.post<TokenResponse>("/auth/register", data);
  tokenStore.setAccess(res.data.access_token);
  tokenStore.setRefresh(res.data.refresh_token);
  return res.data;
}

export async function login(data: LoginRequest): Promise<TokenResponse> {
  const res = await accountApi.post<TokenResponse>("/auth/login", data);
  tokenStore.setAccess(res.data.access_token);
  tokenStore.setRefresh(res.data.refresh_token);
  return res.data;
}

export async function logout(refreshToken: string): Promise<void> {
  await accountApi.post("/auth/logout", { refresh_token: refreshToken });
  tokenStore.clear();
}

export async function getProfile() {
  const res = await accountApi.get("/auth/me");
  return res.data;
}

export async function updateProfile(name: string) {
  const res = await accountApi.put("/auth/me", { name });
  return res.data;
}

export async function changePassword(currentPassword: string, newPassword: string) {
  await accountApi.post("/auth/change-password", {
    current_password: currentPassword,
    new_password: newPassword,
  });
}

export async function forgotPassword(email: string) {
  await accountApi.post("/auth/forgot-password", { email });
}

export async function resetPassword(token: string, newPassword: string) {
  await accountApi.post("/auth/reset-password", { token, new_password: newPassword });
}

export async function verifyEmail(token: string) {
  await accountApi.post("/auth/verify-email", { token });
}
```

---

## 6. Account Service API

### Google OAuth Sign-In

```typescript
// Redirect user to Google consent page
export function initiateGoogleSignIn() {
  window.location.href = `${process.env.NEXT_PUBLIC_ACCOUNT_SERVICE_URL}/auth/google`;
}
// After Google redirects back to /auth/google/callback,
// the backend returns a TokenResponse JSON — read it in your callback page.
```

### Profile Photo Upload

```typescript
export async function uploadProfilePhoto(file: File): Promise<{ photo_url: string }> {
  const form = new FormData();
  form.append("photo", file);
  const res = await accountApi.post("/auth/me/photo", form, {
    headers: { "Content-Type": "multipart/form-data" },
  });
  return res.data;
}
```

### API Keys

```typescript
export async function createApiKey(name: string, scopes: string[]) {
  const res = await accountApi.post("/api-keys", { name, scopes });
  return res.data; // raw_key shown ONCE — store it immediately
}

export async function listApiKeys() {
  const res = await accountApi.get("/api-keys");
  return res.data as ApiKey[];
}

export async function revokeApiKey(id: string) {
  await accountApi.delete(`/api-keys/${id}`);
}
```

### User Management (Tenant Admin+)

```typescript
export async function listUsers(page = 1, pageSize = 20) {
  const res = await accountApi.get("/users", { params: { page, page_size: pageSize } });
  return res.data as ListUsersResponse;
}

export async function updateUserRole(userId: string, role: UserRole) {
  await accountApi.put(`/users/${userId}/role`, { role });
}

export async function suspendUser(userId: string) {
  await accountApi.post(`/users/${userId}/suspend`);
}

export async function activateUser(userId: string) {
  await accountApi.post(`/users/${userId}/activate`);
}
```

---

## 7. Document Service API

> **Important:** document-service requires `X-Tenant-ID` and `X-User-ID` headers.  
> These are injected from the JWT claims stored after login.

```typescript
// src/lib/api/documents.ts
import { documentApi } from "./client";
import type { Document, DocumentListResponse, ResultVersion, ExportResponse } from "../types";

// ── Header helpers ────────────────────────────────────────────────────────
function identityHeaders(tenantId: string, userId: string) {
  return {
    "X-Tenant-ID": tenantId,
    "X-User-ID": userId,
    "X-Internal-Key": process.env.NEXT_PUBLIC_INTERNAL_API_KEY ?? "",
  };
}

// ── Assets — Step 1 of upload flow ───────────────────────────────────────
export async function initUpload(
  tenantId: string,
  userId: string,
  file: File,
  checksum?: string
) {
  const res = await documentApi.post(
    "/assets",
    {
      filename:        file.name,
      media_type:      file.type,
      size_bytes:      file.size,
      checksum_sha256: checksum ?? "",
    },
    { headers: identityHeaders(tenantId, userId) }
  );
  return res.data as { asset_id: string; upload_url: string; expires_at: string };
}

// ── Upload file directly to MinIO using the pre-signed URL ───────────────
export async function uploadToMinIO(uploadUrl: string, file: File) {
  await fetch(uploadUrl, {
    method: "PUT",
    body: file,
    headers: { "Content-Type": file.type },
  });
}

// ── Documents ─────────────────────────────────────────────────────────────
export async function createDocument(
  tenantId: string,
  userId: string,
  data: {
    asset_id: string;
    filename: string;
    language?: string;
    task_type?: "OCR" | "STT";
    title?: string;
    tags?: string[];
  }
) {
  const res = await documentApi.post("/documents", data, {
    headers: identityHeaders(tenantId, userId),
  });
  return res.data as Document;
}

export async function listDocuments(
  tenantId: string,
  userId: string,
  params?: {
    page?: number;
    page_size?: number;
    status?: string;
    language?: string;
    task_type?: string;
  }
) {
  const res = await documentApi.get("/documents", {
    headers: identityHeaders(tenantId, userId),
    params,
  });
  return res.data as DocumentListResponse;
}

export async function getDocument(tenantId: string, userId: string, docId: string) {
  const res = await documentApi.get(`/documents/${docId}`, {
    headers: identityHeaders(tenantId, userId),
  });
  return res.data as Document;
}

export async function deleteDocument(tenantId: string, userId: string, docId: string) {
  await documentApi.delete(`/documents/${docId}`, {
    headers: identityHeaders(tenantId, userId),
  });
}

// ── Result & Review ───────────────────────────────────────────────────────
export async function getResult(tenantId: string, userId: string, docId: string) {
  const res = await documentApi.get(`/documents/${docId}/result`, {
    headers: identityHeaders(tenantId, userId),
  });
  return res.data as ResultVersion;
}

export async function saveDraft(
  tenantId: string,
  userId: string,
  docId: string,
  text: string
) {
  await documentApi.put(
    `/documents/${docId}/result/draft`,
    { text },
    { headers: identityHeaders(tenantId, userId) }
  );
}

export async function approveDocument(tenantId: string, userId: string, docId: string) {
  await documentApi.post(
    `/documents/${docId}/approve`,
    {},
    { headers: identityHeaders(tenantId, userId) }
  );
}

export async function retryDocument(tenantId: string, userId: string, docId: string) {
  await documentApi.post(
    `/documents/${docId}/retry`,
    {},
    { headers: identityHeaders(tenantId, userId) }
  );
}

export async function exportDocument(
  tenantId: string,
  userId: string,
  docId: string,
  format: "TXT" | "JSON" | "DOCX" = "TXT"
) {
  const res = await documentApi.post(
    `/documents/${docId}/exports`,
    { format },
    { headers: identityHeaders(tenantId, userId) }
  );
  return res.data as ExportResponse;
}
```

---

## 8. Job Orchestrator API

```typescript
// src/lib/api/jobs.ts
import { jobApi } from "./client";
import type { Job } from "../types";

function jobHeaders(tenantId: string) {
  return {
    "X-Tenant-ID": tenantId,
    "X-Internal-Key": process.env.NEXT_PUBLIC_INTERNAL_API_KEY ?? "",
  };
}

export async function getJob(tenantId: string, jobId: string) {
  const res = await jobApi.get(`/jobs/${jobId}`, {
    headers: jobHeaders(tenantId),
  });
  return res.data as Job;
}

export async function cancelJob(tenantId: string, jobId: string) {
  await jobApi.post(`/jobs/${jobId}/cancel`, {}, {
    headers: jobHeaders(tenantId),
  });
}
```

---

## 9. AI Chat Service API

> The AI service is called through a **Next.js Route Handler** (`/api/chat`) to keep  
> `AI_CHAT_SECRET` server-side only — never exposed to the browser.

### Next.js Route Handler — `src/app/api/chat/route.ts`

```typescript
import { NextRequest, NextResponse } from "next/server";

const AI_URL    = process.env.NEXT_PUBLIC_AI_CHAT_SERVICE_URL ?? "http://localhost:6006";
const AI_SECRET = process.env.AI_CHAT_SECRET ?? "";

export async function POST(req: NextRequest) {
  const body = await req.json();
  const res = await fetch(`${AI_URL}/chat`, {
    method: "POST",
    headers: {
      "Content-Type":   "application/json",
      "X-Service-Secret": AI_SECRET,
    },
    body: JSON.stringify(body),
  });
  const data = await res.json();
  if (!res.ok) return NextResponse.json(data, { status: res.status });
  return NextResponse.json(data);
}
```

### Client helper — `src/lib/api/ai.ts`

```typescript
// ── Chat ──────────────────────────────────────────────────────────────────
export async function sendChat(
  messages: { role: "user" | "assistant"; content: string }[],
  userId?: string,
  tenantId?: string
) {
  const res = await fetch("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ messages, user_id: userId, tenant_id: tenantId }),
  });
  if (!res.ok) throw new Error(`Chat error: ${res.status}`);
  return (await res.json()) as { reply: string; provider: string; model: string };
}

// ── Voice transcription ───────────────────────────────────────────────────
export async function transcribeAudio(file: File, language = "am") {
  const form = new FormData();
  form.append("file", file);
  form.append("language", language);
  const res = await fetch("/api/transcribe", {
    method: "POST",
    body: form,
  });
  if (!res.ok) throw new Error(`Transcribe error: ${res.status}`);
  return (await res.json()) as { text: string; language: string };
}

// ── Text to speech ────────────────────────────────────────────────────────
export async function textToSpeech(text: string, language = "am"): Promise<Blob> {
  const res = await fetch("/api/speak", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, language }),
  });
  if (!res.ok) throw new Error(`TTS error: ${res.status}`);
  return res.blob(); // MP3 audio blob
}

// ── Analysis ──────────────────────────────────────────────────────────────
export async function summarizeText(text: string, maxSentences = 5) {
  const res = await fetch("/api/ai/summarize", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, max_sentences: maxSentences }),
  });
  return res.json();
}

export async function translateText(text: string, targetLanguage: string) {
  const res = await fetch("/api/ai/translate", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, target_language: targetLanguage }),
  });
  return res.json();
}

export async function detectLanguage(text: string) {
  const res = await fetch("/api/ai/language", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  });
  return res.json();
}

export async function extractKeywords(text: string) {
  const res = await fetch("/api/ai/keywords", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  });
  return res.json();
}

export async function analyzeSentiment(text: string) {
  const res = await fetch("/api/ai/sentiment", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  });
  return res.json();
}

export async function correctOcr(text: string, script: "ethiopic" | "latin" | "mixed" = "ethiopic") {
  const res = await fetch("/api/ai/ocr-correct", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text, script }),
  });
  return res.json();
}
```

### AI Route Handlers for analysis — `src/app/api/ai/[operation]/route.ts`

```typescript
import { NextRequest, NextResponse } from "next/server";

const AI_URL    = process.env.NEXT_PUBLIC_AI_CHAT_SERVICE_URL ?? "http://localhost:6006";
const AI_SECRET = process.env.AI_CHAT_SECRET ?? "";

// Maps Next.js route segment to AI service endpoint
const OPERATION_MAP: Record<string, string> = {
  summarize:   "/analyze/summarize",
  translate:   "/analyze/translate",
  language:    "/analyze/language",
  keywords:    "/analyze/keywords",
  sentiment:   "/analyze/sentiment",
  "ocr-correct": "/analyze/ocr-correct",
};

export async function POST(
  req: NextRequest,
  { params }: { params: { operation: string } }
) {
  const endpoint = OPERATION_MAP[params.operation];
  if (!endpoint) return NextResponse.json({ error: "Unknown operation" }, { status: 404 });

  const body = await req.json();
  const res = await fetch(`${AI_URL}${endpoint}`, {
    method: "POST",
    headers: {
      "Content-Type":     "application/json",
      "X-Service-Secret": AI_SECRET,
    },
    body: JSON.stringify(body),
  });
  const data = await res.json();
  return NextResponse.json(data, { status: res.status });
}
```

---

## 10. File Upload Flow

Full end-to-end flow for uploading a document:

```typescript
// src/lib/api/upload.ts
import { initUpload, uploadToMinIO, createDocument } from "./documents";

export async function uploadDocument(
  tenantId: string,
  userId: string,
  file: File,
  options?: {
    language?: string;
    taskType?: "OCR" | "STT";
    title?: string;
    tags?: string[];
  }
) {
  // Step 1 — Register asset and get pre-signed MinIO URL
  const { asset_id, upload_url } = await initUpload(tenantId, userId, file);

  // Step 2 — Upload file directly to MinIO (bypasses your backend)
  await uploadToMinIO(upload_url, file);

  // Step 3 — Create document record and queue processing job
  const document = await createDocument(tenantId, userId, {
    asset_id,
    filename:  file.name,
    language:  options?.language ?? "amh",
    task_type: options?.taskType ?? "OCR",
    title:     options?.title,
    tags:      options?.tags ?? [],
  });

  return document;
  // document.status === "QUEUED" — job-orchestrator picks it up within 5 seconds
}
```

### Upload Progress Component

```tsx
// src/components/UploadDropzone.tsx
"use client";
import { useState } from "react";
import { uploadDocument } from "@/lib/api/upload";

export function UploadDropzone({
  tenantId,
  userId,
  onSuccess,
}: {
  tenantId: string;
  userId: string;
  onSuccess: (docId: string) => void;
}) {
  const [status, setStatus] = useState<"idle" | "uploading" | "done" | "error">("idle");
  const [progress, setProgress] = useState(0);

  async function handleDrop(e: React.DragEvent) {
    e.preventDefault();
    const file = e.dataTransfer.files[0];
    if (!file) return;

    setStatus("uploading");
    setProgress(33);

    try {
      const doc = await uploadDocument(tenantId, userId, file);
      setProgress(100);
      setStatus("done");
      onSuccess(doc.id);
    } catch {
      setStatus("error");
    }
  }

  return (
    <div
      onDrop={handleDrop}
      onDragOver={(e) => e.preventDefault()}
      className="border-2 border-dashed rounded-xl p-12 text-center cursor-pointer"
    >
      {status === "idle"   && <p>Drop a PDF or image here</p>}
      {status === "uploading" && <p>Uploading… {progress}%</p>}
      {status === "done"   && <p className="text-green-600">✓ Queued for processing</p>}
      {status === "error"  && <p className="text-red-600">Upload failed. Try again.</p>}
    </div>
  );
}
```

---

## 11. Types Reference

### `src/lib/types/index.ts`

```typescript
// ── Auth ──────────────────────────────────────────────────────────────────
export type UserRole =
  | "TENANT_ADMIN" | "CONTRIBUTOR" | "REVIEWER"
  | "READ_ONLY_AUDITOR" | "PLATFORM_OPERATOR";

export type UserStatus = "ACTIVE" | "SUSPENDED" | "PENDING_VERIFICATION" | "DELETED";

export interface User {
  id:                string;
  tenant_id:         string;
  email:             string;
  name:              string;
  role:              UserRole;
  status:            UserStatus;
  email_verified:    boolean;
  profile_photo_key: string | null;
  google_id:         string | null;
  last_login_at:     string | null;
  created_at:        string;
  updated_at:        string;
}

export interface TokenResponse {
  access_token:  string;
  refresh_token: string;
  expires_at:    string;
  token_type:    "Bearer";
  user:          User;
}

export interface RegisterRequest {
  tenant_slug?: string;
  name:         string;
  email:        string;
  password:     string;
}

export interface LoginRequest {
  email:    string;
  password: string;
}

export interface ApiKey {
  id:           string;
  name:         string;
  key_prefix:   string;
  scopes:       string[];
  expires_at:   string | null;
  revoked:      boolean;
  last_used_at: string | null;
  created_at:   string;
}

// ── Documents ─────────────────────────────────────────────────────────────
export type DocumentStatus =
  | "UPLOADED" | "QUEUED" | "PROCESSING" | "REVIEW_REQUIRED"
  | "APPROVED" | "FAILED" | "CANCELLED" | "ARCHIVED" | "DELETED";

export type TaskType = "OCR" | "STT" | "CLASSIFICATION" | "EXPORT";

export interface Document {
  id:               string;
  tenant_id:        string;
  asset_id:         string;
  filename:         string;
  language:         string;
  task_type:        TaskType;
  status:           DocumentStatus;
  title:            string | null;
  description:      string | null;
  tags:             string[];
  page_count:       number | null;
  duration_seconds: number | null;
  created_by:       string;
  approved_by:      string | null;
  approved_at:      string | null;
  created_at:       string;
  updated_at:       string;
}

export interface DocumentListResponse {
  documents:   Document[];
  total:       number;
  page:        number;
  page_size:   number;
  total_pages: number;
}

export interface ResultVersion {
  id:               string;
  document_id:      string;
  version:          number;
  is_approved:      boolean;
  raw_text_key:     string | null;
  approved_text:    string | null;
  confidence_score: number | null;
  model_metadata:   Record<string, unknown>;
  word_count:       number | null;
  char_count:       number | null;
  created_by:       string;
  approved_by:      string | null;
  approved_at:      string | null;
  created_at:       string;
}

export interface ExportResponse {
  export_id:  string;
  format:     string;
  object_key: string;
  status:     string;
  created_at: string;
}

// ── Jobs ──────────────────────────────────────────────────────────────────
export type JobState =
  | "PENDING" | "QUEUED" | "PROCESSING" | "REVIEW_REQUIRED"
  | "COMPLETED" | "FAILED" | "CANCELLED" | "RETRYING";

export interface Job {
  id:            string;
  document_id:   string;
  tenant_id:     string;
  task_type:     TaskType;
  state:         JobState;
  attempts:      number;
  max_attempts:  number;
  model_name:    string | null;
  model_version: string | null;
  language:      string | null;
  error_code:    string | null;
  error_message: string | null;
  priority:      number;
  started_at:    string | null;
  finished_at:   string | null;
  created_at:    string;
  updated_at:    string;
}

// ── AI ────────────────────────────────────────────────────────────────────
export interface ChatMessage {
  role:    "user" | "assistant";
  content: string;
}

export interface SummaryResponse {
  summary:             string;
  key_points:          string[];
  word_count_original: number;
  word_count_summary:  number;
  language_detected:   string | null;
}

export interface TranslateResponse {
  original_text:   string;
  translated_text: string;
  source_language: string | null;
  target_language: string;
}
```

---

## 12. React Hooks

### `src/hooks/useAuth.ts`

```typescript
"use client";
import { create } from "zustand";
import { login, logout, getProfile, register } from "@/lib/api/auth";
import { tokenStore } from "@/lib/api/client";
import type { User, LoginRequest, RegisterRequest } from "@/lib/types";

interface AuthState {
  user:      User | null;
  loading:   boolean;
  signIn:    (data: LoginRequest)    => Promise<void>;
  signUp:    (data: RegisterRequest) => Promise<void>;
  signOut:   ()                      => Promise<void>;
  loadUser:  ()                      => Promise<void>;
}

export const useAuth = create<AuthState>((set) => ({
  user:    null,
  loading: false,

  signIn: async (data) => {
    set({ loading: true });
    const res = await login(data);
    set({ user: res.user, loading: false });
  },

  signUp: async (data) => {
    set({ loading: true });
    const res = await register(data);
    set({ user: res.user, loading: false });
  },

  signOut: async () => {
    const refresh = tokenStore.getRefresh();
    if (refresh) await logout(refresh);
    set({ user: null });
  },

  loadUser: async () => {
    const token = tokenStore.getAccess();
    if (!token) return;
    set({ loading: true });
    try {
      const user = await getProfile();
      set({ user, loading: false });
    } catch {
      tokenStore.clear();
      set({ user: null, loading: false });
    }
  },
}));
```

### `src/hooks/useDocuments.ts`

```typescript
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { listDocuments, getDocument, deleteDocument, retryDocument } from "@/lib/api/documents";
import { useAuth } from "./useAuth";

export function useDocuments(params?: { status?: string; task_type?: string }) {
  const { user } = useAuth();
  return useQuery({
    queryKey: ["documents", user?.tenant_id, params],
    queryFn:  () => listDocuments(user!.tenant_id, user!.id, params),
    enabled:  !!user,
    refetchInterval: 5_000, // poll every 5s to track processing status
  });
}

export function useDocument(docId: string) {
  const { user } = useAuth();
  return useQuery({
    queryKey: ["document", docId],
    queryFn:  () => getDocument(user!.tenant_id, user!.id, docId),
    enabled:  !!user && !!docId,
    refetchInterval: (data) =>
      data?.status === "PROCESSING" || data?.status === "QUEUED" ? 3_000 : false,
  });
}

export function useRetryDocument() {
  const { user } = useAuth();
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (docId: string) => retryDocument(user!.tenant_id, user!.id, docId),
    onSuccess:  (_, docId) => qc.invalidateQueries({ queryKey: ["document", docId] }),
  });
}
```

### `src/hooks/useChat.ts`

```typescript
"use client";
import { useState, useCallback } from "react";
import { sendChat } from "@/lib/api/ai";
import type { ChatMessage } from "@/lib/types";

export function useChat(userId?: string, tenantId?: string) {
  const [messages, setMessages] = useState<ChatMessage[]>([]);
  const [loading,  setLoading]  = useState(false);
  const [error,    setError]    = useState<string | null>(null);

  const send = useCallback(async (text: string) => {
    const userMsg: ChatMessage = { role: "user", content: text };
    const updated = [...messages, userMsg];
    setMessages(updated);
    setLoading(true);
    setError(null);

    try {
      const { reply } = await sendChat(updated, userId, tenantId);
      setMessages([...updated, { role: "assistant", content: reply }]);
    } catch (e: unknown) {
      setError(e instanceof Error ? e.message : "Something went wrong");
    } finally {
      setLoading(false);
    }
  }, [messages, userId, tenantId]);

  const clear = useCallback(() => setMessages([]), []);

  return { messages, loading, error, send, clear };
}
```

---

## 13. Token Management

```
Login / Register
     │
     ▼
Store access_token  → localStorage (15 min - 24 h default)
Store refresh_token → localStorage (7 days default)
     │
     ▼
Every API request attaches: Authorization: Bearer <access_token>
     │
     ▼  (if 401 Unauthorized)
Auto-refresh interceptor calls POST /auth/refresh
     │
     ├── Success → store new tokens, retry original request
     └── Failure → clear tokens, redirect to /login
```

**Parse the access token to get user claims without an API call:**

```typescript
export function parseJwt(token: string): {
  uid: string; tid: string; email: string; role: string; name: string; exp: number;
} | null {
  try {
    return JSON.parse(atob(token.split(".")[1]));
  } catch {
    return null;
  }
}
```

---

## 14. Error Handling

### Standard error response shape (all services)

```typescript
interface ApiError {
  error: string;
  code:  string;         // e.g. "NOT_FOUND", "VALIDATION_ERROR", "AUTH_REQUIRED"
  trace_id?: string;
}
```

### Global error handler

```typescript
// src/lib/api/errorHandler.ts
import { AxiosError } from "axios";

export function getErrorMessage(err: unknown): string {
  if (err instanceof AxiosError) {
    const data = err.response?.data as ApiError | undefined;
    if (data?.error) return data.error;
    if (err.response?.status === 401) return "Session expired. Please log in again.";
    if (err.response?.status === 403) return "You don't have permission to do that.";
    if (err.response?.status === 429) return "Too many requests. Please slow down.";
    if (err.response?.status >= 500) return "Server error. Please try again later.";
  }
  return "Something went wrong.";
}
```

---

## 15. Routing Structure

Suggested Next.js App Router structure:

```
src/app/
├── (auth)/
│   ├── login/page.tsx          # POST /auth/login
│   ├── register/page.tsx       # POST /auth/register
│   ├── forgot-password/page.tsx # POST /auth/forgot-password
│   ├── reset-password/page.tsx  # POST /auth/reset-password
│   └── verify-email/page.tsx    # POST /auth/verify-email
│
├── (dashboard)/
│   ├── layout.tsx              # Auth guard — redirect to /login if no token
│   ├── page.tsx                # Overview / stats
│   ├── documents/
│   │   ├── page.tsx            # GET /documents (list)
│   │   ├── upload/page.tsx     # POST /assets → PUT MinIO → POST /documents
│   │   └── [id]/
│   │       ├── page.tsx        # GET /documents/{id}
│   │       ├── result/page.tsx # GET /documents/{id}/result
│   │       └── review/page.tsx # PUT draft + POST approve
│   ├── settings/
│   │   ├── page.tsx            # GET/PUT /auth/me, POST /auth/me/photo
│   │   ├── api-keys/page.tsx   # GET/POST/DELETE /api-keys
│   │   └── users/page.tsx      # GET/PUT /users (admin only)
│   └── ai/
│       └── page.tsx            # AI chat panel (POST /api/chat)
│
└── api/
    ├── chat/route.ts           # Server proxy → ai-chat /chat
    ├── transcribe/route.ts     # Server proxy → ai-chat /voice/transcribe
    ├── speak/route.ts          # Server proxy → ai-chat /voice/speak
    └── ai/[operation]/route.ts # Server proxy → ai-chat /analyze/*
```

### Auth guard middleware — `src/middleware.ts`

```typescript
import { NextResponse } from "next/server";
import type { NextRequest } from "next/server";

const PUBLIC_PATHS = ["/login", "/register", "/forgot-password", "/reset-password", "/verify-email"];

export function middleware(request: NextRequest) {
  const { pathname } = request.nextUrl;
  const isPublic = PUBLIC_PATHS.some((p) => pathname.startsWith(p));
  const token    = request.cookies.get("ethio_access_token")?.value;

  if (!isPublic && !token) {
    return NextResponse.redirect(new URL("/login", request.url));
  }
  if (isPublic && token) {
    return NextResponse.redirect(new URL("/", request.url));
  }
  return NextResponse.next();
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico|api/).*)"],
};
```

---

## Document Status State Machine

```
UPLOADED → QUEUED → PROCESSING → REVIEW_REQUIRED → APPROVED
                                      │
                                      └──→ FAILED → QUEUED (retry)
```

| Status | What the UI should show |
|---|---|
| `UPLOADED` | Spinner — asset uploaded, waiting |
| `QUEUED` | Spinner — job created, waiting for worker |
| `PROCESSING` | Spinner + progress — OCR/STT running |
| `REVIEW_REQUIRED` | Action button — reviewer can edit + approve |
| `APPROVED` | Green badge — result finalized |
| `FAILED` | Red badge + Retry button |
| `CANCELLED` | Grey badge |

---

*EthioScript Frontend Guide — covers account-service, document-service, job-orchestrator, ai-chat v2.0.0*
