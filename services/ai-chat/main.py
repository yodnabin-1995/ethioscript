"""
EthioScript AI Chat Service  v2.0.0
Port: 6006

Full-featured AI backend for the EthioScript platform.

Endpoints:
  Chat
    POST   /chat                    — conversational AI with history
    GET    /history/{user_id}       — load conversation history
    DELETE /history/{user_id}       — clear conversation history
    GET    /models                  — list available models from provider

  Voice
    POST   /voice/transcribe        — audio file → text (Whisper via Groq)
    POST   /voice/speak             — text → speech MP3 audio

  Analyse
    POST   /analyze/summarize       — long text → structured summary
    POST   /analyze/translate       — text → any target language
    POST   /analyze/keywords        — extract keywords & topics
    POST   /analyze/sentiment       — positive / negative / neutral + score
    POST   /analyze/language        — detect language of text
    POST   /analyze/ocr-correct     — fix Tesseract OCR errors in Ethiopian text

  System
    GET    /health                  — service + Redis status
    GET    /metrics                 — Prometheus metrics
    GET    /docs                    — Swagger UI  (FastAPI built-in)
    GET    /openapi.json            — OpenAPI 3.x spec
"""

import io
import json
import logging
import os
import time
from contextlib import asynccontextmanager
from typing import Literal

import httpx
import redis.asyncio as aioredis
from dotenv import load_dotenv
from fastapi import FastAPI, File, Form, HTTPException, Request, Security, UploadFile, status
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import StreamingResponse
from fastapi.security.api_key import APIKeyHeader
from prometheus_client import Counter, Histogram, generate_latest, CONTENT_TYPE_LATEST
from pydantic import BaseModel, Field
from starlette.responses import Response

load_dotenv()

# ── Config ────────────────────────────────────────────────────────────────────

SERVICE_SECRET   = os.environ["AI_SERVICE_SECRET"]
LLM_PROVIDER     = os.getenv("LLM_PROVIDER", "mock")
LLM_MODEL        = os.getenv("LLM_MODEL", "openai/gpt-oss-120b")
LLM_API_KEY      = os.getenv("LLM_API_KEY", "")
LLM_BASE_URL     = os.getenv("LLM_BASE_URL", "")
WHISPER_MODEL    = os.getenv("WHISPER_MODEL", "whisper-large-v3-turbo")
TTS_PROVIDER     = os.getenv("TTS_PROVIDER", "gtts")          # gtts | openai
TTS_VOICE        = os.getenv("TTS_VOICE", "alloy")            # openai TTS voice
DOCUMENT_SVC_URL = os.getenv("DOCUMENT_SERVICE_URL", "http://document-service:6001")
INTERNAL_API_KEY = os.getenv("INTERNAL_API_KEY", "")
REDIS_URL        = os.getenv("REDIS_URL", "redis://:redispass@redis:6379/2")
MAX_MESSAGES     = int(os.getenv("MAX_CONTEXT_MESSAGES", "20"))
RATE_LIMIT_RPM   = int(os.getenv("CHAT_RATE_LIMIT_RPM", "30"))
HISTORY_TTL_SEC  = int(os.getenv("CHAT_HISTORY_TTL_SEC", "86400"))
ALLOWED_ORIGINS  = os.getenv("ALLOWED_ORIGINS", "http://localhost:3000").split(",")
MAX_AUDIO_MB     = int(os.getenv("MAX_AUDIO_MB", "25"))

# ── Logging ───────────────────────────────────────────────────────────────────

logging.basicConfig(
    level=logging.INFO,
    format='{"time":"%(asctime)s","level":"%(levelname)s","svc":"ai-chat","msg":"%(message)s"}',
)
log = logging.getLogger("ai-chat")

# ── Prometheus ────────────────────────────────────────────────────────────────

CHAT_REQUESTS  = Counter("ai_chat_requests_total",     "Total chat requests",      ["provider", "status"])
VOICE_REQUESTS = Counter("ai_voice_requests_total",    "Total voice requests",     ["operation", "status"])
ANALYZE_REQ    = Counter("ai_analyze_requests_total",  "Total analysis requests",  ["operation", "status"])
CHAT_DURATION  = Histogram("ai_chat_duration_seconds", "LLM call duration",        buckets=[0.5, 1, 2, 5, 10, 20, 30])
RATE_LIMITED   = Counter("ai_chat_rate_limited_total", "Rate-limited requests")

# ── System prompt ─────────────────────────────────────────────────────────────

