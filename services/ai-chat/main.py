"""
EthioScript AI Chat Service
Port: 6006

Standalone FastAPI service that powers the AI assistant panel.
Supports OpenAI, Groq, Ollama, and a mock provider for testing.

Extra features beyond the guide:
- Redis per-user rate limiting
- Prometheus metrics (/metrics)
- Structured JSON logging
- Conversation history stored in Redis (GET /history/{user_id})
- Health check reports LLM reachability
"""

import json
import logging
import os
import time
from contextlib import asynccontextmanager
from typing import Literal

import httpx
import redis.asyncio as aioredis
from dotenv import load_dotenv
from fastapi import FastAPI, HTTPException, Request, status
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse
from prometheus_client import Counter, Histogram, generate_latest, CONTENT_TYPE_LATEST
from pydantic import BaseModel, Field
from starlette.responses import Response

load_dotenv()

# ── Config ────────────────────────────────────────────────────────────────────

SERVICE_SECRET    = os.environ["AI_SERVICE_SECRET"]
LLM_PROVIDER      = os.getenv("LLM_PROVIDER", "mock")
LLM_MODEL         = os.getenv("LLM_MODEL", "gpt-4o-mini")
LLM_API_KEY       = os.getenv("LLM_API_KEY", "")
LLM_BASE_URL      = os.getenv("LLM_BASE_URL", "")
DOCUMENT_SVC_URL  = os.getenv("DOCUMENT_SERVICE_URL", "http://document-service:6001")
INTERNAL_API_KEY  = os.getenv("INTERNAL_API_KEY", "")
REDIS_URL         = os.getenv("REDIS_URL", "redis://:redispass@redis:6379/2")
MAX_MESSAGES      = int(os.getenv("MAX_CONTEXT_MESSAGES", "20"))
RATE_LIMIT_RPM    = int(os.getenv("CHAT_RATE_LIMIT_RPM", "30"))   # requests per minute per user
HISTORY_TTL_SEC   = int(os.getenv("CHAT_HISTORY_TTL_SEC", "86400"))  # 24 h
ALLOWED_ORIGINS   = os.getenv("ALLOWED_ORIGINS", "http://localhost:3000").split(",")

# ── Logging ───────────────────────────────────────────────────────────────────

logging.basicConfig(
    level=logging.INFO,
    format='{"time":"%(asctime)s","level":"%(levelname)s","svc":"ai-chat","msg":"%(message)s"}',
)
log = logging.getLogger("ai-chat")

# ── Prometheus metrics ────────────────────────────────────────────────────────

CHAT_REQUESTS  = Counter("ai_chat_requests_total",  "Total chat requests", ["provider", "status"])
CHAT_DURATION  = Histogram("ai_chat_duration_seconds", "Chat LLM call duration",
                            buckets=[0.5, 1, 2, 5, 10, 20, 30])
RATE_LIMITED   = Counter("ai_chat_rate_limited_total", "Rate-limited requests")

# ── System prompt ─────────────────────────────────────────────────────────────

SYSTEM_PROMPT = """You are EthioScript AI, a helpful assistant embedded inside the EthioScript platform.
EthioScript is an AI-powered platform for processing Ethiopian language documents — supporting OCR,
speech-to-text, and AI analysis for Amharic, Tigrinya, Afaan Oromoo, and English.

You help users:
- Navigate EthioScript features (OCR, STT, Document Management, Exports)
- Understand document statuses: Uploaded → Queued → Processing → Review Required → Approved / Failed
- Upload documents (POST /assets then POST /documents)
- Download and export results (POST /documents/{id}/exports)
- Retry failed documents (POST /documents/{id}/retry)
- Manage API keys and account settings

Always be concise, friendly, and specific to EthioScript. If asked about something unrelated to
EthioScript or Ethiopian languages, politely redirect to what you can help with.
Respond in the same language the user writes in (Amharic, Tigrinya, Oromo, or English).
Use markdown: **bold** for feature names, bullet points for lists, numbered steps for instructions."""

# ── Pydantic models ───────────────────────────────────────────────────────────

class ChatMessage(BaseModel):
    role: Literal["user", "assistant", "system"]
    content: str = Field(..., min_length=1, max_length=32_000)


class ChatRequest(BaseModel):
    messages: list[ChatMessage] = Field(..., min_length=1, max_length=100)
    user_id: str | None = None
    tenant_id: str | None = None
    save_history: bool = True


class ChatResponse(BaseModel):
    reply: str
    provider: str
    model: str


