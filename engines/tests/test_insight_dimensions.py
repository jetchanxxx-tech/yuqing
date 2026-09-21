"""Insight Engine 维度分析测试（TDD RED）—— BettaFish 内核移植。

现状：/analyze 只有「情感+话题」一次调用 + 「摘要」一次调用，单轮单视角，
产出干瘪且跨分析趋同。

目标（吸收 BettaFish 的分析机制，不复制其 GPL 代码）：
  1. 五个固定分析维度各自独立调用一次 LLM：
     背景概述 / 热度传播 / 情感观点 / 群体平台差异 / 深层原因
  2. 每个维度输入「文档素材包」（含来源平台、发布时间、正文）——
     不再让 LLM 在看不到原文的情况下编造结论
  3. 每个维度按固定骨架输出：核心发现 / 数据 / 代表性声音 / 深层解读 / 趋势
  4. 每维度独立人设（不是同一个"资深分析师"），并注入 analysis_type
  5. 话题 trend 由代码按发布时间确定性计算，不再让 LLM 猜（无时间信息必然瞎猜）
  6. 单个维度失败不拖垮整次分析：返回其余维度 + warning

以下测试当前应全部失败。
"""
import json

import pytest
from fastapi.testclient import TestClient

import engines.insight_engine.main as insight_engine
from engines.insight_engine.main import app

client = TestClient(app)

# 5 个维度的 id（顺序即分析顺序）
DIMENSION_IDS = ["background", "heat", "sentiment", "group", "deep_cause"]

DOCS = [
    {
        "id": "d1",
        "title": "雅阁后排空间实测翻车",
        "content": "后排腿部空间局促，身高178cm顶膝，长途舒适度差。",
        "source_type": "news",
        "source_name": "汽车之家",
        "published_at": "2026-09-01T10:00:00Z",
    },
    {
        "id": "d2",
        "title": "车主吐槽后排：腿都伸不直",
        "content": "实测视频显示后排腿部空间不足，评论区大量共鸣。",
        "source_type": "weibo",
        "source_name": "微博",
        "published_at": "2026-09-02T10:00:00Z",
    },
    {
        "id": "d3",
        "title": "本田回应后排空间争议",
        "content": "官方回应称雅阁后排以家用舒适为取向，建议到店体验。",
        "source_type": "news",
        "source_name": "新浪汽车",
        "published_at": "2026-09-10T10:00:00Z",
    },
    {
        "id": "d4",
        "title": "雅阁混动油耗实测 4.2L",
        "content": "多位车主晒出油耗数据，驾驶质感获好评。",
        "source_type": "news",
        "source_name": "懂车帝",
        "published_at": "2026-09-12T10:00:00Z",
    },
]

# LLM 会「谎报」trend（无时间信息只能瞎猜）；代码必须按日期覆盖它
LLM_TOPICS = [
    {"id": "t1", "name": "后排空间争议", "keywords": ["后排", "空间"],
     "doc_count": 2, "doc_ids": ["d1", "d2"], "trend": "stable"},
    {"id": "t2", "name": "官方回应与油耗", "keywords": ["回应", "油耗"],
     "doc_count": 2, "doc_ids": ["d3", "d4"], "trend": "stable"},
]

DIMENSION_PAYLOAD = {
    "findings": "核心发现：后排空间是本次争议的绝对焦点。",
    "data_points": ["4 篇文档中 2 篇直指后排", "负面占比 50%"],
    "quotes": [
        {"text": "腿都伸不直", "source": "微博"},
        {"text": "建议到店体验", "source": "新浪汽车"},
    ],
    "deep_read": "深层解读：产品定位与用户预期出现错位。",
    "trend": "趋势：争议由实测视频驱动，仍在扩散。",
}