SYSTEM_PROMPT = """You are EthioScript AI, a helpful assistant embedded inside the EthioScript platform.
EthioScript is an AI-powered platform for processing Ethiopian language documents — supporting OCR,
speech-to-text, translation, and AI analysis for Amharic, Tigrinya, Afaan Oromoo, and English.

You help users:
- Navigate EthioScript features (OCR, STT, Translation, Document Management, Exports)
- Understand document statuses: Uploaded → Queued → Processing → Review Required → Approved / Failed
- Upload documents (POST /assets then POST /documents)
- Download and export results (POST /documents/{id}/exports)
- Retry failed documents (POST /documents/{id}/retry)
- Manage API keys and account settings
- Interpret OCR and speech-to-text results

Always be concise, friendly, and specific to EthioScript. If asked about something unrelated to
EthioScript or Ethiopian languages, politely redirect to what you can help with.
Respond in the same language the user writes in (Amharic, Tigrinya, Oromo, or English).
Use markdown: **bold** for feature names, bullet points for lists, numbered steps for instructions."""

# ═══════════════════════════════════════════════════════════════════════════════
# Pydantic models
# ═══════════════════════════════════════════════════════════════════════════════

class ChatMessage(BaseModel):
    role: Literal["user", "assistant", "system"]
    content: str = Field(..., min_length=1, max_length=32_000)

class ChatRequest(BaseModel):
    messages: list[ChatMessage] = Field(..., min_length=1, max_length=100)
    user_id: str | None = Field(None, description="User UUID for history + rate limiting")
    tenant_id: str | None = Field(None, description="Tenant UUID for document context")
    save_history: bool = Field(True, description="Persist conversation to Redis")
    max_tokens: int = Field(1024, ge=64, le=4096, description="Max reply tokens")
    temperature: float = Field(0.7, ge=0.0, le=2.0, description="LLM temperature")

class ChatResponse(BaseModel):
    reply: str
    provider: str
    model: str

class HistoryResponse(BaseModel):
    user_id: str
    messages: list[ChatMessage]
    count: int

class HealthResponse(BaseModel):
    status: str
    provider: str
    model: str
    redis: str
    version: str = "2.0.0"

class ModelInfo(BaseModel):
    id: str
    owned_by: str | None = None

class ModelsResponse(BaseModel):
    provider: str
    models: list[ModelInfo]

# ── Voice models ──────────────────────────────────────────────────────────────

class TranscribeResponse(BaseModel):
    text: str
    language: str | None = None
    duration_seconds: float | None = None
    provider: str

class SpeakRequest(BaseModel):
    text: str = Field(..., min_length=1, max_length=4096, description="Text to convert to speech")
    language: str = Field("am", description="BCP-47 language code: am, ti, en, om")
    voice: str = Field("alloy", description="Voice ID (for OpenAI TTS: alloy/echo/fable/onyx/nova/shimmer)")
    speed: float = Field(1.0, ge=0.25, le=4.0, description="Speech speed multiplier")

# ── Analysis models ───────────────────────────────────────────────────────────

class AnalyzeTextRequest(BaseModel):
    text: str = Field(..., min_length=10, max_length=50_000, description="Text to analyse")
    language: str | None = Field(None, description="Source language hint (amh, eng, tir, orm)")
    user_id: str | None = None
    tenant_id: str | None = None

class SummarizeRequest(AnalyzeTextRequest):
    max_sentences: int = Field(5, ge=1, le=20, description="Target summary length in sentences")
    style: Literal["bullet", "paragraph", "structured"] = Field(
        "structured", description="Output format"
    )

class SummaryResponse(BaseModel):
    summary: str
    key_points: list[str]
    word_count_original: int
    word_count_summary: int
    language_detected: str | None = None

class TranslateRequest(AnalyzeTextRequest):
    target_language: str = Field(..., description="Target language (amh, eng, tir, orm, fra, ara, ...)")

class TranslateResponse(BaseModel):
    original_text: str
    translated_text: str
    source_language: str | None = None
    target_language: str

class KeywordsResponse(BaseModel):
    keywords: list[str]
    topics: list[str]
    named_entities: list[str]
    language_detected: str | None = None

class SentimentResponse(BaseModel):
    sentiment: Literal["positive", "negative", "neutral", "mixed"]
    score: float = Field(..., ge=-1.0, le=1.0, description="-1.0=very negative, +1.0=very positive")
    explanation: str
    language_detected: str | None = None

class LanguageResponse(BaseModel):
    language_code: str
    language_name: str
    confidence: float = Field(..., ge=0.0, le=1.0)
    alternatives: list[dict]

class OcrCorrectRequest(AnalyzeTextRequest):
    script: Literal["ethiopic", "latin", "mixed"] = Field(
        "ethiopic", description="Expected script for targeted correction"
    )

class OcrCorrectResponse(BaseModel):
    corrected_text: str
    corrections_made: int
    confidence: float

# ═══════════════════════════════════════════════════════════════════════════════
# Redis
# ═══════════════════════════════════════════════════════════════════════════════

_redis: aioredis.Redis | None = None

async def get_redis() -> aioredis.Redis | None:
    global _redis
    if _redis is None:
        try:
            _redis = await aioredis.from_url(REDIS_URL, decode_responses=True)
            await _redis.ping()
        except Exception as exc:
            log.warning("Redis unavailable: %s", exc)
            _redis = None
    return _redis