class HealthResponse(BaseModel):
    status: str
    provider: str
    model: str
    redis: str


class HistoryResponse(BaseModel):
    user_id: str
    messages: list[ChatMessage]


# ── Redis client ──────────────────────────────────────────────────────────────

_redis: aioredis.Redis | None = None


async def get_redis() -> aioredis.Redis | None:
    global _redis
    if _redis is None:
        try:
            _redis = await aioredis.from_url(REDIS_URL, decode_responses=True)
            await _redis.ping()
        except Exception as exc:
            log.warning("Redis unavailable — rate limiting and history disabled: %s", exc)
            _redis = None
    return _redis


# ── Rate limiting ─────────────────────────────────────────────────────────────

async def check_rate_limit(user_id: str) -> bool:
    """Returns True if request is allowed, False if rate limited."""
    rdb = await get_redis()
    if rdb is None:
        return True  # degrade gracefully if Redis is down
    key = f"ai_chat:rate:{user_id}"
    try:
        count = await rdb.incr(key)
        if count == 1:
            await rdb.expire(key, 60)
        return count <= RATE_LIMIT_RPM
    except Exception:
        return True


# ── Conversation history ──────────────────────────────────────────────────────

async def save_history(user_id: str, messages: list[ChatMessage]) -> None:
    rdb = await get_redis()
    if rdb is None:
        return
    key = f"ai_chat:history:{user_id}"
    try:
        data = json.dumps([m.model_dump() for m in messages])
        await rdb.set(key, data, ex=HISTORY_TTL_SEC)
    except Exception as exc:
        log.warning("Could not save history for %s: %s", user_id, exc)


async def load_history(user_id: str) -> list[ChatMessage]:
    rdb = await get_redis()
    if rdb is None:
        return []
    key = f"ai_chat:history:{user_id}"
    try:
        data = await rdb.get(key)
        if data:
            return [ChatMessage(**m) for m in json.loads(data)]
    except Exception as exc:
        log.warning("Could not load history for %s: %s", user_id, exc)
    return []


# ── Document context ──────────────────────────────────────────────────────────

async def fetch_user_context(tenant_id: str, user_id: str) -> str:
    """Fetch user's recent documents from document-service for context injection."""
    try:
        async with httpx.AsyncClient(timeout=5) as client:
            res = await client.get(
                f"{DOCUMENT_SVC_URL}/documents",
                headers={
                    "X-Tenant-ID":   tenant_id,
                    "X-User-ID":     user_id,
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
                    f"- **{d.get('filename', 'unknown')}** "
                    f"(type: {d.get('task_type','?')}, "
                    f"language: {d.get('language','?')}, "
                    f"status: **{d.get('status','?')}**)"
                )
            return "\n".join(lines)
    except Exception as exc:
        log.warning("Could not fetch user context: %s", exc)
        return ""


# ── LLM caller ────────────────────────────────────────────────────────────────

async def call_llm(messages: list[dict]) -> str:
    """Route to the correct LLM provider and return the reply string."""

    if LLM_PROVIDER == "mock":
        last = next((m["content"] for m in reversed(messages) if m["role"] == "user"), "")
        return (
            f"👋 **EthioScript AI (mock mode)**\n\n"
            f"You said: _{last[:120]}_\n\n"
            f"Set `LLM_PROVIDER=openai|groq|ollama` and add your API key to get real responses."
        )

    if LLM_PROVIDER in ("openai", "groq"):
        if not LLM_API_KEY:
            raise HTTPException(status_code=503, detail="LLM_API_KEY not configured")
        base_url = LLM_BASE_URL or (
            "https://api.openai.com/v1" if LLM_PROVIDER == "openai"
            else "https://api.groq.com/openai/v1"
        )
        async with httpx.AsyncClient(timeout=30) as client:
            res = await client.post(
                f"{base_url}/chat/completions",
                headers={
                    "Authorization": f"Bearer {LLM_API_KEY}",
                    "Content-Type":  "application/json",
                },
                json={
                    "model":       LLM_MODEL,
                    "messages":    messages,
                    "max_tokens":  1024,
                    "temperature": 0.7,
                },
            )
        if res.status_code != 200:
            log.error("LLM error %s: %s", res.status_code, res.text[:300])
            raise HTTPException(status_code=502, detail="LLM provider error")
        return res.json()["choices"][0]["message"]["content"]

    if LLM_PROVIDER == "ollama":
        base_url = LLM_BASE_URL or "http://localhost:11434"
        async with httpx.AsyncClient(timeout=60) as client:
            res = await client.post(
                f"{base_url}/api/chat",
                json={"model": LLM_MODEL, "messages": messages, "stream": False},
            )
        if res.status_code != 200:
            log.error("Ollama error %s: %s", res.status_code, res.text[:300])
            raise HTTPException(status_code=502, detail="Ollama error")
        return res.json()["message"]["content"]

    raise HTTPException(status_code=500, detail=f"Unknown LLM_PROVIDER: {LLM_PROVIDER}")


# ── App ───────────────────────────────────────────────────────────────────────

@asynccontextmanager
async def lifespan(app: FastAPI):
    log.info("AI Chat Service starting | provider=%s model=%s rate_limit=%d/min",
             LLM_PROVIDER, LLM_MODEL, RATE_LIMIT_RPM)
    await get_redis()   # pre-connect
    yield
    log.info("AI Chat Service stopped")


app = FastAPI(
    title="EthioScript AI Chat Service",
    version="1.0.0",
    description="AI assistant for the EthioScript document intelligence platform.",
    lifespan=lifespan,
)

app.add_middleware(
    CORSMiddleware,
    allow_origins=ALLOWED_ORIGINS,
    allow_methods=["POST", "GET", "DELETE"],
    allow_headers=["*"],
)


# ── Auth helper ───────────────────────────────────────────────────────────────

def verify_secret(request: Request) -> None:
    if request.headers.get("X-Service-Secret", "") != SERVICE_SECRET:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED,
                            detail="Invalid service secret")


