"""Query Engine — Multi-source web search + dedup."""
from fastapi import FastAPI
from engines.common.auth import InternalAuthMiddleware

app = FastAPI(title="Query Engine", version="0.1.0")
# app.add_middleware(InternalAuthMiddleware, token=os.getenv("INTERNAL_TOKEN", ""))


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "query"}


@app.post("/search")
async def search(req: dict):
    # TODO: Multi-source search + dedup + relevance ranking.
    return {"documents": [], "total_count": 0, "sources": []}