async def check_rate_limit(user_id: str) -> bool:
    rdb = await get_redis()
    if rdb is None:
        return True
    key = f"ai_chat:rate:{user_id}"
    try:
        count = await rdb.incr(key)
        if count == 1:
            await rdb.expire(key, 60)
        return count <= RATE_LIMIT_RPM
    except Exception:
        return True

async def save_history(user_id: str, messages: list[ChatMessage]) -> None:
    rdb = await get_redis()
    if rdb is None:
        return
    try:
        await rdb.set(
            f"ai_chat:history:{user_id}",
            json.dumps([m.model_dump() for m in messages]),
            ex=HISTORY_TTL_SEC,
        )
    except Exception as exc:
        log.warning("Could not save history for %s: %s", user_id, exc)

async def load_history(user_id: str) -> list[ChatMessage]:
    rdb = await get_redis()
    if rdb is None:
        return []
    try:
        data = await rdb.get(f"ai_chat:history:{user_id}")
        if data:
            return [ChatMessage(**m) for m in json.loads(data)]
    except Exception as exc:
        log.warning("Could not load history for %s: %s", user_id, exc)
    return []

# ═══════════════════════════════════════════════════════════════════════════════
# LLM helpers
# ═══════════════════════════════════════════════════════════════════════════════

def _llm_base_url() -> str:
    if LLM_BASE_URL:
        return LLM_BASE_URL
    return (
        "https://api.openai.com/v1" if LLM_PROVIDER == "openai"
        else "https://api.groq.com/openai/v1"
    )

async def call_llm(
    messages: list[dict],
    max_tokens: int = 1024,
    temperature: float = 0.7,
) -> str:
    if LLM_PROVIDER == "mock":
        last = next((m["content"] for m in reversed(messages) if m["role"] == "user"), "")
        return (
            f"👋 **EthioScript AI (mock mode)**\n\n"
            f"You said: _{last[:120]}_\n\n"
            f"Set `LLM_PROVIDER=openai|groq|ollama` and add your API key for real responses."
        )

    if LLM_PROVIDER in ("openai", "groq"):
        if not LLM_API_KEY:
            raise HTTPException(status_code=503, detail="LLM_API_KEY not configured")
        async with httpx.AsyncClient(timeout=60) as client:
            res = await client.post(
                f"{_llm_base_url()}/chat/completions",
                headers={"Authorization": f"Bearer {LLM_API_KEY}", "Content-Type": "application/json"},
                json={"model": LLM_MODEL, "messages": messages,
                      "max_tokens": max_tokens, "temperature": temperature},
            )
        if res.status_code != 200:
            log.error("LLM error %s: %s", res.status_code, res.text[:400])
            raise HTTPException(status_code=502, detail="LLM provider error")
        return res.json()["choices"][0]["message"]["content"]

    if LLM_PROVIDER == "ollama":
        base_url = LLM_BASE_URL or "http://localhost:11434"
        async with httpx.AsyncClient(timeout=120) as client:
            res = await client.post(
                f"{base_url}/api/chat",
                json={"model": LLM_MODEL, "messages": messages, "stream": False},
            )
        if res.status_code != 200:
            raise HTTPException(status_code=502, detail="Ollama error")
        return res.json()["message"]["content"]

    raise HTTPException(status_code=500, detail=f"Unknown LLM_PROVIDER: {LLM_PROVIDER}")

async def call_llm_json(system: str, user: str) -> dict:
    """Call LLM and parse the response as JSON."""
    raw = await call_llm([
        {"role": "system", "content": system},
        {"role": "user",   "content": user},
    ], max_tokens=2048, temperature=0.3)
    # Extract JSON block if wrapped in markdown
    text = raw.strip()
    if "```json" in text:
        text = text.split("```json")[1].split("```")[0].strip()
    elif "```" in text:
        text = text.split("```")[1].split("```")[0].strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        return {"raw": text}

# ── Document context ──────────────────────────────────────────────────────────

async def fetch_user_context(tenant_id: str, user_id: str) -> str:
    try:
        async with httpx.AsyncClient(timeout=5) as client:
            res = await client.get(
                f"{DOCUMENT_SVC_URL}/documents",
                headers={
                    "X-Tenant-ID":    tenant_id,
                    "X-User-ID":      user_id,
                    "X-Internal-Key": INTERNAL_API_KEY,
                },
                params={"page": 1, "page_size": 5},
            )
            if res.status_code != 200:
                return ""
            docs = res.json().get("documents", [])
            if not docs:
                return ""
            lines = ["User's recent documents:"]
            for d in docs:
                lines.append(
                    f"- **{d.get('filename','?')}** "
                    f"(type:{d.get('task_type','?')}, "
                    f"lang:{d.get('language','?')}, "
                    f"status:**{d.get('status','?')}**)"
                )
            return "\n".join(lines)
    except Exception as exc:
        log.warning("Could not fetch user context: %s", exc)
        return ""