# ── Routes ────────────────────────────────────────────────────────────────────

@app.get("/health", response_model=HealthResponse, tags=["system"])
async def health():
    rdb = await get_redis()
    redis_status = "ok" if rdb is not None else "unavailable"
    return HealthResponse(
        status="ok",
        provider=LLM_PROVIDER,
        model=LLM_MODEL,
        redis=redis_status,
    )


@app.get("/metrics", tags=["system"])
async def metrics():
    return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)


@app.post("/chat", response_model=ChatResponse, tags=["chat"])
async def chat(request: Request, body: ChatRequest):
    verify_secret(request)

    # Rate limit per user_id (fallback to IP if no user_id)
    rate_key = body.user_id or request.client.host
    if not await check_rate_limit(rate_key):
        RATE_LIMITED.inc()
        raise HTTPException(
            status_code=status.HTTP_429_TOO_MANY_REQUESTS,
            detail=f"Rate limit exceeded — max {RATE_LIMIT_RPM} requests per minute",
        )

    # Build system prompt with optional document context
    system_content = SYSTEM_PROMPT
    if body.tenant_id and body.user_id:
        ctx = await fetch_user_context(body.tenant_id, body.user_id)
        if ctx:
            system_content += f"\n\n{ctx}"

    messages = [{"role": "system", "content": system_content}]
    history = body.messages[-MAX_MESSAGES:]
    messages += [{"role": m.role, "content": m.content} for m in history]

    log.info("chat request | user=%s tenant=%s messages=%d provider=%s",
             body.user_id, body.tenant_id, len(history), LLM_PROVIDER)

    start = time.monotonic()
    try:
        reply = await call_llm(messages)
        CHAT_REQUESTS.labels(provider=LLM_PROVIDER, status="success").inc()
    except HTTPException:
        CHAT_REQUESTS.labels(provider=LLM_PROVIDER, status="error").inc()
        raise
    finally:
        CHAT_DURATION.observe(time.monotonic() - start)

    # Save updated history to Redis
    if body.save_history and body.user_id:
        updated = list(history) + [ChatMessage(role="assistant", content=reply)]
        await save_history(body.user_id, updated)

    log.info("chat reply | user=%s length=%d duration=%.2fs",
             body.user_id, len(reply), time.monotonic() - start)

    return ChatResponse(reply=reply, provider=LLM_PROVIDER, model=LLM_MODEL)


@app.get("/history/{user_id}", response_model=HistoryResponse, tags=["chat"])
async def get_history(user_id: str, request: Request):
    verify_secret(request)
    messages = await load_history(user_id)
    return HistoryResponse(user_id=user_id, messages=messages)


@app.delete("/history/{user_id}", status_code=204, tags=["chat"])
async def clear_history(user_id: str, request: Request):
    verify_secret(request)
    rdb = await get_redis()
    if rdb:
        await rdb.delete(f"ai_chat:history:{user_id}")
