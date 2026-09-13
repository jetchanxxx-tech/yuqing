"""Media Engine — Multimodal content understanding (video/image/comment analysis).

Parses short-video content (Douyin/Kuaishou) and extracts structured info.
Mock implementation for MVP — returns pre-built analysis results.
When LLM/vision API keys are provided, replace with real multimodal model calls.
"""
from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI(title="Media Engine", version="0.2.0")


# ── Models ──────────────────────────────────────────────

class MediaAnalyzeRequest(BaseModel):
    document_ids: list[str] = []
    analysis_id: str = ""


class MediaResult(BaseModel):
    document_id: str = ""
    transcript: str = ""
    ocr_text: str = ""
    objects: list[str] = []
    faces: int = 0
    sentiment_hint: str = ""  # positive/negative/neutral — visual sentiment


class MediaAnalyzeResponse(BaseModel):
    results: list[MediaResult]


# ── Mock Data ───────────────────────────────────────────

MOCK_MEDIA_RESULTS: list[MediaResult] = [
    MediaResult(
        document_id="doc_001",
        transcript="测试雅阁后排空间，我178cm坐进去膝盖就顶到前排了...（笑声）但说实话吧，日常代步够用，就是没网上说的那么离谱。",
        ocr_text="",
        objects=["汽车内饰", "后排座椅", "测量工具"],
        faces=1,
        sentiment_hint="neutral",
    ),
    MediaResult(
        document_id="doc_002",
        transcript="弹幕解读：'本田大法好'刷屏 → '笑死我了这空间' → 'A级车既视感' → '但是雅阁其他方面很香'",
        ocr_text="标题：雅阁后排实测 #本田 #雅阁 #后排空间",
        objects=["弹幕截图", "评论互动区"],
        faces=0,
        sentiment_hint="negative",
    ),
    MediaResult(
        document_id="doc_003",
        transcript="竞品对比实测：雅阁 vs 凯美瑞 vs 迈腾后排空间对比。数据说话：雅阁2708mm轴距，凯美瑞2825mm，迈腾2871mm。雅阁确实短，但没有传说中那么夸张。",
        ocr_text="实测数据表：轴距对比 2708/2825/2871mm",
        objects=["对比表格", "汽车后排", "测量尺"],
        faces=1,
        sentiment_hint="neutral",
    ),
    MediaResult(
        document_id="doc_004",
        transcript="（BGM轻松幽默）同事们都说我买了B级车配A级空间，我说'后排坐的是包不是我！' 雅阁车主自黑大会现在开始。",
        ocr_text="评论区：'笑死，心态真好' '自黑式维权第一人' '我凯美瑞车主表示很舒适'",
        objects=["搞笑特效", "手势动作"],
        faces=1,
        sentiment_hint="positive",
    ),
    MediaResult(
        document_id="doc_005",
        transcript="官方人员回应现场：'我们注意到网友讨论，雅阁后排空间数据以实测为准。我们会认真听取用户反馈，下一代产品将重点优化空间体验。'",
        ocr_text="官方声明全文",
        objects=["发布会现场", "官方发言人"],
        faces=2,
        sentiment_hint="neutral",
    ),
]


# ── Routes ──────────────────────────────────────────────

@app.get("/health")
async def health():
    return {"status": "ok", "engine": "media", "version": "0.2.0"}


@app.post("/analyze")
async def analyze(req: MediaAnalyzeRequest) -> MediaAnalyzeResponse:
    """Analyze media content (video transcripts, image OCR, object detection).

    MVP returns pre-built mock results based on the "雅阁后排" case study.
    Real implementation: Gemini Vision / Qwen-VL for multimodal understanding.
    """
    if req.document_ids:
        # Return matching mock results by document_id
        results = [r for r in MOCK_MEDIA_RESULTS if r.document_id in req.document_ids]
    else:
        results = MOCK_MEDIA_RESULTS[:3]

    return MediaAnalyzeResponse(results=[r.model_dump() for r in results])