# ═══════════════════════════════════════════════════════════════════════════════
# App
# ═══════════════════════════════════════════════════════════════════════════════

@asynccontextmanager
async def lifespan(app: FastAPI):
    log.info("AI Chat Service v2.0.0 starting | provider=%s model=%s", LLM_PROVIDER, LLM_MODEL)
    await get_redis()
    yield
    log.info("AI Chat Service stopped")

app = FastAPI(
    title="EthioScript AI Service",
    version="2.0.0",
    description="""
## EthioScript AI Service

AI-powered backend for the EthioScript document intelligence platform.
Supports chat, voice transcription, text-to-speech, document analysis,
translation, and more — all optimised for Ethiopian languages.

### Authentication
All endpoints require the `X-Service-Secret` header.
Click the **Authorize** button above and enter your secret key.

### Supported Languages
`amh` Amharic · `tir` Tigrinya · `orm` Afaan Oromoo · `eng` English

### LLM Providers
`groq` · `openai` · `ollama` · `mock`
    """,
    contact={"name": "EthioScript Team", "email": "api@ethioscript.io"},
    license_info={"name": "Proprietary"},
    lifespan=lifespan,
    docs_url="/docs",
    redoc_url="/redoc",
    openapi_url="/openapi.json",
)

# ── Security scheme — shows Authorize button in Swagger UI ───────────────────
api_key_header = APIKeyHeader(
    name="X-Service-Secret",
    scheme_name="ServiceSecret",
    description="Shared secret key — value of `AI_SERVICE_SECRET` in .env",
    auto_error=False,
)

async def get_api_key(api_key: str = Security(api_key_header)) -> str:
    if api_key != SERVICE_SECRET:
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail="Invalid or missing X-Service-Secret header",
            headers={"WWW-Authenticate": "ApiKey"},
        )
    return api_key

app.add_middleware(
    CORSMiddleware,
    allow_origins=ALLOWED_ORIGINS,
    allow_methods=["POST", "GET", "DELETE", "OPTIONS"],
    allow_headers=["*"],
)

def verify_secret(request: Request) -> None:
    """Legacy helper kept for reference. Use get_api_key Security dependency instead."""
    if request.headers.get("X-Service-Secret", "") != SERVICE_SECRET:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED,
                            detail="Invalid or missing X-Service-Secret header")# ═══════════════════════════════════════════════════════════════════════════════
# SYSTEM routes
# ═══════════════════════════════════════════════════════════════════════════════

@app.get(
    "/health",
    response_model=HealthResponse,
    tags=["system"],
    summary="Service health check",
    description="Returns service status, active LLM provider/model, and Redis connectivity.",
)
async def health():
    rdb = await get_redis()
    return HealthResponse(
        status="ok",
        provider=LLM_PROVIDER,
        model=LLM_MODEL,
        redis="ok" if rdb is not None else "unavailable",
    )

@app.get(
    "/metrics",
    tags=["system"],
    summary="Prometheus metrics",
    description="Exposes Prometheus metrics for chat, voice, and analysis requests.",
)
async def metrics():
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)

@app.get(
    "/models",
    response_model=ModelsResponse,
    tags=["system"],
    summary="List available LLM models",
    description="Queries the active LLM provider and returns all models available on your account.",
)
async def list_models(api_key: str = Security(get_api_key)):
    if LLM_PROVIDER == "mock":
        return ModelsResponse(provider="mock", models=[ModelInfo(id="mock", owned_by="ethioscript")])
    if LLM_PROVIDER in ("openai", "groq"):
        if not LLM_API_KEY:
            raise HTTPException(status_code=503, detail="LLM_API_KEY not configured")
        async with httpx.AsyncClient(timeout=10) as client:
            res = await client.get(
                f"{_llm_base_url()}/models",
                headers={"Authorization": f"Bearer {LLM_API_KEY}"},
            )
        if res.status_code != 200:
            raise HTTPException(status_code=502, detail="Could not fetch models from provider")
        data = res.json().get("data", [])
        models = [ModelInfo(id=m["id"], owned_by=m.get("owned_by")) for m in data]
        return ModelsResponse(provider=LLM_PROVIDER, models=models)
    return ModelsResponse(provider=LLM_PROVIDER, models=[ModelInfo(id=LLM_MODEL)])

# ═══════════════════════════════════════════════════════════════════════════════
# CHAT routes
# ═══════════════════════════════════════════════════════════════════════════════