class DimensionFakeLLM:
    """按调用类型返回预置结果；记录每次调用的完整 prompt 供断言。

    fail_dims：这些维度的调用抛错，用于验证「单维度失败不致命」。
    """

    def __init__(self, fail_dims: set[str] | None = None, topic_trend: str = "stable"):
        self.calls: list[str] = []
        self.fail_dims = fail_dims or set()
        # LLM 谎报的 trend —— 代码必须按日期覆盖它
        self.topic_trend = topic_trend

    async def chat_json(self, model: str, messages: list[dict], **kwargs) -> dict:
        prompt = json.dumps(messages, ensure_ascii=False)
        self.calls.append(prompt)

        if "【分析维度】" in prompt:
            for dim in self.fail_dims:
                if f"id={dim}" in prompt:
                    raise RuntimeError(f"dimension {dim} unavailable")
            return dict(DIMENSION_PAYLOAD)

        if "批判" in prompt:
            return {"critique": "初稿较官方", "revised_summary": "重写后的最终摘要"}

        return {
            "sentiments": [
                {"document_id": "d1", "sentiment": "negative", "level": "非常负面",
                 "score": 0.85, "confidence": 0.92},
                {"document_id": "d2", "sentiment": "negative", "level": "负面",
                 "score": 0.7, "confidence": 0.85},
                {"document_id": "d3", "sentiment": "neutral", "level": "中性",
                 "score": 0.5, "confidence": 0.8},
                {"document_id": "d4", "sentiment": "positive", "level": "正面",
                 "score": 0.9, "confidence": 0.9},
            ],
            "topics": [{**t, "trend": self.topic_trend} for t in LLM_TOPICS],
        }


@pytest.fixture(autouse=True)
def _no_retry_backoff(monkeypatch):
    """重试退避清零：并发闸门的单次重试语义由调用次数断言，不靠真实等待。"""
    monkeypatch.setattr(insight_engine, "_RETRY_DELAY_SECONDS", 0)


@pytest.fixture
def dim_llm(monkeypatch):
    fake = DimensionFakeLLM()
    monkeypatch.setattr(insight_engine, "build_client", lambda api_key="", base_url="", timeout=120.0: fake)
    return fake


def dimension_calls(llm: DimensionFakeLLM) -> list[str]:
    """筛出维度分析的调用 prompt。"""
    return [c for c in llm.calls if "【分析维度】" in c]


# ── 维度产出 ────────────────────────────────────────────────────

def test_analyze_returns_five_dimensions_with_skeleton(dim_llm):
    """五个维度齐备，且每个都带骨架字段（核心发现/数据/原声/解读/趋势）。"""
    resp = client.post(
        "/analyze",
        json={"documents": DOCS, "analysis_id": "a1", "analysis_type": "brand", "api_key": "sk-x"},
    )

    assert resp.status_code == 200
    dims = resp.json()["dimensions"]
    assert [d["id"] for d in dims] == DIMENSION_IDS

    for d in dims:
        assert d["name"], f"维度 {d['id']} 缺中文名"
        assert d["findings"], f"维度 {d['id']} 缺核心发现"
        assert isinstance(d["data_points"], list) and d["data_points"], f"维度 {d['id']} 缺数据点"
        # 代表性声音：逐字原声 + 来源，不允许空
        assert d["quotes"] and d["quotes"][0]["text"] and d["quotes"][0]["source"]
        assert d["deep_read"], f"维度 {d['id']} 缺深层解读"
        assert d["trend"], f"维度 {d['id']} 缺趋势"


def test_each_dimension_called_with_its_own_persona(dim_llm):
    """五个维度各自独立调用，且人设互不相同（不是同一个"资深分析师"复用）。"""
    client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x"})

    calls = dimension_calls(dim_llm)
    assert len(calls) == 5, f"维度调用应为 5 次，实际 {len(calls)}"

    # 每个维度的调用必须能唯一对应到自己的 id
    for dim in DIMENSION_IDS:
        assert any(f"id={dim}" in c for c in calls), f"缺少维度 {dim} 的调用"

    # 人设去重：5 个 prompt 两两不同
    assert len(set(calls)) == 5, "维度 prompt 出现重复（人设/任务未分化）"


def test_dimension_prompt_carries_documents_and_source_metadata(dim_llm):
    """维度 prompt 必须含原文素材与来源元信息 —— 否则 LLM 只能空转编造。"""
    client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x"})

    prompt = dimension_calls(dim_llm)[0]
    assert "雅阁后排空间实测翻车" in prompt, "缺文档标题"
    assert "后排腿部空间局促" in prompt, "缺正文原文"
    assert "汽车之家" in prompt, "缺来源平台（无法做平台差异分析）"
    assert "2026-09-01" in prompt, "缺发布时间（无法做时间演化分析）"


