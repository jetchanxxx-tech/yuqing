"""Mock query engine — 返回预置中文文档，验证 Go→Python→LLM 全链路（不含 Scrapling）。"""
from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI(title="Mock Query Engine")


class SearchRequest(BaseModel):
    keywords: list[str] = [""]
    sources: list[str] = ["news"]
    max_results: int = 20
    analysis_id: str = ""
    bocha_api_key: str = ""


DOCS = [
    {
        "id": "doc-1", "title": "雅阁后排空间实测：178cm 顶膝引热议",
        "url": "https://example.com/news/1", "content": "有车评人实测雅阁后排，身高178cm的体验者坐下后膝盖顶到前排座椅，长途舒适度受质疑。评论区两极分化，部分车主认为够用。",
        "source_type": "news", "source_name": "汽车之家", "published_at": "2026-09-01T10:00:00Z", "content_hash": "a1b2c3d4e5f60718",
    },
    {
        "id": "doc-2", "title": "雅阁混动油耗惊艳：百公里 4.2L 车主好评",
        "url": "https://example.com/news/2", "content": "多位车主晒出雅阁混动油耗数据，市区工况低至百公里4.2L，驾驶质感与静谧性获好评。",
        "source_type": "news", "source_name": "懂车帝", "published_at": "2026-09-05T10:00:00Z", "content_hash": "b2c3d4e5f6071829",
    },
    {
        "id": "doc-3", "title": "本田回应后排空间争议：定位家用舒适取向",
        "url": "https://example.com/news/3", "content": "本田官方回应称雅阁后排以家用舒适为设计取向，建议到店实际体验。舆论认为回应及时但未正面解答空间数据。",
        "source_type": "weibo", "source_name": "新浪汽车", "published_at": "2026-09-10T10:00:00Z", "content_hash": "c3d4e5f60718293a",
    },
]


@app.get("/health")
async def health():
    return {"status": "ok", "engine": "query-mock"}


@app.post("/search")
async def search(req: SearchRequest):
    return {"documents": DOCS, "total_count": len(DOCS), "sources": []}