@app.post(
    "/chat",
    response_model=ChatResponse,
    tags=["chat"],
    summary="Conversational AI chat",
    description="""
Send a conversation history and receive an AI reply.

- Injects user's recent documents as context when `tenant_id` and `user_id` are provided
- Stores conversation in Redis when `save_history=true`
- Per-user rate limited (default 30 req/min)
- Responds in the same language the user writes in
    """,
)
async def chat(request: Request, body: ChatRequest, api_key: str = Security(get_api_key)):
    rate_key = body.user_id or request.client.host
    if not await check_rate_limit(rate_key):
        RATE_LIMITED.inc()
        raise HTTPException(status_code=429,
                            detail=f"Rate limit exceeded — max {RATE_LIMIT_RPM} req/min")

    system_content = SYSTEM_PROMPT
    if body.tenant_id and body.user_id:
        ctx = await fetch_user_context(body.tenant_id, body.user_id)
        if ctx:
            system_content += f"\n\n{ctx}"

    history = body.messages[-MAX_MESSAGES:]
    messages = [{"role": "system", "content": system_content}]
    messages += [{"role": m.role, "content": m.content} for m in history]

    log.info("chat | user=%s tenant=%s msgs=%d", body.user_id, body.tenant_id, len(history))

    start = time.monotonic()
    try:
        reply = await call_llm(messages, body.max_tokens, body.temperature)
        CHAT_REQUESTS.labels(provider=LLM_PROVIDER, status="success").inc()
    except HTTPException:
        CHAT_REQUESTS.labels(provider=LLM_PROVIDER, status="error").inc()
        raise
    finally:
        CHAT_DURATION.observe(time.monotonic() - start)

    if body.save_history and body.user_id:
        updated = list(history) + [ChatMessage(role="assistant", content=reply)]
        await save_history(body.user_id, updated)

    return ChatResponse(reply=reply, provider=LLM_PROVIDER, model=LLM_MODEL)

@app.get(
    "/history/{user_id}",
    response_model=HistoryResponse,
    tags=["chat"],
    summary="Get conversation history",
    description="Retrieves the stored conversation history for a user from Redis.",
)
async def get_history(user_id: str, api_key: str = Security(get_api_key)):
    messages = await load_history(user_id)
    return HistoryResponse(user_id=user_id, messages=messages, count=len(messages))

@app.delete(
    "/history/{user_id}",
    status_code=204,
    tags=["chat"],
    summary="Clear conversation history",
    description="Deletes the stored conversation history for a user from Redis.",
)
async def clear_history(user_id: str, api_key: str = Security(get_api_key)):
    rdb = await get_redis()
    if rdb:
        await rdb.delete(f"ai_chat:history:{user_id}")

# ═══════════════════════════════════════════════════════════════════════════════
# VOICE routes
# ═══════════════════════════════════════════════════════════════════════════════

@app.post(
    "/voice/transcribe",
    response_model=TranscribeResponse,
    tags=["voice"],
    summary="Transcribe audio to text",
    description="""
Transcribes an uploaded audio file to text using Whisper.

**Supported formats:** MP3, MP4, WAV, OGG, WEBM, FLAC, M4A

**Max file size:** 25 MB (configurable via `MAX_AUDIO_MB`)

**Languages:** Optimised for Amharic, Tigrinya, Oromo, and English.
Pass the `language` hint for better accuracy.

When `LLM_PROVIDER=groq`, uses Groq's Whisper API.
When `LLM_PROVIDER=openai`, uses OpenAI's Whisper API.
In `mock` mode returns a placeholder transcript.
    """,
)
async def transcribe_audio(
    file: UploadFile = File(..., description="Audio file (MP3/WAV/OGG/WEBM/FLAC/M4A, max 25 MB)"),
    language: str = Form("", description="Language hint: am, ti, en, om (leave empty for auto-detect)"),
    api_key: str = Security(get_api_key),
):
    start = time.monotonic()

    # Validate file size
    content = await file.read()
    size_mb = len(content) / (1024 * 1024)
    if size_mb > MAX_AUDIO_MB:
        raise HTTPException(status_code=413,
                            detail=f"Audio file too large ({size_mb:.1f} MB). Max: {MAX_AUDIO_MB} MB")

    log.info("transcribe | file=%s size=%.1fMB lang=%s", file.filename, size_mb, language)

    # Mock mode
    if LLM_PROVIDER == "mock":
        VOICE_REQUESTS.labels(operation="transcribe", status="success").inc()
        return TranscribeResponse(
            text="[Mock transcription] This is a placeholder. Set LLM_PROVIDER=groq or openai for real transcription.",
            language="en",
            duration_seconds=None,
            provider="mock",
        )

    # Groq Whisper API
    if LLM_PROVIDER == "groq":
        if not LLM_API_KEY:
            raise HTTPException(status_code=503, detail="LLM_API_KEY not configured")
        try:
            async with httpx.AsyncClient(timeout=120) as client:
                files = {"file": (file.filename or "audio.mp3", io.BytesIO(content), file.content_type or "audio/mpeg")}
                data = {"model": WHISPER_MODEL}
                if language:
                    data["language"] = language
                res = await client.post(
                    "https://api.groq.com/openai/v1/audio/transcriptions",
                    headers={"Authorization": f"Bearer {LLM_API_KEY}"},
                    files=files,
                    data=data,
                )
            if res.status_code != 200:
                log.error("Groq Whisper error %s: %s", res.status_code, res.text[:300])
                raise HTTPException(status_code=502, detail="Whisper transcription failed")
            result = res.json()
            VOICE_REQUESTS.labels(operation="transcribe", status="success").inc()
            return TranscribeResponse(
                text=result.get("text", ""),
                language=result.get("language", language or None),
                duration_seconds=round(time.monotonic() - start, 2),
                provider="groq-whisper",
            )
        except HTTPException:
            VOICE_REQUESTS.labels(operation="transcribe", status="error").inc()
            raise

    # OpenAI Whisper API
    if LLM_PROVIDER == "openai":
        if not LLM_API_KEY:
            raise HTTPException(status_code=503, detail="LLM_API_KEY not configured")
        try:
            async with httpx.AsyncClient(timeout=120) as client:
                files = {"file": (file.filename or "audio.mp3", io.BytesIO(content), file.content_type or "audio/mpeg")}
                data = {"model": "whisper-1"}
                if language:
                    data["language"] = language
                res = await client.post(
                    "https://api.openai.com/v1/audio/transcriptions",
                    headers={"Authorization": f"Bearer {LLM_API_KEY}"},
                    files=files,
                    data=data,
                )
            if res.status_code != 200:
                raise HTTPException(status_code=502, detail="Whisper transcription failed")
            result = res.json()
            VOICE_REQUESTS.labels(operation="transcribe", status="success").inc()
            return TranscribeResponse(
                text=result.get("text", ""),
                language=result.get("language", language or None),
                duration_seconds=round(time.monotonic() - start, 2),
                provider="openai-whisper",
            )
        except HTTPException:
            VOICE_REQUESTS.labels(operation="transcribe", status="error").inc()
            raise

    raise HTTPException(status_code=501, detail=f"Transcription not supported for provider: {LLM_PROVIDER}")