def test_dimension_prompt_enforces_density_and_skeleton(dim_llm):
    """维度 prompt 写入内容密度硬指标与五小节骨架（BettaFish 的关键手法）。"""
    client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x"})

    prompt = dimension_calls(dim_llm)[0]
    # 骨架
    for section in ["核心发现", "数据", "代表性声音", "深入解读", "趋势"]:
        assert section in prompt, f"骨架缺少「{section}」"
    # 密度硬指标：字数与引用条数必须可数
    assert "字" in prompt and any(ch.isdigit() for ch in prompt), "缺字数下限"
    assert "至少" in prompt and "条" in prompt, "缺引用条数下限"


def test_analysis_type_reaches_dimension_prompts(dim_llm):
    """分析类型必须进入维度 prompt —— 否则品牌/竞品/突发事件产出完全同质。"""
    client.post(
        "/analyze",
        json={"documents": DOCS, "analysis_type": "竞品对比", "api_key": "sk-x"},
    )

    calls = dimension_calls(dim_llm)
    assert len(calls) == 5, f"维度调用应为 5 次，实际 {len(calls)}"
    for c in calls:
        assert "竞品对比" in c, "分析类型未进入维度 prompt"


# ── 确定性 trend ────────────────────────────────────────────────

def test_topic_trend_computed_from_dates_not_llm_guess(dim_llm):
    """话题 trend 由发布时间确定性计算，覆盖 LLM 的猜测。

    语料时间线 09-01 / 09-02 / 09-10 / 09-12：
      t1 的文档（09-01、09-02）落在前半段 → falling
      t2 的文档（09-10、09-12）落在后半段 → rising
    LLM 对两者都回答 "stable"，必须被代码纠正。
    """
    resp = client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x"})

    topics = {t["id"]: t for t in resp.json()["topics"]}
    assert topics["t1"]["trend"] == "falling", f"t1 = {topics['t1']['trend']}（LLM 谎报 stable）"
    assert topics["t2"]["trend"] == "rising", f"t2 = {topics['t2']['trend']}（LLM 谎报 stable）"


def test_topic_without_dates_falls_back_to_stable(monkeypatch):
    """无发布时间的语料无法判趋势 → stable，而不是沿用 LLM 的猜测。

    LLM 此处谎报 rising；只有代码侧的「无日期→stable」才能产生 stable。
    """
    fake = DimensionFakeLLM(topic_trend="rising")
    monkeypatch.setattr(insight_engine, "build_client", lambda api_key="", base_url="", timeout=120.0: fake)

    no_dates = [{k: v for k, v in d.items() if k != "published_at"} for d in DOCS]
    resp = client.post("/analyze", json={"documents": no_dates, "api_key": "sk-x"})

    assert all(t["trend"] == "stable" for t in resp.json()["topics"])


# ── 降级 ────────────────────────────────────────────────────────

def test_single_dimension_failure_degrades_not_fatal(monkeypatch):
    """单个维度调用失败：其余维度照常返回 + warning，不整单 502。"""
    fake = DimensionFakeLLM(fail_dims={"heat"})
    monkeypatch.setattr(insight_engine, "build_client", lambda api_key="", base_url="", timeout=120.0: fake)

    resp = client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x"})

    assert resp.status_code == 200
    body = resp.json()
    ids = [d["id"] for d in body["dimensions"]]
    assert "heat" not in ids, "失败维度不应出现在结果里"
    assert len(ids) == 4, f"其余 4 个维度应保留，实际 {ids}"
    assert body["warning"], "降级必须留下 warning 说明原因"


def test_all_dimensions_succeed_leaves_no_warning(dim_llm):
    """全部成功时 warning 为空（避免前端误报降级）。"""
    resp = client.post("/analyze", json={"documents": DOCS, "api_key": "sk-x"})

    assert resp.json()["warning"] == ""

# ── P0：材料 token 预算 ───────────────────────────────────────
#
# 五个维度各自注入全量素材包 → 输入 token ×5。预算按字符上限截断：
# 优先保留发布时间最新的文档（舆情分析里最新证据最相关），
# 截断必须在 prompt 中显式标记，而不是静默丢数据。

def _bulk_docs(n: int) -> list[dict]:
    return [
        {
            "id": f"d{i}", "title": f"文档{i}",
            "content": "内容" * 600,
            "source_type": "news", "source_name": "测试源",
            "published_at": f"2026-09-{i % 28 + 1:02d}T10:00:00Z",
        }
        for i in range(1, n + 1)
    ]