@app.post(
    "/voice/speak",
    tags=["voice"],
    summary="Text to speech",
    description="""
Converts text to speech and returns an MP3 audio file.

**Languages:** `am` Amharic · `ti` Tigrinya · `en` English · `om` Oromo

When `TTS_PROVIDER=openai`, uses OpenAI's TTS API (requires `LLM_API_KEY`).
When `TTS_PROVIDER=gtts`, uses Google Text-to-Speech (free, no key needed).
    """,
    response_class=StreamingResponse,
    responses={
        200: {
            "content": {"audio/mpeg": {}},
            "description": "MP3 audio stream",
        }
    },
)
async def text_to_speech(body: SpeakRequest, api_key: str = Security(get_api_key)):

    lang_map = {"am": "am", "ti": "ti", "en": "en", "om": "om", "amh": "am", "tir": "ti", "eng": "en", "orm": "om"}
    lang = lang_map.get(body.language, "en")

    log.info("tts | lang=%s length=%d provider=%s", lang, len(body.text), TTS_PROVIDER)

    # OpenAI TTS
    if TTS_PROVIDER == "openai":
        if not LLM_API_KEY:
            raise HTTPException(status_code=503, detail="LLM_API_KEY required for OpenAI TTS")
        async with httpx.AsyncClient(timeout=30) as client:
            res = await client.post(
                "https://api.openai.com/v1/audio/speech",
                headers={"Authorization": f"Bearer {LLM_API_KEY}", "Content-Type": "application/json"},
                json={"model": "tts-1", "input": body.text, "voice": body.voice, "speed": body.speed},
            )
        if res.status_code != 200:
            raise HTTPException(status_code=502, detail="OpenAI TTS failed")
        VOICE_REQUESTS.labels(operation="speak", status="success").inc()
        return StreamingResponse(
            io.BytesIO(res.content),
            media_type="audio/mpeg",
            headers={"Content-Disposition": "attachment; filename=speech.mp3"},
        )

    # gTTS (free, works for Amharic, Tigrinya, English)
    try:
        from gtts import gTTS
        tts = gTTS(text=body.text, lang=lang, slow=(body.speed < 0.8))
        buf = io.BytesIO()
        tts.write_to_fp(buf)
        buf.seek(0)
        VOICE_REQUESTS.labels(operation="speak", status="success").inc()
        return StreamingResponse(
            buf,
            media_type="audio/mpeg",
            headers={"Content-Disposition": "attachment; filename=speech.mp3"},
        )
    except ImportError:
        raise HTTPException(status_code=501, detail="gTTS not installed. Set TTS_PROVIDER=openai or add gTTS to requirements.")
    except Exception as exc:
        VOICE_REQUESTS.labels(operation="speak", status="error").inc()
        log.error("gTTS error: %s", exc)
        raise HTTPException(status_code=502, detail=f"TTS failed: {exc}")

# ═══════════════════════════════════════════════════════════════════════════════
# ANALYSE routes
# ═══════════════════════════════════════════════════════════════════════════════