def test_materials_respect_token_budget(dim_llm):
    bulk = _bulk_docs(60)
    client.post("/analyze", json={"documents": bulk, "api_key": "sk-x"})
    prompt = dimension_calls(dim_llm)[0]
    assert "材料截断" in prompt, "截断必须显式标记，不得静默丢数据"


def test_materials_keep_most_recent_when_truncated(dim_llm):
    bulk = _bulk_docs(60)
    client.post("/analyze", json={"documents": bulk, "api_key": "sk-x"})
    prompt = dimension_calls(dim_llm)[0]
    assert "文档27" in prompt or "文档55" in prompt, "最新文档被误截断"


# ── P2：引用保真校验（反幻觉）─────────────────────────────────
#
# 维度结论里的「逐字原声」必须真实存在于源文档正文（归一化后子串）。
# LLM 编造或改写的引语一律丢弃，并在 warning 里记录 —— 分析可以降级，
# 但不允许出现「看起来像引用的编造」。
# 归一化：去除全部空白后比较（网页正文常有换行/空格差异）。

FABRICATED_DIMS = [
    {
        'id': 'background', 'name': '背景与事件概述',
        'findings': '核心发现：测试。',
        'quotes': [
            {'text': '腿都伸不直', 'source': '微博'},       # 真实存在于 doc2
            {'text': '这车彻底不行千万别买', 'source': '微博'},  # 编造 —— 必须被丢弃
        ],
    },
]


def test_fabricated_quotes_dropped_with_warning(monkeypatch):
    class QuoteLLM(DimensionFakeLLM):
        async def chat_json(self, model, messages, **kwargs):
            prompt = json.dumps(messages, ensure_ascii=False)
            self.calls.append(prompt)
            if '【分析维度】' in prompt:
                return json.loads(json.dumps(FABRICATED_DIMS[0])) | {'id': 'background'}
            if '批判' in prompt:
                return {'critique': 'x', 'revised_summary': 'y'}
            return await DimensionFakeLLM.chat_json(self, model, messages, **kwargs)

    fake = QuoteLLM()
    monkeypatch.setattr(insight_engine, 'build_client', lambda api_key='', base_url='', timeout=120.0: fake)

    resp = client.post('/analyze', json={'documents': DOCS, 'api_key': 'sk-x'})

    assert resp.status_code == 200
    body = resp.json()
    dims = {d['id']: d for d in body['dimensions']}
    quotes = dims['background']['quotes']
    texts = [q['text'] for q in quotes]
    assert '腿都伸不直' in texts, '真实引语不得误杀'
    assert '这车彻底不行千万别买' not in texts, '编造引语必须被丢弃'
    assert body['warning'], '丢弃编造引语必须留 warning'


def test_quotes_normalized_against_whitespace(monkeypatch):
    class WhitespaceLLM(DimensionFakeLLM):
        async def chat_json(self, model, messages, **kwargs):
            prompt = json.dumps(messages, ensure_ascii=False)
            self.calls.append(prompt)
            if '【分析维度】' in prompt:
                # 原文是「后排腿部空间局促，身高178cm顶膝」，引语被插入了
                # 多余空白（网页转贴常见）—— 归一化去空白后必须命中原文
                return {'findings': 'f', 'quotes': [{'text': '后排腿部空间局促，身高 178cm\n顶膝', 'source': '新闻'}], 'deep_read': 'd', 'trend': 't'}
            if '批判' in prompt:
                return {'critique': 'x', 'revised_summary': 'y'}
            return await DimensionFakeLLM.chat_json(self, model, messages, **kwargs)

    fake = WhitespaceLLM()
    monkeypatch.setattr(insight_engine, 'build_client', lambda api_key='', base_url='', timeout=120.0: fake)

    resp = client.post('/analyze', json={'documents': DOCS, 'api_key': 'sk-x'})

    body = resp.json()
    dims = {d['id']: d for d in body['dimensions']}
    assert len(dims['background']['quotes']) == 1, '真实内容的引语（空白差异）不得误杀'
    assert body['warning'] == ''


def test_wrapped_quotes_not_mass_dropped(monkeypatch):
    """LLM 给引语套上引号（「」/“”）：包裹符不属于原文，剥掉后再比对。

    不剥的话，LLM 一旦习惯性加引号，全部真引语都会被误杀 —— 维度失去
    「代表性声音」，warning 刷屏，反幻觉机制反而伤害产出。
    """
    class WrappedLLM(DimensionFakeLLM):
        async def chat_json(self, model, messages, **kwargs):
            prompt = json.dumps(messages, ensure_ascii=False)
            self.calls.append(prompt)
            if '【分析维度】' in prompt:
                return {
                    'findings': 'f',
                    'quotes': [
                        {'text': '「腿都伸不直」', 'source': '微博'},       # 包引号但内容真实
                        {'text': '“建议到店体验”', 'source': '新浪汽车'},   # 包引号但内容真实
                    ],
                    'deep_read': 'd', 'trend': 't',
                }
            if '批判' in prompt:
                return {'critique': 'x', 'revised_summary': 'y'}
            return await DimensionFakeLLM.chat_json(self, model, messages, **kwargs)

    fake = WrappedLLM()
    monkeypatch.setattr(insight_engine, 'build_client', lambda api_key='', base_url='', timeout=120.0: fake)

    resp = client.post('/analyze', json={'documents': DOCS, 'api_key': 'sk-x'})

    body = resp.json()
    dims = {d['id']: d for d in body['dimensions']}
    texts = [q['text'] for q in dims['background']['quotes']]
    assert len(texts) == 2, f'包引号的真引语不得误杀，实际保留 {texts}'
    assert body['warning'] == ''


# ── 并发闸门的单次重试 ─────────────────────────────────────────
#
# 闸门限 3 并发；瞬时失败（429/超时）重试一次：
#   · 重试成功 → 维度照常产出，不留失败记录
#   · 重试仍失败 → 异常进 failed 列表（warning 说明原因），维度缺席


class FlakyDimLLM(DimensionFakeLLM):
    """对指定维度前 N 次调用抛错，之后放行；记录每个维度的调用次数。"""

    def __init__(self, fail_dim: str, fail_times: int):
        super().__init__()
        self.fail_dim = fail_dim
        self.fail_times = fail_times
        self.dim_calls: dict[str, int] = {}

    async def chat_json(self, model, messages, **kwargs):
        prompt = json.dumps(messages, ensure_ascii=False)
        self.calls.append(prompt)
        if '【分析维度】' in prompt:
            for dim in DIMENSION_IDS:
                if f'id={dim}' in prompt:
                    self.dim_calls[dim] = self.dim_calls.get(dim, 0) + 1
                    if dim == self.fail_dim and self.dim_calls[dim] <= self.fail_times:
                        raise RuntimeError(f'dimension {dim} transient 429')
                    return dict(DIMENSION_PAYLOAD)
        if '批判' in prompt:
            return {'critique': 'x', 'revised_summary': 'y'}
        return await DimensionFakeLLM.chat_json(self, model, messages, **kwargs)


def test_retry_recovers_transient_dimension_failure(monkeypatch):
    fake = FlakyDimLLM(fail_dim='heat', fail_times=1)
    monkeypatch.setattr(insight_engine, 'build_client', lambda api_key='', base_url='', timeout=120.0: fake)

    resp = client.post('/analyze', json={'documents': DOCS, 'api_key': 'sk-x'})

    body = resp.json()
    ids = [d['id'] for d in body['dimensions']]
    assert 'heat' in ids, '瞬时失败重试成功后维度必须保留'
    assert len(ids) == 5, f'五个维度应齐备，实际 {ids}'
    assert fake.dim_calls.get('heat') == 2, f'heat 应恰好调用 2 次（首次+重试），实际 {fake.dim_calls}'
    assert body['warning'] == '', '重试成功不应留下降级 warning'


def test_persistent_dimension_failure_after_retry_lands_in_failed(monkeypatch):
    fake = FlakyDimLLM(fail_dim='heat', fail_times=99)
    monkeypatch.setattr(insight_engine, 'build_client', lambda api_key='', base_url='', timeout=120.0: fake)

    resp = client.post('/analyze', json={'documents': DOCS, 'api_key': 'sk-x'})

    body = resp.json()
    ids = [d['id'] for d in body['dimensions']]
    assert 'heat' not in ids, '重试仍失败的维度不得出现在结果里'
    assert len(ids) == 4
    assert fake.dim_calls.get('heat') == 2, f'heat 应重试过一次（共 2 次调用），实际 {fake.dim_calls}'
    assert '热度与传播路径' in body['warning'], '重试失败原因必须进入 warning'