@app.post(
    "/analyze/summarize",
    response_model=SummaryResponse,
    tags=["analyze"],
    summary="Summarize document text",
    description="""
Summarizes long text using the configured LLM.

Supports Amharic, Tigrinya, Oromo, and English text.
Output format can be `bullet`, `paragraph`, or `structured` (default).
Returns the summary, key points, and word counts.
    """,
)
async def summarize(body: SummarizeRequest, api_key: str = Security(get_api_key)):

    system = (
        "You are a document summarization assistant specializing in Ethiopian language documents. "
        "Always respond with valid JSON only — no markdown, no explanation outside the JSON."
    )
    user = f"""Summarize the following text in {body.max_sentences} sentences using the '{body.style}' format.

Text language hint: {body.language or 'auto-detect'}

Return JSON with this exact structure:
{{
  "summary": "...",
  "key_points": ["point1", "point2", "point3"],
  "language_detected": "amh|eng|tir|orm"
}}

Text to summarize:
{body.text[:10000]}"""

    log.info("summarize | words=%d style=%s", len(body.text.split()), body.style)
    start = time.monotonic()
    try:
        result = await call_llm_json(system, user)
        ANALYZE_REQ.labels(operation="summarize", status="success").inc()
    except HTTPException:
        ANALYZE_REQ.labels(operation="summarize", status="error").inc()
        raise

    summary = result.get("summary") or result.get("raw", "")
    key_points = result.get("key_points", [])
    if isinstance(key_points, str):
        key_points = [key_points]

    log.info("summarize done | duration=%.2fs", time.monotonic() - start)
    return SummaryResponse(
        summary=summary,
        key_points=key_points,
        word_count_original=len(body.text.split()),
        word_count_summary=len(summary.split()),
        language_detected=result.get("language_detected"),
    )


@app.post(
    "/analyze/translate",
    response_model=TranslateResponse,
    tags=["analyze"],
    summary="Translate text",
    description="""
Translates text between any languages, with special support for:
- **Amharic** (amh) ↔ English, Tigrinya, Oromo, Arabic, French
- **Tigrinya** (tir) ↔ Amharic, English
- **Afaan Oromoo** (orm) ↔ Amharic, English

The LLM auto-detects the source language if not specified.
    """,
)
async def translate(body: TranslateRequest, api_key: str = Security(get_api_key)):

    lang_names = {
        "amh": "Amharic", "eng": "English", "en": "English",
        "tir": "Tigrinya", "orm": "Afaan Oromoo",
        "fra": "French", "ara": "Arabic", "som": "Somali",
    }
    target_name = lang_names.get(body.target_language, body.target_language)

    system = (
        "You are a professional translator specializing in Ethiopian languages. "
        "Respond with valid JSON only."
    )
    user = f"""Translate the following text to {target_name}.

Return JSON with this exact structure:
{{
  "translated_text": "...",
  "source_language": "detected language name"
}}

Text to translate:
{body.text[:8000]}"""

    log.info("translate | target=%s words=%d", body.target_language, len(body.text.split()))
    try:
        result = await call_llm_json(system, user)
        ANALYZE_REQ.labels(operation="translate", status="success").inc()
    except HTTPException:
        ANALYZE_REQ.labels(operation="translate", status="error").inc()
        raise

    return TranslateResponse(
        original_text=body.text,
        translated_text=result.get("translated_text") or result.get("raw", ""),
        source_language=result.get("source_language"),
        target_language=target_name,
    )


@app.post(
    "/analyze/keywords",
    response_model=KeywordsResponse,
    tags=["analyze"],
    summary="Extract keywords and topics",
    description="""
Extracts keywords, topics, and named entities from text.

Particularly useful for:
- Indexing OCR-extracted Amharic/Tigrinya documents
- Building searchable metadata for the document library
- Generating document tags automatically
    """,
)
async def extract_keywords(body: AnalyzeTextRequest, api_key: str = Security(get_api_key)):

    system = "You are a keyword extraction specialist for Ethiopian language documents. Respond with valid JSON only."
    user = f"""Extract keywords, topics, and named entities from the following text.

Return JSON:
{{
  "keywords": ["word1", "word2", ...],
  "topics": ["topic1", "topic2", ...],
  "named_entities": ["entity1", "entity2", ...],
  "language_detected": "amh|eng|tir|orm"
}}

Text:
{body.text[:8000]}"""

    log.info("keywords | words=%d", len(body.text.split()))
    try:
        result = await call_llm_json(system, user)
        ANALYZE_REQ.labels(operation="keywords", status="success").inc()
    except HTTPException:
        ANALYZE_REQ.labels(operation="keywords", status="error").inc()
        raise

    return KeywordsResponse(
        keywords=result.get("keywords", []),
        topics=result.get("topics", []),
        named_entities=result.get("named_entities", []),
        language_detected=result.get("language_detected"),
    )


@app.post(
    "/analyze/sentiment",
    response_model=SentimentResponse,
    tags=["analyze"],
    summary="Sentiment analysis",
    description="""
Analyses the sentiment of text and returns a score from -1.0 (very negative) to +1.0 (very positive).

Works with Amharic, Tigrinya, Oromo, and English.
Useful for analysing feedback, reviews, and document content.
    """,
)
async def analyze_sentiment(body: AnalyzeTextRequest, api_key: str = Security(get_api_key)):

    system = "You are a sentiment analysis specialist for Ethiopian and English text. Respond with valid JSON only."
    user = f"""Analyse the sentiment of the following text.

Return JSON:
{{
  "sentiment": "positive|negative|neutral|mixed",
  "score": <float between -1.0 and 1.0>,
  "explanation": "brief explanation in English",
  "language_detected": "amh|eng|tir|orm"
}}

Text:
{body.text[:4000]}"""

    log.info("sentiment | words=%d", len(body.text.split()))
    try:
        result = await call_llm_json(system, user)
        ANALYZE_REQ.labels(operation="sentiment", status="success").inc()
    except HTTPException:
        ANALYZE_REQ.labels(operation="sentiment", status="error").inc()
        raise

    sentiment = result.get("sentiment", "neutral")
    if sentiment not in ("positive", "negative", "neutral", "mixed"):
        sentiment = "neutral"
    score = float(result.get("score", 0.0))
    score = max(-1.0, min(1.0, score))

    return SentimentResponse(
        sentiment=sentiment,
        score=score,
        explanation=result.get("explanation", ""),
        language_detected=result.get("language_detected"),
    )


@app.post(
    "/analyze/language",
    response_model=LanguageResponse,
    tags=["analyze"],
    summary="Detect language",
    description="""
Detects the language of the provided text.

Optimised for Ethiopian scripts and languages:
- **Amharic** (Ethiopic script / Fidel)
- **Tigrinya** (Ethiopic script / Fidel)
- **Afaan Oromoo** (Latin script)
- **English**, Arabic, French, and other major languages
    """,
)
async def detect_language(body: AnalyzeTextRequest, api_key: str = Security(get_api_key)):

    system = "You are a language detection specialist. Respond with valid JSON only."
    user = f"""Detect the language of the following text.

Return JSON:
{{
  "language_code": "ISO 639-3 code (e.g. amh, tir, orm, eng)",
  "language_name": "Full language name",
  "confidence": <float 0.0-1.0>,
  "alternatives": [
    {{"code": "...", "name": "...", "confidence": 0.x}},
    ...
  ]
}}

Text (first 500 chars used for detection):
{body.text[:500]}"""

    try:
        result = await call_llm_json(system, user)
        ANALYZE_REQ.labels(operation="language", status="success").inc()
    except HTTPException:
        ANALYZE_REQ.labels(operation="language", status="error").inc()
        raise

    return LanguageResponse(
        language_code=result.get("language_code", "und"),
        language_name=result.get("language_name", "Unknown"),
        confidence=min(1.0, max(0.0, float(result.get("confidence", 0.9)))),
        alternatives=result.get("alternatives", []),
    )


@app.post(
    "/analyze/ocr-correct",
    response_model=OcrCorrectResponse,
    tags=["analyze"],
    summary="Post-process and correct OCR text",
    description="""
Corrects common errors in OCR-extracted Ethiopian text.

Fixes:
- Ethiopic character substitution errors (common in Tesseract output)
- Broken word boundaries in Amharic/Tigrinya Fidel script
- Punctuation and spacing errors
- Latin character intrusions in Ethiopic text

Particularly effective for improving low-confidence Tesseract output
before human review.
    """,
)
async def ocr_correct(body: OcrCorrectRequest, api_key: str = Security(get_api_key)):

    script_hints = {
        "ethiopic": "The text uses Ethiopic (Fidel) script — Amharic or Tigrinya. Fix character substitution errors common in Tesseract OCR of Ethiopic script.",
        "latin":    "The text uses Latin script — possibly Afaan Oromoo or English. Fix common OCR errors.",
        "mixed":    "The text mixes Ethiopic and Latin scripts. Fix OCR errors in both.",
    }

    system = (
        "You are an OCR post-processing specialist for Ethiopian language documents. "
        "Respond with valid JSON only. Do not change the meaning — only fix technical OCR errors."
    )
    user = f"""{script_hints.get(body.script, '')}

Return JSON:
{{
  "corrected_text": "...",
  "corrections_made": <integer count of corrections>,
  "confidence": <float 0.0-1.0 of correction quality>
}}

Original OCR text:
{body.text[:8000]}"""

    log.info("ocr-correct | script=%s words=%d", body.script, len(body.text.split()))
    try:
        result = await call_llm_json(system, user)
        ANALYZE_REQ.labels(operation="ocr_correct", status="success").inc()
    except HTTPException:
        ANALYZE_REQ.labels(operation="ocr_correct", status="error").inc()
        raise

    return OcrCorrectResponse(
        corrected_text=result.get("corrected_text") or body.text,
        corrections_made=int(result.get("corrections_made", 0)),
        confidence=min(1.0, max(0.0, float(result.get("confidence", 0.85)))),
    )
